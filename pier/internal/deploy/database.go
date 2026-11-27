package deploy

import (
	"context"
	"strings"

	"pier/internal/store"
)

// DatabaseURLName is the variable a project's database reaches its apps by.
const DatabaseURLName = "DATABASE_URL"

// Databases hands out the address of a project's database. A project with
// none gets an empty url and no error.
type Databases interface {
	// For is DATABASE_URL for a deployment of project for target, and
	// the network its container must join to reach it. A preview's branch
	// picks its own database; branch is empty for one sent by pier deploy.
	For(ctx context.Context, project, target, branch string) (url, network string, err error)
	// Secrets are the passwords and addresses of a project's databases,
	// for hiding from viewers.
	Secrets(ctx context.Context, project string) ([]string, error)
}

// withDatabase adds DATABASE_URL to a deployment's variables when its
// project has a database, unless the project set DATABASE_URL itself, and
// says which network its container joins. The note is for the build log.
func (d *Deployer) withDatabase(ctx context.Context, project, target, branch string, env []string) ([]string, []string, string, error) {
	if d.Databases == nil {
		return env, nil, "", nil
	}
	url, network, err := d.Databases.For(ctx, project, target, branch)
	if err != nil || url == "" {
		return env, nil, "", err
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, DatabaseURLName+"=") {
			return env, []string{network}, "DATABASE_URL is set in this project's variables, so it's used instead of the Pier database's", nil
		}
	}
	note := "with the project's Postgres database as DATABASE_URL"
	if target == store.TargetPreview {
		note = "with an empty Postgres database of its own as DATABASE_URL, kept for this branch's previews"
		if branch == "" {
			note = "with the empty Postgres database previews sent by pier deploy share, as DATABASE_URL"
		}
	}
	return append(append([]string{}, env...), DatabaseURLName+"="+url), []string{network}, note, nil
}

// JobEnvironment is what a job's container gets: production's variables,
// DATABASE_URL among them when the project has a database, and the
// networks to join.
func (d *Deployer) JobEnvironment(ctx context.Context, project string) ([]string, []string, error) {
	env, _, err := d.variables(ctx, project, store.TargetProduction)
	if err != nil {
		return nil, nil, err
	}
	env, networks, _, err := d.withDatabase(ctx, project, store.TargetProduction, "", env)
	return env, networks, err
}
