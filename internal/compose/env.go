package compose

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// InterpEnvFromDir loads .env from composeDir (if present), then overlays the
// process environment so shell exports win over the file (compose-spec behavior).
func InterpEnvFromDir(composeDir string) map[string]string {
	m := make(map[string]string)
	_ = mergeDotEnvFile(m, filepath.Join(composeDir, ".env"))
	for _, pair := range os.Environ() {
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		m[k] = v
	}
	return m
}

func mergeDotEnvFile(dst map[string]string, path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		v = strings.TrimSpace(v)
		v = strings.TrimPrefix(v, `"`)
		v = strings.TrimSuffix(v, `"`)
		v = strings.TrimPrefix(v, `'`)
		v = strings.TrimSuffix(v, `'`)
		dst[k] = v
	}
	return sc.Err()
}

// Substitute expands $VAR, ${VAR}, ${VAR:-default}, and $$ in s using env.
func Substitute(s string, env map[string]string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		c := s[i]
		if c == '$' && i+1 < len(s) {
			if s[i+1] == '$' {
				b.WriteByte('$')
				i += 2
				continue
			}
			if s[i+1] == '{' {
				end := strings.IndexByte(s[i+2:], '}')
				if end >= 0 {
					inner := strings.TrimSpace(s[i+2 : i+2+end])
					key, def, hasDef := inner, "", false
					if k, d, ok := strings.Cut(inner, ":-"); ok {
						key, def, hasDef = strings.TrimSpace(k), d, true
					}
					val := env[key]
					if val == "" && hasDef {
						val = def
					}
					b.WriteString(val)
					i += 2 + end + 1
					continue
				}
			}
			j := i + 1
			for j < len(s) && isSubstIdentByte(s[j]) {
				j++
			}
			if j > i+1 {
				key := s[i+1 : j]
				b.WriteString(env[key])
				i = j
				continue
			}
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}

func isSubstIdentByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case c == '_':
		return true
	default:
		return false
	}
}
