// Package dockerignore implements .dockerignore matching for build contexts.
//
// Rules follow Docker: one pattern per line, "#" comments, a leading "!"
// re-includes, "*", "?" and "[...]" match within a path segment, "**"
// matches any number of segments, a leading "/" is ignored, and a pattern
// that matches a directory excludes everything below it. The last matching
// rule wins. It is pure Go so the nyx client can use it on every platform.
package dockerignore

import (
	"bufio"
	"io"
	"path"
	"regexp"
	"strings"
)

type rule struct {
	re     *regexp.Regexp
	negate bool
}

// Matcher decides whether context paths are excluded.
type Matcher struct {
	rules     []rule
	negations bool
}

// Parse reads .dockerignore rules.
func Parse(r io.Reader) (*Matcher, error) {
	m := &Matcher{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		neg := false
		if strings.HasPrefix(line, "!") {
			neg = true
			line = strings.TrimSpace(line[1:])
		}
		line = path.Clean(strings.TrimPrefix(strings.ReplaceAll(line, "\\", "/"), "/"))
		if line == "." {
			continue
		}
		re, err := regexp.Compile(toRegexp(line))
		if err != nil {
			return nil, err
		}
		m.rules = append(m.rules, rule{re: re, negate: neg})
		m.negations = m.negations || neg
	}
	return m, sc.Err()
}

func toRegexp(p string) string {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c == '*' && i+1 < len(p) && p[i+1] == '*':
			i++
			if i+1 < len(p) && p[i+1] == '/' {
				i++
				b.WriteString("(.*/)?") // "**/" : zero or more directories
			} else {
				b.WriteString(".*")
			}
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		case c == '[':
			j := strings.IndexByte(p[i:], ']')
			if j < 0 {
				b.WriteString(`\[`)
				continue
			}
			class := p[i+1 : i+j]
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			b.WriteString("[" + class + "]")
			i += j
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return b.String()
}

// Excluded reports whether the slash-separated context path rel is excluded.
// A rule matching any parent directory of rel applies to rel as well.
func (m *Matcher) Excluded(rel string) bool {
	if m == nil {
		return false
	}
	rel = path.Clean(strings.TrimPrefix(rel, "/"))
	excluded := false
	for _, r := range m.rules {
		if matchSelfOrParent(r.re, rel) {
			excluded = !r.negate
		}
	}
	return excluded
}

// CanSkipDir reports whether an excluded directory can be skipped entirely
// (no "!" rule could re-include something below it).
func (m *Matcher) CanSkipDir() bool { return m == nil || !m.negations }

func matchSelfOrParent(re *regexp.Regexp, rel string) bool {
	for p := rel; p != "." && p != "/" && p != ""; p = path.Dir(p) {
		if re.MatchString(p) {
			return true
		}
	}
	return false
}
