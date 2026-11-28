// Package database gives projects Postgres. Each project that has it gets
// its own Postgres server in a container, on a network only that project's
// apps share, with its data on a Docker volume. The server holds any number
// of databases, each linked to the environments it serves and the variable
// they get it as: a new server has "production" for production and
// "preview" for previews, both as DATABASE_URL. Nightly backups are a
// pg_dump of each database on this server's disk, and offsite as well when
// offsite storage is set up. An external URL is a role of its own that
// reaches one database from outside Pier, through pierd's TLS port.
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

// The databases a new server starts with. Servers made before a server held
// several databases call production's "app".
const (
	productionName = "production"
	previewName    = "preview"
	appName        = "app"
)

// DefaultVariable is what a database is given to apps as, unless it says.
const DefaultVariable = "DATABASE_URL"

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
	// External is the public host and port external URLs connect to, like
	// db.example.com:5432. Empty means this server makes none.
	External string
	// Now is the time; tests move it on. Nil is time.Now.
	Now func() time.Time

	mu    sync.Mutex
	locks map[string]*sync.Mutex
	down  map[string]int // project -> checks in a row its server didn't answer
	open  map[string]bool
}

// ErrNone means the project has no database.
var ErrNone = errors.New("this project has no database")

// ErrNoDatabase means the project's server has no database by that name.
var ErrNoDatabase = errors.New("there's no database by that name")

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

// The names a person can give a database, and the variables it can be
// given as. Names leave room for the suffixes a restore adds, and ext_ is
// for external URLs' roles.
var (
	databaseName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)
	variableName = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)
)

