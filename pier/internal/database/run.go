package database

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"pier/internal/alerts"
	"pier/internal/store"
)

// CheckEvery is how often each server is checked, and how often pierd
// looks for a nightly backup that's due.
const CheckEvery = time.Minute

// DownAfter is how many checks in a row a server may miss before an alert.
const DownAfter = 3

// Run keeps every project's database going until ctx ends: it starts any
// server that isn't running, makes one again if its container is gone,
// alerts when one stops answering, and makes the nightly backups.
func (m *Manager) Run(ctx context.Context) {
	if err := m.Store.InterruptBackups(ctx); err != nil {
		m.Log.Warn("mark interrupted backups", "err", err)
	}
	m.check(ctx)
	tick := time.NewTicker(CheckEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			m.check(ctx)
		}
	}
}

func (m *Manager) check(ctx context.Context) {
	all, err := m.Store.Databases(ctx)
	if err != nil {
		m.Log.Warn("list databases", "err", err)
		return
	}
	for _, d := range all {
		m.keepUp(ctx, d)
		m.upgrade(ctx, d.Project)
		dbs, err := m.Store.ProjectDatabases(ctx, d.Project)
		if err != nil {
			continue
		}
		var failed error
		did := false
		for _, db := range dbs {
			if !m.nightlyDue(ctx, d.Project, db.Name) {
				continue
			}
			did = true
			if _, err := m.Backup(ctx, d.Project, db.Name, store.BackupNightly, ""); err != nil && failed == nil {
				failed = fmt.Errorf("%s: %w", db.Name, err)
			}
		}
		if did {
			m.reportBackup(ctx, d.Project, failed)
		}
	}
	m.sweepDeleted()
}

// keepUp starts a server that isn't running, and alerts once it has missed
// DownAfter checks in a row.
func (m *Manager) keepUp(ctx context.Context, d store.Database) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	st, err := m.Host.PostgresState(ctx, d.Project)
	problem := ""
	switch {
	case err != nil:
		problem = "Docker didn't answer: " + err.Error()
	case st.State == "missing":
		problem = "its container was gone, so Pier made it again"
		if err := m.withLock(d.Project, func() error { return m.startExisting(ctx, d) }); err != nil {
			problem = "its container was gone, and making it again failed: " + err.Error()
		}
	case st.State == "exited" || st.State == "created" || st.State == "dead":
		problem = "it had stopped, so Pier started it again"
		if st.OOMKilled {
			problem = "it ran out of memory and stopped, so Pier started it again"
		}
		if err := m.withLock(d.Project, func() error { return m.startExisting(ctx, d) }); err != nil {
			problem = "it had stopped, and starting it again failed: " + err.Error()
		}
	case st.Health == "unhealthy":
		problem = "it doesn't answer pg_isready"
	}
	m.mu.Lock()
	if m.down == nil {
		m.down, m.open = map[string]int{}, map[string]bool{}
	}
	if problem == "" {
		m.down[d.Project] = 0
		wasOpen := m.open[d.Project]
		m.open[d.Project] = false
		m.mu.Unlock()
		if wasOpen && m.Alerts != nil {
			m.Alerts.Resolve(ctx, alerts.Event{
				Kind: alerts.DatabaseRecovered, Key: "database:" + d.Project, Project: d.Project,
				Title: d.Project + ": the database is answering again", Summary: "Postgres accepts connections again.",
				Link: "/projects/" + d.Project + "/database",
			})
		}
		return
	}
	m.down[d.Project]++
	raise := m.down[d.Project] == DownAfter
	if raise {
		m.open[d.Project] = true
	}
	m.mu.Unlock()
	m.Log.Warn("database problem", "project", d.Project, "problem", problem)
	if raise && m.Alerts != nil {
		m.Alerts.Raise(ctx, alerts.Event{
			Kind: alerts.DatabaseDown, Key: "database:" + d.Project, Project: d.Project,
			Title:   d.Project + ": the database isn't answering",
			Summary: fmt.Sprintf("Postgres missed %d checks in a row, a minute apart: %s.", DownAfter, problem),
			Link:    "/projects/" + d.Project + "/database",
		})
	}
}

