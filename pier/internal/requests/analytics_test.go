package requests

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"
)

func TestClassify(t *testing.T) {
	const chrome = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	const iphone = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Mobile/15E148 Safari/604.1"
	for _, c := range []struct {
		name                          string
		method, path, agent, referer  string
		accept                        string
		status                        int
		browser, device, bot, refHost string
		page                          bool
	}{
		{"a page from google", "GET", "/", chrome, "https://www.google.com/search?q=shop", "text/html,application/xhtml+xml", 200, "Chrome", "Desktop", "", "google.com", true},
		{"an API call from the site", "GET", "/api/orders", chrome, "https://shop.example.com/orders", "application/json", 200, "Chrome", "Desktop", "", "", false},
		{"a phone", "GET", "/products", iphone, "", "text/html", 200, "Safari", "Mobile", "", "", true},
		{"a failed page isn't a view", "GET", "/missing", chrome, "", "text/html", 404, "Chrome", "Desktop", "", "", false},
		{"googlebot", "GET", "/", "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)", "", "text/html", 200, "Other", "Desktop", "Googlebot", "", false},
		{"curl", "GET", "/", "curl/8.7.1", "", "*/*", 200, "Other", "Desktop", "curl", "", false},
		{"a scanner", "GET", "/.env", chrome, "", "*/*", 404, "Chrome", "Desktop", "Scanners", "", false},
		{"no user agent", "GET", "/", "", "", "", 200, "", "", "No user agent", "", false},
		{"edge on android", "GET", "/", "Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 Chrome/131.0 Mobile Safari/537.36 EdgA/131.0", "", "text/html", 200, "Edge", "Mobile", "", "", true},
	} {
		v := classify(c.method, c.path, "shop.example.com", c.status, c.agent, c.referer, c.accept, "")
		if v.Browser != c.browser || v.Device != c.device || v.Bot != c.bot || v.Referrer != c.refHost || v.Page != c.page {
			t.Errorf("%s: got %+v", c.name, v)
		}
	}
}

