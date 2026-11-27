// Package database gives projects a Postgres database. Each project that
// has one gets its own Postgres server in a container, on a network only
// that project's apps share, with its data on a Docker volume. Production
// uses the database "app"; each branch's previews get an empty database of
// their own on the same server, removed with the branch. Nightly backups
// are pg_dump files on this server's disk, and offsite as well when
// offsite storage is set up.
//
// pierd never connects to Postgres over the network: it runs psql,
// pg_dump and pg_restore inside the container, as the postgres superuser
// over the local socket.
package database

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"pier/internal/alerts"
	"pier/internal/apphost"
	"pier/internal/secrets"
	"pier/internal/store"
)

// Image is the Postgres release new databases run.
const Image = "postgres:18-alpine"

// DefaultMemoryMB is a new database's memory limit.
const DefaultMemoryMB = 256

// The production database and the role that owns it.
const appName = "app"

// Port is the port Postgres listens on, on the project's network.
const Port = "5432"

// How many backups of each kind are kept; older ones are removed.
var keep = map[string]int{
	store.BackupNightly:       7,
	store.BackupManual:        10,
	store.BackupBeforeRestore: 3,
	store.BackupBeforeDelete:  3,
}

// KeepDeleted is how long the last backup of a deleted project is kept.
const KeepDeleted = 30 * 24 * time.Hour

// NightlyAt is the hour, server time, nightly backups are made after.
const NightlyAt = 3

// Alerter hears about a database that stops answering, and a nightly backup
// that fails. Nil sends nothing.
type Alerter interface {
	Raise(ctx context.Context, e alerts.Event)
	Resolve(ctx context.Context, e alerts.Event)
}

// Offsite is storage backups are copied to, like an S3 bucket. Nil keeps
// them on this server only.
type Offsite interface {
	// Configured reports whether offsite storage is set up now.
	Configured(ctx context.Context) bool
	Put(ctx context.Context, key, path string) error
	Delete(ctx context.Context, key string) error
}

// Manager runs every project's database.
type Manager struct {
	Store   *store.Store
	Box     *secrets.Box // seals the passwords
	Host    *apphost.Host
	Backups string // where backups are kept: one folder per project, and deleted/
	Image   string // Image, unless a test says otherwise
	Log     *slog.Logger
	Alerts  Alerter
	Offsite Offsite
	// Now is the time; tests move it on. Nil is time.Now.
	Now func() time.Time

	mu    sync.Mutex
	locks map[string]*sync.Mutex
	down  map[string]int // project -> checks in a row its server didn't answer
	open  map[string]bool
}

// ErrNone means the project has no database.
var ErrNone = errors.New("this project has no database")

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Manager) image() string {
	if m.Image != "" {
		return m.Image
	}
	return Image
}

// lock serializes the changes to one project's database: creating it,
// resizing, backing up, restoring and deleting.
func (m *Manager) lock(project string) func() {
	m.mu.Lock()
	if m.locks == nil {
		m.locks = map[string]*sync.Mutex{}
	}
	l := m.locks[project]
	if l == nil {
		l = &sync.Mutex{}
		m.locks[project] = l
	}
	m.mu.Unlock()
	l.Lock()
	return l.Unlock
}

func binding(project, role string) string { return "database\x00" + project + "\x00" + role }

func password() string { return strings.ToLower(rand.Text()) + strings.ToLower(rand.Text())[:6] }

// names Pier makes for databases and roles; nothing else is ever put into
// SQL, so a check is enough to keep it safe.
var sqlName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

var sqlPassword = regexp.MustCompile(`^[a-z0-9]{16,64}$`)

func (m *Manager) spec(d store.Database, admin string) apphost.PostgresSpec {
	return apphost.PostgresSpec{
		Project: d.Project, Image: d.Image, MemoryMB: d.MemoryMB, Password: admin,
		Settings: apphost.PostgresSettings(d.MemoryMB),
	}
}

// ValidMemory reports whether mb is one of the sizes a database can have.
func ValidMemory(mb int) bool { return slices.Contains(apphost.DatabaseMemoryChoices, mb) }