func (m *Manager) withLock(project string, f func() error) error {
	unlock := m.lock(project)
	defer unlock()
	return f()
}

// startExisting starts a server whose container is stopped or gone.
func (m *Manager) startExisting(ctx context.Context, d store.Database) error {
	admin, err := m.Box.Open(d.AdminPassword, binding(d.Project, "postgres"))
	if err != nil {
		return err
	}
	return m.Host.StartPostgres(ctx, m.spec(d, admin))
}

// nightlyDue reports whether a project's nightly backup for today is still
// to be made: it's past NightlyAt, server time, and the last one was made
// before then.
func (m *Manager) nightlyDue(ctx context.Context, project, database string) bool {
	now := m.now()
	at := time.Date(now.Year(), now.Month(), now.Day(), NightlyAt, 0, 0, 0, now.Location())
	if now.Before(at) {
		at = at.AddDate(0, 0, -1)
	}
	last, err := m.Store.LastBackup(ctx, project, database, store.BackupNightly)
	if errors.Is(err, store.ErrNotFound) {
		// A new database's first nightly backup waits for the next night.
		d, err := m.Store.ProjectDatabase(ctx, project, database)
		return err == nil && d.CreatedAt.Before(at)
	}
	if err != nil {
		return false
	}
	if !last.StartedAt.Before(at) {
		return false
	}
	// A failed attempt waits an hour before the next.
	all, err := m.Store.Backups(ctx, project)
	if err == nil {
		for _, b := range all { // newest first
			if b.Database != database || b.Reason != store.BackupNightly {
				continue
			}
			if b.State == store.BackupFailed && now.Sub(b.StartedAt) < time.Hour {
				return false
			}
			break
		}
	}
	return true
}

func (m *Manager) reportBackup(ctx context.Context, project string, err error) {
	if m.Alerts == nil {
		return
	}
	e := alerts.Event{Key: "backup:" + project, Project: project, Link: "/projects/" + project + "/database"}
	if err != nil {
		e.Kind, e.Title = alerts.BackupFailed, project+": the nightly database backup failed"
		e.Summary = sentence(err) + " Pier tries again in an hour."
		m.Alerts.Raise(ctx, e)
		return
	}
	e.Kind, e.Title, e.Summary = alerts.BackupRecovered, project+": the nightly database backup worked again", "A backup was made and kept."
	m.Alerts.Resolve(ctx, e)
}

// sweepDeleted removes what deleted projects left behind once it's older
// than KeepDeleted.
func (m *Manager) sweepDeleted() {
	dir := filepath.Join(m.Backups, "deleted")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || m.now().Sub(info.ModTime()) < KeepDeleted {
			continue
		}
		os.RemoveAll(filepath.Join(dir, e.Name()))
	}
}

// upgrade finishes moving a server made before servers held several
// databases: its previews had one database per branch, which are kept but
// serve nothing now, so previews get one shared database, once.
func (m *Manager) upgrade(ctx context.Context, project string) {
	key := "database_preview_pending:" + project
	if v, err := m.Store.Setting(ctx, key); err != nil || v == "" {
		return
	}
	if st, err := m.Host.PostgresState(ctx, project); err != nil || st.State != "running" {
		return
	}
	err := m.withLock(project, func() error {
		all, err := m.Store.ProjectDatabases(ctx, project)
		if err != nil {
			return err
		}
		for _, d := range all {
			if d.Serves(store.TargetPreview) {
				return nil
			}
		}
		name := previewName
		for _, d := range all {
			if d.Name == name {
				name = "previews"
			}
		}
		return m.add(ctx, project, store.ProjectDatabase{Name: name, Environments: []string{store.TargetPreview}, Variable: DefaultVariable}, "")
	})
	if err != nil {
		m.Log.Warn("give previews a database", "project", project, "err", err)
		return
	}
	m.Store.SetSetting(ctx, key, "")
}
