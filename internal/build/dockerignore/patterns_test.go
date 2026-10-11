package dockerignore

import (
	"strings"
	"testing"
)

func TestPatternForms(t *testing.T) {
	m, err := Parse(strings.NewReader(`
# comment
**/*.log
!keep/**/*.log
a?c
[a-c]x
[!d]y
[unclosed
/abs/path
docs/**
`))
	if err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string]bool{
		"x.log": true, "deep/dir/x.log": true, "keep/a/x.log": false, "keep/x.log": false,
		"abc": true, "abbc": false, "bx": true, "dx": false, "ey": true, "dy": false,
		"[unclosed": true, "abs/path": true, "abs/path/inside": true, "docs/a/b": true,
		"src/main.go": false,
	} {
		if got := m.Excluded(rel); got != want {
			t.Errorf("%q: %v, want %v", rel, got, want)
		}
	}
	if m.CanSkipDir() {
		t.Error("negations present: directories cannot be skipped")
	}
	var nilM *Matcher
	if nilM.Excluded("x") || !nilM.CanSkipDir() {
		t.Error("nil matcher")
	}
	if _, err := Parse(errReader{}); err == nil {
		t.Error("read error")
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errTest }

var errTest = &testErr{}

type testErr struct{}

func (*testErr) Error() string { return "read failed" }
