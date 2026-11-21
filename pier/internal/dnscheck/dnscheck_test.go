package dnscheck

import (
	"context"
	"net"
	"testing"

	"github.com/miekg/dns"
)

// resolver answers every question with handle's reply, on a loopback port.
func resolver(t *testing.T, handle func(m *dns.Msg, q dns.Question)) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &dns.Server{PacketConn: pc, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(req)
		handle(m, req.Question[0])
		w.WriteMsg(m)
	})}
	go srv.ActivateAndServe()
	t.Cleanup(func() { srv.Shutdown() })
	return pc.LocalAddr().String()
}

// One resolver failing must not look like the delegation being gone: the
// wildcard certificate depends on this answer.
func TestChallengeAuthorityAsksEveryResolver(t *testing.T) {
	failing := resolver(t, func(m *dns.Msg, _ dns.Question) { m.Rcode = dns.RcodeServerFailure })
	soa := func(ns string) func(*dns.Msg, dns.Question) {
		return func(m *dns.Msg, q dns.Question) {
			m.Answer = []dns.RR{&dns.SOA{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeSOA, Class: dns.ClassINET}, Ns: ns, Mbox: "hostmaster." + ns}}
		}
	}
	delegated := resolver(t, soa("pier.example.com."))
	elsewhere := resolver(t, soa("ns1.registrar.example."))
	old := Resolvers
	defer func() { Resolvers = old }()
	ctx := context.Background()

	Resolvers = []string{failing, delegated}
	if !ChallengeAuthority(ctx, "example.com", "pier.example.com") {
		t.Fatal("one resolver's SERVFAIL hid a working delegation")
	}
	Resolvers = []string{failing, elsewhere}
	if ChallengeAuthority(ctx, "example.com", "pier.example.com") {
		t.Fatal("a zone served by someone else passed as delegated to Pier")
	}
	Resolvers = []string{failing}
	if ChallengeAuthority(ctx, "example.com", "pier.example.com") {
		t.Fatal("a failing resolver passed as a delegation")
	}
}
