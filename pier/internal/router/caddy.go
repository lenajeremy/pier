// Package router keeps Caddy serving the right deployment at every address.
//
// pierd owns Caddy's whole config: after every change it builds the complete
// config from its database and loads it into Caddy. Caddy
// swaps configs gracefully, so in-flight requests finish on the old one and a
// rollback takes effect in well under a second.
package router

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Route serves one deployment's files at one host, or, with Upstream set,
// passes the host's requests on to a local server (Pier's own dashboard).
type Route struct {
	Host         string
	Root         string // the deployment's folder on disk
	Redirect     string // absolute canonical URL for a permanent redirect
	Unavailable  bool
	DeploymentID string
	Upstream     string // host:port to proxy to instead of serving files
	Wake         string // pierd's waker, host:port, for an application that is asleep
	Fallback     string // what a path that isn't a file gets: FallbackSPA, FallbackPage, or a plain 404
}

// WakeHeader names the deployment a request to the waker is for. Caddy sets
// it, replacing any a visitor sent.
const WakeHeader = "X-Pier-Wake"

// Fallbacks for a path that isn't a file.
const (
	// FallbackSPA serves index.html, so a single-page app's own routes work
	// when opened directly. Paths that look like files (/logo.png) still 404.
	FallbackSPA = "spa"
	// FallbackPage serves the site's own 404.html, with a 404 status.
	FallbackPage = "404"
)

// Caddy serves routes in process unless an external admin URL is supplied.
type Caddy struct {
	Admin   string   // optional external admin API URL
	Storage string   // embedded Caddy's certificate and key directory
	Listen  []string // where sites are served, e.g. [":8088"] in development
	HTTPS   bool     // false in development: plain HTTP, no certificates
	// AccessLog is a unix socket pierd listens on; Caddy streams every
	// request there as JSON. Empty keeps no request log.
	BaseDomain    string
	ACMECA        string // optional staging or private ACME directory
	WildcardReady func() bool
	Resolvers     []string
	AccessLog     string
	Client        *http.Client
}

// streamCloseDelay keeps proxied WebSockets and other streams open when a
// config is replaced. Pier loads a new config on every deploy of any project,
// and without the delay Caddy closes every application's streams each time.
const streamCloseDelay = "5m"

// unique keeps the first route for each host. Caddy refuses a whole config
// in which two certificate policies name one host, and at startup that would
// take every site down, so an address that two things claim goes to the
// earlier route: callers list routes in the order that should win.
func unique(routes []Route) []Route {
	seen := map[string]bool{}
	var out []Route
	for _, r := range routes {
		host := strings.ToLower(r.Host)
		if !seen[host] {
			seen[host] = true
			out = append(out, r)
		}
	}
	return out
}

