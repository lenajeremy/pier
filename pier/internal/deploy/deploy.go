// Package deploy turns a pushed commit into a live address: it extracts the
// commit, builds it, keeps the output as a deployment that never changes,
// points the branch's address (or production) at it, and reloads the router.
// Everything it does is narrated to the pusher's terminal.
package deploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"pier/internal/build"
	"pier/internal/names"
	"pier/internal/router"
	"pier/internal/store"
)

type Deployer struct {
	Store            *store.Store
	Repos            string // bare git repositories, one per project
	Deployments      string // one folder per deployment, never changed once written
	Logs             string // one build log per deployment, <id>.log
	Tmp              string // scratch space for checkouts, on the same disk as Deployments
	Domain           string // e.g. pier.example.com
	PierHost         string // where the dashboard is reached, e.g. pier.example.com; empty for none
	PierUpstream     string // the dashboard's own listener, e.g. 127.0.0.1:7777
	ProductionBranch string // e.g. main
	Runner           build.Runner
	Router           router.Caddy
	URL              func(host string) string // how an address is reached, for the log
	Log              *slog.Logger
	BuildTimeout     time.Duration

	mu    sync.Mutex
	locks map[string]*sync.Mutex
	dirty bool // the router is behind the database
}

// RepoPath is where a project's bare repository lives.
func (d *Deployer) RepoPath(project string) string { return filepath.Join(d.Repos, project+".git") }

