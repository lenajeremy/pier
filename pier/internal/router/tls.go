package router

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

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

// HasWildcardCertificate reports whether embedded Caddy's storage holds a
// wildcard certificate for the base domain that hasn't expired. While it
// does, the wildcard policy stays in the config whatever a DNS probe says:
// taking the policy out makes Caddy drop the certificate, and every preview
// then asks Let's Encrypt for a certificate of its own.
func (c Caddy) HasWildcardCertificate() bool {
	name := certmagic.StorageKeys.Safe("*." + c.BaseDomain)
	certs := filepath.Join(c.Storage, "certificates")
	issuers, _ := os.ReadDir(certs)
	for _, issuer := range issuers {
		raw, err := os.ReadFile(filepath.Join(certs, issuer.Name(), name, name+".crt"))
		if err != nil {
			continue
		}
		block, _ := pem.Decode(raw)
		if block == nil {
			continue
		}
		if cert, err := x509.ParseCertificate(block.Bytes); err == nil && time.Now().Before(cert.NotAfter) {
			return true
		}
	}
	return false
}

// Certificate loads the certificate embedded Caddy keeps for host, or else
// the base domain's wildcard, from its storage, for a listener of pierd's
// own: the public Postgres port.
func (c Caddy) Certificate(host string) (*tls.Certificate, error) {
	certs := filepath.Join(c.Storage, "certificates")
	issuers, _ := os.ReadDir(certs)
	for _, subject := range []string{host, "*." + c.BaseDomain} {
		name := certmagic.StorageKeys.Safe(subject)
		for _, issuer := range issuers {
			dir := filepath.Join(certs, issuer.Name(), name)
			cert, err := tls.LoadX509KeyPair(filepath.Join(dir, name+".crt"), filepath.Join(dir, name+".key"))
			if err != nil {
				continue
			}
			leaf := cert.Leaf
			if leaf == nil && len(cert.Certificate) > 0 {
				leaf, _ = x509.ParseCertificate(cert.Certificate[0])
			}
			if leaf != nil && time.Now().Before(leaf.NotAfter) {
				return &cert, nil
			}
		}
	}
	return nil, fmt.Errorf("there's no certificate for %s yet", host)
}

// dashboardIP is the dashboard's address when the owner reaches it by bare IP.
// A browser sends no server name for an IP, and Caddy then looks for a
// certificate for the address the connection arrived on. Behind NAT (EC2,
// GCP, Azure) that is a private 10.x address and no certificate matches, so
// the config names the public IP as the certificate for such connections.
func dashboardIP(routes []Route) string {
	for _, r := range routes {
		if r.Upstream != "" && r.DeploymentID == "" && net.ParseIP(r.Host) != nil && certmagic.SubjectQualifiesForPublicCert(r.Host) {
			return r.Host
		}
	}
	return ""
}

// tlsConfig gives generated preview names on-demand certificates until the
// wildcard is available. Verified custom domains and the dashboard start
// obtaining their own certificates when their route becomes active. The
// routes come with one route per host: Caddy refuses a host named twice.
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
		case (r.Upstream == "" || r.DeploymentID != "") && strings.HasSuffix(r.Host, "."+c.BaseDomain):
			generated = append(generated, r.Host)
		default:
			custom = append(custom, r.Host)
		}
	}
	// A host to certify gets a certificate kept up to date like a custom
	// domain's, though no route asks for one, unless the wildcard covers it.
	wildReady := c.WildcardReady != nil && c.WildcardReady()
	var automate []string
	for _, h := range c.Certify {
		h = strings.ToLower(h)
		if wildReady || net.ParseIP(h) != nil || !certmagic.SubjectQualifiesForPublicCert(h) {
			continue
		}
		generated = slices.DeleteFunc(generated, func(g string) bool { return g == h })
		if !slices.Contains(custom, h) {
			custom = append(custom, h)
		}
		automate = append(automate, h)
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
	if wildReady {
		wild := "*." + c.BaseDomain
		i := issuer()
		i["challenges"] = map[string]any{"dns": map[string]any{"provider": map[string]any{"name": "pier", "zone": "_acme-challenge." + c.BaseDomain}, "resolvers": c.Resolvers}}
		policies = append(policies, map[string]any{"subjects": []string{wild}, "issuers": []any{i}})
		automate = append(automate, wild)
	}
	if len(automate) > 0 {
		app["certificates"] = map[string]any{"automate": automate}
	}
	app["automation"] = map[string]any{"on_demand": map[string]any{"permission": map[string]any{"module": "pier", "hosts": allowed}}, "policies": policies}
	return app
}
