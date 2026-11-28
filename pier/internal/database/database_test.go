package database

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pier/internal/apphost"
	"pier/internal/secrets"
	"pier/internal/store"
)

func TestSettingsFollowTheMemoryLimit(t *testing.T) {
	get := func(mb int, name string) string {
		for _, s := range apphost.PostgresSettings(mb) {
			if v, ok := strings.CutPrefix(s, name+"="); ok {
				return v
			}
		}
		return ""
	}
	for _, c := range []struct {
		mb                       int
		buffers, cache, work, mx string
	}{
		{128, "32MB", "64MB", "2MB", "32"},
		{256, "64MB", "128MB", "4MB", "64"},
		{4096, "1024MB", "2048MB", "32MB", "100"},
	} {
		if got := get(c.mb, "shared_buffers"); got != c.buffers {
			t.Errorf("%d MB: shared_buffers %s, want %s", c.mb, got, c.buffers)
		}
		if got := get(c.mb, "effective_cache_size"); got != c.cache {
			t.Errorf("%d MB: effective_cache_size %s, want %s", c.mb, got, c.cache)
		}
		if got := get(c.mb, "work_mem"); got != c.work {
			t.Errorf("%d MB: work_mem %s, want %s", c.mb, got, c.work)
		}
		if got := get(c.mb, "max_connections"); got != c.mx {
			t.Errorf("%d MB: max_connections %s, want %s", c.mb, got, c.mx)
		}
	}
}

func TestNightlyIsDueOncePastThreeAndNotYetMade(t *testing.T) {
	m, st, _ := newManager(t, nil)
	ctx := context.Background()
	project := "shop"
	if err := st.CreateProject(ctx, store.Project{Name: project}); err != nil {
		t.Fatal(err)
	}
	loc := time.UTC
	created := time.Date(2026, 11, 20, 12, 0, 0, 0, loc)
	if err := st.CreateDatabase(ctx, store.Database{Project: project, Image: Image, MemoryMB: 256, CreatedAt: created}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddProjectDatabase(ctx, store.ProjectDatabase{Project: project, Name: "production", Environments: []string{store.TargetProduction}, Variable: DefaultVariable, CreatedAt: created}); err != nil {
		t.Fatal(err)
	}
	at := func(s string) {
		t.Helper()
		now, _ := time.ParseInLocation("2006-01-02 15:04", s, loc)
		m.Now = func() time.Time { return now }
	}
	at("2026-11-20 23:00")
	if m.nightlyDue(ctx, project, "production") {
		t.Fatal("a database made today was due before its first night")
	}
	at("2026-11-21 02:59")
	if m.nightlyDue(ctx, project, "production") {
		t.Fatal("due before three")
	}
	at("2026-11-21 03:01")
	if !m.nightlyDue(ctx, project, "production") {
		t.Fatal("not due after three")
	}
	id, _ := st.StartBackup(ctx, store.Backup{Project: project, Database: "production", Reason: store.BackupNightly, StartedAt: m.now()})
	st.FinishBackup(ctx, store.Backup{ID: id, State: store.BackupDone, File: "x.dump", FinishedAt: m.now()})
	at("2026-11-21 14:00")
	if m.nightlyDue(ctx, project, "production") {
		t.Fatal("due again the same day")
	}
	at("2026-11-22 03:30")
	if !m.nightlyDue(ctx, project, "production") {
		t.Fatal("not due the next night")
	}
	// A failure waits an hour.
	id, _ = st.StartBackup(ctx, store.Backup{Project: project, Database: "production", Reason: store.BackupNightly, StartedAt: m.now()})
	st.FinishBackup(ctx, store.Backup{ID: id, State: store.BackupFailed, FinishedAt: m.now()})
	at("2026-11-22 04:00")
	if m.nightlyDue(ctx, project, "production") {
		t.Fatal("tried again within the hour")
	}
	at("2026-11-22 04:31")
	if !m.nightlyDue(ctx, project, "production") {
		t.Fatal("didn't try again after the hour")
	}
}

func TestDeleteBackup(t *testing.T) {
	m, st, _ := newManager(t, nil)
	ctx := context.Background()
	if err := st.CreateProject(ctx, store.Project{Name: "shop"}); err != nil {
		t.Fatal(err)
	}
	made := func(state string) store.Backup {
		t.Helper()
		b := store.Backup{Project: "shop", Database: "production", Reason: store.BackupManual, StartedAt: time.Now()}
		id, err := st.StartBackup(ctx, b)
		if err != nil {
			t.Fatal(err)
		}
		b.ID, b.State, b.FinishedAt = id, state, time.Now()
		if state == store.BackupDone {
			b.File = "1-x.dump"
			os.MkdirAll(m.projectDir("shop"), 0o700)
			os.WriteFile(m.BackupPath(b), []byte("dump"), 0o600)
		}
		if state != store.BackupRunning {
			st.FinishBackup(ctx, b)
		}
		return b
	}
	done := made(store.BackupDone)
	if _, err := m.DeleteBackup(ctx, "shop", done.ID, "Ada"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.BackupPath(done)); !os.IsNotExist(err) {
		t.Fatal("the backup's file is still there")
	}
	if _, err := st.BackupByID(ctx, "shop", done.ID); err != store.ErrNotFound {
		t.Fatal("the backup is still listed")
	}
	failed := made(store.BackupFailed)
	if _, err := m.DeleteBackup(ctx, "shop", failed.ID, "Ada"); err != nil {
		t.Fatal("a failed backup:", err)
	}
	running := made(store.BackupRunning)
	if _, err := m.DeleteBackup(ctx, "shop", running.ID, "Ada"); err != ErrBackupRunning {
		t.Fatalf("deleted a backup being made: %v", err)
	}
	if _, err := m.DeleteBackup(ctx, "other", running.ID, "Ada"); err != store.ErrNotFound {
		t.Fatalf("deleted another project's backup: %v", err)
	}
}

func newManager(t *testing.T, host *apphost.Host) (*Manager, *store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "pier.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, err := secrets.Load(filepath.Join(dir, "key"))
	if err != nil {
		t.Fatal(err)
	}
	return &Manager{Store: st, Box: box, Host: host, Backups: filepath.Join(dir, "backups"),
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}, st, dir
}

// vars reads a deployment's variables into a map.
func vars(t *testing.T, env []string, err error) map[string]string {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		out[k] = v
	}
	return out
}

