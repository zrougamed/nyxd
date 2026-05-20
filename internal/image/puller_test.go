package image_test

import (
	"testing"

	"github.com/zrougamed/nyxd/internal/image"
)

func TestParseRef(t *testing.T) {
	cases := []struct {
		input    string
		wantReg  string
		wantRepo string
		wantTag  string
		wantDig  string
		wantErr  bool
	}{
		{
			input:   "ubuntu:22.04",
			wantReg: "registry-1.docker.io", wantRepo: "library/ubuntu", wantTag: "22.04",
		},
		{
			input:   "nginx",
			wantReg: "registry-1.docker.io", wantRepo: "library/nginx", wantTag: "latest",
		},
		{
			input:   "myuser/myapp:v1.2",
			wantReg: "registry-1.docker.io", wantRepo: "myuser/myapp", wantTag: "v1.2",
		},
		{
			input:   "registry.example.com:5000/ns/repo:tag",
			wantReg: "registry.example.com:5000", wantRepo: "ns/repo", wantTag: "tag",
		},
		{
			input:   "ubuntu@sha256:abc123",
			wantReg: "registry-1.docker.io", wantRepo: "library/ubuntu", wantDig: "sha256:abc123",
		},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			ref, err := image.ParseRef(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ref.Registry != tc.wantReg {
				t.Errorf("registry: want %q got %q", tc.wantReg, ref.Registry)
			}
			if ref.Repo != tc.wantRepo {
				t.Errorf("repo: want %q got %q", tc.wantRepo, ref.Repo)
			}
			if tc.wantTag != "" && ref.Tag != tc.wantTag {
				t.Errorf("tag: want %q got %q", tc.wantTag, ref.Tag)
			}
			if tc.wantDig != "" && ref.Digest != tc.wantDig {
				t.Errorf("digest: want %q got %q", tc.wantDig, ref.Digest)
			}
		})
	}
}

func TestPruneImagesNotInEmptyStore(t *testing.T) {
	d := t.TempDir()
	s, err := image.NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.PruneImagesNotIn(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty removed, got %v", got)
	}
	gotDry, err := s.PruneImagesNotIn(nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotDry) != 0 {
		t.Fatalf("want empty dry run, got %v", gotDry)
	}
}
