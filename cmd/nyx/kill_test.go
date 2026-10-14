package main

import (
	"strings"
	"testing"
)

func TestKillRunContainer(t *testing.T) {
	var errOut string
	var err error
	_, errOut, err = capture(t, "", func() error { return killRunContainer(stub(t, reply(200, `{"ok":true}`)), "c1") })
	if err != nil || !strings.Contains(errOut, "SIGKILL sent to c1") {
		t.Errorf("kill: %v %q", err, errOut)
	}
	_, errOut, err = capture(t, "", func() error { return killRunContainer(stub(t, reply(500, "crun failed")), "c1") })
	if err == nil || !strings.Contains(errOut, "nyx: kill: crun failed") {
		t.Errorf("kill failure: %v %q", err, errOut)
	}
}

func TestLoginLogoutDispatch(t *testing.T) {
	for _, args := range [][]string{{"login", "-h"}, {"logout", "-x"}} {
		if _, _, err := nyx(t, args...); err == nil || !strings.Contains(err.Error(), "usage") {
			t.Errorf("nyx %v: %v", args, err)
		}
	}
}
