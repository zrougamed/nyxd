//go:build linux

package netdns

import (
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/miekg/dns"
	"github.com/zrougamed/nyxd/internal/testutil/nsrun"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func query(t *testing.T, name string, qtype uint16) *dns.Msg {
	t.Helper()
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), qtype)
	in, _, err := (&dns.Client{Net: "udp"}).Exchange(m, "127.0.0.1:53")
	if err != nil {
		t.Fatalf("query %s: %v", name, err)
	}
	return in
}

// The resolver runs on 127.0.0.1:53 inside a private network namespace.
func TestEmbeddedResolver(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	// A fake upstream for names nyxd does not know.
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	up := &dns.Server{PacketConn: pc, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		m.Answer = append(m.Answer, &dns.A{Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 1}, A: net.IPv4(9, 9, 9, 9)})
		_ = w.WriteMsg(m)
	})}
	go up.ActivateAndServe() //nolint:errcheck
	defer up.Shutdown()      //nolint:errcheck

	e := NewEmbedded(quiet, net.IPv4(127, 0, 0, 1))
	e.upstreams = []string{"127.0.0.1:1", pc.LocalAddr().String()} // first one fails
	for _, bad := range [][2]string{{"Bad Name", "10.0.0.2"}, {"-x", "10.0.0.2"}, {"web", "not-ip"}, {"web", "fd00::1"}} {
		if err := e.Register(bad[0], bad[1]); err == nil {
			t.Errorf("register %v accepted", bad)
		}
	}
	if err := e.Register("Web", "10.0.0.2"); err != nil {
		t.Fatal(err)
	}
	if err := e.Register("db", "10.0.0.3"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"web", "web.nyxd.local", "WEB.nyxd.local"} {
		in := query(t, name, dns.TypeA)
		if len(in.Answer) != 1 || in.Answer[0].(*dns.A).A.String() != "10.0.0.2" {
			t.Errorf("%s: %v", name, in.Answer)
		}
	}
	if in := query(t, "web", dns.TypeAAAA); len(in.Answer) != 0 || in.Rcode != dns.RcodeSuccess {
		t.Errorf("AAAA of a local name: %v", in)
	}
	if in := query(t, "example.org", dns.TypeA); len(in.Answer) != 1 {
		t.Errorf("forwarded: %v", in)
	}
	// No question: format error.
	in, _, err := (&dns.Client{Net: "udp"}).Exchange(&dns.Msg{MsgHdr: dns.MsgHdr{Id: 7}}, "127.0.0.1:53")
	if err == nil && in.Rcode != dns.RcodeFormatError {
		t.Errorf("empty question: %v", in)
	}
	// Upstreams all down: SERVFAIL.
	e.mu.Lock()
	e.upstreams = nil
	e.mu.Unlock()
	if in := query(t, "nowhere.test", dns.TypeA); in.Rcode != dns.RcodeServerFailure {
		t.Errorf("no upstream: %v", in.Rcode)
	}
	if err := e.Deregister(" WEB "); err != nil || e.Deregister("") != nil {
		t.Fatal(err)
	}
	if in := query(t, "web", dns.TypeA); len(in.Answer) != 0 {
		t.Error("deregistered name still answered")
	}
	e.Shutdown()
	e.Shutdown()

	// Port 53 taken: the second resolver reports it.
	busy, _ := net.ListenPacket("udp", "127.0.0.2:53")
	defer busy.Close()
	e2 := NewEmbedded(nil, net.IPv4(127, 0, 0, 2))
	if err := e2.Register("x", "10.0.0.9"); err == nil {
		t.Error("listen on a busy port")
	}
	e3 := NewEmbedded(quiet, nil)
	if err := e3.Register("x", "10.0.0.9"); err == nil {
		t.Error("nil gateway")
	}
}

func TestBackendSelectionAndUpstreams(t *testing.T) {
	for mode, zones := range map[string]bool{"": true, "auto": true, "embedded": true, "off": false, "cni": false, "weird": false} {
		b := NewBackend(mode, "native", quiet, net.IPv4(127, 0, 0, 1))
		if _, ok := b.(*Zones); ok != zones {
			t.Errorf("%q: %T", mode, b)
		}
		b.Shutdown()
	}
	var n Noop
	if n.Register("a", "b") != nil || n.Deregister("a") != nil {
		t.Error("noop")
	}
	t.Setenv("NYXD_GATEWAY_IP", "fd00::1")
	if DefaultGateway().String() != "10.88.0.1" {
		t.Error("IPv6 gateway must fall back")
	}
	t.Setenv("NYXD_GATEWAY_IP", "10.9.0.1")
	if DefaultGateway().String() != "10.9.0.1" {
		t.Error("gateway from env")
	}
	p := filepath.Join(t.TempDir(), "resolv.conf")
	_ = os.WriteFile(p, []byte("# c\n; c\n\nsearch x\nnameserver\nnameserver bogus\nnameserver [fd00::1]\nnameserver 1.2.3.4\n"), 0o644)
	if got := parseResolvConfNameservers(p); len(got) != 2 || got[1] != "1.2.3.4:53" {
		t.Errorf("nameservers %v", got)
	}
	if len(hostUpstreamResolvers()) == 0 {
		t.Error("no upstreams at all")
	}
}

func TestUpstreamFallback(t *testing.T) {
	if !nsrun.Run(t, "/run", "/etc") {
		return
	}
	// No resolv.conf anywhere: public resolvers.
	if got := hostUpstreamResolvers(); len(got) != 2 || got[0] != "8.8.8.8:53" {
		t.Errorf("fallback %v", got)
	}
}
