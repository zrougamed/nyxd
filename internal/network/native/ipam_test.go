//go:build linux

package native

import (
	"fmt"
	"net"
	"sync"
	"testing"
)

func TestIPAMAllocateRelease(t *testing.T) {
	dir := t.TempDir()
	a := newIPAM(dir, "10.88.0.0/24", "10.88.0.1")

	ids := []string{"c1", "c2", "c3"}
	ips := make([]string, len(ids))
	for i, id := range ids {
		ip, err := a.allocate(id)
		if err != nil {
			t.Fatalf("allocate %s: %v", id, err)
		}
		ips[i] = ip
		t.Logf("allocated %s → %s", id, ip)
	}

	// All IPs must be unique.
	seen := make(map[string]bool)
	for _, ip := range ips {
		if seen[ip] {
			t.Fatalf("duplicate IP: %s", ip)
		}
		seen[ip] = true
	}

	// Release c2 and re-allocate — must get same IP back (lowest available).
	if err := a.release("c2"); err != nil {
		t.Fatalf("release: %v", err)
	}
	ip2, err := a.allocate("c2-new")
	if err != nil {
		t.Fatalf("re-allocate: %v", err)
	}
	if ip2 != ips[1] {
		t.Errorf("expected re-use of %s, got %s", ips[1], ip2)
	}
}

func TestIPAMConcurrent(t *testing.T) {
	dir := t.TempDir()
	a := newIPAM(dir, "10.88.0.0/24", "10.88.0.1")

	const n = 20
	var wg sync.WaitGroup
	results := make([]string, n)
	errs := make([]error, n)

	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = a.allocate(fmt.Sprintf("c%d", i))
		}(i)
	}
	wg.Wait()

	seen := make(map[string]bool)
	for i, ip := range results {
		if errs[i] != nil {
			t.Errorf("goroutine %d: %v", i, errs[i])
			continue
		}
		if seen[ip] {
			t.Errorf("duplicate IP: %s", ip)
		}
		seen[ip] = true
	}
}

func TestIPAMExhaustion(t *testing.T) {
	// /29: .0 network, .1 gateway, .2–.6 hosts, .7 broadcast → 5 allocatable addresses.
	a := newIPAM(t.TempDir(), "10.0.0.0/29", "10.0.0.1")

	for i := range 5 {
		_, err := a.allocate(fmt.Sprintf("c%d", i))
		if err != nil {
			t.Fatalf("allocation %d failed unexpectedly: %v", i, err)
		}
	}
	_, err := a.allocate("overflow")
	if err == nil {
		t.Fatal("expected exhaustion, got nil")
	}
	t.Logf("exhaustion (expected): %v", err)
}

func TestIPConversion(t *testing.T) {
	cases := []string{"10.88.0.2", "10.88.255.254", "192.168.1.1"}
	for _, c := range cases {
		ip := net.ParseIP(c).To4()
		if ip == nil {
			t.Fatalf("bad IP: %s", c)
		}
		u := ipToU32(ip)
		back := u32ToIP(u)
		if back.String() != c {
			t.Errorf("round-trip %s → %d → %s", c, u, back)
		}
	}
}

func TestVethNames(t *testing.T) {
	cases := []string{"mycontainer", "c1", "verylongcontainerid123456"}
	for _, id := range cases {
		h, p := vethNames(id)
		if len(h) > 15 {
			t.Errorf("host veth name too long (%d): %s", len(h), h)
		}
		if len(p) > 15 {
			t.Errorf("peer veth name too long (%d): %s", len(p), p)
		}
		if h == p {
			t.Errorf("host and peer names identical: %s", h)
		}
	}
	a, _ := vethNames("nginx-alpine-111111111")
	b, _ := vethNames("nginx-alpine-222222222")
	if a == b {
		t.Errorf("expected different veth host names for distinct ids, got %s", a)
	}
}
