package supervisor

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zrougamed/nyxd/internal/bundle"
	"github.com/zrougamed/nyxd/internal/health"
	"github.com/zrougamed/nyxd/internal/network"
	"github.com/zrougamed/nyxd/pkg/oci"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestRestartPolicyFromString(t *testing.T) {
	for in, want := range map[string]RestartPolicy{
		"": RestartUnlessStopped, "unless_stopped": RestartUnlessStopped, "always": RestartAlways,
		"on-failure": RestartOnFailure, "on_failure": RestartOnFailure, "no": RestartNever, "never": RestartNever, "x": RestartNever,
	} {
		if got := restartPolicyFromString(in); got != want {
			t.Errorf("%q = %s", in, got)
		}
	}
}

func TestShouldRestartAndBackoff(t *testing.T) {
	s := &Supervisor{}
	for _, c := range []struct {
		policy   RestartPolicy
		max      int
		restarts int
		code     int
		want     bool
	}{
		{RestartAlways, 0, 9, 0, true},
		{RestartAlways, 2, 2, 1, false},
		{RestartOnFailure, 0, 0, 0, false},
		{RestartOnFailure, 0, 0, 3, true},
		{RestartUnlessStopped, 0, 0, 0, true},
		{RestartNever, 0, 0, 1, false},
		{"", 0, 0, 1, false},
		{"weird", 0, 0, 1, false},
	} {
		e := &containerEntry{spec: ContainerSpec{RestartPolicy: c.policy, MaxRestarts: c.max}, restarts: c.restarts}
		if got := s.shouldRestart(e, c.code); got != c.want {
			t.Errorf("%+v: %v", c, got)
		}
	}
	if d := backoff(1000); d < 30*time.Second || d > 34*time.Second {
		t.Errorf("backoff cap: %s", d)
	}
	if d := backoff(1); d < 100*time.Millisecond || d > 120*time.Millisecond {
		t.Errorf("backoff(1): %s", d)
	}
}

func TestReadinessWaitContext(t *testing.T) {
	ctx, cancel := readinessWaitContext(context.Background(), nil)
	cancel()
	if _, ok := ctx.Deadline(); ok {
		t.Error("nil config must not add a deadline")
	}
	for _, tc := range []struct {
		cfg  health.Config
		want time.Duration
	}{
		{health.Config{}, 45 * time.Second},
		{health.Config{StartPeriod: time.Hour}, 15 * time.Minute},
		{health.Config{Retries: 30, Timeout: 2 * time.Second}, 38 * (2*time.Second + 500*time.Millisecond)},
	} {
		cfg, want := tc.cfg, tc.want
		c := cfg
		ctx, cancel := readinessWaitContext(context.Background(), &c)
		dl, _ := ctx.Deadline()
		cancel()
		if got := time.Until(dl).Round(time.Second); got != want.Round(time.Second) {
			t.Errorf("%+v: %s, want %s", cfg, got, want)
		}
	}
}

