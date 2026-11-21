// Command piersite serves the Pier landing page and its waitlist API from a
// single binary. The built pages (site/v2 → server/dist, and the V3 design
// site/v3 → server/dist-v3, served at /v3/) are embedded at compile time, so
// run `make build` first.
//
//	piersite                 serve on $ADDR (default :8080)
//	piersite export          print the waitlist as CSV
package main

import (
	"context"
	"embed"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"piersite/internal/waitlist"
)

//go:embed all:dist-v3
var distV3FS embed.FS

func main() {
	addr := flag.String("addr", env("ADDR", ":8080"), "listen address")
	dbPath := flag.String("db", env("DB_PATH", "data/waitlist.db"), "SQLite file for the waitlist")
	trustProxy := flag.Bool("trust-proxy", env("TRUST_PROXY", "") == "1", "read client IPs from X-Forwarded-For")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	store, err := waitlist.Open(*dbPath)
	if err != nil {
		log.Error("open database", "err", err)
		os.Exit(1)
	}
	defer store.Close()

	if flag.Arg(0) == "export" {
		if err := export(store); err != nil {
			log.Error("export", "err", err)
			os.Exit(1)
		}
		return
	}

	siteV3, err := fs.Sub(distV3FS, "dist-v3")
	if err != nil {
		log.Error("embedded v3 site", "err", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.Handle("POST /api/waitlist", &waitlist.Handler{
		Store:      store,
		Limiter:    waitlist.NewRateLimiter(5, time.Minute),
		TrustProxy: *trustProxy,
		Log:        log,
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := store.Ping(ctx); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintln(w, "ok")
	})
	// The install commands on the page are short because these send curl on
	// to the installers in the repository, which fetch the latest release.
	for path, script := range map[string]string{"/install": "get.sh", "/cli": "cli.sh"} {
		mux.Handle("GET "+path, http.RedirectHandler(installers+script, http.StatusFound))
	}
	mux.Handle("GET /", staticHandler(siteV3))

	srv := &http.Server{
		Addr:              *addr,
		Handler:           securityHeaders(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()

	log.Info("serving", "addr", *addr, "db", *dbPath)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server", "err", err)
		os.Exit(1)
	}
}

// staticHandler serves the built site. Hashed files under /assets/ never
// change, so they're cached for a year; everything else is revalidated.
// installers is where the install scripts live, on the main branch.
const installers = "https://raw.githubusercontent.com/lenajeremy/pier/main/pier/install/"

func staticHandler(site fs.FS) http.Handler {
	files := http.FileServerFS(site)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	csp := strings.Join([]string{
		"default-src 'self'",
		"script-src 'self'",
		"style-src 'self'",
		"img-src 'self' data:",
		"font-src 'self'",
		"connect-src 'self'",
		"base-uri 'self'",
		"form-action 'self'",
		"frame-ancestors 'none'",
	}, "; ")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

func export(store *waitlist.Store) error {
	entries, err := store.All(context.Background())
	if err != nil {
		return err
	}
	out := csv.NewWriter(os.Stdout)
	_ = out.Write([]string{"email", "created_at"})
	for _, e := range entries {
		_ = out.Write([]string{e.Email, e.CreatedAt})
	}
	out.Flush()
	return out.Error()
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
