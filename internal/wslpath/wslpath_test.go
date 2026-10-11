package wslpath

import "testing"

func TestToWSL(t *testing.T) {
	ok := map[string]string{
		`C:\Users\me\app\compose.yaml`: "/mnt/c/Users/me/app/compose.yaml",
		`d:\data`:                      "/mnt/d/data",
		`E:/mixed/slashes`:             "/mnt/e/mixed/slashes",
		`C:\`:                          "/mnt/c",
		`C:`:                           "/mnt/c",
		`/already/linux`:               "/already/linux",
	}
	for in, want := range ok {
		got, err := ToWSL(in)
		if err != nil || got != want {
			t.Errorf("%s = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{`\\server\share\x`, `//server/share`, `relative\path`, `1:\x`, `C:relative`, ``} {
		if _, err := ToWSL(bad); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}

func TestHasDrive(t *testing.T) {
	for s, want := range map[string]bool{
		`C:\data:/data`: true,
		`c:/data`:       true,
		`C:`:            false,
		`C:data`:        false,
		`data:/data`:    false,
		`/abs:/data`:    false,
		`1:\x`:          false,
	} {
		if got := HasDrive(s); got != want {
			t.Errorf("HasDrive(%q) = %v, want %v", s, got, want)
		}
	}
}