// Create gives a project a database: it starts the server, which downloads
// Postgres the first time, and makes the production database. If any step
// fails, nothing is left behind.
func (m *Manager) Create(ctx context.Context, project string, memoryMB int, by string) error {
	if !ValidMemory(memoryMB) {
		return fmt.Errorf("a database's memory can be %s", choices())
	}
	unlock := m.lock(project)
	defer unlock()
	admin, app := password(), password()
	sealedAdmin, err := m.Box.Seal(admin, binding(project, "postgres"))
	if err != nil {
		return err
	}
	sealedApp, err := m.Box.Seal(app, binding(project, appName))
	if err != nil {
		return err
	}
	d := store.Database{
		Project: project, Image: m.image(), MemoryMB: memoryMB, AdminPassword: sealedAdmin, Password: sealedApp,
		CreatedAt: m.now(), CreatedBy: by,
	}
	if err := m.Store.CreateDatabase(ctx, d); err != nil {
		return err
	}
	undo := func() {
		gone, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		if err := m.Host.RemovePostgres(gone, project, true); err != nil {
			m.Log.Warn("remove a database that didn't start", "project", project, "err", err)
		}
		m.Store.DeleteDatabase(gone, project)
	}
	if err := m.Host.StartPostgres(ctx, m.spec(d, admin)); err != nil {
		undo()
		return fmt.Errorf("Postgres didn't start: %w", err)
	}
	if err := m.Host.WaitPostgres(ctx, project, 2*time.Minute); err != nil {
		undo()
		return err
	}
	if err := m.makeDatabase(ctx, project, appName, app); err != nil {
		undo()
		return err
	}
	// Nobody but a database's own role may connect to it, so a preview's
	// role can't reach production's data on the same server.
	if _, err := m.psql(ctx, project, "postgres", `REVOKE CONNECT ON DATABASE postgres FROM PUBLIC`); err != nil {
		undo()
		return err
	}
	m.Store.RecordEvent(context.WithoutCancel(ctx), project, "Added a Postgres database", fmt.Sprintf("Postgres %s with %d MB of memory", major(d.Image), memoryMB), "", by)
	return nil
}

func choices() string {
	var s []string
	for _, n := range apphost.DatabaseMemoryChoices {
		s = append(s, strconv.Itoa(n)+" MB")
	}
	return strings.Join(s[:len(s)-1], ", ") + " or " + s[len(s)-1]
}

// major is the Postgres release in an image's tag: "18" in postgres:18-alpine.
func major(image string) string {
	_, tag, _ := strings.Cut(image, ":")
	v, _, _ := strings.Cut(tag, "-")
	return v
}

// makeDatabase makes a role that can log in and a database it owns, which
// only it may connect to.
func (m *Manager) makeDatabase(ctx context.Context, project, name, pass string) error {
	if !sqlName.MatchString(name) || !sqlPassword.MatchString(pass) {
		return errors.New("that isn't a name or password Pier makes")
	}
	// Each -c runs on its own, as CREATE DATABASE can't be in a transaction.
	_, err := m.psql(ctx, project, "postgres",
		fmt.Sprintf(`CREATE ROLE %s LOGIN PASSWORD '%s'`, name, pass),
		fmt.Sprintf(`CREATE DATABASE %s OWNER %s`, name, name),
		fmt.Sprintf(`REVOKE ALL ON DATABASE %s FROM PUBLIC`, name),
		fmt.Sprintf(`GRANT ALL ON DATABASE %s TO %s`, name, name))
	return err
}

// dropDatabase removes a database and its role, closing its connections.
func (m *Manager) dropDatabase(ctx context.Context, project, name string) error {
	if !sqlName.MatchString(name) || name == appName {
		return errors.New("that isn't a preview database")
	}
	_, err := m.psql(ctx, project, "postgres",
		fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, name),
		fmt.Sprintf(`DROP ROLE IF EXISTS %s`, name))
	return err
}

