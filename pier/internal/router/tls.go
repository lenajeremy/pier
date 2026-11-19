package router

import (
	"context"
	"net"
	"strings"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddytls"
	"github.com/caddyserver/certmagic"
)

// Certificate requests at handshake time must match a route in this config.
type allowedHosts struct {
	Hosts   []string `json:"hosts"`
	allowed map[string]bool
}

func init() { caddy.RegisterModule(allowedHosts{}) }
func (allowedHosts) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{ID: "tls.permission.pier", New: func() caddy.Module { return new(allowedHosts) }}
}
func (p *allowedHosts) Provision(caddy.Context) error {
	p.allowed = map[string]bool{}
	for _, host := range p.Hosts {
		p.allowed[host] = true
	}
	return nil
}
func (p *allowedHosts) CertificateAllowed(_ context.Context, name string) error {
	if p.allowed[strings.ToLower(name)] {
		return nil
	}
	return caddytls.ErrPermissionDenied
}

// tlsConfig gives generated preview names on-demand certificates until the
// wildcard is available. Verified custom domains and the dashboard start
// obtaining their own certificates when their route becomes active.
func (c Caddy) tlsConfig(routes []Route) map[string]any {
	var allowed, generated, custom, ips []string
	for _, r := range routes {
		if !certmagic.SubjectQualifiesForPublicCert(r.Host) {
			continue
		}
		allowed = append(allowed, r.Host)
		switch {
		case net.ParseIP(r.Host) != nil:
			ips = append(ips, r.Host)
		case r.Upstream == "" && strings.HasSuffix(r.Host, "."+c.BaseDomain):
			generated = append(generated, r.Host)
		default:
			custom = append(custom, r.Host)
		}
	}
	issuer := func() map[string]any {
		ca := c.ACMECA
		if ca == "" {
			ca = certmagic.LetsEncryptProductionCA
		}
		return map[string]any{"module": "acme", "ca": ca}
	}
	var policies []any
	if len(generated) > 0 {
		policies = append(policies, map[string]any{"subjects": generated, "on_demand": true, "issuers": []any{issuer()}})
	}
	if len(custom) > 0 {
		policies = append(policies, map[string]any{"subjects": custom, "issuers": []any{issuer()}})
	}
	if len(ips) > 0 {
		i := issuer()
		i["profile"] = "shortlived"
		policies = append(policies, map[string]any{"subjects": ips, "issuers": []any{i}})
	}
	app := map[string]any{}
	if c.WildcardReady != nil && c.WildcardReady() {
		wild := "*." + c.BaseDomain
		i := issuer()
		i["challenges"] = map[string]any{"dns": map[string]any{"provider": map[string]any{"name": "pier", "zone": "_acme-challenge." + c.BaseDomain}, "resolvers": c.Resolvers}}
		policies = append(policies, map[string]any{"subjects": []string{wild}, "issuers": []any{i}})
		app["certificates"] = map[string]any{"automate": []string{wild}}
	}
	app["automation"] = map[string]any{"on_demand": map[string]any{"permission": map[string]any{"module": "pier", "hosts": allowed}}, "policies": policies}
	return app
}
