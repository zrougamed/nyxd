package compose

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zrougamed/nyxd/internal/health"
	"github.com/zrougamed/nyxd/internal/image"
	"github.com/zrougamed/nyxd/internal/supervisor"
	"github.com/zrougamed/nyxd/pkg/oci"
	"gopkg.in/yaml.v3"
)

func TestDependsOnForms(t *testing.T) {
	type wrap struct {
		D DependsOn `yaml:"depends_on"`
	}
	for src, want := range map[string]string{
		"depends_on: [a, b]":                        "a,b",
		"depends_on:\n  a: {condition: x}\n  b: {}": "a,b",
		"depends_on: null":                          "",
		"depends_on: ~":                             "",
		"depends_on: ''":                            "",
	} {
		var w wrap
		if err := yaml.Unmarshal([]byte(src), &w); err != nil {
			t.Errorf("%q: %v", src, err)
			continue
		}
		var names []string
		for _, n := range w.D {
			names = append(names, n)
		}
		if got := strings.Join(names, ","); got != want && !(want == "a,b" && got == "b,a") {
			t.Errorf("%q: %q", src, got)
		}
	}
	for _, src := range []string{"depends_on: x", "depends_on: [[x]]", "depends_on:\n  - {a: 1}", "depends_on:\n  [a]: 1", "depends_on: !!binary aGk="} {
		var w wrap
		if err := yaml.Unmarshal([]byte(src), &w); err == nil {
			t.Errorf("%q accepted", src)
		}
	}
}

func TestDotEnvAndDiscovery(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ".env"), []byte("# c\n\nNOEQ\n=empty\nA = 1\nB=\"two\"\nC='three'\n"), 0o644)
	t.Setenv("C", "from-shell")
	m := InterpEnvFromDir(dir)
	if m["A"] != "1" || m["B"] != "two" || m["C"] != "from-shell" || m[""] != "" {
		t.Errorf("env %v %v %v", m["A"], m["B"], m["C"])
	}
	if err := mergeDotEnvFile(map[string]string{}, filepath.Join(dir, "missing")); err != nil {
		t.Error("missing .env must be fine")
	}
	if err := mergeDotEnvFile(map[string]string{}, dir); err == nil {
		t.Error(".env that is a directory")
	}
	_ = os.Mkdir(filepath.Join(dir, "unreadable.env"), 0o700)
	wd, _ := os.Getwd()
	defer os.Chdir(wd) //nolint:errcheck
	_ = os.Chdir(dir)
	if _, err := DefaultComposePath(""); err == nil {
		t.Error("no compose file in cwd")
	}
	if !isSubstIdentByte('_') || isSubstIdentByte('-') {
		t.Error("isSubstIdentByte")
	}
	if _, err := ParseFile(filepath.Join(dir, "nope.yaml")); err == nil {
		t.Error("ParseFile missing")
	}
}

func TestHealthcheckForms(t *testing.T) {
	d := func(s string) Duration { v, _ := time.ParseDuration(s); return Duration{Duration: v} }
	for _, c := range []struct {
		h    *Healthcheck
		typ  health.Type
		cmd  string
		fail bool
	}{
		{nil, "", "", false},
		{&Healthcheck{}, "", "", false},
		{&Healthcheck{Test: []string{"NONE"}}, health.TypeNone, "", false},
		{&Healthcheck{Test: []string{"CMD", "true"}, Interval: d("1s"), Timeout: d("2s"), Retries: 2}, health.TypeExec, "true", false},
		{&Healthcheck{Test: []string{"CMD-SHELL", "a", "b"}}, health.TypeExec, "/bin/sh -c a b", false},
		{&Healthcheck{Test: []string{"curl", "-f"}}, health.TypeExec, "curl -f", false},
		{&Healthcheck{Test: []string{"CMD"}}, "", "", true},
	} {
		cfg, err := HealthcheckToConfig(c.h)
		if (err != nil) != c.fail {
			t.Errorf("%+v: %v", c.h, err)
			continue
		}
		if cfg == nil {
			if c.typ != "" {
				t.Errorf("%+v: nil config", c.h)
			}
			continue
		}
		if cfg.Type != c.typ || (c.cmd != "" && strings.Join(cfg.Command, " ") != c.cmd) {
			t.Errorf("%+v: %+v", c.h, cfg)
		}
		if c.typ == health.TypeExec && (cfg.Interval <= 0 || cfg.Timeout <= 0 || cfg.Retries <= 0) {
			t.Errorf("defaults not applied: %+v", cfg)
		}
	}
}

