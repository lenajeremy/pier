package deploy

import (
	"context"
	"strings"

	"pier/internal/store"
)

// Databases hands out the variables of a project's databases. A project
// with none gets no variables and no error.
type Databases interface {
	// For is the variables of every database linked to target, like
	// DATABASE_URL, and the network the container must join to reach them.
	For(ctx context.Context, project, target string) (env []string, network string, err error)
	// Secrets are the passwords and addresses of a project's databases,
	// for hiding from viewers.
	Secrets(ctx context.Context, project string) ([]string, error)
}

// withDatabase adds the variables of the databases linked to target,
// except any the project set itself, and says which network the container
// joins. The note is for the build log.
func (d *Deployer) withDatabase(ctx context.Context, project, target string, env []string) ([]string, []string, string, error) {
	if d.Databases == nil {
		return env, nil, "", nil
	}
	vars, network, err := d.Databases.For(ctx, project, target)
	if err != nil || len(vars) == 0 {
		return env, nil, "", err
	}
	set := map[string]bool{}
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		set[k] = true
	}
	out := append([]string{}, env...)
	var given, kept []string
	for _, kv := range vars {
		k, _, _ := strings.Cut(kv, "=")
		if set[k] {
			kept = append(kept, k)
			continue
		}
		out = append(out, kv)
		given = append(given, k)
	}
	note := ""
	if len(given) > 0 {
		note = "with the project's Postgres as " + strings.Join(given, ", ")
	}
	if len(kept) > 0 {
		if note != "" {
			note += "; "
		}
		note += strings.Join(kept, ", ") + " is set in this project's variables, so it's used instead"
	}
	return out, []string{network}, note, nil
}

// JobEnvironment is what a job's container gets: production's variables,
// DATABASE_URL among them when the project has a database, and the
// networks to join.
func (d *Deployer) JobEnvironment(ctx context.Context, project string) ([]string, []string, error) {
	env, _, err := d.variables(ctx, project, store.TargetProduction)
	if err != nil {
		return nil, nil, err
	}
	env, networks, _, err := d.withDatabase(ctx, project, store.TargetProduction, env)
	if err != nil {
		return nil, nil, err
	}
	env, _, err = d.withStorage(ctx, project, store.TargetProduction, env)
	return env, networks, err
}
