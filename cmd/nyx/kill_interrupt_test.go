package main

import (
	"strings"
	"testing"
)

// Ctrl+C on a foreground run whose container already exited is quiet.
func TestKillRunContainerAlreadyGone(t *testing.T) {
	sock := stub(t, reply(404, "container c1 not found"))
	_, errOut, err := capture(t, "", func() error { return killRunContainer(sock, "c1") })
	if err != nil || strings.Contains(errOut, "kill") {
		t.Errorf("container already gone: %v %q", err, errOut)
	}
	if err := doKillWithContext(t.Context(), sock, "c1"); !httpStatusNotFound(err) || !strings.Contains(err.Error(), "not found") {
		t.Errorf("404 not recognised: %v", err)
	}
}
