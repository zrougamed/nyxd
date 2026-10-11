package main

import (
	"encoding/base64"
	"testing"
)

func TestRegistryHost(t *testing.T) {
	for ref, want := range map[string]string{
		"alpine":                          "docker.io",
		"library/alpine:3.20":             "docker.io",
		"ghcr.io/team/app:1":              "ghcr.io",
		"localhost:5000/app":              "localhost:5000",
		"localhost/app":                   "localhost",
		"registry.example.com:8443/x/y:z": "registry.example.com:8443",
	} {
		if got := registryHost(ref); got != want {
			t.Errorf("%s = %s, want %s", ref, got, want)
		}
	}
}

func TestDockerConfigAuth(t *testing.T) {
	enc := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	cfg := []byte(`{"auths":{
		"https://index.docker.io/v1/":{"auth":"` + enc("hubuser:hubpass") + `"},
		"ghcr.io":{"auth":"` + enc("gh:tok:with:colons") + `"},
		"bad.example":{"auth":"%%%"}
	}}`)
	cases := map[string][2]string{
		"docker.io":     {"hubuser", "hubpass"},
		"ghcr.io":       {"gh", "tok:with:colons"},
		"bad.example":   {"", ""},
		"other.example": {"", ""},
	}
	for host, want := range cases {
		u, p := dockerConfigAuth(cfg, host)
		if u != want[0] || p != want[1] {
			t.Errorf("%s = %q/%q, want %q/%q", host, u, p, want[0], want[1])
		}
	}
}