// psql runs SQL in a project's server as the superuser and returns what it
// printed, unaligned, one row a line.
func (m *Manager) psql(ctx context.Context, project, db string, commands ...string) (string, error) {
	cmd := []string{"psql", "-X", "-U", "postgres", "-d", db, "-v", "ON_ERROR_STOP=1", "-Atq"}
	for _, c := range commands {
		cmd = append(cmd, "-c", c)
	}
	var out strings.Builder
	code, stderr, err := m.Host.Exec(ctx, m.Host.DatabaseContainer(project), cmd, nil, &out)
	if err != nil {
		return "", fmt.Errorf("couldn't reach the database: %w", err)
	}
	if code != 0 {
		return "", fmt.Errorf("Postgres said: %s", firstLine(stderr))
	}
	return strings.TrimSpace(out.String()), nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "psql:")
	line, _, _ := strings.Cut(s, "\n")
	line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "ERROR:"))
	if line == "" {
		return "nothing"
	}
	return line
}

// connURL is the address an app reaches a database by, on the project's
// network. The network is private to the project, so there's no TLS.
func connURL(role, pass, db string) string {
	u := url.URL{Scheme: "postgres", User: url.UserPassword(role, pass), Host: apphost.DatabaseHost + ":" + Port, Path: "/" + db, RawQuery: "sslmode=disable"}
	return u.String()
}

// Production is DATABASE_URL for a project's production, with the network
// its apps must join. ErrNone means the project has no database.
func (m *Manager) Production(ctx context.Context, project string) (string, string, error) {
	d, err := m.Store.Database(ctx, project)
	if errors.Is(err, store.ErrNotFound) {
		return "", "", ErrNone
	}
	if err != nil {
		return "", "", err
	}
	pass, err := m.Box.Open(d.Password, binding(project, appName))
	if err != nil {
		return "", "", err
	}
	return connURL(appName, pass, appName), m.Host.ProjectNetwork(project), nil
}

// Preview is DATABASE_URL for a branch's previews, made the first time a
// preview of the branch builds. Branch is empty for previews sent with
// pier deploy, which share one.
func (m *Manager) Preview(ctx context.Context, project, branch string) (string, string, error) {
	if _, err := m.Store.Database(ctx, project); errors.Is(err, store.ErrNotFound) {
		return "", "", ErrNone
	} else if err != nil {
		return "", "", err
	}
	unlock := m.lock(project)
	defer unlock()
	p, err := m.Store.PreviewDatabase(ctx, project, branch)
	if errors.Is(err, store.ErrNotFound) {
		p = store.PreviewDatabase{Project: project, Branch: branch, CreatedAt: m.now()}
		p.Name = "preview_" + strings.ToLower(rand.Text())[:10]
		pass := password()
		if p.Password, err = m.Box.Seal(pass, binding(project, p.Name)); err != nil {
			return "", "", err
		}
		if err := m.makeDatabase(ctx, project, p.Name, pass); err != nil {
			return "", "", err
		}
		if err := m.Store.AddPreviewDatabase(ctx, p); err != nil {
			m.dropDatabase(context.WithoutCancel(ctx), project, p.Name)
			return "", "", err
		}
	} else if err != nil {
		return "", "", err
	}
	pass, err := m.Box.Open(p.Password, binding(project, p.Name))
	if err != nil {
		return "", "", err
	}
	return connURL(p.Name, pass, p.Name), m.Host.ProjectNetwork(project), nil
}

// DropPreview removes a branch's preview database. The branch's next
// preview gets a new, empty one.
func (m *Manager) DropPreview(ctx context.Context, project, branch string) error {
	p, err := m.Store.PreviewDatabase(ctx, project, branch)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	unlock := m.lock(project)
	defer unlock()
	if err := m.dropDatabase(ctx, project, p.Name); err != nil {
		return err
	}
	return m.Store.DeletePreviewDatabase(ctx, project, branch)
}

// Status is what the dashboard shows about a project's database.
type Status struct {
	store.Database
	Version     string                `json:"version"`
	Container   apphost.DatabaseState `json:"container"`
	DataBytes   int64                 `json:"data_bytes"` // production's database
	DiskBytes   int64                 `json:"disk_bytes"` // everything on the volume: every database, and the WAL
	Connections int                   `json:"connections"`
	MaxConns    int                   `json:"max_connections"`
	Previews    []PreviewStatus       `json:"previews"`
	Problem     string                `json:"problem,omitempty"` // why the numbers are missing
	Host        string                `json:"host"`
	Port        string                `json:"port"`
}

