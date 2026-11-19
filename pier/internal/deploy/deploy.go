// Package deploy turns a pushed commit into a live address: it extracts the
// commit, builds it, keeps the output as a deployment that never changes,
// points the branch's address (or production) at it, and reloads the router.
// Everything it does is narrated to the pusher's terminal.
package deploy

import (
	"bytes"
	"context"
	"encoding/json"
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
	"pier/internal/bundle"
	"pier/internal/names"
	"pier/internal/router"
	"pier/internal/secrets"
	"pier/internal/store"
)

type Deployer struct {
	Store            *store.Store
	Repos            string       // bare git repositories, one per project
	Deployments      string       // one folder per deployment, never changed once written
	Logs             string       // one build log per deployment, <id>.log
	Sources          string       // each upload's archive, <id>.tar.gz, kept so it can be redeployed
	Secrets          *secrets.Box // opens the project's variables for its builds
	Tmp              string       // scratch space for checkouts, on the same disk as Deployments
	Domain           string       // e.g. pier.example.com
	PierHost         string       // where the dashboard is reached, e.g. pier.example.com; empty for none
	PierUpstream     string       // the dashboard's own listener, e.g. 127.0.0.1:7777
	ProductionBranch string       // e.g. main
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

// Source is what a deployment is built from: a pushed commit, or a folder
// uploaded by pier deploy.
type Source struct {
	GitHubJob int64  // queue record for a GitHub-triggered build
	Kind      string // git, github or cli
	Branch    string // the git branch, or the branch the folder was on
	Commit    string
	Message   string
	Target    string          // store.TargetProduction or store.TargetPreview
	Files     int             // for an upload: how many files came
	Tools     string          // for an upload: the package manager on the developer's computer, like pnpm@10.33.0
	Note      string          // said first in the log, like what a redeploy rebuilds
	OnStart   func(id string) // told the new deployment's ID as soon as it exists
	archive   string          // for an upload: the gzipped tar to build, kept afterwards as the source
	extract   func(ctx context.Context, dir string) error
}

// Deploy builds a pushed commit and, if the build works, points the branch's
// address (or production's) at it. The project is created on its first push.
func (d *Deployer) Deploy(ctx context.Context, project, branch, commit string, out io.Writer) error {
	if err := d.Store.EnsureProject(ctx, project); err != nil {
		return err
	}
	target := store.TargetPreview
	if branch == d.productionBranch(ctx, project) {
		target = store.TargetProduction
	}
	return d.run(ctx, project, Source{
		Kind: store.SourceGit, Branch: branch, Commit: commit, Target: target,
		Message: d.commitMessage(commit, project),
		extract: func(ctx context.Context, dir string) error { return d.extract(ctx, project, commit, dir) },
	}, out)
}

// DeployUpload builds a folder that pier deploy uploaded as a gzipped tar.
// A preview gets only its own address; production moves production's.
func (d *Deployer) DeployUpload(ctx context.Context, project, archive string, src Source, out io.Writer) error {
	src.Kind = store.SourceCLI
	src.archive = archive
	return d.run(ctx, project, src, out)
}

// DeployGitHub builds an immutable GitHub archive and keeps it for redeploy.
func (d *Deployer) DeployGitHub(ctx context.Context, project, archive string, src Source, out io.Writer) error {
	src.Kind = store.SourceGitHub
	src.archive = archive
	return d.run(ctx, project, src, out)
}

// ErrNoSource means a deployment's source is gone, so it can't be rebuilt.
var ErrNoSource = errors.New("the source isn't kept any more")

// Redeploy builds an existing deployment's source again, with the project's
// settings and variables as they are now. target says where it goes;
// empty keeps the old deployment's.
func (d *Deployer) Redeploy(ctx context.Context, project, id, target string, onStart func(string), out io.Writer) error {
	old, err := d.Store.Deployment(ctx, id)
	if err != nil || old.Project != project {
		return ErrNotServable
	}
	if target == "" {
		target = old.Target
	}
	src := Source{
		Kind: old.Source, Branch: old.Branch, Commit: old.Commit, Message: old.Message, Target: target,
		Note: "redeploying " + id + " with today's settings and variables", OnStart: onStart,
	}
	switch old.Source {
	case store.SourceCLI, store.SourceGitHub:
		copyPath := filepath.Join(d.Tmp, "redeploy-"+names.NewID()+".tar.gz")
		if err := copyFile(d.sourcePath(id), copyPath); err != nil {
			return ErrNoSource
		}
		src.archive = copyPath
		defer os.Remove(copyPath)
	default:
		commit := old.Commit
		src.Kind = store.SourceGit
		src.extract = func(ctx context.Context, dir string) error { return d.extract(ctx, project, commit, dir) }
	}
	return d.run(ctx, project, src, out)
}

func (d *Deployer) sourcePath(id string) string { return filepath.Join(d.Sources, id+".tar.gz") }

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// variables opens a project's variables for one scope, as NAME=value pairs.
func (d *Deployer) variables(ctx context.Context, project, scope string) ([]string, []string, error) {
	if d.Secrets == nil {
		return nil, nil, nil
	}
	all, err := d.Store.Variables(ctx, project)
	if err != nil {
		return nil, nil, err
	}
	var env, names_ []string
	for _, v := range all {
		if v.Scope != scope {
			continue
		}
		value, err := d.Secrets.Open(v.Value, SealedFor(project, scope, v.Name))
		if err != nil {
			return nil, nil, fmt.Errorf("variable %s: %w", v.Name, err)
		}
		env = append(env, v.Name+"="+value)
		names_ = append(names_, v.Name)
	}
	return env, names_, nil
}

// SealedFor is what a variable's sealed value is bound to.
func SealedFor(project, scope, name string) string { return project + "\x00" + scope + "\x00" + name }

// productionBranch is the project's own production branch, or the server's.
func (d *Deployer) productionBranch(ctx context.Context, project string) string {
	if p, err := d.Store.Project(ctx, project); err == nil && p.ProductionBranch != "" {
		return p.ProductionBranch
	}
	return d.ProductionBranch
}

// run builds src for project and points the right address at the result.
// Builds for one project run one at a time.
func (d *Deployer) run(ctx context.Context, project string, src Source, out io.Writer) error {
	unlock := d.lock(project)
	defer unlock()
	start := time.Now()

	p, err := d.Store.Project(ctx, project)
	if err != nil {
		return err
	}
	dep := store.Deployment{
		ID: names.NewID(), Project: project, Branch: src.Branch, Commit: src.Commit,
		Message: src.Message, Status: store.Building, CreatedAt: start, Target: src.Target, Source: src.Kind,
	}
	for range 5 { // six hex characters can collide, rarely
		if _, err := d.Store.Deployment(ctx, dep.ID); errors.Is(err, store.ErrNotFound) {
			break
		}
		dep.ID = names.NewID()
	}
	if src.GitHubJob != 0 {
		err = d.Store.StartGitHubDeployment(ctx, src.GitHubJob, dep)
	} else {
		err = d.Store.CreateDeployment(ctx, dep)
	}
	if err != nil {
		return err
	}
	if src.OnStart != nil {
		src.OnStart(dep.ID)
	}
	if src.archive != "" {
		// Keep the upload as this deployment's source, so it can be rebuilt.
		kept := d.sourcePath(dep.ID)
		archive := src.archive
		if err := os.MkdirAll(d.Sources, 0o755); err == nil && os.Rename(archive, kept) == nil {
			archive = kept
		}
		src.extract = func(_ context.Context, dir string) error {
			if src.Kind == store.SourceGitHub {
				return bundle.UntarGitHub(archive, dir)
			}
			return bundle.Untar(archive, dir)
		}
	}
	if logFile, err := d.createLog(dep.ID); err != nil {
		d.Log.Warn("build log can't be kept", "deployment", dep.ID, "err", err)
	} else {
		tl := &timedLog{w: logFile, start: start}
		defer func() { tl.Flush(); logFile.Close() }()
		out = io.MultiWriter(out, tl)
	}
	if src.Note != "" {
		say(out, "pier", "%s, for %s", src.Note, src.Target)
	} else if src.Kind == store.SourceCLI {
		say(out, "pier", "received %s from pier deploy, for %s", plural(src.Files, "file"), src.Target)
	} else {
		say(out, "pier", "received %s at %s", src.Branch, short(src.Commit))
	}

	buildCtx, cancel := context.WithTimeout(ctx, d.BuildTimeout)
	defer cancel()
	env, varNames, err := d.variables(ctx, project, src.Target)
	if err != nil {
		say(out, "pier", "can't open this project's variables: %s", err)
		return err
	}
	if len(varNames) > 0 {
		say(out, "build", "with %s for %s: %s", plural(len(varNames), "variable"), src.Target, strings.Join(varNames, ", "))
	}
	res, err := d.build(buildCtx, dep, src, p.Build, env, out)
	elapsed := time.Since(start)
	dep.BuildMS = elapsed.Milliseconds()
	dep.Framework, dep.Files, dep.Bytes, dep.Plan = res.label, res.files, res.bytes, res.plan
	if err != nil {
		dep.Status, dep.Error = store.Failed, err.Error()
		if ferr := d.Store.FinishDeployment(context.WithoutCancel(ctx), dep); ferr != nil {
			d.Log.Error("record failed build", "err", ferr)
		}
		say(out, "pier", "build failed after %s: %s", seconds(elapsed), err)
		if host := d.addressFor(project, src); host != "" {
			if prev := d.serving(ctx, host); prev != "" {
				say(out, "pier", "%s still serves %s", host, prev)
			}
		}
		return err
	}
	dep.Status = store.Ready
	if err := d.Store.FinishDeployment(ctx, dep); err != nil {
		return err
	}
	say(out, "deploy", "kept as %s: %s, %s, never changed again", dep.ID, plural(res.files, "file"), size(res.bytes))

	held := false
	if src.Target == store.TargetProduction {
		// After a rollback, production builds don't go live on their own.
		if p, err := d.Store.Project(ctx, project); err == nil && p.RolledBackFrom != "" {
			held = true
		}
	}
	if host := d.addressFor(project, src); host != "" && !held {
		alias := store.Alias{Project: project, Branch: src.Branch, DeploymentID: dep.ID, Kind: store.KindBranch, Host: host}
		if src.Target == store.TargetProduction {
			alias.Kind = store.KindProduction
		}
		if err := d.Store.SetAlias(ctx, alias); err != nil {
			return err
		}
	}
	if err := d.Sync(ctx); err != nil {
		say(out, "alias", "the router didn't take the change yet (%s); pierd will retry", err)
	}
	if held {
		say(out, "pier", "production is rolled back, so this build didn't replace it; promote it in the dashboard, or undo the rollback")
	} else if host := d.addressFor(project, src); host != "" {
		say(out, "alias", "%s", d.URL(host))
	}
	say(out, "alias", "%s", d.URL(names.Deployment(project, dep.ID, d.Domain)))
	say(out, "pier", "%s ready in %s", src.Target, seconds(time.Since(start)))
	return nil
}

type result struct {
	label string
	files int
	bytes int64
	plan  string
}

func (d *Deployer) build(ctx context.Context, dep store.Deployment, src Source, settings store.Build, env []string, out io.Writer) (result, error) {
	var res result
	if err := os.MkdirAll(d.Tmp, 0o755); err != nil {
		return res, err
	}
	work, err := os.MkdirTemp(d.Tmp, dep.ID+"-")
	if err != nil {
		return res, err
	}
	defer os.RemoveAll(work)

	if err := src.extract(ctx, work); err != nil {
		if src.Kind == store.SourceCLI {
			return res, fmt.Errorf("unpacking the upload: %w", err)
		}
		return res, fmt.Errorf("checking out %s: %w", short(src.Commit), err)
	}
	o := build.Overrides{Root: settings.Root, Preset: settings.Preset, Install: settings.Install, Build: settings.Command, Output: settings.Output}
	plan, root, err := build.PlanFor(work, o, src.Tools)
	if err != nil {
		return res, err
	}
	res.label = plan.Label
	if raw, err := json.Marshal(map[string]string{
		"label": plan.Label, "source": plan.Source, "root": root,
		"install": plan.Install, "build": plan.Build, "output": plan.Output,
	}); err == nil {
		res.plan = string(raw)
	}
	where := plan.Source
	if root != "." {
		where = root + "/" + where
	}
	say(out, "build", "%s, found in %s", plan.Label, where)
	if !o.Empty() {
		say(out, "build", "with this project's build settings")
	}
	if plan.Tooling != "" {
		say(out, "build", "using %s", plan.Tooling)
	}
	dir := filepath.Join(work, root)

	if plan.Build != "" || plan.Install != "" {
		say(out, "build", "running in %s", d.Runner.Name())
		log := prefixed(out, "build   ")
		for _, step := range []string{plan.Install, plan.Build} {
			if step == "" {
				continue
			}
			t := time.Now()
			say(out, "build", "$ %s", step)
			if err := d.Runner.Run(ctx, dir, step, env, log); err != nil {
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

	output := filepath.Join(dir, filepath.Clean(plan.Output))
	if rel, err := filepath.Rel(work, output); err != nil || strings.HasPrefix(rel, "..") {
		return res, fmt.Errorf("output folder %q is outside the project", plan.Output)
	}
	if fi, err := os.Stat(output); err != nil || !fi.IsDir() {
		return res, fmt.Errorf("the build didn't produce %s/; check the output folder in the project's settings", plan.Output)
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

// ErrNotServable means a deployment can't serve: it doesn't exist, belongs
// to another project, or didn't build.
var ErrNotServable = errors.New("that deployment can't serve")

// SetProduction points production at a deployment that already built, for
// a promote, a rollback or an undo. Nothing is rebuilt, and it doesn't wait
// for a build in progress: production must move the moment it's asked.
func (d *Deployer) SetProduction(ctx context.Context, project, id string) error {
	dep, err := d.Store.Deployment(ctx, id)
	if err != nil || dep.Project != project || dep.Status != store.Ready {
		return ErrNotServable
	}
	err = d.Store.SetAlias(ctx, store.Alias{
		Host: names.Production(project, d.Domain), Project: project, Kind: store.KindProduction,
		Branch: dep.Branch, DeploymentID: id,
	})
	if err != nil {
		return err
	}
	if err := d.Sync(ctx); err != nil {
		d.Log.Warn("router behind after moving production; it will retry", "err", err)
	}
	return nil
}

// addressFor is the address a source moves when it builds: production's,
// a pushed branch's, or none for a preview uploaded by pier deploy, which
// only gets its own permanent address.
func (d *Deployer) addressFor(project string, src Source) string {
	switch {
	case src.Target == store.TargetProduction:
		return names.Production(project, d.Domain)
	case src.Kind == store.SourceGit || src.Kind == store.SourceGitHub:
		return names.Branch(project, src.Branch, d.Domain)
	}
	return ""
}

// serving returns the deployment an address serves now, if any.
func (d *Deployer) serving(ctx context.Context, host string) string {
	aliases, err := d.Store.Aliases(ctx)
	if err != nil {
		return ""
	}
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
	fallback := map[string]string{}
	var routes []router.Route
	for _, dep := range ready {
		fallback[dep.ID] = fallbackFor(root(dep.ID), dep.Framework)
		routes = append(routes, router.Route{Host: names.Deployment(dep.Project, dep.ID, d.Domain), Root: root(dep.ID), DeploymentID: dep.ID, Fallback: fallback[dep.ID]})
	}
	for _, a := range aliases {
		routes = append(routes, router.Route{Host: a.Host, Root: root(a.DeploymentID), DeploymentID: a.DeploymentID, Fallback: fallback[a.DeploymentID]})
	}
	if d.PierHost != "" && d.PierUpstream != "" {
		routes = append(routes, router.Route{Host: d.PierHost, Upstream: d.PierUpstream})
	}
	return routes, nil
}

// fallbackFor decides what a path that isn't a file gets: the site's own
// 404.html when it has one, index.html for a single-page app built with
// Vite or Create React App, and otherwise a plain 404.
func fallbackFor(root, framework string) string {
	if fi, err := os.Stat(filepath.Join(root, "404.html")); err == nil && fi.Mode().IsRegular() {
		return router.FallbackPage
	}
	name, _, _ := strings.Cut(framework, " ")
	if name == "vite" || name == "create-react-app" {
		return router.FallbackSPA
	}
	return ""
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
