package compose

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseVolumeMountNamed(t *testing.T) {
	stack := &Stack{
		Volumes: map[string]Volume{"data": {}},
		Services: map[string]Service{
			"web": {Volumes: []string{"data:/data:ro"}},
		},
	}
	dir := t.TempDir()
	dataDir := t.TempDir()
	m, err := parseVolumeMount("data:/data:ro", stack, dir, "proj", dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Destination != "/data" {
		t.Fatalf("dest %q", m.Destination)
	}
	if m.Source == "" {
		t.Fatal("empty source")
	}
	if !containsOpt(m.Options, "ro") {
		t.Fatalf("options %#v", m.Options)
	}
}

func TestParseVolumeMountBindRelative(t *testing.T) {
	stack := &Stack{Services: map[string]Service{}}
	root := t.TempDir()
	sub := filepath.Join(root, "hostdata")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	m, err := parseVolumeMount("hostdata:/mnt", stack, root, "p", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.Abs(filepath.Join(root, "hostdata"))
	if m.Source != want {
		t.Fatalf("source want %q got %q", want, m.Source)
	}
}

func TestParseVolumeMountBindEscape(t *testing.T) {
	stack := &Stack{Services: map[string]Service{}}
	root := t.TempDir()
	_, err := parseVolumeMount("../outside:/mnt", stack, root, "p", t.TempDir())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseVolumeMountWindowsDrive(t *testing.T) {
	stack := &Stack{Services: map[string]Service{}}
	for line, want := range map[string]struct {
		src string
		ro  bool
	}{
		`C:\Users\me\data:/data`: {"/mnt/c/Users/me/data", false},
		`d:/cache:/cache:ro`:     {"/mnt/d/cache", true},
		`E:\src:/src:rw`:         {"/mnt/e/src", false},
	} {
		m, err := parseVolumeMount(line, stack, t.TempDir(), "p", t.TempDir())
		if err != nil {
			t.Fatalf("%s: %v", line, err)
		}
		if m.Source != want.src || containsOpt(m.Options, "ro") != want.ro {
			t.Errorf("%s: source %q options %v; want %q ro=%v", line, m.Source, m.Options, want.src, want.ro)
		}
	}
	for _, bad := range []string{`C:\data`, `C:\data:relative`, `C:\a:/b:/c:ro`} {
		if _, err := parseVolumeMount(bad, stack, t.TempDir(), "p", t.TempDir()); err == nil {
			t.Errorf("%s: expected error", bad)
		}
	}
}

func containsOpt(opts []string, want string) bool {
	for _, o := range opts {
		if o == want {
			return true
		}
	}
	return false
}