func TestBundleMeta(t *testing.T) {
	dir := t.TempDir()
	if err := writeBundleRunMeta("", bundleRunMeta{}); err == nil {
		t.Error("empty dir")
	}
	if err := os.WriteFile(filepath.Join(dir, "f"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeBundleRunMeta(filepath.Join(dir, "f", "b"), bundleRunMeta{}); err == nil {
		t.Error("dir under a file")
	}
	b := filepath.Join(dir, "b")
	if _, err := readBundleRunMeta(b); err == nil {
		t.Error("missing meta")
	}
	if err := writeBundleRunMeta(b, bundleRunMeta{Image: "x:1", Publish: []string{"1:2/tcp"}}); err != nil {
		t.Fatal(err)
	}
	if m, err := readBundleRunMeta(b); err != nil || m.Image != "x:1" {
		t.Fatalf("%+v %v", m, err)
	}
	if err := writeBundleRunMeta(b, bundleRunMeta{}); err != nil {
		t.Fatal(err)
	}
	if _, err := readBundleRunMeta(b); err == nil || !strings.Contains(err.Error(), "missing image") {
		t.Errorf("meta without image: %v", err)
	}
	if err := os.WriteFile(filepath.Join(b, bundleMetaFileName), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readBundleRunMeta(b); err == nil {
		t.Error("corrupt meta")
	}
	removeBundle("")
	removeBundle(b)
	if _, err := os.Stat(b); !os.IsNotExist(err) {
		t.Error("bundle not removed")
	}
	if got := portMappingsToPublishStrings([]network.PortMapping{{HostPort: 1, ContainerPort: 2}, {HostPort: 3, ContainerPort: 4, Protocol: "UDP"}}); strings.Join(got, ",") != "1:2/tcp,3:4/udp" {
		t.Errorf("publish strings %v", got)
	}
	if DisplayID("") != "" || len(DisplayID("x")) != 12 {
		t.Error("DisplayID")
	}
}

func TestPersistErrors(t *testing.T) {
	dir := t.TempDir()
	s := &Supervisor{baseDir: dir, log: quiet}
	if err := s.persistContainerEntry(nil); err != nil {
		t.Error("nil entry")
	}
	if err := s.persistContainerEntry(&containerEntry{}); err == nil {
		t.Error("empty id")
	}
	s.removePersistedState("")
	dir = t.TempDir()
	s = &Supervisor{baseDir: dir, log: quiet}
	if err := os.WriteFile(filepath.Join(dir, "supervisor"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.persistContainerEntry(&containerEntry{spec: ContainerSpec{ID: "x"}}); err == nil {
		t.Error("state dir under a file")
	}
	s.reconcilePersisted() // logs and returns
}

type fakeDNS struct {
	err        error
	registered map[string]string
}

func (f *fakeDNS) Register(h, ip string) error {
	if f.registered == nil {
		f.registered = map[string]string{}
	}
	f.registered[h] = ip
	return f.err
}
func (f *fakeDNS) Deregister(h string) error { delete(f.registered, h); return f.err }
func (f *fakeDNS) Shutdown()                 {}

func TestDNSPolicy(t *testing.T) {
	for _, c := range []struct {
		driver, mode string
		embed        bool
		want         bool
	}{
		{"host", "embedded", true, false},
		{"native", "off", true, false},
		{"native", "cni", true, false},
		{"cni", "embedded", false, true},
		{"native", "", false, true},
		{"native", "auto", false, true},
		{"cni", "auto", false, false},
		{"cni", "auto", true, true},
		{"native", "bogus", true, false},
	} {
		s := &Supervisor{netDriver: c.driver, dnsMode: c.mode}
		if got := s.shouldRegisterDNS(ContainerSpec{EmbedDNS: c.embed}); got != c.want {
			t.Errorf("%+v: %v", c, got)
		}
	}
	if dnsHostForSpec(ContainerSpec{ID: "Id1"}) != "id1" || dnsHostForSpec(ContainerSpec{ID: "x", Hostname: " Web "}) != "web" {
		t.Error("dnsHostForSpec")
	}

	f := &fakeDNS{}
	s := &Supervisor{netDriver: "native", dnsMode: "auto", dns: f, log: quiet}
	s.registerEmbeddedDNS(ContainerSpec{ID: "a"}, "")
	s.registerEmbeddedDNS(ContainerSpec{ID: "a"}, "10.0.0.2")
	if f.registered["a"] != "10.0.0.2" {
		t.Errorf("registered %v", f.registered)
	}
	s.dnsDeregisterForSpec(ContainerSpec{ID: "a"})
	f.err = errors.New("dns down")
	s.registerEmbeddedDNS(ContainerSpec{ID: "b"}, "10.0.0.3") // logged
	s.dnsDeregisterForSpec(ContainerSpec{ID: "b"})            // logged
	(&Supervisor{netDriver: "host"}).dnsDeregisterForSpec(ContainerSpec{ID: "b"})
}

func TestWriteBundleResolv(t *testing.T) {
	dir := t.TempDir()
	s := &Supervisor{netDriver: "native", dnsMode: "off"}
	if p, err := s.writeBundleResolv(dir, ContainerSpec{}); p != "" || err != nil {
		t.Errorf("no dns: %q %v", p, err)
	}
	for gw, want := range map[string]string{"10.9.0.1": "nameserver 10.9.0.1", "bogus": "nameserver 10.88.0.1", "": "nameserver 10.88.0.1"} {
		s := &Supervisor{netDriver: "native", dnsMode: "embedded", dnsGateway: gw}
		p, err := s.writeBundleResolv(dir, ContainerSpec{})
		b, _ := os.ReadFile(p)
		if err != nil || !strings.Contains(string(b), want) {
			t.Errorf("gw %q: %s %v", gw, b, err)
		}
	}
	s = &Supervisor{netDriver: "native", dnsMode: "embedded"}
	if _, err := s.writeBundleResolv(filepath.Join(dir, "nope", "x"), ContainerSpec{}); err == nil {
		t.Error("write into a missing dir")
	}
	if p, err := (&Supervisor{netDriver: "host"}).writeBundleResolv(dir, ContainerSpec{}); err != nil || (p != "" && p != "/etc/resolv.conf") {
		t.Errorf("host: %q %v", p, err)
	}
}

func TestComposeComparable(t *testing.T) {
	no := false
	base := ContainerSpec{
		ID: "s", Image: "img:1", Args: []string{"a"}, Env: []string{"E=1"},
		ImageConfig:    &oci.ImageConfig{Config: oci.ContainerConfig{Entrypoint: []string{"/e"}}},
		ManifestLayers: []oci.Descriptor{{Digest: "sha256:1"}},
		PortMappings:   []network.PortMapping{{HostPort: 2, ContainerPort: 2}, {HostPort: 1, ContainerPort: 1, Protocol: "udp"}, {HostPort: 1, ContainerPort: 3}},
		ExtraMounts: []bundle.Mount{
			{Type: "bind", Source: "/b", Destination: "/d", Options: []string{"rw", "rbind"}},
			{Type: "bind", Source: "/a", Destination: "/d", Options: []string{"ro"}},
			{Type: "tmpfs", Source: "t", Destination: "/a"},
			{Type: "bind", Source: "/a", Destination: "/d", Options: []string{"rw"}},
			{Type: "aaa", Source: "/a", Destination: "/d", Options: []string{"rw"}},
		},
		UIDMappings: []bundle.LinuxIDMapping{{ContainerID: 1}, {ContainerID: 0}},
		GIDMappings: []bundle.LinuxIDMapping{{ContainerID: 1}, {ContainerID: 0}},
		User:        &bundle.User{UID: 1},
		Healthcheck: &health.Config{Type: health.TypeTCP, Address: "x:1"},
		Resources:   &bundle.Resources{},
		CapAdd:      []string{"B", "A"}, NoNewPrivileges: &no,
	}
	same := base
	same.ExtraMounts = append([]bundle.Mount(nil), base.ExtraMounts...)
	same.ExtraMounts[0].Options = []string{"rbind", "rw"}
	if !composeSpecsEqual(base, same) {
		t.Error("option order must not matter")
	}
	other := base
	other.Args = []string{"b"}
	if composeSpecsEqual(base, other) {
		t.Error("different args compared equal")
	}
	if len(composeComparableJSON(ContainerSpec{})) == 0 {
		t.Error("empty spec")
	}
}

type failNet struct{ setupErr, teardownErr error }

func (f failNet) EnsureNetwork() error { return nil }
func (f failNet) Setup(context.Context, string, string, []network.PortMapping, *network.SetupOptions) (string, error) {
	return "10.88.0.9", f.setupErr
}
func (f failNet) Teardown(context.Context, string, string) error { return f.teardownErr }

func TestTeardownNetworkLogsFailures(t *testing.T) {
	s := &Supervisor{netDriver: "native", dnsMode: "auto", dns: &fakeDNS{}, log: quiet,
		net: failNet{teardownErr: errors.New("boom")}, containers: map[string]*containerEntry{"c": {spec: ContainerSpec{ID: "c"}}}}
	s.teardownNetwork(context.Background(), "c")
	s.teardownNetwork(context.Background(), "unknown")
}

func TestKillForRestartUnknown(t *testing.T) {
	s := &Supervisor{containers: map[string]*containerEntry{}, log: quiet}
	if err := s.KillForRestart(context.Background(), "x"); err == nil {
		t.Error("unknown container")
	}
	if err := s.Kill(context.Background(), "x", ""); err == nil {
		t.Error("kill unknown container")
	}
	if err := s.Stop(context.Background(), "x"); err == nil {
		t.Error("stop unknown container")
	}
	if err := s.Remove(context.Background(), "x"); err == nil {
		t.Error("remove unknown container")
	}
}

func TestComposeComparableOrdering(t *testing.T) {
	a := ContainerSpec{
		PortMappings: []network.PortMapping{{HostPort: 9, ContainerPort: 1, Protocol: "udp"}, {HostPort: 8, ContainerPort: 1, Protocol: "tcp"}, {HostPort: 8, ContainerPort: 1, Protocol: "udp"}},
		UIDMappings:  []bundle.LinuxIDMapping{{ContainerID: 0, HostID: 2}, {ContainerID: 0, HostID: 1}},
		GIDMappings:  []bundle.LinuxIDMapping{{ContainerID: 0, HostID: 2}, {ContainerID: 0, HostID: 1}},
	}
	b := a
	b.PortMappings = []network.PortMapping{a.PortMappings[2], a.PortMappings[0], a.PortMappings[1]}
	b.UIDMappings = []bundle.LinuxIDMapping{a.UIDMappings[1], a.UIDMappings[0]}
	b.GIDMappings = []bundle.LinuxIDMapping{a.GIDMappings[1], a.GIDMappings[0]}
	if !composeSpecsEqual(a, b) {
		t.Error("order of ports and ID mappings must not matter")
	}
	s := &Supervisor{netDriver: "host"}
	if s.IsBindSourceInUse("") || s.IsBindSourceInUse(".") {
		t.Error("empty bind source")
	}
}
