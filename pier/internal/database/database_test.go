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
	at := func(s string) {
		t.Helper()
		now, _ := time.ParseInLocation("2006-01-02 15:04", s, loc)
		m.Now = func() time.Time { return now }
	}
	at("2026-11-20 23:00")
	if m.nightlyDue(ctx, project) {
		t.Fatal("a database made today was due before its first night")
	}
	at("2026-11-21 02:59")
	if m.nightlyDue(ctx, project) {
		t.Fatal("due before three")
	}
	at("2026-11-21 03:01")
	if !m.nightlyDue(ctx, project) {
		t.Fatal("not due after three")
	}
	id, _ := st.StartBackup(ctx, store.Backup{Project: project, Reason: store.BackupNightly, StartedAt: m.now()})
	st.FinishBackup(ctx, store.Backup{ID: id, State: store.BackupDone, File: "x.dump", FinishedAt: m.now()})
	at("2026-11-21 14:00")
	if m.nightlyDue(ctx, project) {
		t.Fatal("due again the same day")
	}
	at("2026-11-22 03:30")
	if !m.nightlyDue(ctx, project) {
		t.Fatal("not due the next night")
	}
	// A failure waits an hour.
	id, _ = st.StartBackup(ctx, store.Backup{Project: project, Reason: store.BackupNightly, StartedAt: m.now()})
	st.FinishBackup(ctx, store.Backup{ID: id, State: store.BackupFailed, FinishedAt: m.now()})
	at("2026-11-22 04:00")
	if m.nightlyDue(ctx, project) {
		t.Fatal("tried again within the hour")
	}
	at("2026-11-22 04:31")
	if !m.nightlyDue(ctx, project) {
		t.Fatal("didn't try again after the hour")
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
		t.Fatal("a second database was made")
	}
	prod, network, err := m.For(ctx, project, store.TargetProduction, "")
	if err != nil || !strings.HasPrefix(prod, "postgres://app:") || !strings.HasSuffix(prod, "@postgres:5432/app?sslmode=disable") {
		t.Fatalf("production url %q, %v", prod, err)
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
	// The dashboard reads it through pierd's loopback port.
	tables, err := m.Tables(ctx, project, "")
	if err != nil || len(tables) != 1 || tables[0].Name != "orders" || tables[0].Rows != 1 {
		t.Fatalf("tables %+v %v", tables, err)
	}
	page, err := m.Rows(ctx, project, "", "public", "orders", "note", true, "", 0, 50)
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
	if _, err := m.Rows(ctx, project, "", "public", "orders", "", false, "true) UNION SELECT 1, (SELECT 'x'", 0, 50); err == nil {
		t.Log("a filter that closes the bracket only reads, in a read-only transaction")
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

	preview, _, err := m.For(ctx, project, store.TargetPreview, "feature/login")
	if err != nil || !strings.Contains(preview, "preview_") {
		t.Fatalf("preview url %q, %v", preview, err)
	}
	again, _, _ := m.For(ctx, project, store.TargetPreview, "feature/login")
	if again != preview {
		t.Fatal("the same branch got a second preview database")
	}
	if out, err := app(preview, `SELECT count(*) FROM pg_tables WHERE schemaname = 'public'`); err != nil || out != "0" {
		t.Fatalf("the preview database isn't empty: %s %v", out, err)
	}
	// A preview's role can't reach production's data.
	role := strings.TrimPrefix(strings.SplitN(preview, ":", 3)[1], "//")
	cross := strings.Replace(preview, "/"+role+"?", "/app?", 1)
	if out, err := app(cross, `SELECT count(*) FROM orders`); err == nil {
		t.Fatalf("a preview's role read production: %s", out)
	}

	st1, err := m.Status(ctx, project)
	if err != nil || st1.Container.State != "running" || st1.DataBytes == 0 || st1.DiskBytes == 0 || len(st1.Previews) != 1 {
		t.Fatalf("status %+v %v", st1, err)
	}
	t.Logf("memory %d KB of %d MB, data %d KB, disk %d KB", st1.Container.MemoryBytes>>10, st1.Container.LimitBytes>>20, st1.DataBytes>>10, st1.DiskBytes>>10)

	b, err := m.Backup(ctx, project, store.BackupManual, "Ada")
	if err != nil || b.Bytes == 0 {
		t.Fatalf("backup %+v %v", b, err)
	}
	if _, err := app(prod, `INSERT INTO orders(note) VALUES ('after the backup'); CREATE TABLE later (x int)`); err != nil {
		t.Fatal(err)
	}
	if err := m.Restore(ctx, project, b.ID, "Ada"); err != nil {
		t.Fatal("restore:", err)
	}
	if out, err := app(prod, `SELECT string_agg(note, ',') FROM orders`); err != nil || out != "first" {
		t.Fatalf("after restoring: %q %v", out, err)
	}
	if out, _ := app(prod, `SELECT count(*) FROM pg_tables WHERE tablename = 'later'`); out != "0" {
		t.Fatal("a table made after the backup survived the restore")
	}
	all, _ := st.Backups(ctx, project)
	if len(all) != 2 || all[0].Reason != store.BackupBeforeRestore {
		t.Fatalf("backups %+v", all)
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

	if err := m.DropPreview(ctx, project, "feature/login"); err != nil {
		t.Fatal(err)
	}
	if out, err := app(preview, `SELECT 1`); err == nil {
		t.Fatalf("the dropped preview database still answers: %s", out)
	}

	if err := m.Delete(ctx, project, "Ada"); err != nil {
		t.Fatal("delete:", err)
	}
	if s, _ := host.PostgresState(ctx, project); s.State != "missing" {
		t.Fatalf("still there: %+v", s)
	}
	all, _ = st.Backups(ctx, project)
	if len(all) == 0 || all[0].Reason != store.BackupBeforeDelete {
		t.Fatalf("no last backup: %+v", all)
	}
	if _, err := os.Stat(m.BackupPath(all[0])); err != nil {
		t.Fatal(err)
	}
}
