package deploy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"pier/internal/router"
	"pier/internal/store"
)

// wokenFor is how long an application that was woken stays up after its
// last visit when SleepAfter is zero. Previews then stay awake, but a
// superseded deployment someone opened still goes back to sleep.
const wokenFor = 30 * time.Minute

// wakeRetry is how long visits wait before trying again to start an
// application that failed to start. Without it, every visit to one that
// crashes at boot would hold a request for a minute.
const wakeRetry = time.Minute

// restful reports whether a sleeper's container may stop now. A superseded
// deployment stops at once unless someone woke or restarted it, and then
// sleeps again SleepAfter after the last visit. A preview sleeps SleepAfter
// after the latest of its last visit, its build and pierd's start: pierd
// doesn't know when a preview was last visited before it started.
func (d *Deployer) restful(z store.Sleeper, now time.Time) bool {
	d.wakeMu.Lock()
	if d.boot.IsZero() {
		d.boot = now
	}
	woken, wasWoken := d.woken[z.Deployment]
	boot := d.boot
	d.wakeMu.Unlock()
	if z.Superseded && !wasWoken {
		return true
	}
	window := d.SleepAfter
	if window <= 0 {
		if !z.Superseded {
			return false
		}
		window = wokenFor
	}
	last := woken
	if d.Visited != nil {
		last = later(last, d.Visited(z.Deployment))
	}
	if !z.Superseded {
		last = later(last, later(z.Ready, boot))
	}
	return now.Sub(last) >= window
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// touch records that a deployment's application was just woken or
// restarted, so the sweep leaves it up for a while.
func (d *Deployer) touch(id, name string) {
	d.wakeMu.Lock()
	defer d.wakeMu.Unlock()
	if d.woken == nil {
		d.woken = map[string]time.Time{}
	}
	d.woken[id] = time.Now()
	delete(d.wakeFails, name)
}

// slept forgets that a deployment was woken, once it is asleep again.
func (d *Deployer) slept(id string) {
	d.wakeMu.Lock()
	defer d.wakeMu.Unlock()
	delete(d.woken, id)
}

// Restart restarts a deployment's container without rebuilding it. It
// counts as a visit, so one that was asleep stays up for a while.
func (d *Deployer) Restart(ctx context.Context, id, name string) error {
	if d.Apps == nil {
		return ErrNotServable
	}
	defer d.lockApp(name)()
	err := d.Apps.Restart(ctx, name)
	if err == nil {
		d.touch(id, name)
	}
	return err
}

// SleepIdle puts idle applications to sleep every so often until ctx ends.
func (d *Deployer) SleepIdle(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := d.StopSuperseded(ctx, ""); err != nil && ctx.Err() == nil {
				d.Log.Warn("idle applications not put to sleep yet; pierd will retry", "err", err)
			}
		}
	}
}

// WakeHandler answers what Caddy sends to an application that is asleep:
// it starts the container, answers the request through it, and moves the
// application's addresses back to it. The visitor waits as long as the
// application takes to boot, at most a minute. Serve it on loopback only:
// router.WakeHeader names the deployment to wake.
func (d *Deployer) WakeHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(router.WakeHeader)
		r.Header.Del(router.WakeHeader)
		upstream, err := d.wake(r.Context(), id)
		if err != nil {
			d.Log.Warn("application didn't wake for a visit", "deployment", id, "err", err)
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusServiceUnavailable)
			io.WriteString(w, "Application unavailable.\n")
			return
		}
		proxy := &httputil.ReverseProxy{
			Rewrite: func(pr *httputil.ProxyRequest) {
				pr.SetURL(&url.URL{Scheme: "http", Host: upstream})
				pr.Out.Host = pr.In.Host
				// Caddy, which pierd trusts, already said who asked.
				for _, h := range []string{"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"} {
					if v := pr.In.Header.Values(h); len(v) > 0 {
						pr.Out.Header[h] = v
					}
				}
			},
			FlushInterval: -1,
			ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
				d.Log.Warn("woken application didn't answer", "deployment", id, "err", err)
				w.WriteHeader(http.StatusBadGateway)
			},
		}
		proxy.ServeHTTP(w, r)
	})
}

// wake starts a deployment's application if it is asleep and returns where
// it listens. A start runs to the end even if the visitor gives up, or the
// half-started container would be stopped again.
func (d *Deployer) wake(ctx context.Context, id string) (string, error) {
	if d.Apps == nil {
		return "", ErrNotServable
	}
	dep, err := d.Store.Deployment(ctx, id)
	if err != nil || dep.Status != store.Ready || dep.Runtime == "" {
		return "", ErrNotServable
	}
	name := dep.Runtime
	start, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
	defer cancel()
	unlock := d.lockApp(name)
	d.wakeMu.Lock()
	failed, hasFailed := d.wakeFails[name]
	d.wakeMu.Unlock()
	if hasFailed && time.Since(failed) < wakeRetry {
		unlock()
		return "", fmt.Errorf("%w: it failed to start less than a minute ago", ErrNotRunning)
	}
	if err = d.Apps.Resume(start, name); err != nil {
		d.wakeMu.Lock()
		if d.wakeFails == nil {
			d.wakeFails = map[string]time.Time{}
		}
		d.wakeFails[name] = time.Now()
		d.wakeMu.Unlock()
		unlock()
		return "", fmt.Errorf("%w: %v", ErrNotRunning, err)
	}
	d.touch(id, name)
	unlock()
	// A container that starts again may publish a different port, so the
	// router learns where it is now, and this visit uses the same answer.
	if err := d.syncRoutes(start, false); err != nil {
		d.Log.Warn("router behind after waking an application; it will retry", "err", err)
	}
	d.endpointMu.Lock()
	upstream := d.lastEndpoints[name]
	d.endpointMu.Unlock()
	if upstream == "" {
		return "", fmt.Errorf("%w: Docker doesn't say where it listens", ErrNotRunning)
	}
	return upstream, nil
}
