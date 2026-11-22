package api

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"pier/internal/auth"
	"pier/internal/dnscheck"
	"pier/internal/store"
	"strings"
	"time"
)

type addresses struct {
	Address string `json:"address"`
	Base    string `json:"base"`
}

func (a *API) savedAddresses(ctx context.Context) (addresses, error) {
	addr, err := a.Store.Setting(ctx, store.SettingAddress)
	if err != nil {
		return addresses{}, err
	}
	base, err := a.Store.Setting(ctx, store.SettingBaseDomain)
	if err != nil {
		return addresses{}, err
	}
	if addr == "" {
		addr = a.Deployer.PierHost
	}
	if base == "" {
		base = a.Deployer.Domain
	}
	return addresses{addr, base}, nil
}
func (a *API) serverSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	current := addresses{a.Deployer.PierHost, a.Deployer.Domain}
	if r.Method == "PUT" {
		var next addresses
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&next); err != nil {
			writeJSON(w, 400, map[string]string{"error": "Enter a dashboard address and a base domain."})
			return
		}
		canon := func(s string) string { return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), ".") }
		next.Address = canon(next.Address)
		next.Base = canon(next.Base)
		if (net.ParseIP(next.Address) == nil && !dnscheck.ValidDomain(next.Address)) || !dnscheck.ValidDomain(next.Base) {
			writeJSON(w, 400, map[string]string{"error": "Enter a hostname or IPv4 address for the dashboard and a domain for project addresses."})
			return
		}
		if strings.Contains(next.Address, ":") {
			writeJSON(w, 400, map[string]string{"error": "Use a hostname or an IPv4 address."})
			return
		}
		assigned, err := a.Store.Domains(ctx, "")
		if err != nil {
			a.fail(w, err)
			return
		}
		for _, d := range assigned {
			for _, host := range []string{d.Host, d.Redirect} {
				if host != "" && (host == next.Address || host == next.Base || strings.HasSuffix(host, "."+next.Base)) {
					writeJSON(w, 409, map[string]string{"error": "That address conflicts with an attached custom domain."})
					return
				}
			}
		}
		projects, err := a.Store.Projects(ctx)
		if err != nil {
			a.fail(w, err)
			return
		}
		for _, p := range projects {
			if p+"."+next.Base == next.Address {
				writeJSON(w, 409, map[string]string{"error": "That dashboard address belongs to a project."})
				return
			}
		}
		if strings.HasSuffix(next.Address, "."+next.Base) {
			routes, err := a.Deployer.Routes(ctx)
			if err != nil {
				a.fail(w, err)
				return
			}
			label := strings.TrimSuffix(next.Address, "."+next.Base)
			for _, r := range routes {
				if r.DeploymentID != "" && r.Host == label+"."+a.Deployer.Domain {
					writeJSON(w, 409, map[string]string{"error": "That dashboard address belongs to a deployment or branch."})
					return
				}
			}
		}
		if err := a.Store.SaveAddresses(ctx, next.Address, next.Base); err != nil {
			a.fail(w, err)
			return
		}
		a.event(ctx, "", "Updated server addresses", "", "")
	}
	saved, err := a.savedAddresses(ctx)
	if err != nil {
		a.fail(w, err)
		return
	}
	ip, err := a.Store.Setting(ctx, store.SettingPublicIP)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"saved": saved, "active": current, "ip": ip, "pending": current != saved})
}
func (a *API) checkAddresses(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	saved, err := a.savedAddresses(ctx)
	if err != nil {
		a.fail(w, err)
		return
	}
	ip, err := a.Store.Setting(ctx, store.SettingPublicIP)
	if err != nil {
		a.fail(w, err)
		return
	}
	if ip == "" {
		writeJSON(w, 409, map[string]string{"error": "Set the server IP with pierd init before checking DNS."})
		return
	}
	check := func(host string, wild bool) string {
		if net.ParseIP(host) != nil {
			if host == ip {
				return "Matches"
			}
			return "Expected " + ip
		}
		if strings.HasSuffix(host, ".localhost") {
			return "Local development address"
		}
		if wild {
			return dnsResult(dnscheck.CheckWildcard(ctx, host, ip))
		}
		return dnsResult(dnscheck.CheckA(ctx, host, ip))
	}
	writeJSON(w, 200, map[string]string{"address": check(saved.Address, false), "base": check(saved.Base, true)})
}

// password changes the signed-in person's own password, whatever their
// role, and signs them out everywhere.
func (a *API) password(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.ID == 0 {
		writeJSON(w, 403, map[string]string{"error": "Sign in to the dashboard to change your password."})
		return
	}
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "Enter the current and new passwords."})
		return
	}
	if err := auth.CheckPasswordRules(req.New); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	verified, err := auth.SignIn(r.Context(), a.Store, u.Email, req.Current)
	if err != nil {
		writeJSON(w, 403, map[string]string{"error": "Couldn't verify the current password. Check it or try again later."})
		return
	}
	hash, err := auth.HashPassword(req.New)
	if err != nil {
		a.fail(w, err)
		return
	}
	if err = a.Store.ChangePassword(r.Context(), u.ID, verified.PasswordHash, hash); err != nil {
		a.fail(w, err)
		return
	}
	a.event(r.Context(), "", "Changed password", "", "")
	a.setCookie(w, r, "", -1)
	writeJSON(w, 200, map[string]bool{"changed": true})
}
