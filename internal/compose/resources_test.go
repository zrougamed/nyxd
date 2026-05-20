package compose

import "testing"

func TestParseByteSize(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"128m", 128 * 1024 * 1024},
		{"1g", 1024 * 1024 * 1024},
		{"512", 512},
		{"0.5g", 512 * 1024 * 1024},
	}
	for _, tc := range cases {
		got, err := parseByteSize(tc.in)
		if err != nil {
			t.Fatalf("%q: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("%q: got %d want %d", tc.in, got, tc.want)
		}
	}
}

func TestParseCPUQuota(t *testing.T) {
	q, p, err := parseCPUQuota("0.5")
	if err != nil {
		t.Fatal(err)
	}
	if p != 100000 || q != 50000 {
		t.Fatalf("got quota=%d period=%d", q, p)
	}
}

func TestBundleResourcesFromDeployPids(t *testing.T) {
	n := 42
	d := &Deploy{Resources: Resources{Limits: ResourceSpec{PidsLimit: &n}}}
	r, err := BundleResourcesFromDeploy(d)
	if err != nil {
		t.Fatal(err)
	}
	if r == nil || r.Pids == nil || r.Pids.Limit != 42 {
		t.Fatalf("pids: %+v", r)
	}
}
