package netdns

import (
	"io"
	"log/slog"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func TestZonesIsolationAndAPI(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	p1 := freeUDPPort(t)
	p2 := freeUDPPort(t)
	// Same port for both gateways on loopback aliases is fine: each binds gw:port.
	_ = p2
	z := NewZones(quiet, net.IPv4(127, 0, 0, 1))
	z.SetUDPPort(strconv.Itoa(p1))
	z.setUpstreams(nil)
	defer z.Shutdown()

	if err := z.Register("runner", "10.89.0.2"); err != nil {
		t.Fatal(err)
	}
	if err := z.RegisterOn("127.0.0.2", "other", "10.90.0.2"); err != nil {
		t.Fatal(err)
	}
	// Bad inputs.
	for _, bad := range []struct{ gw, host, ip string }{
		{"not-ip", "a", "10.0.0.1"},
		{"127.0.0.1", "", "10.0.0.1"},
		{"127.0.0.1", "Bad Name", "10.0.0.1"},
		{"127.0.0.1", "ok", "not-ip"},
		{"127.0.0.1", "ok", "fd00::1"},
	} {
		if err := z.RegisterOn(bad.gw, bad.host, bad.ip); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}

	srv1 := net.JoinHostPort("127.0.0.1", strconv.Itoa(p1))
	srv2 := net.JoinHostPort("127.0.0.2", strconv.Itoa(p1))
	waitDNS(t, srv1)

	if in := queryAt(t, srv1, "runner", dns.TypeA); len(in.Answer) != 1 {
		t.Fatalf("runner on gw1: %v", in.Answer)
	}
	if in := queryAt(t, srv1, "other", dns.TypeA); len(in.Answer) != 0 {
		t.Fatalf("other visible on gw1: %v", in.Answer)
	}
	if in := queryAt(t, srv2, "other", dns.TypeA); len(in.Answer) != 1 {
		t.Fatalf("other on gw2: %v", in.Answer)
	}
	if in := queryAt(t, srv2, "runner", dns.TypeA); len(in.Answer) != 0 {
		t.Fatalf("runner visible on gw2: %v", in.Answer)
	}
	// AAAA for a local name: empty success.
	if in := queryAt(t, srv1, "runner", dns.TypeAAAA); len(in.Answer) != 0 || in.Rcode != dns.RcodeSuccess {
		t.Fatalf("AAAA: %v", in)
	}
	// Unknown name with no upstreams → SERVFAIL.
	if in := queryAt(t, srv1, "nowhere.test", dns.TypeA); in.Rcode != dns.RcodeServerFailure {
		t.Fatalf("no upstream: %v", in.Rcode)
	}
	// Forward path.
	upPort := freeUDPPort(t)
	upPC, err := net.ListenPacket("udp", "127.0.0.1:"+strconv.Itoa(upPort))
	if err != nil {
		t.Fatal(err)
	}
	up := &dns.Server{PacketConn: upPC, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		m.Answer = append(m.Answer, &dns.A{
			Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 1},
			A:   net.IPv4(9, 9, 9, 9),
		})
		_ = w.WriteMsg(m)
	})}
	go up.ActivateAndServe() //nolint:errcheck
	defer up.Shutdown()      //nolint:errcheck
	z.setUpstreams([]string{"127.0.0.1:1", upPC.LocalAddr().String()})
	if in := queryAt(t, srv1, "example.org", dns.TypeA); len(in.Answer) != 1 {
		t.Fatalf("forward: %v", in.Answer)
	}

	// Clear upstreams so an unknown/deregistered name is not answered by forward.
	z.setUpstreams(nil)
	if err := z.Deregister(" runner "); err != nil {
		t.Fatal(err)
	}
	_ = z.Deregister("")
	_ = z.DeregisterOn("bad", "x")
	if in := queryAt(t, srv1, "runner", dns.TypeA); len(in.Answer) != 0 {
		t.Fatalf("deregistered still answered: %v rcode=%d", in.Answer, in.Rcode)
	}
	z.Shutdown()
	z.Shutdown()
}

