package challengedns

import (
	"context"
	"testing"

	"github.com/libdns/libdns"
	"github.com/miekg/dns"
)

func TestAuthoritativeChallenge(t *testing.T) {
	s, err := Start("example.test", "ns.example.test", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	addr := s.servers[0].Listener.Addr().String()
	qname := "_ACME-Challenge.Example.Test."
	provider := Provider{Zone: s.zone}
	records := []libdns.Record{libdns.RR{Name: "@", Type: "TXT", Data: "one"}, libdns.RR{Name: "@", Type: "TXT", Data: "two"}}
	ctx := context.Background()
	if _, err := provider.AppendRecords(ctx, s.zone, records); err != nil {
		t.Fatal(err)
	}
	query := func(network, name string, typ uint16) *dns.Msg {
		t.Helper()
		q := new(dns.Msg)
		q.SetQuestion(name, typ)
		q.SetEdns0(1232, true)
		r, _, err := (&dns.Client{Net: network}).Exchange(q, addr)
		if err != nil {
			t.Fatal(err)
		}
		if r.Question[0].Name != name || r.IsEdns0() == nil || r.RecursionAvailable {
			t.Fatal(r)
		}
		return r
	}
	for _, network := range []string{"tcp", "udp"} {
		for _, typ := range []uint16{dns.TypeTXT, dns.TypeNS, dns.TypeSOA} {
			r := query(network, qname, typ)
			if !r.Authoritative || len(r.Answer) == 0 {
				t.Fatal(r)
			}
			if typ == dns.TypeTXT && len(r.Answer) != 2 {
				t.Fatal(r)
			}
		}
		if r := query(network, "another.example.test.", dns.TypeA); r.Rcode != dns.RcodeRefused {
			t.Fatal(r)
		}
	}
	if _, err := provider.DeleteRecords(ctx, s.zone, records[:1]); err != nil {
		t.Fatal(err)
	}
	if r := query("udp", qname, dns.TypeTXT); len(r.Answer) != 1 || r.Answer[0].(*dns.TXT).Txt[0] != "two" {
		t.Fatal(r)
	}
	if _, err := provider.AppendRecords(ctx, "other.test.", records); err == nil {
		t.Fatal("accepted an unrelated zone")
	}
}
