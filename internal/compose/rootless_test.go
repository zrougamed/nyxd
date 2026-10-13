package compose

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "common.env"), []byte("# comment\nA=from-file\nB=\"quoted\"\nC=keep\n"), 0o600) //nolint:errcheck
	os.WriteFile(filepath.Join(dir, "local.env"), []byte("C=override\n"), 0o600)                                    //nolint:errcheck
	st, err := Parse([]byte(`
name: MyStack
services:
  one:
    image: x
    env_file: common.env
    environment:
      A: from-environment
  many:
    image: x
    env_file: [common.env, local.env]
  long:
    image: x
    env_file:
      - path: missing.env
        required: false
      - path: local.env
  broken:
    image: x
    env_file: [nope.env]
`))
	if err != nil {
		t.Fatal(err)
	}
	if st.Name != "MyStack" || ProjectName(st, "/a/dir/compose.yaml") != "MyStack" || ProjectName(&Stack{}, "/a/dir/compose.yaml") != "dir" {
		t.Errorf("project name: %q", st.Name)
	}
	check := func(svc string, want map[string]string) {
		t.Helper()
		s := st.Services[svc]
		env, err := ServiceEnv(&s, dir)
		if err != nil {
			t.Fatalf("%s: %v", svc, err)
		}
		for k, v := range want {
			if env[k] != v {
				t.Errorf("%s: %s=%q, want %q (all: %v)", svc, k, env[k], v, env)
			}
		}
	}
	check("one", map[string]string{"A": "from-environment", "B": "quoted", "C": "keep"})
	check("many", map[string]string{"A": "from-file", "C": "override"})
	check("long", map[string]string{"C": "override"})
	b := st.Services["broken"]
	if _, err := ServiceEnv(&b, dir); err == nil || !strings.Contains(err.Error(), "nope.env") {
		t.Errorf("missing required env_file: %v", err)
	}
	if _, err := Parse([]byte("services:\n  x:\n    image: x\n    env_file: {a: b}\n")); err == nil {
		t.Error("mapping env_file accepted")
	}
}

func TestHostNetworkWarnings(t *testing.T) {
	st, err := Parse([]byte(`
services:
  db:
    image: x
    networks: [backend]
  web:
    image: x
    networks: [frontend]
    ports: ["8443:1443", "8080:80"]
  web2:
    image: x
    ports: ["9443:1443"]
networks:
  frontend: {}
  backend:
    internal: true
`))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(HostNetworkWarnings(st), "\n")
	for _, want := range []string{"internal networks are not isolated", "db can be reached", "web and web2 both use port 1443/tcp"} {
		if !strings.Contains(got, want) {
			t.Errorf("warnings lack %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "web can be reached") {
		t.Errorf("non-internal service flagged:\n%s", got)
	}
	if w := HostNetworkWarnings(&Stack{Services: map[string]Service{"a": {Image: "x"}}}); len(w) != 0 {
		t.Errorf("plain stack: %v", w)
	}
}
