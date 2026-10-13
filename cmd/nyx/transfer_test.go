package main

import (
	"testing"

	"github.com/zrougamed/nyxd/internal/image"
)

func TestRegistryHost(t *testing.T) {
	for ref, want := range map[string]string{
		"alpine":                          "docker.io",
		"library/alpine:3.20":             "docker.io",
		"docker.io/library/alpine":        "docker.io",
		"ghcr.io/team/app:1":              "ghcr.io",
		"localhost:5000/app":              "localhost:5000",
		"localhost/app":                   "localhost",
		"registry.example.com:8443/x/y:z": "registry.example.com:8443",
	} {
		if got := image.RegistryHost(ref); got != want {
			t.Errorf("%s = %s, want %s", ref, got, want)
		}
	}
}
