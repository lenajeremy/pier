package apphost

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
)

// Probe says how a health check asks an application whether it's well.
type Probe struct {
	Path string // the path to request; empty uses PIER_HEALTH_PATH, then /
	Off  bool   // ask nothing: only check that the process runs
}

// CheckResult is what one health check found.
type CheckResult struct {
	Asked   string // what was checked: "GET /health", or "the process"
	Failure string // why it failed, in words; empty when it passed
}

// healthClient asks for a health check's path. It waits longer than the
// startup check does, since a busy application can be slow and still well.
var healthClient = &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// Check asks one running container whether it's healthy. It never starts
// a container that isn't running, and asks it nothing through the router,
// so a sleeping application stays asleep. An error means Docker didn't
// answer, which says nothing about the application.
func (h *Host) Check(ctx context.Context, name string, p Probe) (CheckResult, error) {
	c, err := h.engine(ctx)
	if err != nil {
		return CheckResult{}, err
	}
	res := CheckResult{Asked: "the process"}
	app, err := h.owned(ctx, c, name)
	if errors.Is(err, ErrNotFound) {
		res.Failure = "its container is gone"
		return res, nil
	}
	if err != nil {
		return res, err
	}
	if app.State == nil {
		return res, errors.New("Docker returned no application state")
	}
	switch {
	case app.State.OOMKilled && !app.State.Running:
		res.Failure = "its process ran out of memory and was stopped"
		return res, nil
	case app.State.Restarting:
		res.Failure = fmt.Sprintf("its process keeps stopping (it last exited with code %d), and Docker is restarting it", app.State.ExitCode)
		return res, nil
	case app.State.Paused:
		res.Failure = "its container is paused"
		return res, nil
	case !app.State.Running:
		res.Failure = fmt.Sprintf("its process isn't running (it exited with code %d)", app.State.ExitCode)
		return res, nil
	}
	if app.State.Health != nil && app.State.Health.Status == container.Unhealthy {
		res.Failure = "the image's own HEALTHCHECK says it's unhealthy"
		return res, nil
	}
	if p.Off {
		return res, nil
	}
	path, strict := p.Path, true
	if path == "" && app.Config != nil {
		path = envValue(app.Config.Env, "PIER_HEALTH_PATH")
	}
	if path == "" {
		path, strict = "/", false
	}
	res.Asked = "GET " + path
	u, err := url.ParseRequestURI(path)
	if err != nil || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		res.Failure = "the health check path isn't a local path"
		return res, nil
	}
	ports := loopbackPorts(app)
	if len(ports) == 0 {
		res.Failure = "its container publishes no port"
		return res, nil
	}
	u.Scheme, u.Host = "http", "127.0.0.1:"+ports[0]
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return res, err
	}
	req.Header.Set("User-Agent", "Pier-Health-Check")
	resp, err := healthClient.Do(req)
	if err != nil {
		var nerr net.Error
		if errors.As(err, &nerr) && nerr.Timeout() {
			res.Failure = res.Asked + " didn't answer within 5 seconds"
		} else {
			res.Failure = res.Asked + " got no answer: " + cause(err)
		}
		return res, nil
	}
	resp.Body.Close()
	// Without a path of its own, any answer but a server error will do, as
	// at startup: an app with no page at / still answers 404.
	if resp.StatusCode >= 500 || (strict && resp.StatusCode >= 400) {
		res.Failure = res.Asked + " answered " + resp.Status
	}
	return res, nil
}

// loopbackPorts are the ports a container publishes on loopback, which is
// how Pier reaches it.
func loopbackPorts(app container.InspectResponse) []string {
	var out []string
	if app.NetworkSettings == nil {
		return nil
	}
	for _, bindings := range app.NetworkSettings.Ports {
		for _, b := range bindings {
			if b.HostIP == loopback && b.HostPort != "" {
				out = append(out, b.HostPort)
			}
		}
	}
	return out
}

// cause is the end of a network error, without the URL it was about.
func cause(err error) string {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		err = uerr.Err
	}
	var oerr *net.OpError
	if errors.As(err, &oerr) && oerr.Err != nil {
		err = oerr.Err
	}
	return err.Error()
}
