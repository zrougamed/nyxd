package image

import "testing"

func TestParseRefDockerIOHostUsesRegistryAPI(t *testing.T) {
	pr, err := ParseRef("docker.io/library/busybox:1.36")
	if err != nil {
		t.Fatal(err)
	}
	if pr.Registry != defaultRegistry {
		t.Fatalf("registry: got %q want %q", pr.Registry, defaultRegistry)
	}
	if pr.Repo != "library/busybox" || pr.Tag != "1.36" {
		t.Fatalf("repo/tag: %+v", pr)
	}
}

func TestParseRefBareBusyboxUnchanged(t *testing.T) {
	pr, err := ParseRef("busybox:1.36")
	if err != nil {
		t.Fatal(err)
	}
	if pr.Registry != defaultRegistry {
		t.Fatalf("registry: got %q", pr.Registry)
	}
	if pr.Repo != "library/busybox" || pr.Tag != "1.36" {
		t.Fatalf("repo/tag: %+v", pr)
	}
}
