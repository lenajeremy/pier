// Package router keeps Caddy serving the right deployment at every address.
//
// pierd owns Caddy's whole config: after every change it builds the complete
// config from its database and loads it through Caddy's admin API. Caddy
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
	"time"
)

// Route serves one deployment's files at one host, or, with Upstream set,
// passes the host's requests on to a local server (Pier's own dashboard).
type Route struct {
	Host         string
	Root         string // the deployment's folder on disk
	DeploymentID string
	Upstream     string // host:port to proxy to instead of serving files
}

// Caddy talks to a running Caddy's admin API.
type Caddy struct {
	Admin  string   // admin API base URL, e.g. http://127.0.0.1:2019
	Listen []string // where sites are served, e.g. [":8088"] in development
	HTTPS  bool     // false in development: plain HTTP, no certificates
	Client *http.Client
}

// Config builds Caddy's complete JSON config for these routes.
func (c Caddy) Config(routes []Route) ([]byte, error) {
	u, err := url.Parse(c.Admin)
	if err != nil {
		return nil, fmt.Errorf("admin address: %w", err)
	}
	sorted := append([]Route(nil), routes...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Host < sorted[j].Host })

	var handlers []any
	for _, r := range sorted {
		h := siteHandler(r)
		if r.Upstream != "" {
			h = map[string]any{"handler": "reverse_proxy", "upstreams": []any{map[string]any{"dial": r.Upstream}}}
		}
		handlers = append(handlers, map[string]any{
			"match":    []any{map[string]any{"host": []string{r.Host}}},
			"handle":   []any{h},
			"terminal": true,
		})
	}
	// Anything else on the server: say so plainly instead of Caddy's empty 200.
	handlers = append(handlers, map[string]any{
		"handle": []any{map[string]any{
			"handler":     "static_response",
			"status_code": 404,
			"headers":     map[string][]string{"Content-Type": {"text/plain; charset=utf-8"}},
			"body":        "Nothing is deployed at this address.\n",
		}},
	})

	server := map[string]any{"listen": c.Listen, "routes": handlers}
	if !c.HTTPS {
		server["automatic_https"] = map[string]any{"disable": true}
	}
	cfg := map[string]any{
		"admin": map[string]any{"listen": u.Host},
		"apps":  map[string]any{"http": map[string]any{"servers": map[string]any{"pier": server}}},
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// siteHandler serves a deployment's files with clean URLs: /about finds
// about.html or about/index.html, like most static hosts.
func siteHandler(r Route) map[string]any {
	return map[string]any{
		"handler": "subroute",
		"routes": []any{
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
			map[string]any{"handle": []any{map[string]any{"handler": "file_server", "root": r.Root}}},
		},
	}
}

// Apply loads the config for these routes into Caddy.
func (c Caddy) Apply(ctx context.Context, routes []Route) error {
	body, err := c.Config(routes)
	if err != nil {
		return err
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
