package database

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"pier/internal/store"
)

// ErrNoExternals means this server doesn't make external URLs: it has no
// public address for them.
var ErrNoExternals = errors.New("this server doesn't make external URLs")

// externalURL is how a service outside Pier reaches a database: through
// pierd's TLS port, as the external URL's own role.
func (m *Manager) externalURL(role, pass, db string) string {
	u := url.URL{Scheme: "postgres", User: url.UserPassword(role, pass), Host: m.External, Path: "/" + db, RawQuery: "sslmode=require"}
	return u.String()
}

// ExternalView is an external URL as the dashboard lists it, without its
// password.
type ExternalView struct {
	store.External
	Host string `json:"host"`
}

// Externals lists a project's external URLs.
func (m *Manager) Externals(ctx context.Context, project string) ([]ExternalView, error) {
	all, err := m.Store.Externals(ctx, project)
	if err != nil {
		return nil, err
	}
	out := []ExternalView{}
	for _, e := range all {
		out = append(out, ExternalView{External: e, Host: m.External})
	}
	return out, nil
}

// CreateExternal makes an external URL for one of a project's databases: a
// role of its own that acts as the database's role once connected, so what
// it makes belongs to the database. It returns the URL, password and all.
func (m *Manager) CreateExternal(ctx context.Context, project, database, by string) (store.External, string, error) {
	if m.External == "" {
		return store.External{}, "", ErrNoExternals
	}
	unlock := m.lock(project)
	defer unlock()
	d, err := m.Store.ProjectDatabase(ctx, project, database)
	if errors.Is(err, store.ErrNotFound) {
		return store.External{}, "", ErrNoDatabase
	}
	if err != nil {
		return store.External{}, "", err
	}
	existing, err := m.Store.Externals(ctx, project)
	if err != nil {
		return store.External{}, "", err
	}
	for _, e := range existing {
		if e.Database == d.Name {
			return store.External{}, "", errors.New("this database has an external URL already; revoke it to make a new one")
		}
	}
	role := "ext_" + strings.ToLower(rand.Text())[:12]
	pass := password()
	if !sqlName.MatchString(role) || !sqlName.MatchString(d.Name) || !sqlPassword.MatchString(pass) {
		return store.External{}, "", errors.New("that isn't a name or password Pier makes")
	}
	sealed, err := m.Box.Seal(pass, binding(project, role))
	if err != nil {
		return store.External{}, "", err
	}
	if _, err := m.psql(ctx, project, "postgres",
		fmt.Sprintf(`CREATE ROLE %s LOGIN PASSWORD '%s' IN ROLE %s`, role, pass, d.Name),
		fmt.Sprintf(`ALTER ROLE %s SET role = %s`, role, d.Name)); err != nil {
		return store.External{}, "", err
	}
	e := store.External{Role: role, Project: project, Database: d.Name, Password: sealed, CreatedAt: m.now(), CreatedBy: by}
	if err := m.Store.AddExternal(ctx, e); err != nil {
		m.psql(context.WithoutCancel(ctx), project, "postgres", `DROP ROLE IF EXISTS `+role)
		return store.External{}, "", err
	}
	m.Store.RecordEvent(context.WithoutCancel(ctx), project, "Made an external URL for "+d.Name, "As "+role+".", "", by)
	return e, m.externalURL(role, pass, d.Name), nil
}

// ExternalURL shows an external URL again, password and all.
func (m *Manager) ExternalURL(ctx context.Context, project, role string) (string, error) {
	e, err := m.Store.External(ctx, role)
	if err != nil || e.Project != project {
		return "", store.ErrNotFound
	}
	if m.External == "" {
		return "", ErrNoExternals
	}
	pass, err := m.Box.Open(e.Password, binding(project, role))
	if err != nil {
		return "", err
	}
	return m.externalURL(role, pass, e.Database), nil
}

// RevokeExternal stops an external URL working: its connections are closed
// and its role dropped.
func (m *Manager) RevokeExternal(ctx context.Context, project, role, by string) error {
	unlock := m.lock(project)
	defer unlock()
	e, err := m.Store.External(ctx, role)
	if err != nil || e.Project != project {
		return store.ErrNotFound
	}
	if !sqlName.MatchString(role) || !sqlName.MatchString(e.Database) {
		return errors.New("that isn't a role Pier makes")
	}
	if _, err := m.psql(ctx, project, "postgres",
		fmt.Sprintf(`ALTER ROLE %s NOLOGIN`, role),
		fmt.Sprintf(`SELECT count(pg_terminate_backend(pid)) FROM pg_stat_activity WHERE usename = '%s'`, role)); err != nil {
		return err
	}
	// A client that reset its role may have made objects as the external
	// role itself, or been granted some; the database's role takes them,
	// or the role couldn't be dropped.
	if _, err := m.psql(ctx, project, e.Database,
		fmt.Sprintf(`REASSIGN OWNED BY %s TO %s`, role, e.Database),
		fmt.Sprintf(`DROP OWNED BY %s`, role)); err != nil {
		return err
	}
	if _, err := m.psql(ctx, project, "postgres", fmt.Sprintf(`DROP ROLE IF EXISTS %s`, role)); err != nil {
		return err
	}
	if err := m.Store.DeleteExternal(ctx, project, role); err != nil {
		return err
	}
	m.Store.RecordEvent(context.WithoutCancel(ctx), project, "Revoked an external URL for "+e.Database, "As "+role+".", "", by)
	return nil
}

// Route is where pierd's TLS port sends a connection for a role: the
// project's server on this machine's loopback.
func (m *Manager) Route(ctx context.Context, user string) (string, error) {
	e, err := m.Store.External(ctx, user)
	if errors.Is(err, store.ErrNotFound) {
		return "", errors.New("there's no external URL for that user")
	}
	if err != nil {
		return "", errors.New("Pier couldn't look up that user")
	}
	addr, err := m.Host.PostgresAddress(ctx, e.Project)
	if err != nil {
		return "", errors.New("the database isn't running")
	}
	return addr, nil
}
