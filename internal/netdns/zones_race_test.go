package netdns

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// Concurrent register/deregister/query must stay race-clean and keep answers coherent.
func TestZonesConcurrentRegisterQuery(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	port := freeUDPPort(t)
	z := NewZones(quiet, net.IPv4(127, 0, 0, 1))
	z.SetUDPPort(strconv.Itoa(port))
	z.setUpstreams(nil)
	defer z.Shutdown()

	if err := z.Register("seed", "10.0.0.2"); err != nil {
		t.Fatal(err)
	}
	srv := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	waitDNS(t, srv)

	const workers = 8
	const iters = 40
	var wg sync.WaitGroup
	wg.Add(workers * 2)
	for w := 0; w < workers; w++ {
		w := w
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				host := fmt.Sprintf("h%d-%d", w, i%5)
				ip := fmt.Sprintf("10.0.%d.%d", w+1, (i%200)+2)
				if err := z.Register(host, ip); err != nil {
					t.Errorf("register: %v", err)
					return
				}
				_ = z.Deregister(host)
			}
		}()
		go func() {
			defer wg.Done()
			c := &dns.Client{Net: "udp", Timeout: 500 * time.Millisecond}
			for i := 0; i < iters; i++ {
				m := new(dns.Msg)
				m.SetQuestion(dns.Fqdn("seed"), dns.TypeA)
				_, _, _ = c.Exchange(m, srv)
			}
		}()
	}
	wg.Wait()
	if in := queryAt(t, srv, "seed", dns.TypeA); len(in.Answer) != 1 {
		t.Fatalf("seed lost under concurrency: %v", in.Answer)
	}
}

// Shutdown must release the UDP port (no PacketConn leak) and allow a fresh listen.
func TestZonesShutdownReleasesPort(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	port := freeUDPPort(t)
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	z := NewZones(quiet, net.IPv4(127, 0, 0, 1))
	z.SetUDPPort(strconv.Itoa(port))
	z.setUpstreams(nil)
	if err := z.Register("a", "10.0.0.2"); err != nil {
		t.Fatal(err)
	}
	waitDNS(t, addr)
	z.Shutdown()

	deadline := time.Now().Add(2 * time.Second)
	var last error
	released := false
	for time.Now().Before(deadline) {
		pc, err := net.ListenPacket("udp", addr)
		if err == nil {
			_ = pc.Close()
			released = true
			break
		}
		last = err
		time.Sleep(20 * time.Millisecond)
	}
	if !released {
		t.Fatalf("port still held after Shutdown: %v", last)
	}
	// Register after Shutdown must restart the listener (no leaked "dead" zone).
	if err := z.Register("b", "10.0.0.3"); err != nil {
		t.Fatal(err)
	}
	defer z.Shutdown()
	waitDNS(t, addr)
	if in := queryAt(t, addr, "a", dns.TypeA); len(in.Answer) != 1 {
		t.Fatalf("records should survive restart: %v", in.Answer)
	}
	if in := queryAt(t, addr, "b", dns.TypeA); len(in.Answer) != 1 {
		t.Fatalf("new name after restart: %v", in.Answer)
	}
}

// Shutdown while queries are in flight must not deadlock (mutex vs dns.Server.Shutdown).
func TestZonesShutdownUnderLoad(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	port := freeUDPPort(t)
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	z := NewZones(quiet, net.IPv4(127, 0, 0, 1))
	z.SetUDPPort(strconv.Itoa(port))
	z.setUpstreams(nil)
	if err := z.Register("a", "10.0.0.2"); err != nil {
		t.Fatal(err)
	}
	waitDNS(t, addr)

	done := make(chan struct{})
	go func() {
		defer close(done)
		c := &dns.Client{Net: "udp", Timeout: 100 * time.Millisecond}
		for i := 0; i < 200; i++ {
			m := new(dns.Msg)
			m.SetQuestion(dns.Fqdn("a"), dns.TypeA)
			_, _, _ = c.Exchange(m, addr)
		}
	}()
	time.Sleep(20 * time.Millisecond)
	finished := make(chan struct{})
	go func() {
		z.Shutdown()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown deadlocked under load")
	}
	<-done
}
