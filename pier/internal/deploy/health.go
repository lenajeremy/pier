package deploy

import (
	"context"
	"fmt"
	"sync"
	"time"

	"pier/internal/alerts"
	"pier/internal/apphost"
	"pier/internal/store"
)

// Production's servers are checked every HealthEvery. HealthFailures failed
// checks in a row make one unhealthy: Pier restarts it and sends an alert.
// While restarts don't fix it, each waits longer than the one before, from
// firstBackoff up to maxBackoff.
const (
	HealthEvery    = 30 * time.Second
	HealthFailures = 3
	firstBackoff   = time.Minute
	maxBackoff     = 30 * time.Minute
)

type healthState struct {
	store.Health
	backoff time.Duration // the wait after the last restart; doubles each time it doesn't help
}

func (d *Deployer) now() time.Time {
	if d.clock != nil {
		return d.clock()
	}
	return time.Now()
}

// tryLockApp holds a container still, like lockApp, unless something else
// is starting, stopping or restarting it right now.
func (d *Deployer) tryLockApp(name string) (func(), bool) {
	d.mu.Lock()
	if d.locks == nil {
		d.locks = map[string]*sync.Mutex{}
	}
	l, ok := d.locks["app "+name]
	if !ok {
		l = &sync.Mutex{}
		d.locks["app "+name] = l
	}
	d.mu.Unlock()
	if !l.TryLock() {
		return nil, false
	}
	return l.Unlock, true
}

// WatchHealth checks production's servers every so often until ctx ends.
// A round that's still restarting something doesn't hold up the next: each
// server is checked by one round at a time.
func (d *Deployer) WatchHealth(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			go d.CheckHealth(ctx)
		}
	}
}

