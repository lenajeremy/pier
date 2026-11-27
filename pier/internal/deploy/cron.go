package deploy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	"pier/internal/store"
)

// CronSecretName is the variable production's app gets the cron secret
// in, named as Vercel names it, so an app moved from there works as it is.
const CronSecretName = "CRON_SECRET"

func cronBinding(project string) string { return "cron\x00" + project }

// CronSecret is the secret a job that calls a path sends as its bearer
// token, made the first time it's needed, and when it was made: a
// deployment built before then doesn't have it.
func (d *Deployer) CronSecret(ctx context.Context, project string) (string, time.Time, error) {
	sealed, made, err := d.Store.CronSecret(ctx, project)
	if err != nil {
		return "", time.Time{}, err
	}
	if sealed == "" {
		b := make([]byte, 32)
		rand.Read(b)
		fresh, err := d.Secrets.Seal(hex.EncodeToString(b), cronBinding(project))
		if err != nil {
			return "", time.Time{}, err
		}
		if err := d.Store.SetCronSecret(ctx, project, fresh); err != nil {
			return "", time.Time{}, err
		}
		// Another build may have made one first; theirs is kept.
		if sealed, made, err = d.Store.CronSecret(ctx, project); err != nil {
			return "", time.Time{}, err
		}
	}
	secret, err := d.Secrets.Open(sealed, cronBinding(project))
	return secret, made, err
}

// Variables opens a project's variables for one scope as NAME=value pairs,
// as its builds and its server get them.
func (d *Deployer) Variables(ctx context.Context, project, scope string) ([]string, error) {
	env, _, err := d.variables(ctx, project, scope)
	return env, err
}

// SecretValues are the values Pier gives a project's builds and processes:
// its variables in both scopes, shared ones included, and its cron secret
// once one has been made. They're what a viewer must never see, so their
// logs are read with these hidden. Nothing is made here.
func (d *Deployer) SecretValues(ctx context.Context, project string) ([]string, error) {
	if d.Secrets == nil {
		return nil, nil
	}
	var out []string
	for _, scope := range []string{store.TargetProduction, store.TargetPreview} {
		env, _, err := d.variables(ctx, project, scope)
		if err != nil {
			return nil, err
		}
		for _, kv := range env {
			_, v, _ := strings.Cut(kv, "=")
			out = append(out, v)
		}
	}
	if d.Databases != nil {
		hidden, err := d.Databases.Secrets(ctx, project)
		if err != nil {
			return nil, err
		}
		out = append(out, hidden...)
	}
	sealed, _, err := d.Store.CronSecret(ctx, project)
	if err != nil {
		return nil, err
	}
	if sealed != "" {
		secret, err := d.Secrets.Open(sealed, cronBinding(project))
		if err != nil {
			return nil, err
		}
		out = append(out, secret)
	}
	return out, nil
}

// withCronSecret adds CRON_SECRET to production's variables, unless the
// project set its own, which then wins: Pier sends whichever the app has.
func (d *Deployer) withCronSecret(ctx context.Context, project, scope string, env []string) ([]string, error) {
	if scope != store.TargetProduction || d.Secrets == nil {
		return env, nil
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, CronSecretName+"=") {
			return env, nil
		}
	}
	secret, _, err := d.CronSecret(ctx, project)
	if err != nil {
		return nil, err
	}
	return append(env, CronSecretName+"="+secret), nil
}

// Production is the deployment production serves now, if it serves one.
func (d *Deployer) Production(ctx context.Context, project string) (store.Deployment, bool, error) {
	host := d.ProductionHost(ctx, project)
	id := d.serving(ctx, host)
	if id == "" {
		return store.Deployment{}, false, nil
	}
	dep, err := d.Store.Deployment(ctx, id)
	if err != nil {
		return store.Deployment{}, false, err
	}
	return dep, true, nil
}