func TestResources(t *testing.T) {
	i := func(v int) *int { return &v }
	b := true
	if r, err := BundleResourcesFromDeploy(nil); r != nil || err != nil {
		t.Error("nil deploy")
	}
	if r, err := BundleResourcesFromDeploy(&Deploy{}); r != nil || err != nil {
		t.Error("empty deploy")
	}
	r, err := BundleResourcesFromDeploy(&Deploy{Resources: Resources{
		Limits: ResourceSpec{CPUs: "0.005", Memory: "1.5g", PidsLimit: i(10), OomKillDisable: &b, OomScoreAdj: i(5),
			CpusetCpus: " 0-1 ", BlkioWeight: i(100000)},
		Reservations: ResourceSpec{Memory: "64mb"},
	}})
	if err != nil || r.Memory.Limit != 1610612736 || r.Memory.Reservation != 64<<20 || r.CPU.Quota != 1000 ||
		r.CPU.Cpus != "0-1" || r.Pids.Limit != 10 || r.BlockIO.Weight != 0xffff {
		t.Errorf("%+v %v", r, err)
	}
	// Reservation only; OOM knobs alone; cpuset alone.
	if r, _ := BundleResourcesFromDeploy(&Deploy{Resources: Resources{Reservations: ResourceSpec{Memory: "1k"}}}); r.Memory.Reservation != 1024 {
		t.Errorf("reservation only %+v", r)
	}
	if r, _ := BundleResourcesFromDeploy(&Deploy{Resources: Resources{Limits: ResourceSpec{OomScoreAdj: i(1)}}}); r.Memory == nil {
		t.Error("oom score only")
	}
	if r, _ := BundleResourcesFromDeploy(&Deploy{Resources: Resources{Limits: ResourceSpec{CpusetCpus: "1"}}}); r.CPU == nil {
		t.Error("cpuset only")
	}
	for _, bad := range []Deploy{
		{Resources: Resources{Limits: ResourceSpec{Memory: "x"}}},
		{Resources: Resources{Reservations: ResourceSpec{Memory: "1q"}}},
		{Resources: Resources{Limits: ResourceSpec{CPUs: "-1"}}},
		{Resources: Resources{Limits: ResourceSpec{CPUs: "lots"}}},
	} {
		if _, err := BundleResourcesFromDeploy(&bad); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	for s, want := range map[string]int64{"": 0, "10": 10, "10b": 10, "2kib": 2048, "1mib": 1 << 20, "1gib": 1 << 30} {
		if v, err := parseByteSize(s); err != nil || v != want {
			t.Errorf("%q: %d %v", s, v, err)
		}
	}
	for _, s := range []string{"1.2.3m", "-1", "1x"} {
		if _, err := parseByteSize(s); err == nil {
			t.Errorf("%q accepted", s)
		}
	}
	if q, p, err := parseCPUQuota(""); q != 0 || p != 0 || err != nil {
		t.Error("empty cpus")
	}
	for in, want := range map[string]string{"": "", "unconfined": "unconfined", "/abs.json": "/abs.json", "rel.json": "/c/rel.json"} {
		if got := ResolveSeccompProfilePath("/c", in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
	if ResolveSeccompProfilePath("", "rel.json") != "rel.json" {
		t.Error("no compose dir")
	}
}

func TestMountsAndNames(t *testing.T) {
	stack := &Stack{Volumes: map[string]Volume{"v": {}}}
	for _, bad := range []string{"", "a", "a:/b:zz", "a:rel", "../x:/y", "C:\\x:/b:/c:ro"} {
		if _, err := parseVolumeMount(bad, stack, t.TempDir(), "p", t.TempDir()); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	// One-letter volume names are not Windows drives.
	for line, ro := range map[string]bool{"v:/d:ro": true, "v:/d:rw": false, "v:/d": false, "/abs:/d:RO": true, "C:\\x:/d": false} {
		m, err := parseVolumeMount(line, stack, t.TempDir(), "p", t.TempDir())
		if err != nil || containsOpt(m.Options, "ro") != ro {
			t.Errorf("%q: %+v %v", line, m, err)
		}
	}
	if _, err := ServiceExtraMounts(&Service{Volumes: []string{"bad"}}, stack, t.TempDir(), "p", t.TempDir()); err == nil {
		t.Error("ServiceExtraMounts error")
	}
	if _, err := parseVolumeMount("v:/d", stack, t.TempDir(), "p", "/proc/nope"); err == nil {
		t.Error("named volume dir cannot be created")
	}
	if isNamedVolume(" ", stack) {
		t.Error("blank volume name")
	}
	for in, want := range map[string]string{"My App!": "my-app", "---": "default", "": "default", "a_b-1": "a_b-1"} {
		if got := sanitizePathPart(in); got != want {
			t.Errorf("sanitize %q: %q", in, got)
		}
	}
	if EnvMapToSlice(nil) != nil || strings.Join(EnvMapToSlice(map[string]string{"B": "2", "A": "1"}), ",") != "A=1,B=2" {
		t.Error("EnvMapToSlice")
	}
	if composeContainerID("default", "web") != "web" || composeContainerID("proj", "web") != "proj-web" {
		t.Error("composeContainerID")
	}
	for in, ok := range map[string]bool{"": true, "1:2": true, "1": false, "x:1": false, "1:y": false} {
		if _, err := parseComposeUser(in); (err == nil) != ok {
			t.Errorf("user %q: %v", in, err)
		}
	}
	for in, want := range map[RestartPolicy]supervisor.RestartPolicy{"always": supervisor.RestartAlways, "on-failure": supervisor.RestartOnFailure,
		"unless-stopped": supervisor.RestartUnlessStopped, "no": supervisor.RestartNever, "": supervisor.RestartNever} {
		if got := composeRestartPolicy(in); got != want {
			t.Errorf("restart %q: %s", in, got)
		}
	}
	// Docker semantics: entrypoint and command combine, compose overrides the image.
	cfg := &oci.ImageConfig{Config: oci.ContainerConfig{Entrypoint: []string{"/e"}, Cmd: []string{"c"}}}
	for _, c := range []struct {
		svc  Service
		want string
	}{
		{Service{}, "/e c"},
		{Service{Command: []string{"x"}}, "/e x"},
		{Service{Entrypoint: []string{"/ep"}}, "/ep c"},
		{Service{Entrypoint: []string{"/ep"}, Command: []string{"a"}}, "/ep a"},
	} {
		if got := strings.Join(composeArgs(&c.svc, cfg), " "); got != c.want {
			t.Errorf("composeArgs(%v %v) = %q, want %q", c.svc.Entrypoint, c.svc.Command, got, c.want)
		}
	}
	if composeArgs(&Service{Entrypoint: []string{"/ep"}}, nil) == nil {
		t.Error("entrypoint without image config")
	}
	m := image.AuthMap{"ghcr.io": {Username: "g", Password: "p"}}
	if pullAuth(&Service{}, nil, "x") != nil || pullAuth(&Service{RegistryUsername: "u"}, m, "ghcr.io/a").Username != "u" ||
		pullAuth(&Service{}, m, "ghcr.io/a/b:1").Username != "g" || pullAuth(&Service{}, m, "alpine") != nil {
		t.Error("pullAuth")
	}
}