func TestOverview(t *testing.T) {
	ctx := context.Background()
	l, err := Open(filepath.Join(t.TempDir(), "requests.db"), func(string) string { return "shop" }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	start := time.Date(2026, 11, 28, 14, 0, 0, 0, time.UTC).UnixMilli()
	minute := int64(60_000)
	var lines []Line
	add := func(at int64, path string, ms float64, status int, page bool, visitor int64, bot, browser, device, ref, env string) {
		lines = append(lines, Line{Project: "shop", Deployment: "d1", At: at, Method: "GET", Path: path, Status: status, MS: ms, Bytes: 100,
			Page: page, Visitor: visitor, Bot: bot, Browser: browser, Device: device, Referrer: ref, Env: env})
	}
	// Three people read pages, one twice; an API route gets slow; a bot and a scanner call.
	add(start+1*minute, "/", 30, 200, true, 1, "", "Chrome", "Desktop", "google.com", "production")
	add(start+2*minute, "/products?page=2", 40, 200, true, 1, "", "Chrome", "Desktop", "", "production")
	add(start+3*minute, "/", 35, 200, true, 2, "", "Safari", "Mobile", "", "production")
	add(start+4*minute, "/", 20, 200, true, 3, "", "Firefox", "Desktop", "github.com", "preview")
	for i := 0; i < 20; i++ {
		ms := 50.0
		if i >= 18 {
			ms = 900
		}
		add(start+int64(10+i)*minute, "/api/orders", ms, 200, false, 1, "", "Chrome", "Desktop", "", "production")
	}
	add(start+40*minute, "/api/orders", 60, 502, false, 2, "", "Safari", "Mobile", "", "production")
	add(start+41*minute, "/", 5, 200, false, 0, "Googlebot", "Other", "Desktop", "", "production")
	add(start+42*minute, "/.env", 2, 404, false, 0, "Scanners", "Chrome", "Desktop", "", "production")
	// The hour before had one visitor.
	add(start-30*minute, "/", 30, 200, true, 9, "", "Chrome", "Desktop", "", "production")
	l.Add(lines...)
	if err := l.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	o, err := l.Overview(ctx, "shop", "", start, start+60*minute, 5)
	if err != nil {
		t.Fatal(err)
	}
	if o.Requests != 27 || o.Errors != 1 || o.Visitors != 3 || o.PageViews != 4 {
		t.Fatalf("headline %+v", o)
	}
	if o.Before.Visitors != 1 || o.Before.PageViews != 1 {
		t.Fatalf("the hour before %+v", o.Before)
	}
	if o.P95 != 900 {
		t.Fatalf("p95 %v, want 900: bots are left out and the slow 2 of 25 are the top 5%%", o.P95)
	}
	if len(o.Trend) != 60 || o.Trend[10].Requests != 1 || o.Trend[1].PageViews != 1 || o.Trend[0].P95 != nil || o.Trend[10].P95 == nil {
		t.Fatalf("trend %+v", o.Trend[:12])
	}
	if o.Pages.Top[0].Name != "/" || o.Pages.Top[0].Count != 3 || o.Pages.Top[1].Name != "/products" {
		t.Fatalf("pages %+v", o.Pages)
	}
	if o.Referrers.Top[0].Name != "" || o.Referrers.Top[0].Count != 2 {
		t.Fatalf("referrers %+v", o.Referrers)
	}
	if o.Browsers.Top[0].Name != "Chrome" || o.Browsers.Top[0].Count != 1 || len(o.Browsers.Top) != 3 {
		t.Fatalf("browsers count people, not requests: %+v", o.Browsers)
	}
	if len(o.Bots.Top) != 2 {
		t.Fatalf("bots %+v", o.Bots)
	}
	if len(o.Slowest) != 1 || o.Slowest[0].Route != "GET /api/orders" || o.Slowest[0].Requests != 21 || o.Slowest[0].Errors != 1 || o.Slowest[0].P95 != 900 {
		t.Fatalf("slowest %+v", o.Slowest)
	}
	prod, err := l.Overview(ctx, "shop", "production", start, start+60*minute, 5)
	if err != nil {
		t.Fatal(err)
	}
	if prod.PageViews != 3 || prod.Visitors != 2 {
		t.Fatalf("production only %+v", prod)
	}
	short, err := l.Overview(ctx, "shop", "", start, start+60*minute, 2)
	if err != nil || len(short.Pages.Top) != 2 || short.Pages.Rest != 0 && short.Pages.Others == 0 {
		t.Fatalf("top 2 %+v %v", short.Pages, err)
	}
}

func TestParseReadsHeaders(t *testing.T) {
	l, err := Open(filepath.Join(t.TempDir(), "requests.db"), func(string) string { return "shop" }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	l.Target = func(string) string { return "production" }
	raw := `{"ts":1795000000.5,"request":{"method":"GET","uri":"/pricing","host":"shop.example.com","client_ip":"203.0.113.9",
		"headers":{"User-Agent":["Mozilla/5.0 (Macintosh) Chrome/131.0 Safari/537.36"],"Referer":["https://news.ycombinator.com/item?id=1"],"Accept":["text/html"]}},
		"duration":0.012,"size":5120,"status":200,"resp_headers":{"X-Pier-Deployment":["d1"]}}`
	line, ok := l.parse([]byte(raw))
	if !ok || line.Host != "shop.example.com" || !line.Page || line.Referrer != "news.ycombinator.com" || line.Browser != "Chrome" || line.Env != "production" || line.Visitor == 0 {
		t.Fatalf("%+v %v", line, ok)
	}
	again, _ := l.parse([]byte(raw))
	if again.Visitor != line.Visitor {
		t.Fatal("the same person on the same day got two visitor numbers")
	}
}

func TestFieldFilters(t *testing.T) {
	ctx := context.Background()
	l, err := Open(filepath.Join(t.TempDir(), "requests.db"), func(string) string { return "shop" }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	now := time.Now().UnixMilli()
	for i, c := range []struct {
		status  int
		browser string
	}{{200, "Chrome"}, {200, "Safari"}, {404, "Chrome"}, {502, "Chrome"}, {502, "Firefox"}} {
		l.Add(Line{Project: "shop", Deployment: "d1", At: now - int64(10-i)*1000, Method: "GET", Path: "/x?y=1", Status: c.status, Browser: c.browser})
	}
	if err := l.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	f := Filter{From: now - 60_000, To: now + 1, Match: []Match{{Field: "browser", Value: "Chrome"}, {Field: "level", Value: "info", Not: true}}}
	lines, err := l.Lines(ctx, "shop", f)
	if err != nil || len(lines) != 2 {
		t.Fatalf("Chrome and not info: %d lines, %v", len(lines), err)
	}
	vals, rest, err := l.Values(ctx, "shop", "browser", f, 5)
	if err != nil || len(vals) != 2 || vals[0].Name != "Chrome" || vals[0].Count != 2 || rest != 0 {
		t.Fatalf("browsers among errors and warnings, the browser match left out: %+v %d %v", vals, rest, err)
	}
	if v, _, _ := l.Values(ctx, "shop", "path", Filter{From: now - 60_000, To: now + 1}, 5); len(v) != 1 || v[0].Name != "/x" {
		t.Fatalf("paths lose their query: %+v", v)
	}
	if _, _, err := l.Values(ctx, "shop", "secret", f, 5); err == nil {
		t.Fatal("grouped by a field that isn't one")
	}
	b, _, err := l.ChartOf(ctx, "shop", Filter{From: now - 60_000, To: now + 1, Match: []Match{{Field: "status", Value: "502"}}}, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, x := range b {
		total += x.E5xx
	}
	if total != 2 {
		t.Fatalf("chart of status 502 counts %d", total)
	}
}
