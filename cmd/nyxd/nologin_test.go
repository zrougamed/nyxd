package main

import "testing"

func TestIsNologin(t *testing.T) {
	for argv0, want := range map[string]bool{
		"/usr/sbin/nologin":   true,
		"-nologin":            true,
		"nologin":             true,
		"/usr/local/bin/nyxd": false,
		"-nyxd":               false,
		"nyxd":                false,
	} {
		if got := isNologin(argv0); got != want {
			t.Errorf("isNologin(%q) = %v, want %v", argv0, got, want)
		}
	}
}
