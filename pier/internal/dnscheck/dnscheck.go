// Package dnscheck checks the DNS records pierd init asks the owner to add.
// It asks public resolvers rather than the machine's own, so a stale local
// cache can't make a missing record look present.
package dnscheck

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/miekg/dns"
)

// Resolvers are asked in turn until one answers.
var Resolvers = []string{"1.1.1.1:53", "8.8.8.8:53", "9.9.9.9:53"}

var client = &dns.Client{Timeout: 4 * time.Second}

func exchange(ctx context.Context, m *dns.Msg, servers []string) (*dns.Msg, error) {
	var last error
	for _, s := range servers {
		r, _, err := client.ExchangeContext(ctx, m, s)
		if err == nil && r.Truncated {
			tcp := &dns.Client{Net: "tcp", Timeout: client.Timeout}
			r, _, err = tcp.ExchangeContext(ctx, m, s)
		}
		if err == nil {
			return r, nil
		}
		last = err
	}
	return nil, last
}

// A returns the IPv4 addresses a name resolves to.
func A(ctx context.Context, host string) ([]string, error) {
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(host), dns.TypeA)
	m.RecursionDesired = true
	r, err := exchange(ctx, m, Resolvers)
	if err != nil {
		return nil, err
	}
	if r.Rcode != dns.RcodeSuccess && r.Rcode != dns.RcodeNameError {
		return nil, fmt.Errorf("resolver returned %s", dns.RcodeToString[r.Rcode])
	}
	var ips []string
	for _, rr := range r.Answer {
		if a, ok := rr.(*dns.A); ok {
			ips = append(ips, a.A.String())
		}
	}
	return ips, nil
}

// Wildcard returns what a made-up name under domain resolves to, which is
// what the domain's * record says.
func Wildcard(ctx context.Context, domain string) ([]string, error) {
	b := make([]byte, 4)
	rand.Read(b)
	return A(ctx, "pier-check-"+hex.EncodeToString(b)+"."+domain)
}

// Delegation returns the nameservers that name is handed to, as the parent
// zone's own nameservers say. Asking them directly means the check works
// before the delegated server (pierd) is answering.
func Delegation(ctx context.Context, name, parent string) ([]string, error) {
	q := new(dns.Msg)
	q.SetQuestion(dns.Fqdn(parent), dns.TypeNS)
	q.RecursionDesired = true
	r, err := exchange(ctx, q, Resolvers)
	if err != nil {
		return nil, err
	}
	var servers []string
	for _, rr := range r.Answer {
		ns, ok := rr.(*dns.NS)
		if !ok {
			continue
		}
		ips, err := A(ctx, ns.Ns)
		if err != nil {
			continue
		}
		for _, ip := range ips {
			servers = append(servers, net.JoinHostPort(ip, "53"))
		}
	}
	if len(servers) == 0 {
		return nil, fmt.Errorf("can't find %s's nameservers", parent)
	}
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), dns.TypeNS)
	m.RecursionDesired = false
	resp, err := exchange(ctx, m, servers)
	if err != nil {
		return nil, err
	}
	var hosts []string
	for _, rr := range append(resp.Answer, resp.Ns...) {
		if ns, ok := rr.(*dns.NS); ok && strings.EqualFold(ns.Hdr.Name, dns.Fqdn(name)) {
			hosts = append(hosts, strings.TrimSuffix(strings.ToLower(ns.Ns), "."))
		}
	}
	return hosts, nil
}

// Result is one record's check.
type Result struct {
	Found bool
	Got   string // what DNS returns instead, when it isn't what was asked for
	Err   error
}

// CheckA says whether host resolves to ip.
func CheckA(ctx context.Context, host, ip string) Result {
	return compare(A(ctx, host))(ip)
}

// CheckWildcard says whether every name under domain resolves to ip.
func CheckWildcard(ctx context.Context, domain, ip string) Result {
	return compare(Wildcard(ctx, domain))(ip)
}

// CheckDelegation says whether name is handed to the nameserver want.
func CheckDelegation(ctx context.Context, name, parent, want string) Result {
	got, err := Delegation(ctx, name, parent)
	return compare(got, err)(strings.ToLower(strings.TrimSuffix(want, ".")))
}

func compare(got []string, err error) func(string) Result {
	return func(want string) Result {
		if err != nil {
			return Result{Err: err}
		}
		for _, g := range got {
			if g == want {
				return Result{Found: true}
			}
		}
		return Result{Got: strings.Join(got, ", ")}
	}
}

// ValidDomain is a light check that s is a domain name, not a URL or an IP.
func ValidDomain(s string) bool {
	if net.ParseIP(s) != nil || len(s) > 253 || !strings.Contains(s, ".") {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

// ErrNoPublicIP means the machine has no public IPv4 address of its own.
var ErrNoPublicIP = errors.New("no public IPv4 address on this machine")

// PublicIP finds the machine's public IPv4 address from its own network
// interfaces, without asking anyone outside. Cloud machines behind NAT (EC2,
// Lightsail) only see a private address; then it returns that, and
// ErrNoPublicIP, and the owner types the public one.
func PublicIP() (string, error) {
	var private string
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.To4() == nil {
				continue
			}
			ip := ipn.IP.To4()
			if ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip[0] == 100 && ip[1]&0xc0 == 64 { // 100.64/10 is carrier NAT
				if private == "" {
					private = ip.String()
				}
				continue
			}
			return ip.String(), nil
		}
	}
	return private, ErrNoPublicIP
}

// CheckCNAME compares the record at host itself, ignoring DNS case and the final dot.
func CheckCNAME(ctx context.Context, host, want string) Result {
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(host), dns.TypeCNAME)
	r, err := exchange(ctx, m, Resolvers)
	if err != nil {
		return Result{Err: err}
	}
	if r.Rcode != dns.RcodeSuccess && r.Rcode != dns.RcodeNameError {
		return Result{Err: fmt.Errorf("resolver returned %s", dns.RcodeToString[r.Rcode])}
	}
	for _, rr := range r.Answer {
		if c, ok := rr.(*dns.CNAME); ok && strings.EqualFold(c.Hdr.Name, dns.Fqdn(host)) {
			got := strings.TrimSuffix(strings.ToLower(c.Target), ".")
			return Result{Found: got == want, Got: got}
		}
	}
	return Result{}
}

// ChallengeAuthority checks the delegated zone through recursive resolvers.
// Seeing the responder's own SOA verifies the delegation and reachability.
// One resolver's SERVFAIL or lost packet says nothing about the delegation,
// so each resolver is asked until one has seen it. Even then false can be a
// passing failure: callers act on several in a row, not on one.
func ChallengeAuthority(ctx context.Context, base, ns string) bool {
	name := dns.Fqdn("_acme-challenge." + base)
	q := new(dns.Msg)
	q.SetQuestion(name, dns.TypeSOA)
	q.RecursionDesired = true
	for _, resolver := range Resolvers {
		r, err := exchange(ctx, q, []string{resolver})
		if err != nil || r.Rcode != dns.RcodeSuccess {
			continue
		}
		for _, rr := range r.Answer {
			if soa, ok := rr.(*dns.SOA); ok && strings.EqualFold(soa.Hdr.Name, name) && strings.EqualFold(soa.Ns, dns.Fqdn(ns)) {
				return true
			}
		}
	}
	return false
}