// TestPostgresLifecycle runs a real server in Docker: PIER_TEST_DOCKER=1.
func TestPostgresLifecycle(t *testing.T) {
	if os.Getenv("PIER_TEST_DOCKER") == "" {
		t.Skip("set PIER_TEST_DOCKER=1 to run Postgres in Docker")
	}
	ctx := context.Background()
	host := apphost.New(t.TempDir())
	m, st, _ := newManager(t, host)
	project := "shop"
	if err := st.CreateProject(ctx, store.Project{Name: project}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { host.RemovePostgres(context.Background(), project, true) })

	if err := m.Create(ctx, project, 128, "Ada"); err != nil {
		t.Fatal("create:", err)
	}
	if err := m.Create(ctx, project, 128, "Ada"); err == nil {
		t.Fatal("a second server was made")
	}
	env, network, err := m.For(ctx, project, store.TargetProduction)
	prod := vars(t, env, err)["DATABASE_URL"]
	if !strings.HasPrefix(prod, "postgres://production:") || !strings.HasSuffix(prod, "@postgres:5432/production?sslmode=disable") || len(env) != 1 {
		t.Fatalf("production's variables %v", env)
	}
	if network != host.ProjectNetwork(project) {
		t.Fatalf("network %q", network)
	}

	// An app on the project's network reaches it by DATABASE_URL. The
	// postgres image stands in for one, with psql.
	app := func(url, sql string) (string, error) {
		out, err := exec.Command("docker", "run", "--rm", "--network", network, Image, "psql", url, "-Atqc", sql).CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	if out, err := app(prod, `CREATE TABLE orders (id serial PRIMARY KEY, note text); INSERT INTO orders(note) VALUES ('first')`); err != nil {
		t.Fatal(out)
	}
	// The dashboard reads it through pierd's loopback port; no name is
	// production's.
	tables, err := m.Tables(ctx, project, "")
	if err != nil || len(tables) != 1 || tables[0].Name != "orders" || tables[0].Rows != 1 {
		t.Fatalf("tables %+v %v", tables, err)
	}
	page, err := m.Rows(ctx, project, "production", "public", "orders", "note", true, "", 0, 50)
	if err != nil || len(page.Rows) != 1 || *page.Rows[0][1] != "first" || page.Columns[0].Type != "int4" || len(page.Key) != 1 {
		t.Fatalf("rows %+v %v", page, err)
	}
	if _, err := m.Rows(ctx, project, "", "public", "nope", "", false, "", 0, 50); err == nil {
		t.Fatal("read a table that isn't there")
	}
	q, err := m.Query(ctx, project, "", "INSERT INTO orders(note) VALUES ('from query'); SELECT count(*) AS n, NULL::text AS nothing FROM orders", "Ada")
	if err != nil || q.Error != nil || len(q.Results) != 2 || q.Results[0].Tag != "INSERT 0 1" || *q.Results[1].Rows[0][0] != "2" || q.Results[1].Rows[0][1] != nil || q.Results[1].Columns[0].Type != "int8" {
		t.Fatalf("query %+v %v", q, err)
	}
	q, err = m.Query(ctx, project, "", "SELECT nope FROM orders", "Ada")
	if err != nil || q.Error == nil || q.Error.Position == 0 {
		t.Fatalf("a bad query: %+v %v", q, err)
	}
	if _, err := app(prod, `DELETE FROM orders WHERE note = 'from query'`); err != nil {
		t.Fatal(err)
	}
	// Browsing can't change anything.
	if _, err := m.Rows(ctx, project, "", "public", "orders", "note; DROP TABLE orders", false, "", 0, 50); err != nil {
		t.Fatal(err)
	}
	// A filter can't end the read-only transaction or add a statement.
	if _, err := m.Rows(ctx, project, "", "public", "orders", "", false, "true; COMMIT; DROP TABLE orders", 0, 50); err == nil {
		t.Fatal("a filter ran a second statement")
	}
	if page, err := m.Rows(ctx, project, "", "public", "orders", "", false, "note = 'first'", 0, 50); err != nil || page.Count != 1 {
		t.Fatalf("filter: %+v %v", page, err)
	}
	if _, err := m.Rows(ctx, project, "", "public", "orders", "", false, "(DELETE FROM orders) IS NULL", 0, 50); err == nil {
		t.Fatal("a filter wrote")
	}
	if out, _ := app(prod, `SELECT count(*) FROM orders`); out != "1" {
		t.Fatal("a sort name changed the table")
	}

	// An app on another network can't.
	if out, err := exec.Command("docker", "run", "--rm", Image, "psql", prod, "-Atqc", "SELECT 1").CombinedOutput(); err == nil {
		t.Fatalf("reached the database from outside its network: %s", out)
	}

	// Previews share one empty database of their own.
	env, _, err = m.For(ctx, project, store.TargetPreview)
	preview := vars(t, env, err)["DATABASE_URL"]
	if !strings.Contains(preview, "/preview?") {
		t.Fatalf("preview url %q", preview)
	}
	if out, err := app(preview, `SELECT count(*) FROM pg_tables WHERE schemaname = 'public'`); err != nil || out != "0" {
		t.Fatalf("the preview database isn't empty: %s %v", out, err)
	}
	// A preview's role can't reach production's data.
	cross := strings.Replace(preview, "/preview?", "/production?", 1)
	if out, err := app(cross, `SELECT count(*) FROM orders`); err == nil {
		t.Fatalf("a preview's role read production: %s", out)
	}

	forVars := func(target string) map[string]string {
		t.Helper()
		e, _, err := m.For(ctx, project, target)
		return vars(t, e, err)
	}
	// A second database, given to production under its own variable.
	if err := m.AddDatabase(ctx, project, Spec{Name: "analytics", Environments: []string{store.TargetProduction}}, "Ada"); err == nil {
		t.Fatal("two databases gave production DATABASE_URL")
	}
	if err := m.AddDatabase(ctx, project, Spec{Name: "Bad name"}, "Ada"); err == nil {
		t.Fatal("a bad name was taken")
	}
	if err := m.AddDatabase(ctx, project, Spec{Name: "analytics", Environments: []string{store.TargetProduction}, Variable: "analytics_url"}, "Ada"); err != nil {
		t.Fatal(err)
	}
	pv := forVars(store.TargetProduction)
	if !strings.Contains(pv["ANALYTICS_URL"], "/analytics?") || pv["DATABASE_URL"] != prod {
		t.Fatalf("production's variables with two databases %v", pv)
	}
	// Previews can be moved onto production's database, and back.
	if err := m.LinkDatabase(ctx, project, "production", []string{store.TargetProduction, store.TargetPreview}, "", "Ada"); err == nil {
		t.Fatal("two databases gave previews DATABASE_URL")
	}

	st1, err := m.Status(ctx, project)
	if err != nil || st1.Container.State != "running" || st1.DataBytes == 0 || st1.DiskBytes == 0 || len(st1.Databases) != 3 || st1.Databases[0].Bytes == 0 {
		t.Fatalf("status %+v %v", st1, err)
	}
	t.Logf("memory %d KB of %d MB, data %d KB, disk %d KB", st1.Container.MemoryBytes>>10, st1.Container.LimitBytes>>20, st1.DataBytes>>10, st1.DiskBytes>>10)

	// An external URL reaches production through the TLS port, acts as
	// production's role, and stops working when it's revoked.
	m.External = "db.example.test:5432"
	e, ext, err := m.CreateExternal(ctx, project, "production", "Ada")
	if err != nil || !strings.HasPrefix(ext, "postgres://ext_") || !strings.HasSuffix(ext, "@db.example.test:5432/production?sslmode=require") {
		t.Fatalf("external url %q %v", ext, err)
	}
	if _, _, err := m.CreateExternal(ctx, project, "production", "Ada"); err == nil {
		t.Fatal("a database got a second external URL")
	}
	proxied := externalThroughProxy(t, m, ext)
	if out := proxied(`SELECT count(*) FROM orders`); out != "1" {
		t.Fatalf("through the external url: %q", out)
	}
	if out := proxied(`CREATE TABLE from_outside (x int); SELECT tableowner FROM pg_tables WHERE tablename = 'from_outside'`); out != "production" {
		t.Fatalf("a table made through the external url belongs to %q", out)
	}
	if err := m.RevokeExternal(ctx, project, e.Role, "Ada"); err != nil {
		t.Fatal(err)
	}
	if out, err := tryProxied(t, m, ext, `SELECT 1`); err == nil {
		t.Fatalf("a revoked url still connects: %s", out)
	}

	all, err := m.BackupAll(ctx, project, store.BackupManual, "Ada")
	if err != nil || len(all) != 3 || all[0].Database != "production" || all[0].Bytes == 0 {
		t.Fatalf("backups %+v %v", all, err)
	}
	if _, err := app(prod, `INSERT INTO orders(note) VALUES ('after the backup'); CREATE TABLE later (x int)`); err != nil {
		t.Fatal(err)
	}
	if err := m.Restore(ctx, project, all[0].ID, "Ada"); err != nil {
		t.Fatal("restore:", err)
	}
	if out, err := app(prod, `SELECT string_agg(note, ',') FROM orders`); err != nil || out != "first" {
		t.Fatalf("after restoring: %q %v", out, err)
	}
	if out, _ := app(prod, `SELECT count(*) FROM pg_tables WHERE tablename = 'later'`); out != "0" {
		t.Fatal("a table made after the backup survived the restore")
	}
	list, _ := st.Backups(ctx, project)
	if len(list) != 4 || list[0].Reason != store.BackupBeforeRestore || list[0].Database != "production" {
		t.Fatalf("backups %+v", list)
	}

	if err := m.SetMemory(ctx, project, 256, "Ada"); err != nil {
		t.Fatal("resize:", err)
	}
	if out, err := app(prod, `SHOW shared_buffers`); err != nil || out != "64MB" {
		t.Fatalf("after resizing: %q %v", out, err)
	}
	if out, _ := app(prod, `SELECT count(*) FROM orders`); out != "1" {
		t.Fatal("resizing lost data")
	}

	// Deleting a database keeps a last backup of it.
	analytics := forVars(store.TargetProduction)["ANALYTICS_URL"]
	if err := m.DropDatabase(ctx, project, "analytics", "Ada"); err != nil {
		t.Fatal(err)
	}
	if out, err := app(analytics, `SELECT 1`); err == nil {
		t.Fatalf("the dropped database still answers: %s", out)
	}
	if list, _ := st.Backups(ctx, project); list[0].Database != "analytics" || list[0].Reason != store.BackupBeforeDelete {
		t.Fatalf("no last backup of analytics: %+v", list[0])
	}

	// A server from before several databases gets previews a database of
	// their own, once.
	if err := m.LinkDatabase(ctx, project, "preview", nil, "", "Ada"); err != nil {
		t.Fatal(err)
	}
	st.SetSetting(ctx, "database_preview_pending:"+project, "1")
	m.upgrade(ctx, project)
	if pv := forVars(store.TargetPreview); !strings.Contains(pv["DATABASE_URL"], "/previews?") {
		t.Fatalf("after the upgrade, previews get %v", pv)
	}

	if err := m.Delete(ctx, project, "Ada"); err != nil {
		t.Fatal("delete:", err)
	}
	if s, _ := host.PostgresState(ctx, project); s.State != "missing" {
		t.Fatalf("still there: %+v", s)
	}
	list, _ = st.Backups(ctx, project)
	if len(list) == 0 || list[0].Reason != store.BackupBeforeDelete {
		t.Fatalf("no last backup: %+v", list)
	}
	if _, err := os.Stat(m.BackupPath(list[0])); err != nil {
		t.Fatal(err)
	}
}
