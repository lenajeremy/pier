package deploy

import (
	"context"
	"strings"
)

// Storage hands out the variables of a project's buckets.
type Storage interface {
	// For is the variables of every bucket linked to target; none for a
	// project without buckets.
	For(ctx context.Context, project, target string) ([]string, error)
	// Secrets are the bucket keys' secrets, for hiding from viewers.
	Secrets(ctx context.Context, project string) ([]string, error)
}

// withStorage adds the variables of the buckets linked to target, except
// any the project set itself. The note is for the build log.
func (d *Deployer) withStorage(ctx context.Context, project, target string, env []string) ([]string, string, error) {
	if d.Storage == nil {
		return env, "", nil
	}
	vars, err := d.Storage.For(ctx, project, target)
	if err != nil || len(vars) == 0 {
		return env, "", err
	}
	set := map[string]bool{}
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		set[k] = true
	}
	out := append([]string{}, env...)
	var buckets, kept []string
	for _, kv := range vars {
		k, v, _ := strings.Cut(kv, "=")
		if set[k] {
			kept = append(kept, k)
			continue
		}
		out = append(out, kv)
		if strings.HasSuffix(k, "_BUCKET") {
			buckets = append(buckets, v)
		}
	}
	note := "with " + plural(len(buckets), "bucket") + ": " + strings.Join(buckets, ", ")
	if len(kept) > 0 {
		note += "; the project's own " + strings.Join(kept, ", ") + " kept"
	}
	return out, note, nil
}