// CheckHealth checks every server production serves, once. Static sites
// have no server, previews aren't checked, and a container that's asleep,
// or on its way to sleep, is left alone: checking it would wake it, or
// call its rest a failure. Checks go straight to the container's own port,
// never through the router, which would wake a sleeping one.
func (d *Deployer) CheckHealth(ctx context.Context) {
	if d.Apps == nil {
		return
	}
	apps, err := d.Store.ProductionApps(ctx)
	if err != nil {
		d.Log.Warn("health checks: list production's servers", "err", err)
		return
	}
	sleepers, err := d.Store.Sleepers(ctx, "")
	if err != nil {
		d.Log.Warn("health checks: list what may sleep", "err", err)
		return
	}
	asleep := map[string]bool{}
	for _, z := range sleepers {
		asleep[z.Name] = true
	}
	d.endpointMu.Lock()
	for name := range d.stopping {
		asleep[name] = true
	}
	d.endpointMu.Unlock()
	keep := []string{}
	var wg sync.WaitGroup
	for _, app := range apps {
		keep = append(keep, app.Deployment)
		if asleep[app.Name] {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.checkApp(ctx, app)
		}()
	}
	wg.Wait()
	d.healthMu.Lock()
	for id := range d.health {
		if !contains(keep, id) {
			delete(d.health, id)
		}
	}
	d.healthMu.Unlock()
	if err := d.Store.PruneHealth(ctx, keep); err != nil {
		d.Log.Warn("health checks: forget old deployments", "err", err)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// healthOf is a deployment's state, as the last check left it, or as pierd
// kept it before it restarted.
func (d *Deployer) healthOf(ctx context.Context, app store.ProductionApp) *healthState {
	d.healthMu.Lock()
	defer d.healthMu.Unlock()
	if d.health == nil {
		d.health = map[string]*healthState{}
	}
	if st, ok := d.health[app.Deployment]; ok {
		return st
	}
	st := &healthState{Health: store.Health{Deployment: app.Deployment, Project: app.Project}}
	if kept, ok, err := d.Store.HealthOf(ctx, app.Deployment); err == nil && ok {
		st.Health = kept
		if st.State == store.Restarting { // pierd stopped during the restart
			st.State = store.Unhealthy
		}
	}
	d.health[app.Deployment] = st
	return st
}

// mayBeOpen says whether an alert about a project's health might still be
// open. After pierd starts nobody knows, so the first check that passes
// resolves it anyway: the alert only goes out if there was one.
func (d *Deployer) mayBeOpen(project string) bool {
	d.healthMu.Lock()
	defer d.healthMu.Unlock()
	open, known := d.healthOpen[project]
	return open || !known
}

func (d *Deployer) setOpen(project string, open bool) {
	d.healthMu.Lock()
	defer d.healthMu.Unlock()
	if d.healthOpen == nil {
		d.healthOpen = map[string]bool{}
	}
	d.healthOpen[project] = open
}

func (d *Deployer) checkApp(ctx context.Context, app store.ProductionApp) {
	unlock, ok := d.tryLockApp(app.Name)
	if !ok {
		return // being started, stopped or restarted: the next round checks it
	}
	defer unlock()
	st := d.healthOf(ctx, app)
	checking, cancel := context.WithTimeout(ctx, 15*time.Second)
	res, err := d.Apps.Check(checking, app.Name, apphost.Probe{Path: app.HealthPath, Off: app.HealthOff})
	cancel()
	if err != nil {
		if ctx.Err() == nil {
			d.Log.Warn("health check: Docker didn't answer; trying again next round", "project", app.Project, "err", err)
		}
		return
	}
	now := d.now()
	st.CheckedAt, st.Asked = now, res.Asked
	if res.Failure == "" {
		d.passed(ctx, app, st, now)
	} else {
		d.failed(ctx, app, st, res, now)
	}
	if err := d.Store.SaveHealth(context.WithoutCancel(ctx), st.Health); err != nil {
		d.Log.Warn("health check: keep the result", "err", err)
	}
}

func (d *Deployer) passed(ctx context.Context, app store.ProductionApp, st *healthState, now time.Time) {
	was := st.State
	st.Failures = 0
	if was != store.Healthy {
		st.State, st.Since = store.Healthy, now
		// Restarts count within one spell of failing; the last failure stays.
		st.backoff, st.NextRestart, st.Restarts = 0, time.Time{}, 0
	}
	recovered := was == store.Unhealthy || was == store.Restarting
	if recovered {
		d.Log.Info("production passed its health check again", "project", app.Project, "deployment", app.Deployment)
		if err := d.Store.RecordEvent(ctx, app.Project, "Passed its health check again", st.Asked, app.Deployment, ""); err != nil {
			d.Log.Warn("record activity", "err", err)
		}
	}
	if (recovered || d.mayBeOpen(app.Project)) && d.Alerts != nil {
		d.Alerts.Resolve(ctx, alerts.Event{
			Kind: alerts.HealthRecovered, Key: "health:" + app.Project, Project: app.Project, Deployment: app.Deployment,
			Title:   app.Project + ": production is healthy again",
			Summary: fmt.Sprintf("%s passed its health check again (%s).", app.Host, st.Asked),
			Link:    "/projects/" + app.Project + "/deployments/" + app.Deployment,
		})
	}
	d.setOpen(app.Project, false)
}

func (d *Deployer) failed(ctx context.Context, app store.ProductionApp, st *healthState, res apphost.CheckResult, now time.Time) {
	st.Failures++
	st.LastFailure, st.LastFailureAt = res.Failure, now
	if st.State == store.Healthy || st.State == "" {
		st.State, st.Since = store.Failing, now
	}
	if st.Failures < HealthFailures {
		return
	}
	// Since stays when the first failure in the row was: that's when the
	// trouble began.
	first := st.State != store.Unhealthy
	if first {
		st.State = store.Unhealthy
		d.Log.Warn("production failed its health check", "project", app.Project, "deployment", app.Deployment, "why", res.Failure)
	}
	if now.Before(st.NextRestart) {
		return // the last restart didn't help; the next waits longer
	}
	// Its own output says why, so the alert quotes it before the restart.
	var lines []string
	if logs, err := d.Apps.Logs(ctx, app.Name, alerts.MaxLines, ""); err == nil {
		for _, l := range logs {
			lines = append(lines, fmt.Sprintf("%-7s %s", l.Stream, l.Text))
		}
	}
	st.State = store.Restarting
	if err := d.Store.SaveHealth(context.WithoutCancel(ctx), st.Health); err != nil {
		d.Log.Warn("health check: keep the result", "err", err)
	}
	restarting, cancel := context.WithTimeout(ctx, 90*time.Second)
	err := d.Apps.Restart(restarting, app.Name)
	cancel()
	st.State, st.Failures = store.Unhealthy, 0
	st.Restarts++
	st.RestartedAt = d.now()
	st.backoff = min(max(st.backoff*2, firstBackoff), maxBackoff)
	st.NextRestart = st.RestartedAt.Add(st.backoff)
	after := " Pier restarted its container, and checks it again every " + alerts.Span(HealthEvery) + "."
	if err != nil {
		d.Log.Warn("restarting an unhealthy container failed", "project", app.Project, "container", app.Name, "err", err)
		after = fmt.Sprintf(" Pier restarted its container, but it didn't come back: %s. It tries again in %s.", err, alerts.Span(st.backoff))
	} else {
		d.Log.Info("restarted an unhealthy container", "project", app.Project, "container", app.Name)
	}
	if err := d.Store.RecordEvent(ctx, app.Project, "Restarted an unhealthy container", res.Failure, app.Deployment, ""); err != nil {
		d.Log.Warn("record activity", "err", err)
	}
	if err := d.syncRoutes(ctx, false); err != nil {
		d.Log.Warn("router behind after restarting an unhealthy container", "err", err)
	}
	if first && d.Alerts != nil {
		d.Alerts.Raise(ctx, alerts.Event{
			Kind: alerts.HealthFailing, Key: "health:" + app.Project, Project: app.Project, Deployment: app.Deployment,
			Title:   app.Project + ": production is failing its health check",
			Summary: fmt.Sprintf("%s failed %d health checks in a row, %s apart, because %s.", app.Host, HealthFailures, alerts.Span(HealthEvery), res.Failure) + after,
			Lines:   lines,
			Link:    "/projects/" + app.Project + "/deployments/" + app.Deployment,
		})
		d.setOpen(app.Project, true)
	}
}