// PreviewStatus is one branch's preview database.
type PreviewStatus struct {
	store.PreviewDatabase
	Bytes int64 `json:"bytes"`
}

// Status reads how a project's database is doing.
func (m *Manager) Status(ctx context.Context, project string) (Status, error) {
	d, err := m.Store.Database(ctx, project)
	if errors.Is(err, store.ErrNotFound) {
		return Status{}, ErrNone
	}
	if err != nil {
		return Status{}, err
	}
	st := Status{Database: d, Version: major(d.Image), Host: apphost.DatabaseHost, Port: Port, MaxConns: maxConnections(d.MemoryMB)}
	previews, err := m.Store.PreviewDatabases(ctx, project)
	if err != nil {
		return st, err
	}
	for _, p := range previews {
		st.Previews = append(st.Previews, PreviewStatus{PreviewDatabase: p})
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if st.Container, err = m.Host.PostgresState(ctx, project); err != nil {
		st.Problem = "Docker didn't answer: " + err.Error()
		return st, nil
	}
	if st.Container.State != "running" {
		return st, nil
	}
	out, err := m.psql(ctx, project, "postgres",
		`SELECT datname, pg_database_size(datname) FROM pg_database WHERE NOT datistemplate`,
		`SELECT count(*) FROM pg_stat_activity WHERE backend_type = 'client backend' AND datname IS NOT NULL`,
		`SELECT current_setting('server_version')`)
	if err != nil {
		st.Problem = err.Error()
		return st, nil
	}
	sizes := map[string]int64{}
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		if name, n, ok := strings.Cut(line, "|"); ok {
			sizes[name], _ = strconv.ParseInt(n, 10, 64)
		}
	}
	if len(lines) >= 2 {
		st.Connections, _ = strconv.Atoi(lines[len(lines)-2])
		st.Version = lines[len(lines)-1]
	}
	st.DataBytes = sizes[appName]
	for i := range st.Previews {
		st.Previews[i].Bytes = sizes[st.Previews[i].Name]
	}
	var du strings.Builder
	if code, _, err := m.Host.Exec(ctx, m.Host.DatabaseContainer(project), []string{"du", "-sk", "/var/lib/postgresql"}, nil, &du); err == nil && code == 0 {
		kb, _, _ := strings.Cut(strings.TrimSpace(du.String()), "\t")
		n, _ := strconv.ParseInt(strings.TrimSpace(kb), 10, 64)
		st.DiskBytes = n << 10
	}
	return st, nil
}

func maxConnections(memoryMB int) int {
	for _, s := range apphost.PostgresSettings(memoryMB) {
		if v, ok := strings.CutPrefix(s, "max_connections="); ok {
			n, _ := strconv.Atoi(v)
			return n
		}
	}
	return 0
}

// SetMemory changes a database's memory limit. The server restarts with
// settings sized for it, which takes a few seconds; the data stays.
func (m *Manager) SetMemory(ctx context.Context, project string, memoryMB int, by string) error {
	if !ValidMemory(memoryMB) {
		return fmt.Errorf("a database's memory can be %s", choices())
	}
	unlock := m.lock(project)
	defer unlock()
	d, err := m.Store.Database(ctx, project)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNone
	}
	if err != nil {
		return err
	}
	if d.MemoryMB == memoryMB {
		return nil
	}
	if err := m.Store.SetDatabaseMemory(ctx, project, memoryMB); err != nil {
		return err
	}
	d.MemoryMB = memoryMB
	if err := m.recreate(ctx, d); err != nil {
		return err
	}
	m.Store.RecordEvent(context.WithoutCancel(ctx), project, "Changed the database's memory", fmt.Sprintf("%d MB", memoryMB), "", by)
	return nil
}

// recreate makes the server's container again from what the store says,
// keeping the volume.
func (m *Manager) recreate(ctx context.Context, d store.Database) error {
	admin, err := m.Box.Open(d.AdminPassword, binding(d.Project, "postgres"))
	if err != nil {
		return err
	}
	if err := m.Host.RemovePostgres(ctx, d.Project, false); err != nil {
		return err
	}
	if err := m.Host.StartPostgres(ctx, m.spec(d, admin)); err != nil {
		return err
	}
	return m.Host.WaitPostgres(ctx, d.Project, 2*time.Minute)
}

