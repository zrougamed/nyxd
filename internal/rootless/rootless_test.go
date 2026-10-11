//go:build linux

package rootless

import (
	"bufio"
	"strings"
	"testing"
)

func TestParseSubID(t *testing.T) {
	data := "# comment\nalice:100000:65536\nbob:165536:65536\n1001:231072:1000\nbad:line\n"
	cases := []struct {
		keys      map[string]bool
		start, n  int
		wantError bool
	}{
		{map[string]bool{"bob": true}, 165536, 65536, false},
		{map[string]bool{"1001": true, "carol": true}, 231072, 1000, false},
		{map[string]bool{"dave": true}, 0, 0, true},
	}
	for _, c := range cases {
		r, err := parseSubID(bufio.NewScanner(strings.NewReader(data)), c.keys, "subuid")
		if c.wantError != (err != nil) || r.start != c.start || r.count != c.n {
			t.Errorf("keys %v: got %+v, %v", c.keys, r, err)
		}
	}
}

func TestSetEnv(t *testing.T) {
	env := setEnv([]string{"A=1", "_NYXD_USERNS=map", "B=2"}, envStage, stageReady)
	if strings.Join(env, ",") != "A=1,B=2,_NYXD_USERNS=ready" {
		t.Fatalf("env = %v", env)
	}
}

func TestCleanEnv(t *testing.T) {
	env := CleanEnv([]string{"A=1", "_NYXD_USERNS=ready", "_NYXD_USERNS_MAPPING=subid", "_NYXD_HOST_UID=1000", "B=2"})
	if strings.Join(env, ",") != "A=1,B=2" {
		t.Fatalf("env = %v", env)
	}
}
