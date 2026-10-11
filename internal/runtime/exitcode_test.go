//go:build linux

package runtime

import (
	"errors"
	"fmt"
	"os/exec"
	"testing"
)

func TestExitCode(t *testing.T) {
	run := func(script string) error {
		err := exec.Command("/bin/sh", "-c", script).Run()
		if err != nil {
			return fmt.Errorf("crun run: %w", err) // wrapped like RunForeground
		}
		return nil
	}
	cases := []struct {
		err  error
		want int
	}{
		{nil, 0},
		{run("exit 0"), 0},
		{run("exit 3"), 3},
		{run("kill -9 $$"), 137},
		{errors.New("context canceled"), -1},
	}
	for i, c := range cases {
		if got := ExitCode(c.err); got != c.want {
			t.Errorf("case %d (%v): ExitCode = %d, want %d", i, c.err, got, c.want)
		}
	}
}