// Restart restarts a project's server, making its container again if it's
// gone.
func (m *Manager) Restart(ctx context.Context, project, by string) error {
	unlock := m.lock(project)
	defer unlock()
	d, err := m.Store.Database(ctx, project)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNone
	}
	if err != nil {
		return err
	}
	if err := m.recreate(ctx, d); err != nil {
		return err
	}
	m.Store.RecordEvent(context.WithoutCancel(ctx), project, "Restarted the database", "", "", by)
	return nil
}

// Delete removes a project's database for good, after a last backup, which
// is kept with the project's other backups.
func (m *Manager) Delete(ctx context.Context, project, by string) error {
	if _, err := m.Store.Database(ctx, project); errors.Is(err, store.ErrNotFound) {
		return ErrNone
	} else if err != nil {
		return err
	}
	if st, err := m.Host.PostgresState(ctx, project); err == nil && st.State == "running" {
		if _, err := m.Backup(ctx, project, store.BackupBeforeDelete, by); err != nil {
			return fmt.Errorf("the backup before deleting failed, so the database is still here: %w", err)
		}
	}
	unlock := m.lock(project)
	defer unlock()
	if err := m.Host.RemovePostgres(ctx, project, true); err != nil {
		return err
	}
	if err := m.Store.DeleteDatabase(ctx, project); err != nil {
		return err
	}
	m.Store.RecordEvent(context.WithoutCancel(ctx), project, "Deleted the database", "A last backup was kept.", "", by)
	return nil
}

// Purge is for a project that is being deleted: its rows are gone already.
// It makes a last backup if the server runs, moves the project's backups
// to deleted/, where they're kept for KeepDeleted, and removes the server
// and its data.
func (m *Manager) Purge(ctx context.Context, project string) error {
	unlock := m.lock(project)
	defer unlock()
	st, err := m.Host.PostgresState(ctx, project)
	if err != nil {
		return err
	}
	if st.State == "missing" {
		return m.Host.RemovePostgres(ctx, project, true)
	}
	dir := filepath.Join(m.Backups, "deleted", project+"-"+m.now().UTC().Format("20060102-150405"))
	if old := m.projectDir(project); exists(old) {
		if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
			return err
		}
		if err := os.Rename(old, dir); err != nil {
			return err
		}
	}
	if st.State == "running" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		if _, err := m.dump(ctx, project, filepath.Join(dir, "last.dump")); err != nil {
			return fmt.Errorf("the last backup failed, so the database was kept: %w", err)
		}
	}
	return m.Host.RemovePostgres(ctx, project, true)
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

func (m *Manager) projectDir(project string) string { return filepath.Join(m.Backups, project) }

// BackupPath is where a backup's file is.
func (m *Manager) BackupPath(b store.Backup) string {
	return filepath.Join(m.projectDir(b.Project), b.File)
}

// Backup dumps a project's production database to a file on this server,
// compressed, sends it offsite if that's set up, and removes the oldest
// backups of the same kind beyond the number kept.
func (m *Manager) Backup(ctx context.Context, project, reason, by string) (store.Backup, error) {
	unlock := m.lock(project)
	defer unlock()
	return m.backup(ctx, project, reason, by)
}