// Config builds Caddy's complete JSON config for these routes.
func (c Caddy) Config(routes []Route) ([]byte, error) {
	sorted := unique(routes)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Host < sorted[j].Host })

	var handlers []any
	for _, r := range sorted {
		h := siteHandler(r)
		if r.Redirect != "" {
			h = map[string]any{"handler": "static_response", "status_code": 308,
				"headers": map[string][]string{"Location": {r.Redirect + "{http.request.uri}"}}}
		} else if r.Unavailable {
			h = map[string]any{"handler": "static_response", "status_code": 503, "body": "Application unavailable.\n", "headers": map[string][]string{"X-Pier-Deployment": {r.DeploymentID}}}
		} else if r.Wake != "" {
			// The waker starts the application, answers this request
			// through it, and moves the address back to it.
			h = map[string]any{"handler": "reverse_proxy", "upstreams": []any{map[string]any{"dial": r.Wake}}, "stream_close_delay": streamCloseDelay,
				"headers": map[string]any{
					"request":  map[string]any{"set": map[string][]string{WakeHeader: {r.DeploymentID}}},
					"response": map[string]any{"set": map[string][]string{"X-Pier-Deployment": {r.DeploymentID}}},
				}}
		} else if r.Upstream != "" {
			h = map[string]any{"handler": "reverse_proxy", "upstreams": []any{map[string]any{"dial": r.Upstream}}, "stream_close_delay": streamCloseDelay}
			if r.DeploymentID != "" {
				h["headers"] = map[string]any{"response": map[string]any{"set": map[string][]string{"X-Pier-Deployment": {r.DeploymentID}}}}
			}
		}
		handlers = append(handlers, map[string]any{
			"match":    []any{map[string]any{"host": []string{r.Host}}},
			"handle":   []any{h},
			"terminal": true,
		})
	}
	// Anything else on the server: say so, instead of Caddy's empty 200. A
	// browser gets the page; curl and scripts get one plain line.
	handlers = append(handlers, map[string]any{
		"match": []any{map[string]any{"header": map[string][]string{"Accept": {"*text/html*"}}}},
		"handle": []any{map[string]any{
			"handler":     "static_response",
			"status_code": 404,
			"headers":     map[string][]string{"Content-Type": {"text/html; charset=utf-8"}, "Cache-Control": {"no-store"}},
			"body":        notFoundPage,
		}},
		"terminal": true,
	}, map[string]any{
		"handle": []any{map[string]any{
			"handler":     "static_response",
			"status_code": 404,
			"headers":     map[string][]string{"Content-Type": {"text/plain; charset=utf-8"}},
			"body":        notFoundText,
		}},
	})

	server := map[string]any{"listen": c.Listen, "routes": handlers}
	if !c.HTTPS {
		server["automatic_https"] = map[string]any{"disable": true}
	}
	cfg := map[string]any{
		"admin": map[string]any{"disabled": true, "config": map[string]any{"persist": false}},
		"apps":  map[string]any{"http": map[string]any{"servers": map[string]any{"pier": server}}},
	}
	if c.Admin == "" && c.HTTPS {
		cfg["apps"].(map[string]any)["tls"] = c.tlsConfig(sorted)
		if ip := dashboardIP(sorted); ip != "" {
			server["tls_connection_policies"] = []any{map[string]any{"default_sni": ip}}
		}
	}
	if c.Admin != "" {
		u, err := url.Parse(c.Admin)
		if err != nil {
			return nil, fmt.Errorf("admin address: %w", err)
		}
		cfg["admin"] = map[string]any{"listen": u.Host}
	} else if c.Storage != "" {
		cfg["storage"] = map[string]any{"module": "file_system", "root": c.Storage}
	}
	if c.AccessLog != "" {
		server["logs"] = map[string]any{"default_logger_name": "pier_access"}
		cfg["logging"] = map[string]any{"logs": map[string]any{
			"pier_access": map[string]any{
				// soft_start: Caddy serves even while pierd isn't listening.
				"writer":  map[string]any{"output": "net", "address": "unix/" + c.AccessLog, "soft_start": true},
				"encoder": map[string]any{"format": "json"},
				"include": []string{"http.log.access.pier_access"},
			},
			"default": map[string]any{"exclude": []string{"http.log.access.pier_access"}},
		}}
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// siteHandler serves a deployment's files with clean URLs: /about finds
// about.html or about/index.html, like most static hosts. A path that isn't
// a file then gets the route's fallback.
func siteHandler(r Route) map[string]any {
	routes := []any{
		map[string]any{"handle": []any{map[string]any{
			"handler":  "headers",
			"response": map[string]any{"set": map[string][]string{"X-Pier-Deployment": {r.DeploymentID}}},
		}}},
		map[string]any{
			"match": []any{map[string]any{"file": map[string]any{
				"root":      r.Root,
				"try_files": []string{"{http.request.uri.path}", "{http.request.uri.path}.html", "{http.request.uri.path}/index.html"},
			}}},
			"handle": []any{map[string]any{"handler": "rewrite", "uri": "{http.matchers.file.relative}"}},
		},
	}
	// After the rewrite above, a path that's still not a file had no match.
	missing := map[string]any{"file": map[string]any{"root": r.Root, "try_files": []string{"{http.request.uri.path}"}}}
	switch r.Fallback {
	case FallbackSPA:
		routes = append(routes, map[string]any{
			// Not a file, and not shaped like one (no extension).
			"match":  []any{map[string]any{"not": []any{missing, map[string]any{"path_regexp": map[string]any{"pattern": `\.[^/]*$`}}}}},
			"handle": []any{map[string]any{"handler": "rewrite", "uri": "/index.html"}},
		})
	case FallbackPage:
		routes = append(routes, map[string]any{
			"match": []any{map[string]any{"not": []any{missing}}},
			"handle": []any{
				map[string]any{"handler": "rewrite", "uri": "/404.html"},
				map[string]any{"handler": "file_server", "root": r.Root, "hide": []string{".*"}, "status_code": 404},
			},
			"terminal": true,
		})
	}
	routes = append(routes, map[string]any{"handle": []any{map[string]any{"handler": "file_server", "root": r.Root, "hide": []string{".*"}}}})
	return map[string]any{"handler": "subroute", "routes": routes}
}

// Apply loads the config for these routes into Caddy.
func (c Caddy) Apply(ctx context.Context, routes []Route) error {
	body, err := c.Config(routes)
	if err != nil {
		return err
	}
	if c.Admin == "" {
		if err := ctx.Err(); err != nil {
			return err
		}
		return loadEmbedded(body)
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Admin+"/load", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("caddy isn't reachable at %s: %w", c.Admin, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("caddy refused the config (%s): %s", resp.Status, bytes.TrimSpace(msg))
	}
	return nil
}