func (d *Deployer) lock(project string) func() {
	d.mu.Lock()
	if d.locks == nil {
		d.locks = map[string]*sync.Mutex{}
	}
	l, ok := d.locks[project]
	if !ok {
		l = &sync.Mutex{}
		d.locks[project] = l
	}
	d.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// say writes one narrated line: a short tag, then the message.
func say(w io.Writer, tag, format string, args ...any) {
	fmt.Fprintf(w, "%-8s%s\n", tag, fmt.Sprintf(format, args...))
}

// Deploy builds commit on branch for project and, if the build works, points
// the branch's address at it. Builds for one project run one at a time.
func (d *Deployer) Deploy(ctx context.Context, project, branch, commit string, out io.Writer) error {
	unlock := d.lock(project)
	defer unlock()
	start := time.Now()

	if err := d.Store.EnsureProject(ctx, project); err != nil {
		return err
	}
	dep := store.Deployment{
		ID: names.NewID(), Project: project, Branch: branch, Commit: commit,
		Message: d.commitMessage(commit, project), Status: store.Building, CreatedAt: start,
	}
	for range 5 { // six hex characters can collide, rarely
		if _, err := d.Store.Deployment(ctx, dep.ID); errors.Is(err, store.ErrNotFound) {
			break
		}
		dep.ID = names.NewID()
	}
	if err := d.Store.CreateDeployment(ctx, dep); err != nil {
		return err
	}
	if logFile, err := d.createLog(dep.ID); err != nil {
		d.Log.Warn("build log can't be kept", "deployment", dep.ID, "err", err)
	} else {
		tl := &timedLog{w: logFile, start: start}
		defer func() { tl.Flush(); logFile.Close() }()
		out = io.MultiWriter(out, tl)
	}
	say(out, "pier", "received %s at %s", branch, short(commit))

	buildCtx, cancel := context.WithTimeout(ctx, d.BuildTimeout)
	defer cancel()
	res, err := d.build(buildCtx, dep, out)
	elapsed := time.Since(start)
	dep.BuildMS = elapsed.Milliseconds()
	dep.Framework, dep.Files, dep.Bytes = res.label, res.files, res.bytes
	if err != nil {
		dep.Status, dep.Error = store.Failed, err.Error()
		if ferr := d.Store.FinishDeployment(context.WithoutCancel(ctx), dep); ferr != nil {
			d.Log.Error("record failed build", "err", ferr)
		}
		say(out, "pier", "build failed after %s: %s", seconds(elapsed), err)
		if prev := d.serving(ctx, project, branch); prev != "" {
			say(out, "pier", "%s still serves %s", d.addressFor(project, branch), prev)
		}
		return err
	}
	dep.Status = store.Ready
	if err := d.Store.FinishDeployment(ctx, dep); err != nil {
		return err
	}
	say(out, "deploy", "kept as %s: %s, %s, never changed again", dep.ID, plural(res.files, "file"), size(res.bytes))

	alias := store.Alias{Project: project, Branch: branch, DeploymentID: dep.ID,
		Kind: store.KindBranch, Host: names.Branch(project, branch, d.Domain)}
	what := "preview"
	if branch == d.ProductionBranch {
		alias.Kind, alias.Host, what = store.KindProduction, names.Production(project, d.Domain), "production"
	}
	if err := d.Store.SetAlias(ctx, alias); err != nil {
		return err
	}
	if err := d.Sync(ctx); err != nil {
		say(out, "alias", "the router didn't take the change yet (%s); pierd will retry", err)
	}
	say(out, "alias", "%s", d.URL(alias.Host))
	say(out, "alias", "%s", d.URL(names.Deployment(project, dep.ID, d.Domain)))
	say(out, "pier", "%s ready in %s", what, seconds(time.Since(start)))
	return nil
}

type result struct {
	label string
	files int
	bytes int64
}

func (d *Deployer) build(ctx context.Context, dep store.Deployment, out io.Writer) (result, error) {
	var res result
	if err := os.MkdirAll(d.Tmp, 0o755); err != nil {
		return res, err
	}
	work, err := os.MkdirTemp(d.Tmp, dep.ID+"-")
	if err != nil {
		return res, err
	}
	defer os.RemoveAll(work)

	if err := d.extract(ctx, dep.Project, dep.Commit, work); err != nil {
		return res, fmt.Errorf("checking out %s: %w", short(dep.Commit), err)
	}
	plan, err := build.Detect(work)
	if err != nil {
		return res, err
	}
	res.label = plan.Label
	say(out, "build", "%s, found in %s", plan.Label, plan.Source)

	if plan.Build != "" {
		say(out, "build", "running in %s", d.Runner.Name())
		log := prefixed(out, "build   ")
		for _, step := range []string{plan.Install, plan.Build} {
			t := time.Now()
			say(out, "build", "$ %s", step)
			if err := d.Runner.Run(ctx, work, step, log); err != nil {
				log.Flush()
				if ctx.Err() != nil {
					return res, fmt.Errorf("%s timed out after %s", step, seconds(time.Since(t)))
				}
				return res, fmt.Errorf("%s failed: %w", step, err)
			}
			log.Flush()
			say(out, "build", "%s took %s", step, seconds(time.Since(t)))
		}
	}

	output := filepath.Join(work, filepath.Clean(plan.Output))
	if rel, err := filepath.Rel(work, output); err != nil || strings.HasPrefix(rel, "..") {
		return res, fmt.Errorf("output folder %q is outside the project", plan.Output)
	}
	if fi, err := os.Stat(output); err != nil || !fi.IsDir() {
		return res, fmt.Errorf("the build didn't produce %s/; check the framework's output folder", plan.Output)
	}
	res.files, res.bytes, err = count(output)
	if err != nil {
		return res, err
	}
	if res.files == 0 {
		return res, fmt.Errorf("%s/ is empty", plan.Output)
	}
	if err := os.MkdirAll(d.Deployments, 0o755); err != nil {
		return res, err
	}
	if err := os.Rename(output, filepath.Join(d.Deployments, dep.ID)); err != nil {
		return res, fmt.Errorf("keeping the output: %w", err)
	}
	return res, nil
}

// extract writes the commit's files (and nothing from .git) into dir.
func (d *Deployer) extract(ctx context.Context, project, commit, dir string) error {
	archive := exec.CommandContext(ctx, "git", "--git-dir", d.RepoPath(project), "archive", "--format=tar", commit)
	untar := exec.CommandContext(ctx, "tar", "-x", "-C", dir)
	pipe, err := archive.StdoutPipe()
	if err != nil {
		return err
	}
	var errs bytes.Buffer
	archive.Stderr, untar.Stderr = &errs, &errs
	untar.Stdin = pipe
	if err := archive.Start(); err != nil {
		return err
	}
	if err := untar.Run(); err != nil {
		archive.Wait()
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(errs.String()))
	}
	if err := archive.Wait(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(errs.String()))
	}
	return nil
}