func (m *Manager) backup(ctx context.Context, project, reason, by string) (store.Backup, error) {
	b := store.Backup{Project: project, Reason: reason, MadeBy: by, StartedAt: m.now()}
	id, err := m.Store.StartBackup(ctx, b)
	if err != nil {
		return b, err
	}
	b.ID = id
	b.File = fmt.Sprintf("%d-%s.dump", id, b.StartedAt.UTC().Format("20060102-150405"))
	dir := m.projectDir(project)
	err = os.MkdirAll(dir, 0o700)
	if err == nil {
		b.Bytes, err = m.dump(ctx, project, filepath.Join(dir, b.File))
	}
	b.FinishedAt = m.now()
	if err != nil {
		b.State, b.Error, b.File = store.BackupFailed, sentence(err), ""
	} else {
		b.State = store.BackupDone
	}
	if ferr := m.Store.FinishBackup(context.WithoutCancel(ctx), b); ferr != nil && err == nil {
		err = ferr
	}
	if err != nil {
		return b, err
	}
	if m.Offsite != nil && m.Offsite.Configured(ctx) {
		result := "sent"
		if err := m.Offsite.Put(ctx, offsiteKey(b), m.BackupPath(b)); err != nil {
			result = sentence(err)
			m.Log.Warn("send a backup offsite", "project", project, "backup", b.ID, "err", err)
		}
		m.Store.SetBackupOffsite(context.WithoutCancel(ctx), b.ID, result)
		b.Offsite = result
	}
	m.prune(context.WithoutCancel(ctx), project, reason)
	return b, nil
}

func offsiteKey(b store.Backup) string { return b.Project + "/" + b.File }

