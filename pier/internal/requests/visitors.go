package requests

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// What Analytics knows about a request comes from its headers as Caddy logs
// them. A visitor's address is never kept: it's hashed with the User-Agent
// and a salt that changes every UTC day, so the same person counts once a
// day and can't be followed from one day to the next. Salts older than two
// days are deleted.

// visit is what a request's headers say about who sent it.
type visit struct {
	Browser, Device, Bot, Referrer, Country string
	Page                                    bool
}

var (
	botAgent = regexp.MustCompile(`(?i)(bot|crawl|spider|slurp|preview|monitor|uptime|curl/|wget/|python-requests|python-urllib|go-http-client|okhttp|axios/|node-fetch|headlesschrome|lighthouse|httpclient|libwww|scrapy|java/)`)
	botName  = regexp.MustCompile(`(?i)([a-z][a-z0-9._-]*(?:bot|crawler|spider))`)
	// Paths that only scanners ask for: no app on Pier serves them, and the
	// requests that ask are probing for leaked secrets or old admin pages.
	scannerPath = regexp.MustCompile(`(?i)^/(\.env|\.git/|\.aws/|\.ssh/|\.vscode/|wp-(admin|login|content|includes)|xmlrpc\.php|phpmyadmin|pma/|vendor/phpunit|cgi-bin/|boaform|actuator/|server-status|config\.json|\.ds_store|@vite/env|debug/default)`)
)

// classify reads one request's headers.
func classify(method, path, host string, status int, agent, referer, accept, country string) visit {
	var v visit
	switch {
	case scannerPath.MatchString(path):
		v.Bot = "Scanners"
	case agent == "":
		v.Bot = "No user agent"
	case botAgent.MatchString(agent):
		v.Bot = botLabel(agent)
	}
	v.Browser, v.Device = browser(agent)
	if v.Bot == "" {
		v.Page = method == "GET" && status >= 200 && status < 300 && strings.Contains(accept, "text/html")
		v.Referrer = referrerHost(referer, host)
		if len(country) == 2 && country != "XX" && country != "T1" {
			v.Country = strings.ToUpper(country)
		}
	}
	return v
}

func botLabel(agent string) string {
	lower := strings.ToLower(agent)
	for _, known := range []struct{ match, name string }{
		{"googlebot", "Googlebot"}, {"bingbot", "Bingbot"}, {"applebot", "Applebot"}, {"duckduckbot", "DuckDuckBot"},
		{"yandex", "YandexBot"}, {"baiduspider", "Baiduspider"}, {"facebookexternalhit", "Facebook"}, {"twitterbot", "Twitterbot"},
		{"slackbot", "Slackbot"}, {"discordbot", "Discordbot"}, {"linkedinbot", "LinkedInBot"}, {"gptbot", "GPTBot"},
		{"claudebot", "ClaudeBot"}, {"ahrefsbot", "AhrefsBot"}, {"semrushbot", "SemrushBot"}, {"curl/", "curl"},
		{"wget/", "Wget"}, {"python-requests", "Python"}, {"python-urllib", "Python"}, {"go-http-client", "Go"},
		{"headlesschrome", "Headless Chrome"}, {"lighthouse", "Lighthouse"}, {"uptime", "Uptime checks"},
	} {
		if strings.Contains(lower, known.match) {
			return known.name
		}
	}
	if m := botName.FindString(agent); m != "" {
		return m
	}
	return "Other bots"
}

// browser names the browser family and the kind of device.
func browser(agent string) (string, string) {
	a := agent
	device := "Desktop"
	switch {
	case strings.Contains(a, "iPad"), strings.Contains(a, "Tablet"), strings.Contains(a, "Android") && !strings.Contains(a, "Mobile"):
		device = "Tablet"
	case strings.Contains(a, "Mobi"), strings.Contains(a, "iPhone"), strings.Contains(a, "Android"):
		device = "Mobile"
	}
	name := "Other"
	switch {
	case agent == "":
		return "", ""
	case strings.Contains(a, "Edg/"), strings.Contains(a, "EdgA/"), strings.Contains(a, "EdgiOS/"):
		name = "Edge"
	case strings.Contains(a, "OPR/"), strings.Contains(a, "Opera"):
		name = "Opera"
	case strings.Contains(a, "SamsungBrowser/"):
		name = "Samsung Internet"
	case strings.Contains(a, "Firefox/"), strings.Contains(a, "FxiOS/"):
		name = "Firefox"
	case strings.Contains(a, "Chrome/"), strings.Contains(a, "CriOS/"):
		name = "Chrome"
	case strings.Contains(a, "Safari/"):
		name = "Safari"
	}
	return name, device
}

// referrerHost is the site a visitor came from, or "" when they typed the
// address, came from a bookmark, or moved within the same site.
func referrerHost(referer, host string) string {
	if referer == "" {
		return ""
	}
	u, err := url.Parse(referer)
	if err != nil || u.Host == "" {
		return ""
	}
	h := strings.ToLower(strings.TrimPrefix(u.Hostname(), "www."))
	if h == strings.ToLower(strings.TrimPrefix(hostOnly(host), "www.")) {
		return ""
	}
	return h
}

func hostOnly(host string) string {
	if i := strings.LastIndexByte(host, ':'); i > 0 && !strings.Contains(host[i:], "]") {
		return host[:i]
	}
	return host
}

// salts keeps one random salt per UTC day.
type salts struct {
	mu    sync.Mutex
	db    *sql.DB
	day   int64
	value []byte
}

const saltSchema = `CREATE TABLE IF NOT EXISTS salts (day INTEGER PRIMARY KEY, salt BLOB NOT NULL);`

func (s *salts) get(at time.Time) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	day := at.UTC().Unix() / 86400
	if day == s.day && s.value != nil {
		return s.value
	}
	ctx := context.Background()
	var v []byte
	if err := s.db.QueryRowContext(ctx, `SELECT salt FROM salts WHERE day = ?`, day).Scan(&v); err != nil {
		v = make([]byte, 32)
		rand.Read(v)
		if _, err := s.db.ExecContext(ctx, `INSERT INTO salts(day, salt) VALUES(?, ?) ON CONFLICT(day) DO NOTHING`, day, v); err == nil {
			s.db.QueryRowContext(ctx, `SELECT salt FROM salts WHERE day = ?`, day).Scan(&v)
		}
		s.db.ExecContext(ctx, `DELETE FROM salts WHERE day < ?`, day-1)
	}
	s.day, s.value = day, v
	return v
}

// visitor is a number that stands for one person on one day.
func visitor(salt []byte, ip, agent string) int64 {
	if ip == "" {
		return 0
	}
	h := sha256.New()
	h.Write(salt)
	h.Write([]byte(ip))
	h.Write([]byte{0})
	h.Write([]byte(agent))
	return int64(binary.BigEndian.Uint64(h.Sum(nil)[:8]) >> 1)
}
