package netdns

import (
	"net"
	"testing"

	"github.com/miekg/dns"
)

func TestZonesIsolation(t *testing.T) {
	if !canBindDNS(t) {
		t.Skip("need udp/53 on 127.0.0.1 and 127.0.0.2")
	}
	z := NewZones(quiet, net.IPv4(127, 0, 0, 1))
	defer z.Shutdown()
	if err := z.RegisterOn("127.0.0.1", "runner", "10.89.0.2"); err != nil {
		t.Fatal(err)
	}
	if err := z.RegisterOn("127.0.0.2", "other", "10.90.0.2"); err != nil {
		t.Fatal(err)
	}
	if in := queryAt(t, "127.0.0.1:53", "runner", dns.TypeA); len(in.Answer) != 1 {
		t.Fatalf("runner on gw1: %v", in.Answer)
	}
	if in := queryAt(t, "127.0.0.1:53", "other", dns.TypeA); len(in.Answer) != 0 {
		t.Fatalf("other visible on gw1: %v", in.Answer)
	}
	if in := queryAt(t, "127.0.0.2:53", "other", dns.TypeA); len(in.Answer) != 1 {
		t.Fatalf("other on gw2: %v", in.Answer)
	}
	if in := queryAt(t, "127.0.0.2:53", "runner", dns.TypeA); len(in.Answer) != 0 {
		t.Fatalf("runner visible on gw2: %v", in.Answer)
	}
}

func canBindDNS(t *testing.T) bool {
	t.Helper()
	for _, addr := range []string{"127.0.0.1:53", "127.0.0.2:53"} {
		pc, err := net.ListenPacket("udp", addr)
		if err != nil {
			return false
		}
		_ = pc.Close()
	}
	return true
}

func queryAt(t *testing.T, server, name string, qtype uint16) *dns.Msg {
	t.Helper()
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), qtype)
	in, _, err := (&dns.Client{Net: "udp"}).Exchange(m, server)
	if err != nil {
		t.Fatalf("query %s @ %s: %v", name, server, err)
	}
	return in
}