func checkName(name string) error {
	if !databaseName.MatchString(name) || name == "postgres" || strings.HasPrefix(name, "template") || strings.HasPrefix(name, "ext_") ||
		strings.HasSuffix(name, "_restoring") || strings.HasSuffix(name, "_replaced") {
		return errors.New("a name is lowercase letters, digits and underscores, starting with a letter, up to 40")
	}
	return nil
}

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
	admin := password()
	sealedAdmin, err := m.Box.Seal(admin, binding(project, "postgres"))
	if err != nil {
		return err
	}
	d := store.Database{
		Project: project, Image: m.image(), MemoryMB: memoryMB, AdminPassword: sealedAdmin,
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
	// Nobody but a database's own role may connect to it, so a preview's
	// role can't reach production's data on the same server.
	if _, err := m.psql(ctx, project, "postgres", `REVOKE CONNECT ON DATABASE postgres FROM PUBLIC`); err != nil {
		undo()
		return err
	}
	for _, first := range []store.ProjectDatabase{
		{Name: productionName, Environments: []string{store.TargetProduction}, Variable: DefaultVariable},
		{Name: previewName, Environments: []string{store.TargetPreview}, Variable: DefaultVariable},
	} {
		if err := m.add(ctx, project, first, by); err != nil {
			undo()
			return err
		}
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

// dropDatabase removes a database, its role and its external URLs'
// roles, closing their connections.
func (m *Manager) dropDatabase(ctx context.Context, project, name string, externals []string) error {
	if !sqlName.MatchString(name) || name == "postgres" {
		return errors.New("that isn't a database Pier made")
	}
	cmds := []string{fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, name)}
	for _, r := range externals {
		if sqlName.MatchString(r) {
			cmds = append(cmds, fmt.Sprintf(`DROP ROLE IF EXISTS %s`, r))
		}
	}
	cmds = append(cmds, fmt.Sprintf(`DROP ROLE IF EXISTS %s`, name))
	_, err := m.psql(ctx, project, "postgres", cmds...)
	return err
}

// add makes a database and its role, and records it. The caller holds the
// project's lock.
func (m *Manager) add(ctx context.Context, project string, d store.ProjectDatabase, by string) error {
	pass := password()
	sealed, err := m.Box.Seal(pass, binding(project, d.Name))
	if err != nil {
		return err
	}
	if err := m.makeDatabase(ctx, project, d.Name, pass); err != nil {
		return err
	}
	d.Project, d.Password, d.CreatedAt, d.CreatedBy = project, sealed, m.now(), by
	if _, err := m.Store.AddProjectDatabase(ctx, d); err != nil {
		m.dropDatabase(context.WithoutCancel(ctx), project, d.Name, nil)
		return err
	}
	return nil
}

// targets puts environments in order and drops anything else.
func targets(envs []string) []string {
	out := []string{}
	for _, t := range []string{store.TargetProduction, store.TargetPreview} {
		if slices.Contains(envs, t) {
			out = append(out, t)
		}
	}
	return out
}

// clash finds another database that already gives one of these
// environments the same variable.
func clash(all []store.ProjectDatabase, name, variable string, envs []string) error {
	for _, o := range all {
		if o.Name == name || o.Variable != variable {
			continue
		}
		for _, e := range envs {
			if o.Serves(e) {
				return fmt.Errorf("%s gets its %s from %s already; pick another variable", e, variable, o.Name)
			}
		}
	}
	return nil
}

// Spec is a new database, or a change to one.
type Spec struct {
	Name         string   `json:"name"`
	Environments []string `json:"environments"`
	Variable     string   `json:"variable"`
}

// AddDatabase makes a database on a project's server.
func (m *Manager) AddDatabase(ctx context.Context, project string, spec Spec, by string) error {
	spec.Name = strings.TrimSpace(strings.ToLower(spec.Name))
	spec.Variable = strings.TrimSpace(strings.ToUpper(spec.Variable))
	if spec.Variable == "" {
		spec.Variable = DefaultVariable
	}
	spec.Environments = targets(spec.Environments)
	if err := checkName(spec.Name); err != nil {
		return err
	}
	if !variableName.MatchString(spec.Variable) {
		return errors.New("a variable is capital letters, digits and underscores, starting with a letter")
	}
	if _, err := m.Store.Database(ctx, project); errors.Is(err, store.ErrNotFound) {
		return ErrNone
	} else if err != nil {
		return err
	}
	unlock := m.lock(project)
	defer unlock()
	all, err := m.Store.ProjectDatabases(ctx, project)
	if err != nil {
		return err
	}
	for _, o := range all {
		if o.Name == spec.Name {
			return fmt.Errorf("%s already has a database called %s", project, spec.Name)
		}
	}
	if err := clash(all, spec.Name, spec.Variable, spec.Environments); err != nil {
		return err
	}
	if err := m.add(ctx, project, store.ProjectDatabase{Name: spec.Name, Environments: spec.Environments, Variable: spec.Variable}, by); err != nil {
		return err
	}
	m.Store.RecordEvent(context.WithoutCancel(ctx), project, "Added the database "+spec.Name, linked(spec.Environments, spec.Variable), "", by)
	return nil
}

// linked says, for the activity feed, what a database serves.
func linked(envs []string, variable string) string {
	switch len(envs) {
	case 0:
		return "Linked to no environment."
	case 2:
		return "Linked to production and previews, as " + variable + "."
	}
	if envs[0] == store.TargetProduction {
		return "Linked to production, as " + variable + "."
	}
	return "Linked to previews, as " + variable + "."
}

// LinkDatabase changes the environments a database serves and the
// variable it's given as. Deployments get the change when they next start.
func (m *Manager) LinkDatabase(ctx context.Context, project, name string, envs []string, variable, by string) error {
	unlock := m.lock(project)
	defer unlock()
	d, err := m.Store.ProjectDatabase(ctx, project, name)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNoDatabase
	}
	if err != nil {
		return err
	}
	envs = targets(envs)
	variable = strings.TrimSpace(strings.ToUpper(variable))
	if variable == "" {
		variable = d.Variable
	}
	if !variableName.MatchString(variable) {
		return errors.New("a variable is capital letters, digits and underscores, starting with a letter")
	}
	all, err := m.Store.ProjectDatabases(ctx, project)
	if err != nil {
		return err
	}
	if err := clash(all, name, variable, envs); err != nil {
		return err
	}
	if err := m.Store.LinkProjectDatabase(ctx, project, name, envs, variable); err != nil {
		return err
	}
	m.Store.RecordEvent(context.WithoutCancel(ctx), project, "Changed the database "+name, linked(envs, variable), "", by)
	return nil
}

// DropDatabase removes one database from a project's server, after a last
// backup of it, and its external URLs.
func (m *Manager) DropDatabase(ctx context.Context, project, name, by string) error {
	d, err := m.Store.ProjectDatabase(ctx, project, name)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNoDatabase
	}
	if err != nil {
		return err
	}
	if _, err := m.Backup(ctx, project, name, store.BackupBeforeDelete, by); err != nil {
		return fmt.Errorf("the backup before deleting failed, so %s is still here: %w", name, err)
	}
	unlock := m.lock(project)
	defer unlock()
	externals, err := m.Store.Externals(ctx, project)
	if err != nil {
		return err
	}
	var roles []string
	for _, e := range externals {
		if e.Database == d.Name {
			roles = append(roles, e.Role)
		}
	}
	if err := m.dropDatabase(ctx, project, d.Name, roles); err != nil {
		return err
	}
	if err := m.Store.DeleteProjectDatabase(ctx, project, d.Name); err != nil {
		return err
	}
	m.Store.RecordEvent(context.WithoutCancel(ctx), project, "Deleted the database "+name, "A last backup was kept.", "", by)
	return nil
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

// connURL for one of a project's databases, opened from the store.
func (m *Manager) urlFor(d store.ProjectDatabase) (string, error) {
	pass, err := m.Box.Open(d.Password, binding(d.Project, d.Name))
	if err != nil {
		return "", err
	}
	return connURL(d.Name, pass, d.Name), nil
}