func (d *Deployer) commitMessage(commit, project string) string {
	out, err := exec.Command("git", "--git-dir", d.RepoPath(project), "log", "-1", "--format=%s", commit).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (d *Deployer) addressFor(project, branch string) string {
	if branch == d.ProductionBranch {
		return names.Production(project, d.Domain)
	}
	return names.Branch(project, branch, d.Domain)
}

// serving returns the deployment an address serves now, if any.
func (d *Deployer) serving(ctx context.Context, project, branch string) string {
	aliases, err := d.Store.Aliases(ctx)
	if err != nil {
		return ""
	}
	host := d.addressFor(project, branch)
	for _, a := range aliases {
		if a.Host == host {
			return a.DeploymentID
		}
	}
	return ""
}

// Routes lists every address the router should serve: each alias, and each
// deployment's own permanent address.
func (d *Deployer) Routes(ctx context.Context) ([]router.Route, error) {
	ready, err := d.Store.ReadyDeployments(ctx)
	if err != nil {
		return nil, err
	}
	aliases, err := d.Store.Aliases(ctx)
	if err != nil {
		return nil, err
	}
	root := func(id string) string { return filepath.Join(d.Deployments, id) }
	var routes []router.Route
	for _, dep := range ready {
		routes = append(routes, router.Route{Host: names.Deployment(dep.Project, dep.ID, d.Domain), Root: root(dep.ID), DeploymentID: dep.ID})
	}
	for _, a := range aliases {
		routes = append(routes, router.Route{Host: a.Host, Root: root(a.DeploymentID), DeploymentID: a.DeploymentID})
	}
	if d.PierHost != "" && d.PierUpstream != "" {
		routes = append(routes, router.Route{Host: d.PierHost, Upstream: d.PierUpstream})
	}
	return routes, nil
}

// Sync loads the current routes into the router.
func (d *Deployer) Sync(ctx context.Context) error {
	routes, err := d.Routes(ctx)
	if err == nil {
		err = d.Router.Apply(ctx, routes)
	}
	d.mu.Lock()
	d.dirty = err != nil
	d.mu.Unlock()
	return err
}

// KeepSynced retries Sync until it works whenever the router fell behind,
// for example because Caddy was restarted or started after pierd.
func (d *Deployer) KeepSynced(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.mu.Lock()
			dirty := d.dirty
			d.mu.Unlock()
			if !dirty {
				continue
			}
			if err := d.Sync(ctx); err != nil {
				d.Log.Warn("router still out of date", "err", err)
			} else {
				d.Log.Info("router caught up")
			}
		}
	}
}

func count(dir string) (files int, bytes int64, err error) {
	err = filepath.WalkDir(dir, func(_ string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.Type().IsRegular() {
			info, err := e.Info()
			if err != nil {
				return err
			}
			files++
			bytes += info.Size()
		}
		return nil
	})
	return
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func short(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}

func seconds(d time.Duration) string { return fmt.Sprintf("%.1fs", d.Seconds()) }

func size(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1f MB", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.0f KB", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}

// lineWriter prefixes every line written through it.
type lineWriter struct {
	mu     sync.Mutex
	w      io.Writer
	prefix string
	buf    []byte
}

func prefixed(w io.Writer, prefix string) *lineWriter { return &lineWriter{w: w, prefix: prefix} }

func (l *lineWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = append(l.buf, p...)
	for {
		i := bytes.IndexAny(l.buf, "\r\n")
		if i < 0 {
			break
		}
		line := l.buf[:i]
		l.buf = l.buf[i+1:]
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if _, err := fmt.Fprintf(l.w, "%s%s\n", l.prefix, line); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// Flush writes out a last line that had no newline.
func (l *lineWriter) Flush() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(bytes.TrimSpace(l.buf)) > 0 {
		fmt.Fprintf(l.w, "%s%s\n", l.prefix, l.buf)
	}
	l.buf = nil
}
