// Package deploy turns a pushed commit into a live address: it extracts the
// commit, builds it, keeps the output as a deployment that never changes,
// points the branch's address (or production) at it, and reloads the router.
// Everything it does is narrated to the pusher's terminal.
package deploy

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"pier/internal/apphost"
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
	PreviewRailpack  *build.Railpack
	Apps             *apphost.Host
	Router           router.Caddy
	URL              func(host string) string // how an address is reached, for the log
	Log              *slog.Logger
	BuildTimeout     time.Duration
	// Waker is pierd's loopback listener for WakeHandler. An application that
	// is asleep is routed there, so the next visit starts it. Empty answers
	// "unavailable" instead.
	Waker string
	// SleepAfter is how long a preview's application runs without a visit
	// before it sleeps, and how long one that was woken stays up. Zero keeps
	// previews awake.
	SleepAfter time.Duration
	// Visited reports the last request a deployment answered, from the
	// request log. Nil counts none.
	Visited func(deployment string) time.Time
	// Alerts hears about every build that fails, and the next one of the
	// same address that works, and decides what to send. Nil sends nothing.
	Alerts Alerter
	// Databases gives a project's deployments DATABASE_URL when it has a
	// Postgres database. Nil gives none.
	Databases Databases

	mu         sync.Mutex
	locks      map[string]*sync.Mutex
	logChanges map[string]chan struct{}
	routeMu    sync.Mutex // serializes snapshots and loads across projects
	lastRoutes []router.Route
	hasApps    bool
	dirty      bool // protected by routeMu

	endpointMu    sync.Mutex
	lastEndpoints map[string]string // where each application listened when Docker last answered
	stopping      map[string]bool   // applications going to sleep, routed to the waker already
	stopsBehind   atomic.Bool       // stopping superseded applications failed and is retried

	wakeMu    sync.Mutex
	woken     map[string]time.Time // deployment -> when it was last woken or restarted
	wakeFails map[string]time.Time // application -> when it last failed to start for a visit
	boot      time.Time            // when the first sweep ran, which stands for pierd's start

	healthMu   sync.Mutex
	health     map[string]*healthState // deployment -> how its checks are going, while production serves it
	healthOpen map[string]bool         // project -> whether an alert about its health may still be open
	clock      func() time.Time        // what time it is; tests move it on. Nil is time.Now.
}

// RepoPath is where a project's bare repository lives.
func (d *Deployer) RepoPath(project string) string { return filepath.Join(d.Repos, project+".git") }

// lock makes a project's builds run one at a time.
func (d *Deployer) lock(project string) func() { return d.lockKey("project " + project) }

// lockApp holds one container still while it is stopped, or while it is
// started and production is pointed at it, so neither undoes the other.
func (d *Deployer) lockApp(name string) func() { return d.lockKey("app " + name) }

