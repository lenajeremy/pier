package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A server made when a project had production's "app" and one database per
// preview branch keeps all of them, with "app" serving production, and is
// marked for a shared preview database.
func TestProjectDatabasesMigrate(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "pier.db")
	at := slices.IndexFunc(migrations, func(m string) bool { return strings.Contains(m, "CREATE TABLE project_databases") })
	if at < 0 {
		t.Fatal("no migration makes project_databases")
	}
	old, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	old.SetMaxOpenConns(1)
	for _, stmt := range append([]string{schema}, migrations[:at]...) {
		if _, err := old.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	for _, stmt := range []string{
		"INSERT INTO projects(name,created_at) VALUES('shop',1)",
		"INSERT INTO databases(project,image,memory_mb,admin_password,password,created_at,created_by) VALUES('shop','postgres:18-alpine',256,'sealed-admin','sealed-app',1000,'Ada')",
		"INSERT INTO preview_databases(project,branch,name,password,created_at) VALUES('shop','feature/login','preview_k3x9q2abcd','sealed-preview',2000)",
		"INSERT INTO database_backups(project,reason,state,started_at) VALUES('shop','nightly','done',3000)",
		fmt.Sprintf("PRAGMA user_version=%d", at),
	} {
		if _, err := old.Exec(stmt); err != nil {
			t.Fatal(stmt, err)
		}
	}
	old.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	all, err := s.ProjectDatabases(ctx, "shop")
	if err != nil || len(all) != 2 {
		t.Fatalf("databases %+v %v", all, err)
	}
	if d := all[0]; d.Name != "app" || !d.Serves(TargetProduction) || d.Serves(TargetPreview) || d.Variable != "DATABASE_URL" || d.Password != "sealed-app" || d.CreatedBy != "Ada" {
		t.Fatalf("app came through as %+v", d)
	}
	if d := all[1]; d.Name != "preview_k3x9q2abcd" || len(d.Environments) != 0 || d.Password != "sealed-preview" {
		t.Fatalf("the branch's database came through as %+v", d)
	}
	if v, _ := s.Setting(ctx, "database_preview_pending:shop"); v != "1" {
		t.Fatal("previews weren't marked for a database of their own")
	}
	if b, _ := s.Backups(ctx, "shop"); len(b) != 1 || b[0].Database != "app" {
		t.Fatalf("the backup came through as %+v", b)
	}
	if err := s.DeleteProject(ctx, "shop"); err != nil {
		t.Fatal(err)
	}
}
