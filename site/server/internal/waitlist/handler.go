package waitlist

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"
)

type adder interface {
	Add(ctx context.Context, email string) (bool, error)
}

// Handler serves POST /api/waitlist.
//
// Responses are JSON:
//
//	201 {"status":"joined"}    new address
//	200 {"status":"already"}   address was already on the list
//	400 {"error":"..."}        the message is shown to the visitor
//	429 {"error":"..."}        too many attempts from one client
type Handler struct {
	Store      adder
	Limiter    *RateLimiter
	TrustProxy bool // read the client address from X-Forwarded-For (set behind Caddy)
	Log        *slog.Logger
}

type request struct {
	Email   string `json:"email"`
	Website string `json:"website"` // honeypot: people never see this field, bots fill it
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "Send the request as JSON."})
		return
	}
	if h.Limiter != nil && !h.Limiter.Allow(h.clientIP(r)) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{
			"error": "Too many tries from your network. Wait a minute and try again.",
		})
		return
	}

	var req request
	r.Body = http.MaxBytesReader(w, r.Body, 2<<10)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "That request couldn't be read. Reload the page and try again."})
		return
	}
	if req.Website != "" {
		// Look successful so bots don't learn to adapt; store nothing.
		writeJSON(w, http.StatusCreated, map[string]string{"status": "joined"})
		return
	}

	email, err := NormalizeEmail(req.Email)
	if err != nil {
		if ve, ok := errors.AsType[*ValidationError](err); ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": ve.Msg})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": errInvalid.Msg})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	added, err := h.Store.Add(ctx, email)
	if err != nil {
		h.logger().Error("waitlist insert failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "Something went wrong on our side. Try again in a minute.",
		})
		return
	}
	if !added {
		writeJSON(w, http.StatusOK, map[string]string{"status": "already"})
		return
	}
	h.logger().Info("waitlist joined")
	writeJSON(w, http.StatusCreated, map[string]string{"status": "joined"})
}

func (h *Handler) clientIP(r *http.Request) string {
	if h.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			// The last entry was added by our own proxy, so it can't be spoofed.
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (h *Handler) logger() *slog.Logger {
	if h.Log != nil {
		return h.Log
	}
	return slog.Default()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
