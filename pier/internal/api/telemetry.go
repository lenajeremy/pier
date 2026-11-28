package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"

	"pier/internal/telemetry"
)

// receiveTelemetry is POST /otlp/v1/{signal}: OpenTelemetry from a
// project's app, with the token its deploy was given. Its SDK finds this
// through OTEL_EXPORTER_OTLP_ENDPOINT and OTEL_EXPORTER_OTLP_HEADERS.
func (a *API) receiveTelemetry(w http.ResponseWriter, r *http.Request) {
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	project, env, ok := a.eventsTokenOwner(r.Context(), strings.TrimSpace(token))
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "send the token in OTEL_EXPORTER_OTLP_HEADERS"})
		return
	}
	if a.Telemetry == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": telemetry.ErrOff.Error()})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ct := r.Header.Get("Content-Type")
	code, err := a.Telemetry.Forward(r.Context(), r.PathValue("signal"), project, env, ct, r.Header.Get("Content-Encoding"), body)
	if err != nil {
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	// An empty Export…ServiceResponse: everything was accepted.
	if strings.HasPrefix(ct, "application/json") {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, "{}")
		return
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
}

// telemetryDoing is "starting" or "stopping" while the stack changes.
var telemetryDoing struct {
	sync.Mutex
	what string
}

func doingTelemetry() string {
	telemetryDoing.Lock()
	defer telemetryDoing.Unlock()
	return telemetryDoing.what
}

// telemetryStatus is the stack's state, for the Server page.
func (a *API) telemetryStatus(w http.ResponseWriter, r *http.Request) {
	if a.Telemetry == nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"available": true, "doing": doingTelemetry(), "status": a.Telemetry.Status(r.Context())})
}

// setTelemetry turns the stack on or off. Starting it can mean downloading
// the servers' images, so it happens after the answer; the Server page
// watches the state.
func (a *API) setTelemetry(w http.ResponseWriter, r *http.Request) {
	if a.Telemetry == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "this server can't run containers"})
		return
	}
	var req struct {
		On bool `json:"on"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": `send {"on": true} or {"on": false}`})
		return
	}
	doing := "stopping"
	if req.On {
		doing = "starting"
	}
	telemetryDoing.Lock()
	if busy := telemetryDoing.what; busy != "" {
		telemetryDoing.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"error": "already " + busy + "; wait a moment"})
		return
	}
	telemetryDoing.what = doing
	telemetryDoing.Unlock()
	by := userFrom(r.Context()).DisplayName()
	go func() {
		defer func() {
			telemetryDoing.Lock()
			telemetryDoing.what = ""
			telemetryDoing.Unlock()
		}()
		ctx := context.WithoutCancel(r.Context())
		var err error
		if req.On {
			err = a.Telemetry.Enable(ctx)
		} else {
			err = a.Telemetry.Disable(ctx)
		}
		if err == nil && a.Deployer != nil {
			// Caddy starts traces only while the stack is on.
			err = a.Deployer.Sync(ctx)
		}
		what := "Turned off traces, metrics and app logs"
		if req.On {
			what = "Turned on traces, metrics and app logs"
		}
		if err != nil {
			a.Log.Warn("telemetry", "on", req.On, "err", err)
			what = "Couldn't change traces, metrics and app logs"
		}
		a.Store.RecordEvent(ctx, "", what, errText(err), "", by)
	}()
	writeJSON(w, http.StatusAccepted, map[string]bool{"on": req.On})
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
