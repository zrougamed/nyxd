package supervisor

import "testing"

func TestDisplayIDDifferentSlugs(t *testing.T) {
	a := DisplayID("nyx-compose-alpha")
	b := DisplayID("nyx-compose-beta")
	if a == "" || b == "" || a == b {
		t.Fatalf("DisplayID collision or empty: %q %q", a, b)
	}
	if len(a) != 12 || len(b) != 12 {
		t.Fatalf("length: %q %q", a, b)
	}
}
