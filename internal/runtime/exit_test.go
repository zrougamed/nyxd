package runtime

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func TestExitSummary(t *testing.T) {
	caddy := `{"level":"info","msg":"using config from file","file":"/etc/caddy/Caddyfile"}
{"level":"info","logger":"http","msg":"servers shutting down with eternal grace period"}
{"level":"error","ts":1.5,"msg":"loading initial config: loading new config: http app module: start: listening on :443: listen tcp :443: bind: permission denied"}
`
	cases := map[string]string{
		caddy: "loading initial config: loading new config: http app module: start: listening on :443: listen tcp :443: bind: permission denied",
		"starting\nError: cannot open /data: permission denied\nbye\n":  "Error: cannot open /data: permission denied",
		"just output\nlast line\n\n":                                    "last line",
		`{"level":"info","msg":"bad","error":"boom"}`:                   "bad: boom",
		`{"severity":"FATAL","message":"no db"}`:                        "no db",
		`{"level":"warn","error":"x failed: y"}`:                        "x failed: y",
		`crun: executable file "nope" not found in $PATH: No such file`: `crun: executable file "nope" not found in $PATH: No such file`,
		"": "",
	}
	for in, want := range cases {
		if got := ExitSummary(in); got != want {
			t.Errorf("ExitSummary(%q)\n got %q\nwant %q", in, got, want)
		}
	}
	if got := ExitSummary(strings.Repeat("é", 1000)); len([]rune(got)) != maxSummary+1 || !strings.HasSuffix(got, "…") {
		t.Errorf("not capped: %d runes", len([]rune(got)))
	}
}

func TestStartExitError(t *testing.T) {
	exitErr := exec.Command("false").Run()
	e := &StartExitError{Err: exitErr, Output: "boot\nfatal: port in use\n"}
	if e.Error() != "exit status 1: fatal: port in use" {
		t.Errorf("Error() = %q", e.Error())
	}
	var xe *exec.ExitError
	if !errors.As(e, &xe) || ExitCode(e) != 1 {
		t.Error("exit status not reachable through the error")
	}
	if (&StartExitError{Err: exitErr}).Error() != "exit status 1" {
		t.Error("empty output")
	}
}