func TestZonesBusyPort(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	port := freeUDPPort(t)
	busy, err := net.ListenPacket("udp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	z := NewZones(quiet, net.IPv4(127, 0, 0, 1))
	z.SetUDPPort(strconv.Itoa(port))
	if err := z.Register("x", "10.0.0.9"); err == nil {
		t.Fatal("listen on busy port")
	}
	// Cached zone error must stick on the next RegisterOn for the same gateway.
	if err := z.Register("y", "10.0.0.8"); err == nil {
		t.Fatal("expected cached listen error")
	}
}

func TestZonesServeFormatErrorAndStartOnce(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	port := freeUDPPort(t)
	z := NewZones(quiet, net.IPv4(127, 0, 0, 1))
	z.SetUDPPort(strconv.Itoa(port))
	z.setUpstreams(nil)
	defer z.Shutdown()
	if err := z.Register("a", "10.0.0.2"); err != nil {
		t.Fatal(err)
	}
	z.mu.Lock()
	zn := z.zones["127.0.0.1"]
	z.mu.Unlock()
	if zn == nil {
		t.Fatal("missing zone")
	}
	if err := z.startZone(zn); err != nil {
		t.Fatalf("idempotent start: %v", err)
	}
	// Empty question list → format error path.
	rw := &captureRW{}
	z.serveZone(zn, rw, new(dns.Msg))
	if rw.msg == nil || rw.msg.Rcode != dns.RcodeFormatError {
		t.Fatalf("want FormErr, got %+v", rw.msg)
	}
}

type captureRW struct {
	msg *dns.Msg
}

func (c *captureRW) LocalAddr() net.Addr       { return &net.UDPAddr{} }
func (c *captureRW) RemoteAddr() net.Addr      { return &net.UDPAddr{} }
func (c *captureRW) WriteMsg(m *dns.Msg) error { c.msg = m; return nil }
func (c *captureRW) Close() error              { return nil }
func (c *captureRW) TsigStatus() error         { return nil }
func (c *captureRW) TsigTimersOnly(bool)       {}
func (c *captureRW) Hijack()                   {}
func (c *captureRW) Write([]byte) (int, error) { return 0, nil }

func TestNoopShutdown(t *testing.T) {
	var n Noop
	n.Shutdown()
	_ = n.Register("a", "1.2.3.4")
	_ = n.Deregister("a")
}

func TestParseGateway(t *testing.T) {
	if _, err := parseGateway("10.88.0.1"); err != nil {
		t.Fatal(err)
	}
	if _, err := parseGateway("not-an-ip"); err == nil {
		t.Fatal("expected error")
	}
	if _, err := parseGateway("fd00::1"); err == nil {
		t.Fatal("ipv6 rejected")
	}
}

func TestNewZonesNilLogAndSetUDPPort(t *testing.T) {
	z := NewZones(nil, nil)
	z.SetUDPPort("")
	z.SetUDPPort("5353")
	if z.udpPort != "5353" {
		t.Fatalf("port=%q", z.udpPort)
	}
}

func freeUDPPort(t *testing.T) int {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	return pc.LocalAddr().(*net.UDPAddr).Port
}

func waitDNS(t *testing.T, server string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		m := new(dns.Msg)
		m.SetQuestion(dns.Fqdn("."), dns.TypeA)
		if _, _, err := (&dns.Client{Net: "udp", Timeout: 100 * time.Millisecond}).Exchange(m, server); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func queryAt(t *testing.T, server, name string, qtype uint16) *dns.Msg {
	t.Helper()
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), qtype)
	in, _, err := (&dns.Client{Net: "udp", Timeout: 2 * time.Second}).Exchange(m, server)
	if err != nil {
		t.Fatalf("query %s @ %s: %v", name, server, err)
	}
	return in
}