func (d *Deployer) lockKey(key string) func() {
	d.mu.Lock()
	if d.locks == nil {
		d.locks = map[string]*sync.Mutex{}
	}
	l, ok := d.locks[key]
	if !ok {
		l = &sync.Mutex{}
		d.locks[key] = l
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
	SentBy    string          // who uploaded it or asked for the rebuild, by name; empty for a push
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
// empty keeps the old deployment's. by is who asked, by name.
func (d *Deployer) Redeploy(ctx context.Context, project, id, target, by string, onStart func(string), out io.Writer) error {
	old, err := d.Store.Deployment(ctx, id)
	if err != nil || old.Project != project {
		return ErrNotServable
	}
	if target == "" {
		target = old.Target
	}
	src := Source{
		Kind: old.Source, Branch: old.Branch, Commit: old.Commit, Message: old.Message, Target: target, SentBy: by,
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
	all, err := d.Store.BuildVariables(ctx, project, scope)
	if err != nil {
		return nil, nil, err
	}
	var env, names_ []string
	for _, v := range all {
		binding := SealedFor(project, scope, v.Name)
		if v.Project == "" {
			binding = SharedBinding(v.Name)
		}
		value, err := d.Secrets.Open(v.Value, binding)
		if err != nil {
			return nil, nil, fmt.Errorf("variable %s: %w", v.Name, err)
		}
		env = append(env, v.Name+"="+value)
		names_ = append(names_, v.Name)
	}
	// Production also gets the cron secret, so its app can tell a job's
	// request is from Pier. The build log names only the owner's own.
	env, err = d.withCronSecret(ctx, project, scope, env)
	return env, names_, err
}

// SealedFor is what a variable's sealed value is bound to.
func SealedFor(project, scope, name string) string { return project + "\x00" + scope + "\x00" + name }

func SharedBinding(name string) string { return "shared\x00" + name }

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
	var dep store.Deployment
	// The alert is told last, once the build log is complete, so a failure's
	// alert can quote its last lines.
	defer func() { d.reportBuild(context.WithoutCancel(ctx), dep, src) }()

	p, err := d.Store.Project(ctx, project)
	if err != nil {
		return err
	}
	dep = store.Deployment{
		ID: names.NewID(), Project: project, Branch: src.Branch, Commit: src.Commit,
		Message: src.Message, Status: store.Building, CreatedAt: start, Target: src.Target, Source: src.Kind,
		SentBy: src.SentBy,
	}
	for range 5 { // six hex characters can collide, rarely
		if taken, err := d.Store.IDTaken(ctx, dep.ID); err == nil && !taken {
			break
		}
		dep.ID = names.NewID()
	}
	finishLog, err := d.beginLog(dep.ID)
	if err != nil {
		return err
	}
	defer finishLog()
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
		tl := &timedLog{w: logFile, start: start, changed: func() { d.signalLog(dep.ID, false) }}
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
		dep.Status, dep.Error, dep.BuildMS = store.Failed, err.Error(), time.Since(start).Milliseconds()
		if ferr := d.Store.FinishDeployment(context.WithoutCancel(ctx), dep); ferr != nil {
			d.Log.Error("record failed build", "err", ferr)
		}
		say(out, "pier", "can't open this project's variables: %s", err)
		return err
	}
	if len(varNames) > 0 {
		say(out, "build", "with %s for %s: %s", plural(len(varNames), "variable"), src.Target, strings.Join(varNames, ", "))
	}
	env, networks, dbNote, err := d.withDatabase(ctx, project, src.Target, src.Branch, env)
	if err != nil {
		dep.Status, dep.Error, dep.BuildMS = store.Failed, "the project's database: "+err.Error(), time.Since(start).Milliseconds()
		if ferr := d.Store.FinishDeployment(context.WithoutCancel(ctx), dep); ferr != nil {
			d.Log.Error("record failed build", "err", ferr)
		}
		say(out, "pier", "can't give this build its database: %s", err)
		return err
	}
	if dbNote != "" {
		say(out, "build", "%s", dbNote)
	}
	res, err := d.build(buildCtx, dep, src, p.Build, p.Runtime, env, networks, out)
	elapsed := time.Since(start)
	dep.BuildMS = elapsed.Milliseconds()
	dep.Framework, dep.Files, dep.Bytes, dep.Plan = res.label, res.files, res.bytes, res.plan
	if err != nil {
		dep.Status, dep.Error = store.Failed, err.Error()
		if ferr := d.Store.FinishDeployment(context.WithoutCancel(ctx), dep); ferr != nil {
			d.Log.Error("record failed build", "err", ferr)
		}
		if d.Apps != nil {
			clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
			if err := d.CleanupApps(clean); err != nil {
				d.Log.Warn("application cleanup deferred", "err", err)
			}
			cancel()
		}
		say(out, "pier", "build failed after %s: %s", seconds(elapsed), err)
		if host := d.addressFor(ctx, project, src); host != "" {
			if prev := d.serving(ctx, host); prev != "" {
				say(out, "pier", "%s still serves %s", host, prev)
			}
		}
		return err
	}
	dep.Status = store.Ready
	if err := d.Store.FinishDeployment(context.WithoutCancel(ctx), dep); err != nil {
		return err
	}
	if res.server {
		say(out, "deploy", "application %s passed its startup check", dep.ID)
	} else {
		say(out, "deploy", "kept as %s: %s, %s, never changed again", dep.ID, plural(res.files, "file"), size(res.bytes))
	}

	held := false
	if src.Target == store.TargetProduction {
		// After a rollback, production builds don't go live on their own.
		if p, err := d.Store.Project(ctx, project); err == nil && p.RolledBackFrom != "" {
			held = true
		}
	}
	if host := d.addressFor(ctx, project, src); host != "" && !held {
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
	} else if host := d.addressFor(ctx, project, src); host != "" {
		say(out, "alias", "%s", d.URL(host))
	}
	say(out, "alias", "%s", d.URL(names.Deployment(project, dep.ID, d.Domain)))
	say(out, "pier", "%s ready in %s", src.Target, seconds(time.Since(start)))
	// Only now that the new deployment serves is the one it replaced stopped.
	d.StopSupersededLater(project)
	return nil
}

type result struct {
	server bool
	label  string
	files  int
	bytes  int64
	plan   string
}

func (d *Deployer) build(ctx context.Context, dep store.Deployment, src Source, settings store.Build, runtime store.Runtime, env, networks []string, out io.Writer) (result, error) {
	var res result
	if err := os.MkdirAll(d.Tmp, 0o755); err != nil {
		return res, err
	}
	work, err := os.MkdirTemp(d.Tmp, dep.ID+"-")
	if err != nil {
		return res, err
	}
	defer os.RemoveAll(work)

	checkout := filepath.Join(work, "src")
	if err := os.Mkdir(checkout, 0o755); err != nil {
		return res, err
	}
	if err := src.extract(ctx, checkout); err != nil {
		if src.Kind == store.SourceCLI {
			return res, fmt.Errorf("unpacking the upload: %w", err)
		}
		return res, fmt.Errorf("checking out %s: %w", short(src.Commit), err)
	}
	o := build.Overrides{Root: settings.Root, Preset: settings.Preset, Install: settings.Install, Build: settings.Command, Output: settings.Output}
	railpack := d.PreviewRailpack != nil && src.Target == store.TargetPreview
	if err := build.CheckTree(checkout); err != nil {
		return res, err
	}
	plan, root, err := build.PlanFor(checkout, o, src.Tools)
	if err != nil {
		return res, err
	}
	res.label = plan.Label
	if raw, err := json.Marshal(map[string]string{
		"label": plan.Label, "source": plan.Source, "root": root,
		"install": plan.Install, "build": plan.Build, "output": plan.Output, "runtime": plan.Runtime,
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
	dir := filepath.Join(checkout, root)
	output := filepath.Join(dir, plan.Output)

	if plan.Runtime != "" {
		if d.Apps == nil {
			return res, errors.New("application runtime is unavailable")
		}
		if plan.Runtime == "server" && runtime.StartCommand != "" {
			plan.Start = runtime.StartCommand
		}
		name := "pier-app-" + strings.ToLower(rand.Text())
		if err := d.Store.TrackApp(ctx, dep.ID, name); err != nil {
			return res, err
		}
		log := prefixed(out, "build   ")
		say(out, "build", "building a runnable image with %s", plan.Label)
		err := build.BuildImage(ctx, dir, work, dep.Project, name+":app", plan, env, log)
		log.Flush()
		if err != nil {
			return res, err
		}
		opts := apphost.StartOptions{
			HealthPath: runtime.HealthPath,
			HealthOff:  runtime.HealthOff,
			MemoryMB:   runtime.MemoryMB,
			CPUs:       runtime.CPUs,
			Networks:   networks,
		}
		if plan.Runtime == "dockerfile" && runtime.StartCommand != "" {
			opts.StartCommand = runtime.StartCommand
		}
		say(out, "deploy", "starting the process and checking HTTP readiness")
		if err := d.Apps.StartWithOptions(ctx, name, env, opts); err != nil {
			return res, fmt.Errorf("starting application: %w", err)
		}
		res.server = true
		return res, nil
	}
	if railpack {
		say(out, "build", "using Railpack for this preview")
		log := prefixed(out, "build   ")
		output, err = d.PreviewRailpack.Export(ctx, dir, work, dep.Project, plan, o, env, log)
		log.Flush()
		if err != nil {
			return res, err
		}
	} else if plan.Build != "" || plan.Install != "" {
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

	rel, err := filepath.Rel(work, output)
	if err != nil || strings.HasPrefix(rel, "..") {
		return res, fmt.Errorf("output folder %q is outside the project", plan.Output)
	}
	workRoot, err := os.OpenRoot(work)
	if err != nil {
		return res, err
	}
	fi, lerr := workRoot.Lstat(rel)
	workRoot.Close()
	if lerr != nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return res, fmt.Errorf("the build didn't produce %s/; check the output folder in the project's settings", plan.Output)
	}
	if err := build.CheckTree(output); err != nil {
		return res, fmt.Errorf("check output folder %q: %w", plan.Output, err)
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
	if commit == "" || strings.HasPrefix(commit, "-") {
		return fmt.Errorf("invalid commit %q", commit)
	}
	archive := exec.CommandContext(ctx, "git", "--git-dir", d.RepoPath(project), "archive", "--format=tar", commit)
	untar := exec.CommandContext(ctx, "tar", "-x", "-C", dir)
	pipe, err := archive.StdoutPipe()
	if err != nil {
		return err
	}
	var archiveErr, untarErr bytes.Buffer
	archive.Stderr, untar.Stderr = &archiveErr, &untarErr
	untar.Stdin = pipe
	if err := archive.Start(); err != nil {
		return err
	}
	if err := untar.Run(); err != nil {
		archive.Wait()
		msg := strings.TrimSpace(untarErr.String() + " " + archiveErr.String())
		return fmt.Errorf("%w: %s", err, msg)
	}
	if err := archive.Wait(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(archiveErr.String()))
	}
	return nil
}

func (d *Deployer) commitMessage(commit, project string) string {
	if commit == "" || strings.HasPrefix(commit, "-") {
		return ""
	}
	out, err := exec.Command("git", "--git-dir", d.RepoPath(project), "log", "-1", "--format=%s", commit, "--").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// ErrNotServable means a deployment can't serve: it doesn't exist, belongs
// to another project, or didn't build.
var ErrNotServable = errors.New("that deployment can't serve")

// ErrNotRunning means a deployment's application couldn't be started, so
// production stayed where it was.
var ErrNotRunning = errors.New("the application didn't start")

// SetProduction points production at a deployment that already built, for
// a promote, a rollback or an undo. Nothing is rebuilt, and it doesn't wait
// for a build in progress: production must move the moment it's asked. The
// caller stops what this leaves superseded, once it has recorded the move.
func (d *Deployer) SetProduction(ctx context.Context, project, id string) error {
	dep, err := d.Store.Deployment(ctx, id)
	if err != nil || dep.Project != project || dep.Status != store.Ready {
		return ErrNotServable
	}
	if err := d.pointProduction(ctx, dep); err != nil {
		return err
	}
	if err := d.Sync(ctx); err != nil {
		d.Log.Warn("router behind after moving production; it will retry", "err", err)
	}
	return nil
}

// pointProduction moves production's alias to dep. An application that was
// superseded is stopped, so its container is started and answering before
// the alias moves, and nothing may stop it in between. That takes as long as
// the application takes to boot, and fails after a minute.
func (d *Deployer) pointProduction(ctx context.Context, dep store.Deployment) error {
	if dep.Runtime != "" {
		if d.Apps == nil {
			return ErrNotServable
		}
		defer d.lockApp(dep.Runtime)()
		if err := d.Apps.Resume(ctx, dep.Runtime); err != nil {
			return fmt.Errorf("%w: %v", ErrNotRunning, err)
		}
	}
	return d.Store.SetAlias(ctx, store.Alias{
		Host: d.ProductionHost(ctx, dep.Project), Project: dep.Project, Kind: store.KindProduction,
		Branch: dep.Branch, DeploymentID: dep.ID,
	})
}

// ProductionHost is a project's production address under the base domain:
// its name, or the name its owner gave the address instead.
func (d *Deployer) ProductionHost(ctx context.Context, project string) string {
	label := project
	if p, err := d.Store.Project(ctx, project); err == nil {
		label = p.Label()
	}
	return names.Production(label, d.Domain)
}

// addressFor is the address a source moves when it builds: production's,
// a pushed branch's, or none for a preview uploaded by pier deploy, which
// only gets its own permanent address.
func (d *Deployer) addressFor(ctx context.Context, project string, src Source) string {
	switch {
	case src.Target == store.TargetProduction:
		return d.ProductionHost(ctx, project)
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
	byID := map[string]router.Route{}
	var endpoints map[string]string
	probed := false
	var own []router.Route
	for _, dep := range ready {
		r := router.Route{DeploymentID: dep.ID, Host: names.Deployment(dep.Project, dep.ID, d.Domain)}
		if dep.Runtime != "" {
			if !probed {
				endpoints, probed = d.endpoints(ctx), true
			}
			// A stopped container has no endpoint, and its addresses wake
			// it. Without a waker they say the application is unavailable.
			r.Upstream = endpoints[dep.Runtime]
			if r.Upstream == "" && d.Waker != "" {
				r.Wake = d.Waker
			}
			r.Unavailable = r.Upstream == "" && r.Wake == ""
		} else {
			r.Root = filepath.Join(d.Deployments, dep.ID)
			r.Fallback = fallbackFor(r.Root, dep.Framework)
		}
		byID[dep.ID] = r
		own = append(own, r)
	}
	// Two things can claim one address: a project named blog-c19e77 and
	// deployment c19e77 of blog, or a custom domain under a base domain saved
	// later. The router keeps the first route for a host, so this order says
	// who wins: the dashboard, which the owner must always reach, then
	// production and branches, then deployments' own addresses, then custom
	// domains.
	var routes []router.Route
	if d.PierHost != "" && d.PierUpstream != "" {
		routes = append(routes, router.Route{Host: d.PierHost, Upstream: d.PierUpstream})
	}
	appendHost := func(id, host string) {
		if r, ok := byID[id]; ok {
			r.Host = host
			routes = append(routes, r)
		}
	}

	production := map[string]string{}
	for _, a := range aliases {
		if a.Kind == store.KindProduction {
			production[a.Project] = a.DeploymentID
		}
		appendHost(a.DeploymentID, a.Host)
	}
	routes = append(routes, own...)
	domains, err := d.Store.Domains(ctx, "")
	if err != nil {
		return nil, err
	}
	for _, domain := range domains {
		id := production[domain.Project]
		if !domain.Verified || id == "" {
			continue
		}
		appendHost(id, domain.Host)
		if domain.Redirect != "" {
			routes = append(routes, router.Route{Host: domain.Redirect, Redirect: d.URL(domain.Host)})
		}
	}
	return routes, nil
}

// endpoints asks Docker where each running application listens. When Docker
// doesn't answer in time, because it is busy with builds or restarting, the
// last answer stands: a config with every application unavailable would turn
// one slow probe into an outage. Applications keep their loopback ports
// across a Docker restart, so the last answer stays right.
func (d *Deployer) endpoints(ctx context.Context) map[string]string {
	if d.Apps == nil {
		return nil
	}
	probe, cancel := context.WithTimeout(ctx, 3*time.Second)
	current, err := d.Apps.Endpoints(probe)
	cancel()
	d.endpointMu.Lock()
	defer d.endpointMu.Unlock()
	if err != nil {
		d.Log.Warn("application endpoints unavailable; keeping the last known ones", "err", err)
	} else {
		d.lastEndpoints = current
	}
	if len(d.stopping) == 0 {
		return d.lastEndpoints
	}
	out := maps.Clone(d.lastEndpoints)
	for name := range d.stopping {
		delete(out, name)
	}
	return out
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
func (d *Deployer) Sync(ctx context.Context) error { return d.syncRoutes(ctx, true) }
func (d *Deployer) syncRoutes(ctx context.Context, force bool) error {
	d.routeMu.Lock()
	defer d.routeMu.Unlock()
	routes, err := d.Routes(ctx)
	if err == nil {
		d.hasApps = slices.ContainsFunc(routes, func(r router.Route) bool {
			return r.DeploymentID != "" && (r.Upstream != "" || r.Wake != "" || r.Unavailable)
		})
		if force || d.dirty || !slices.Equal(routes, d.lastRoutes) {
			err = d.Router.Apply(ctx, routes)
			if err == nil {
				d.lastRoutes = routes
			}
		}
	}
	d.dirty = err != nil
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
			d.routeMu.Lock()
			dirty := d.dirty || d.hasApps
			d.routeMu.Unlock()
			if !dirty {
				continue
			}
			if err := d.syncRoutes(ctx, false); err != nil {
				d.Log.Warn("router still out of date", "err", err)
			}
		}
	}
}

// CleanupApps retries partial Docker cleanup using durable ownership records.
func (d *Deployer) CleanupApps(ctx context.Context) error {
	names, err := d.Store.UnusedApps(ctx)
	if err != nil {
		return err
	}
	for _, name := range names {
		if d.Apps == nil {
			return errors.New("application runtime is unavailable")
		}
		if err := d.Apps.Remove(ctx, name); err != nil {
			return err
		}
		if err := d.Store.ForgetApp(ctx, name); err != nil {
			return err
		}
	}
	return nil
}

// StopSuperseded puts to sleep the containers that nothing needs running,
// in one project or, with project empty, in all of them: a superseded
// deployment's at once, and a preview's once nobody has visited it for
// SleepAfter. Each keeps its image and its own address, and the next visit
// there wakes it (see WakeHandler). Without this every server deployment
// ever built would stay in memory. store.Sleepers says what may sleep.
func (d *Deployer) StopSuperseded(ctx context.Context, project string) error {
	err := d.stopSuperseded(ctx, project)
	if err != nil {
		d.stopsBehind.Store(true)
	}
	return err
}

func (d *Deployer) stopSuperseded(ctx context.Context, project string) error {
	if d.Apps == nil {
		return nil
	}
	sleepers, err := d.Store.Sleepers(ctx, project)
	if err != nil {
		return err
	}
	now := time.Now()
	var due []store.Sleeper
	for _, z := range sleepers {
		if d.restful(z, now) {
			due = append(due, z)
		}
	}
	if len(due) == 0 {
		return nil
	}
	// Most of them are asleep already, and a sweep that finds nothing to do
	// shouldn't reload the router.
	running, err := d.Apps.Running(ctx)
	if err != nil {
		return err
	}
	due = slices.DeleteFunc(due, func(z store.Sleeper) bool { return !running[z.Name] })
	if len(due) == 0 {
		return nil
	}
	// The router must have left a deployment before its container goes: an
	// alias that has moved here but not yet in Caddy still reaches the old one.
	if err := d.syncRoutes(ctx, false); err != nil {
		return err
	}
	stopped := false
	for _, z := range due {
		did, err := d.stopIfIdle(ctx, project, z.Name)
		if err != nil {
			return err
		}
		stopped = stopped || did
	}
	if !stopped {
		return nil
	}
	return d.syncRoutes(ctx, false)
}

// stopIfIdle stops one container unless something came to need it since it
// was listed, like a rollback that is starting it right now or a visit. Its
// addresses move to the waker first, so a visit while it stops wakes it
// again instead of failing.
func (d *Deployer) stopIfIdle(ctx context.Context, project, name string) (bool, error) {
	defer d.lockApp(name)()
	sleepers, err := d.Store.Sleepers(ctx, project)
	if err != nil {
		return false, err
	}
	i := slices.IndexFunc(sleepers, func(z store.Sleeper) bool { return z.Name == name })
	if i < 0 || !d.restful(sleepers[i], time.Now()) {
		return false, nil
	}
	d.endpointMu.Lock()
	if d.stopping == nil {
		d.stopping = map[string]bool{}
	}
	d.stopping[name] = true
	d.endpointMu.Unlock()
	defer func() {
		d.endpointMu.Lock()
		delete(d.stopping, name)
		d.endpointMu.Unlock()
	}()
	if err := d.syncRoutes(ctx, false); err != nil {
		return false, err
	}
	if err := d.Apps.Stop(ctx, name); err != nil {
		return false, err
	}
	d.slept(sleepers[i].Deployment)
	if sleepers[i].Superseded {
		d.Log.Info("stopped a superseded application", "container", name)
	} else {
		d.Log.Info("put a preview to sleep", "container", name, "idle", d.SleepAfter)
	}
	return true, nil
}

// StopSupersededLater stops superseded applications in the background. A
// container can take ten seconds to exit, and nobody waiting for a deploy or
// a rollback should wait for that too.
func (d *Deployer) StopSupersededLater(project string) {
	if d.Apps == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := d.StopSuperseded(ctx, project); err != nil {
			d.Log.Warn("superseded applications not stopped yet; pierd will retry", "err", err)
		}
	}()
}

// RetryStops stops superseded applications again if the last attempt failed,
// for example because Docker was restarting.
func (d *Deployer) RetryStops(ctx context.Context) error {
	if !d.stopsBehind.Swap(false) {
		return nil
	}
	return d.StopSuperseded(ctx, "")
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
