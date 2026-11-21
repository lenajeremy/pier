package api

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"

	"pier/internal/dnscheck"
	"pier/internal/names"
	"pier/internal/store"
)

func (a *API) domainRoutes(mux *http.ServeMux) {
	const base = "/api/projects/{project}/domains"
	mux.Handle("GET "+base, a.auth(http.HandlerFunc(a.domains)))
	mux.Handle("POST "+base, a.auth(http.HandlerFunc(a.addDomain)))
	mux.Handle("POST "+base+"/{host}/check", a.auth(http.HandlerFunc(a.checkDomain)))
	mux.Handle("DELETE "+base+"/{host}", a.auth(http.HandlerFunc(a.removeDomain)))
}

func (a *API) addDomain(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	if _, ok := a.findProject(w, r, project); !ok {
		return
	}
	var input struct {
		Host string `json:"host"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&input); err != nil {
		writeJSON(w, 400, map[string]string{"error": "Enter a domain name."})
		return
	}
	host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(input.Host), "."))
	apex, err := publicsuffix.EffectiveTLDPlusOne(host)
	if !dnscheck.ValidDomain(host) || err != nil {
		writeJSON(w, 400, map[string]string{"error": "Enter a domain name, such as example.com."})
		return
	}
	redirect := ""
	if host == apex {
		redirect = "www." + host
	}
	// Addresses the owner saved take effect at the next restart, and a custom
	// domain under them would then be an address Pier generates itself.
	saved, err := a.savedAddresses(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	for _, used := range []addresses{{a.Deployer.PierHost, a.Deployer.Domain}, saved} {
		for _, candidate := range []string{host, redirect} {
			if candidate != "" && (candidate == used.Address || candidate == used.Base || strings.HasSuffix(candidate, "."+used.Base)) {
				writeJSON(w, 400, map[string]string{"error": "Pier already uses that address for the dashboard or project deployments."})
				return
			}
		}
	}
	err = a.Store.AddDomain(r.Context(), project, host, redirect)
	if errors.Is(err, store.ErrExists) {
		writeJSON(w, 409, map[string]string{"error": "That domain or its www address is already attached to a project."})
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 201, map[string]string{"host": host})
}

type domainView struct {
	store.CustomDomain
	TLS         bool `json:"tls"`
	RedirectTLS bool `json:"redirect_tls"`
}

func (a *API) hasCertificate(ctx context.Context, host string) bool {
	if !a.Deployer.Router.HTTPS || a.SiteListener == "" {
		return false
	}
	// Use the local listener with this hostname as SNI. Never connect to an
	// address supplied by the owner or follow a redirect from a deployment.
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	dialer := tls.Dialer{Config: &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}}
	conn, err := dialer.DialContext(ctx, "tcp", a.SiteListener)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func (a *API) domains(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	ps, ok := a.findProject(w, r, project)
	if !ok {
		return
	}
	ds, err := a.Store.Domains(r.Context(), project)
	if err != nil {
		a.fail(w, err)
		return
	}
	ip, err := a.Store.Setting(r.Context(), store.SettingPublicIP)
	if err != nil {
		a.fail(w, err)
		return
	}
	views := make([]domainView, 0, len(ds))
	for _, d := range ds {
		v := domainView{CustomDomain: d}
		if d.Verified && ps.production != nil {
			v.TLS = a.hasCertificate(r.Context(), d.Host)
			if d.Redirect != "" {
				v.RedirectTLS = a.hasCertificate(r.Context(), d.Redirect)
			}
		}
		views = append(views, v)
	}
	base := names.Production(project, a.Deployer.Domain)
	writeJSON(w, 200, map[string]any{"domains": views, "public_ip": ip, "default_host": base, "https": a.Deployer.Router.HTTPS, "default_tls": ps.production != nil && a.hasCertificate(r.Context(), base), "deployed": ps.production != nil})
}

func (a *API) removeDomain(w http.ResponseWriter, r *http.Request) {
	err := a.Store.RemoveDomain(r.Context(), r.PathValue("project"), r.PathValue("host"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, 404, map[string]string{"error": "No such domain on this project."})
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	if err = a.Deployer.Sync(r.Context()); err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"removed": true})
}

func (a *API) checkDomain(w http.ResponseWriter, r *http.Request) {
	ds, err := a.Store.Domains(r.Context(), r.PathValue("project"))
	if err != nil {
		a.fail(w, err)
		return
	}
	for _, d := range ds {
		if d.Host == r.PathValue("host") {
			if err := a.refreshDomain(r.Context(), d); err != nil {
				a.fail(w, err)
				return
			}
			writeJSON(w, 200, map[string]bool{"checked": true})
			return
		}
	}
	writeJSON(w, 404, map[string]string{"error": "No such domain on this project."})
}

func dnsResult(r dnscheck.Result) string {
	switch {
	case r.Err != nil:
		return "Resolver unavailable"
	case r.Found:
		return "Matches"
	case r.Got != "":
		return "Points to " + r.Got
	default:
		return "Not found yet"
	}
}

func (a *API) refreshDomain(ctx context.Context, d store.CustomDomain) error {
	ip, err := a.Store.Setting(ctx, store.SettingPublicIP)
	if err != nil {
		return err
	}
	if ip == "" {
		return nil
	}
	query, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	address := dnscheck.CheckA(query, d.Host, ip)
	redirect := dnscheck.Result{Found: true}
	d.AddressResult = dnsResult(address)
	if d.Redirect != "" {
		redirect = dnscheck.CheckCNAME(query, d.Redirect, d.Host)
		d.RedirectResult = dnsResult(redirect)
	}
	wasVerified := d.Verified
	if address.Err == nil && redirect.Err == nil {
		d.Verified = address.Found && redirect.Found
	}
	saved, err := a.Store.RecordDomainCheck(ctx, d)
	if err != nil {
		return err
	}
	if saved && d.Verified != wasVerified {
		return a.Deployer.Sync(ctx)
	}
	return nil
}

func (a *API) RunDomainChecks(ctx context.Context) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		ds, err := a.Store.Domains(ctx, "")
		if err != nil {
			if ctx.Err() == nil {
				a.Log.Warn("list domains for DNS checks", "err", err)
			}
			continue
		}
		for _, d := range ds {
			if d.Verified {
				continue
			}
			if err := a.refreshDomain(ctx, d); err != nil && ctx.Err() == nil {
				a.Log.Warn("check domain", "host", d.Host, "err", err)
			}
		}
	}
}
