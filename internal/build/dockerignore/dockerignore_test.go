package dockerignore

import (
	"strings"
	"testing"
)

func TestMatcher(t *testing.T) {
	m, err := Parse(strings.NewReader(`
# comment
node_modules
/build
*.log
**/*.tmp
docs/**
!docs/keep.md
src/*/gen
.git
`))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{
		"node_modules":          true,
		"node_modules/pkg/x.js": true,
		"web/node_modules":      false, // no leading **/: only at the root
		"build/out.bin":         true,
		"app.log":               true,
		"logs/app.log":          false, // * does not cross "/"
		"a/b/c.tmp":             true,
		"x.tmp":                 true,
		"docs/guide.md":         true,
		"docs/keep.md":          false,
		"src/api/gen/types.go":  true,
		"src/api/handler.go":    false,
		".git/HEAD":             true,
		"Dockerfile":            false,
		"main.go":               false,
	}
	for p, want := range cases {
		if got := m.Excluded(p); got != want {
			t.Errorf("Excluded(%q) = %v, want %v", p, got, want)
		}
	}
	if m.CanSkipDir() {
		t.Error("negation present: directories must not be skipped")
	}
	var nilM *Matcher
	if nilM.Excluded("x") || !nilM.CanSkipDir() {
		t.Error("nil matcher must include everything")
	}
}