// dump writes the production database to path with pg_dump's custom
// format, compressed with zstd, which pg_restore reads. It's written
// beside path first, so a failure never leaves half a file.
func (m *Manager) dump(ctx context.Context, project, path string) (int64, error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".dump-")
	if err != nil {
		return 0, err
	}
	defer os.Remove(tmp.Name())
	code, stderr, err := m.Host.Exec(ctx, m.Host.DatabaseContainer(project),
		[]string{"pg_dump", "-U", "postgres", "-d", appName, "--format=custom", "--compress=zstd:3", "--no-owner"}, nil, tmp)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return 0, fmt.Errorf("couldn't reach the database: %w", err)
	}
	if code != 0 {
		return 0, fmt.Errorf("pg_dump said: %s", firstLine(stderr))
	}
	fi, err := os.Stat(tmp.Name())
	if err != nil {
		return 0, err
	}
	if fi.Size() == 0 {
		return 0, errors.New("pg_dump wrote nothing")
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

// prune removes a project's backups of one kind beyond the number kept,
// oldest first, and failed ones older than a month.
func (m *Manager) prune(ctx context.Context, project, reason string) {
	all, err := m.Store.Backups(ctx, project)
	if err != nil {
		return
	}
	kept := 0
	for _, b := range all { // newest first
		if b.State == store.BackupRunning {
			continue
		}
		old := false
		switch {
		case b.State == store.BackupFailed:
			old = m.now().Sub(b.StartedAt) > KeepDeleted
		case b.Reason == reason:
			kept++
			old = kept > keep[reason]
		}
		if !old {
			continue
		}
		if b.File != "" {
			if err := os.Remove(m.BackupPath(b)); err != nil && !errors.Is(err, os.ErrNotExist) {
				continue
			}
			if m.Offsite != nil && b.Offsite == "sent" && m.Offsite.Configured(ctx) {
				if err := m.Offsite.Delete(ctx, offsiteKey(b)); err != nil {
					m.Log.Warn("remove an old backup offsite", "project", project, "backup", b.ID, "err", err)
				}
			}
		}
		m.Store.DeleteBackup(ctx, b.ID)
	}
}

// Restore replaces a project's production database with a backup. A
// backup of what's there now is made first. The backup is restored into
// a new database beside the old one, and only once all of it is in are
// the two swapped, so a restore that fails changes nothing; apps that were
// connected lose their connections at the swap and connect again.
func (m *Manager) Restore(ctx context.Context, project string, id int64, by string) error {
	unlock := m.lock(project)
	defer unlock()
	b, err := m.Store.BackupByID(ctx, project, id)
	if err != nil {
		return err
	}
	if b.State != store.BackupDone || b.File == "" {
		return errors.New("that backup didn't finish, so it can't be restored")
	}
	f, err := os.Open(m.BackupPath(b))
	if err != nil {
		return fmt.Errorf("the backup's file is gone: %w", err)
	}
	defer f.Close()
	if _, err := m.backup(ctx, project, store.BackupBeforeRestore, by); err != nil {
		return fmt.Errorf("the backup before restoring failed, so nothing changed: %w", err)
	}
	const fresh, old = "app_restoring", "app_replaced"
	cleanup := func() {
		m.psql(context.WithoutCancel(ctx), project, "postgres", `DROP DATABASE IF EXISTS `+fresh+` WITH (FORCE)`)
	}
	if _, err := m.psql(ctx, project, "postgres",
		`DROP DATABASE IF EXISTS `+fresh+` WITH (FORCE)`,
		`DROP DATABASE IF EXISTS `+old+` WITH (FORCE)`,
		`CREATE DATABASE `+fresh+` OWNER `+appName,
		`REVOKE ALL ON DATABASE `+fresh+` FROM PUBLIC`); err != nil {
		return err
	}
	code, stderr, err := m.Host.Exec(ctx, m.Host.DatabaseContainer(project),
		[]string{"pg_restore", "-U", "postgres", "-d", fresh, "--no-owner", "--role=" + appName, "--exit-on-error", "--single-transaction"}, f, io.Discard)
	if err == nil && code != 0 {
		err = fmt.Errorf("pg_restore said: %s", firstLine(stderr))
	}
	if err != nil {
		cleanup()
		return fmt.Errorf("the restore failed, so nothing changed: %w", err)
	}
	// New connections to the old database are refused before its open ones
	// are closed, so nothing can connect in between and stop the rename.
	if _, err := m.psql(ctx, project, "postgres",
		`ALTER DATABASE `+appName+` ALLOW_CONNECTIONS false`,
		`SELECT count(pg_terminate_backend(pid)) FROM pg_stat_activity WHERE datname = '`+appName+`' AND pid <> pg_backend_pid()`,
		`ALTER DATABASE `+appName+` RENAME TO `+old,
		`ALTER DATABASE `+fresh+` RENAME TO `+appName,
		`GRANT ALL ON DATABASE `+appName+` TO `+appName,
		`DROP DATABASE `+old+` WITH (FORCE)`); err != nil {
		// Put the old one back if the swap stopped half way.
		m.psql(context.WithoutCancel(ctx), project, "postgres",
			`DO $$BEGIN IF NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = '`+appName+`') AND EXISTS (SELECT 1 FROM pg_database WHERE datname = '`+old+`') THEN EXECUTE 'ALTER DATABASE `+old+` RENAME TO `+appName+`'; END IF; END$$`,
			`ALTER DATABASE `+appName+` ALLOW_CONNECTIONS true`)
		cleanup()
		return fmt.Errorf("the restore couldn't swap the databases, so nothing changed: %w", err)
	}
	m.Store.RecordEvent(context.WithoutCancel(ctx), project, "Restored the database", "From the backup of "+b.StartedAt.UTC().Format("2 January 2006, 15:04 UTC"), "", by)
	return nil
}

func sentence(err error) string {
	s := strings.TrimSpace(err.Error())
	if s == "" {
		return "It failed."
	}
	s = strings.ToUpper(s[:1]) + s[1:]
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
}

// For is DATABASE_URL for a deployment, as the deployer asks for it: an
// empty url when the project has no database.
func (m *Manager) For(ctx context.Context, project, target, branch string) (string, string, error) {
	var u, network string
	var err error
	if target == store.TargetProduction {
		u, network, err = m.Production(ctx, project)
	} else {
		u, network, err = m.Preview(ctx, project, branch)
	}
	if errors.Is(err, ErrNone) {
		return "", "", nil
	}
	return u, network, err
}

// Secrets are a project's database passwords and the addresses that hold
// them, which a viewer must never see.
func (m *Manager) Secrets(ctx context.Context, project string) ([]string, error) {
	d, err := m.Store.Database(ctx, project)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	add := func(sealed, role string) error {
		pass, err := m.Box.Open(sealed, binding(project, role))
		if err != nil {
			return err
		}
		out = append(out, pass, connURL(role, pass, role))
		return nil
	}
	if err := add(d.Password, appName); err != nil {
		return nil, err
	}
	if err := add(d.AdminPassword, "postgres"); err != nil {
		return nil, err
	}
	previews, err := m.Store.PreviewDatabases(ctx, project)
	if err != nil {
		return nil, err
	}
	for _, p := range previews {
		if err := add(p.Password, p.Name); err != nil {
			return nil, err
		}
	}
	return out, nil
}