// Status is what the dashboard shows about a project's database.
type Status struct {
	store.Database
	Version     string                `json:"version"`
	Container   apphost.DatabaseState `json:"container"`
	DataBytes   int64                 `json:"data_bytes"` // every database's data
	DiskBytes   int64                 `json:"disk_bytes"` // everything on the volume: every database, and the WAL
	Connections int                   `json:"connections"`
	MaxConns    int                   `json:"max_connections"`
	Databases   []DatabaseStatus      `json:"databases"`
	Problem     string                `json:"problem,omitempty"` // why the numbers are missing
	Host        string                `json:"host"`
	Port        string                `json:"port"`
}

// DatabaseStatus is one of the server's databases, with its size.
type DatabaseStatus struct {
	store.ProjectDatabase
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
	all, err := m.Store.ProjectDatabases(ctx, project)
	if err != nil {
		return st, err
	}
	st.Databases = []DatabaseStatus{}
	for _, p := range all {
		st.Databases = append(st.Databases, DatabaseStatus{ProjectDatabase: p})
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
	for i := range st.Databases {
		st.Databases[i].Bytes = sizes[st.Databases[i].Name]
		st.DataBytes += st.Databases[i].Bytes
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
		if _, err := m.BackupAll(ctx, project, store.BackupBeforeDelete, by); err != nil {
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

// names are the databases on a project's server, as Postgres has them: a
// project being deleted has none in the store any more.
func (m *Manager) names(ctx context.Context, project string) ([]string, error) {
	out, err := m.psql(ctx, project, "postgres", `SELECT datname FROM pg_database WHERE NOT datistemplate AND datname <> 'postgres' ORDER BY oid`)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, n := range strings.Split(out, "\n") {
		if n = strings.TrimSpace(n); sqlName.MatchString(n) {
			names = append(names, n)
		}
	}
	return names, nil
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
		names, err := m.names(ctx, project)
		if err != nil {
			return err
		}
		for _, name := range names {
			if _, err := m.dump(ctx, project, name, filepath.Join(dir, "last-"+name+".dump")); err != nil {
				return fmt.Errorf("the last backup failed, so the database was kept: %w", err)
			}
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

// Backup dumps one of a project's databases to a file on this server,
// compressed, sends it offsite if that's set up, and removes the oldest
// backups of the same kind beyond the number kept.
func (m *Manager) Backup(ctx context.Context, project, database, reason, by string) (store.Backup, error) {
	unlock := m.lock(project)
	defer unlock()
	return m.backup(ctx, project, database, reason, by)
}

// BackupAll backs up every database on a project's server, one file each.
// It carries on past one that fails, and returns the first failure.
func (m *Manager) BackupAll(ctx context.Context, project, reason, by string) ([]store.Backup, error) {
	all, err := m.Store.ProjectDatabases(ctx, project)
	if err != nil {
		return nil, err
	}
	var out []store.Backup
	var first error
	for _, d := range all {
		b, err := m.Backup(ctx, project, d.Name, reason, by)
		out = append(out, b)
		if err != nil && first == nil {
			first = fmt.Errorf("%s: %w", d.Name, err)
		}
	}
	return out, first
}

func (m *Manager) backup(ctx context.Context, project, database, reason, by string) (store.Backup, error) {
	if !sqlName.MatchString(database) {
		return store.Backup{}, ErrNoDatabase
	}
	b := store.Backup{Project: project, Database: database, Reason: reason, MadeBy: by, StartedAt: m.now()}
	id, err := m.Store.StartBackup(ctx, b)
	if err != nil {
		return b, err
	}
	b.ID = id
	b.File = fmt.Sprintf("%d-%s.dump", id, b.StartedAt.UTC().Format("20060102-150405"))
	dir := m.projectDir(project)
	err = os.MkdirAll(dir, 0o700)
	if err == nil {
		b.Bytes, err = m.dump(ctx, project, database, filepath.Join(dir, b.File))
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
	m.prune(context.WithoutCancel(ctx), project, database, reason)
	return b, nil
}

func offsiteKey(b store.Backup) string { return b.Project + "/" + b.File }

// dump writes one database to path with pg_dump's custom format,
// compressed with zstd, which pg_restore reads. It's written beside path
// first, so a failure never leaves half a file.
func (m *Manager) dump(ctx context.Context, project, database, path string) (int64, error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".dump-")
	if err != nil {
		return 0, err
	}
	defer os.Remove(tmp.Name())
	code, stderr, err := m.Host.Exec(ctx, m.Host.DatabaseContainer(project),
		[]string{"pg_dump", "-U", "postgres", "-d", database, "--format=custom", "--compress=zstd:3", "--no-owner"}, nil, tmp)
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

// prune removes a database's backups of one kind beyond the number kept,
// oldest first, and failed ones older than a month.
func (m *Manager) prune(ctx context.Context, project, database, reason string) {
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
		case b.Reason == reason && b.Database == database:
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

// Restore replaces the database a backup is of with the backup. A backup
// of what's there now is made first. The backup is restored into
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
	db := b.Database
	if _, err := m.Store.ProjectDatabase(ctx, project, db); err != nil || !sqlName.MatchString(db) {
		return fmt.Errorf("%s isn't on the server any more, so its backup can't be restored", db)
	}
	if _, err := m.backup(ctx, project, db, store.BackupBeforeRestore, by); err != nil {
		return fmt.Errorf("the backup before restoring failed, so nothing changed: %w", err)
	}
	fresh, old := db+"_restoring", db+"_replaced"
	cleanup := func() {
		m.psql(context.WithoutCancel(ctx), project, "postgres", `DROP DATABASE IF EXISTS `+fresh+` WITH (FORCE)`)
	}
	if _, err := m.psql(ctx, project, "postgres",
		`DROP DATABASE IF EXISTS `+fresh+` WITH (FORCE)`,
		`DROP DATABASE IF EXISTS `+old+` WITH (FORCE)`,
		`CREATE DATABASE `+fresh+` OWNER `+db,
		`REVOKE ALL ON DATABASE `+fresh+` FROM PUBLIC`); err != nil {
		return err
	}
	code, stderr, err := m.Host.Exec(ctx, m.Host.DatabaseContainer(project),
		[]string{"pg_restore", "-U", "postgres", "-d", fresh, "--no-owner", "--role=" + db, "--exit-on-error", "--single-transaction"}, f, io.Discard)
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
		`ALTER DATABASE `+db+` ALLOW_CONNECTIONS false`,
		`SELECT count(pg_terminate_backend(pid)) FROM pg_stat_activity WHERE datname = '`+db+`' AND pid <> pg_backend_pid()`,
		`ALTER DATABASE `+db+` RENAME TO `+old,
		`ALTER DATABASE `+fresh+` RENAME TO `+db,
		`GRANT ALL ON DATABASE `+db+` TO `+db,
		`DROP DATABASE `+old+` WITH (FORCE)`); err != nil {
		// Put the old one back if the swap stopped half way.
		m.psql(context.WithoutCancel(ctx), project, "postgres",
			`DO $$BEGIN IF NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = '`+db+`') AND EXISTS (SELECT 1 FROM pg_database WHERE datname = '`+old+`') THEN EXECUTE 'ALTER DATABASE `+old+` RENAME TO `+db+`'; END IF; END$$`,
			`ALTER DATABASE `+db+` ALLOW_CONNECTIONS true`)
		cleanup()
		return fmt.Errorf("the restore couldn't swap the databases, so nothing changed: %w", err)
	}
	m.Store.RecordEvent(context.WithoutCancel(ctx), project, "Restored the database "+db, "From the backup of "+b.StartedAt.UTC().Format("2 January 2006, 15:04 UTC"), "", by)
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

// For is a deployment's database variables, as the deployer asks for
// them: those of every database linked to its target, and the network its
// container must join to reach them. None when the project has no server.
func (m *Manager) For(ctx context.Context, project, target string) ([]string, string, error) {
	if _, err := m.Store.Database(ctx, project); errors.Is(err, store.ErrNotFound) {
		return nil, "", nil
	} else if err != nil {
		return nil, "", err
	}
	all, err := m.Store.ProjectDatabases(ctx, project)
	if err != nil {
		return nil, "", err
	}
	var env []string
	for _, d := range all {
		if !d.Serves(target) {
			continue
		}
		u, err := m.urlFor(d)
		if err != nil {
			return nil, "", err
		}
		env = append(env, d.Variable+"="+u)
	}
	if len(env) == 0 {
		return nil, "", nil
	}
	return env, m.Host.ProjectNetwork(project), nil
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
	add := func(sealed, role, db string) error {
		pass, err := m.Box.Open(sealed, binding(project, role))
		if err != nil {
			return err
		}
		out = append(out, pass, connURL(role, pass, db))
		return nil
	}
	if err := add(d.AdminPassword, "postgres", "postgres"); err != nil {
		return nil, err
	}
	all, err := m.Store.ProjectDatabases(ctx, project)
	if err != nil {
		return nil, err
	}
	for _, p := range all {
		if err := add(p.Password, p.Name, p.Name); err != nil {
			return nil, err
		}
	}
	externals, err := m.Store.Externals(ctx, project)
	if err != nil {
		return nil, err
	}
	for _, e := range externals {
		if err := add(e.Password, e.Role, e.Database); err != nil {
			return nil, err
		}
	}
	return out, nil
}
