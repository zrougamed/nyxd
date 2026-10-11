// Package wslpath maps Windows drive paths to the form WSL2 mounts them at
// (C:\Users\me -> /mnt/c/Users/me).
package wslpath

import (
	"fmt"
	"strings"
)

// HasDrive reports whether s starts with an absolute drive path such as
// C:\ or C:/ (a bare "C:" is not one).
func HasDrive(s string) bool {
	return len(s) >= 3 && isLetter(s[0]) && s[1] == ':' && (s[2] == '\\' || s[2] == '/')
}

// ToWSL maps an absolute Windows drive path to /mnt/<drive>/...; Linux
// absolute paths are returned unchanged.
func ToWSL(p string) (string, error) {
	if strings.HasPrefix(p, `\\`) || strings.HasPrefix(p, "//") {
		return "", fmt.Errorf("%s: network (UNC) paths are not reachable from WSL; copy the files to a local drive", p)
	}
	if strings.HasPrefix(p, "/") {
		return p, nil // already a Linux path
	}
	if len(p) < 2 || p[1] != ':' || !isLetter(p[0]) || (len(p) > 2 && !HasDrive(p)) {
		return "", fmt.Errorf("%s: expected an absolute Windows path like C:\\dir", p)
	}
	rest := strings.ReplaceAll(p[2:], `\`, "/")
	rest = strings.TrimPrefix(rest, "/")
	out := "/mnt/" + strings.ToLower(p[:1])
	if rest != "" {
		out += "/" + rest
	}
	return out, nil
}

func isLetter(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }
