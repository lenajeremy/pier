// Package challengedns serves the single zone delegated to Pier for ACME DNS-01.
package challengedns

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/libdns/libdns"
	"github.com/miekg/dns"
)

type Server struct {
	zone, ns string
	mu       sync.RWMutex
	values   map[string]int
	serial   uint32
	servers  []*dns.Server
}

// Caddy creates a provider on each config reload. The listener and active
// challenges belong to pierd and survive those reloads.
var zones sync.Map // lower-case FQDN -> *Server

func Start(base, ns, addr string) (*Server, error) {
	s := &Server{zone: dns.CanonicalName("_acme-challenge." + base), ns: dns.CanonicalName(ns), values: map[string]int{}, serial: uint32(time.Now().Unix())}
	tcp, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	udp, err := net.ListenPacket("udp", tcp.Addr().String())
	if err != nil {
		tcp.Close()
		return nil, err
	}
	if _, loaded := zones.LoadOrStore(s.zone, s); loaded {
		tcp.Close()
		udp.Close()
		return nil, fmt.Errorf("already serving %s", s.zone)
	}
	s.servers = []*dns.Server{{Listener: tcp, Handler: s}, {PacketConn: udp, Handler: s}}
	for _, srv := range s.servers {
		ready := make(chan struct{})
		srv.NotifyStartedFunc = func() { close(ready) }
		go srv.ActivateAndServe()
		<-ready
	}
	return s, nil
}

func (s *Server) Close() {
	zones.Delete(s.zone)
	for _, srv := range s.servers {
		srv.Shutdown()
	}
}

func (s *Server) ServeDNS(w dns.ResponseWriter, req *dns.Msg) {
	m := new(dns.Msg)
	m.SetReply(req)
	if opt := req.IsEdns0(); opt != nil {
		m.SetEdns0(min(opt.UDPSize(), 1232), opt.Do())
	}
	if req.Opcode != dns.OpcodeQuery || len(req.Question) != 1 || req.Question[0].Qclass != dns.ClassINET || !strings.EqualFold(req.Question[0].Name, s.zone) {
		m.Rcode = dns.RcodeRefused
		w.WriteMsg(m)
		return
	}
	m.Authoritative = true
	q := req.Question[0]
	header := func(typ uint16) dns.RR_Header {
		return dns.RR_Header{Name: q.Name, Rrtype: typ, Class: dns.ClassINET, Ttl: 5}
	}
	s.mu.RLock()
	soa := &dns.SOA{Hdr: header(dns.TypeSOA), Ns: s.ns, Mbox: "hostmaster." + s.ns, Serial: s.serial, Refresh: 60, Retry: 30, Expire: 3600, Minttl: 5}
	switch q.Qtype {
	case dns.TypeSOA:
		m.Answer = []dns.RR{soa}
	case dns.TypeNS:
		m.Answer = []dns.RR{&dns.NS{Hdr: header(dns.TypeNS), Ns: s.ns}}
	case dns.TypeTXT:
		var values []string
		for v := range s.values {
			values = append(values, v)
		}
		sort.Strings(values)
		for _, v := range values {
			m.Answer = append(m.Answer, &dns.TXT{Hdr: header(dns.TypeTXT), Txt: []string{v}})
		}
		if len(values) == 0 {
			m.Ns = []dns.RR{soa}
		}
	default:
		m.Ns = []dns.RR{soa}
	}
	s.mu.RUnlock()
	if w.LocalAddr().Network() == "udp" {
		size := dns.MinMsgSize
		if opt := m.IsEdns0(); opt != nil {
			size = int(opt.UDPSize())
		}
		m.Truncate(size)
	}
	w.WriteMsg(m)
}

type Provider struct {
	Zone string `json:"zone"`
}

func init() { caddy.RegisterModule(Provider{}) }
func (Provider) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{ID: "dns.providers.pier", New: func() caddy.Module { return new(Provider) }}
}
func (p Provider) AppendRecords(ctx context.Context, zone string, rr []libdns.Record) ([]libdns.Record, error) {
	return p.change(ctx, zone, rr, 1)
}
func (p Provider) DeleteRecords(ctx context.Context, zone string, rr []libdns.Record) ([]libdns.Record, error) {
	return p.change(ctx, zone, rr, -1)
}
func (p Provider) change(ctx context.Context, zone string, records []libdns.Record, delta int) ([]libdns.Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	value, ok := zones.Load(dns.CanonicalName(p.Zone))
	if !ok {
		return nil, fmt.Errorf("DNS listener for %s is unavailable", p.Zone)
	}
	s := value.(*Server)
	for _, rec := range records {
		rr := rec.RR()
		if rr.Type != "TXT" || !strings.EqualFold(dns.Fqdn(libdns.AbsoluteName(rr.Name, zone)), s.zone) || len(rr.Data) > 255 {
			return nil, fmt.Errorf("only challenge TXT records at %s are supported", s.zone)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rec := range records {
		v := rec.RR().Data
		s.values[v] += delta
		if s.values[v] <= 0 {
			delete(s.values, v)
		}
	}
	s.serial++
	return records, nil
}
