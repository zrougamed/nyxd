package compose

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestTmpfsMount(t *testing.T) {
	cases := map[string]struct {
		dst, opts string
	}{
		"/tmp":                         {"/tmp", "nosuid,nodev,noexec"},
		"/run/app/:size=64m,mode=1777": {"/run/app", "nosuid,nodev,noexec,size=64m,mode=1777"},
		"/x:exec,suid,dev":             {"/x", "suid,dev,exec"},
		"/x:ro,uid=1000,gid=1000":      {"/x", "nosuid,nodev,noexec,ro,uid=1000,gid=1000"},
		"/x:ro,rw,,nr_inodes=10":       {"/x", "nosuid,nodev,noexec,nr_inodes=10"},
	}
	for in, want := range cases {
		m, err := TmpfsMount(in)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if m.Destination != want.dst || m.Type != "tmpfs" || strings.Join(m.Options, ",") != want.opts {
			t.Errorf("%q: got %s %v, want %s %s", in, m.Destination, m.Options, want.dst, want.opts)
		}
	}
	for _, bad := range []string{"", "tmp", "/", "/x/..", "/x:bind", "/x:context=foo", "/x:loud"} {
		if _, err := TmpfsMount(bad); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
	if _, err := TmpfsMounts([]string{"/a", "rel"}); err == nil {
		t.Error("TmpfsMounts: expected error")
	}
}

func TestTmpfsYAML(t *testing.T) {
	var svc struct {
		Tmpfs Tmpfs `yaml:"tmpfs"`
	}
	if err := yaml.Unmarshal([]byte("tmpfs: /tmp"), &svc); err != nil || strings.Join(svc.Tmpfs, "|") != "/tmp" {
		t.Errorf("scalar: %v %v", svc.Tmpfs, err)
	}
	if err := yaml.Unmarshal([]byte("tmpfs: [/tmp, '/run:size=1m']"), &svc); err != nil || strings.Join(svc.Tmpfs, "|") != "/tmp|/run:size=1m" {
		t.Errorf("list: %v %v", svc.Tmpfs, err)
	}
	for _, bad := range []string{"tmpfs: {a: b}", "tmpfs: [[a]]"} {
		if err := yaml.Unmarshal([]byte(bad), &svc); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}

	mounts, err := ServiceExtraMounts(&Service{Tmpfs: Tmpfs{"/cache"}}, &Stack{}, "", "p", "")
	if err != nil || len(mounts) != 1 || mounts[0].Type != "tmpfs" {
		t.Errorf("ServiceExtraMounts: %v %v", mounts, err)
	}
	if _, err := ServiceExtraMounts(&Service{Tmpfs: Tmpfs{"cache"}}, &Stack{}, "", "p", ""); err == nil {
		t.Error("ServiceExtraMounts: expected error for relative tmpfs")
	}
}

func TestBundleResourcesFromRun(t *testing.T) {
	adj := -500
	r, err := BundleResourcesFromRun(RunLimits{Memory: "256m", MemorySwap: "512m", CPUs: "0.5", PidsLimit: 100, OomScoreAdj: &adj})
	if err != nil {
		t.Fatal(err)
	}
	if r.Memory.Limit != 256<<20 || r.Memory.Swap != 512<<20 || *r.Memory.OomScoreAdj != -500 ||
		r.CPU.Quota != 50000 || r.CPU.Period != 100000 || r.Pids.Limit != 100 {
		t.Errorf("resources = %+v %+v %+v", r.Memory, r.CPU, r.Pids)
	}
	if r, err := BundleResourcesFromRun(RunLimits{}); r != nil || err != nil {
		t.Errorf("empty: %v %v", r, err)
	}
	big := 1001
	for want, l := range map[string]RunLimits{
		"memory: ":        {Memory: "lots"},
		"memory_swap: ":   {MemorySwap: "-1"},
		"cpus: ":          {CPUs: "0"},
		"pids_limit: ":    {PidsLimit: -1},
		"oom_score_adj: ": {OomScoreAdj: &big},
	} {
		if _, err := BundleResourcesFromRun(l); err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("%+v: err = %v, want prefix %q", l, err, want)
		}
	}
}
