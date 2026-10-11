package dockerfile

import (
	"fmt"
	"strings"
	"unicode"
)

// Lookup returns the value of a build variable (ENV or ARG).
type Lookup func(name string) (string, bool)

// Words splits s into words the way Docker processes instruction arguments:
// whitespace separates words, single quotes are literal, double quotes allow
// variable expansion, the escape rune escapes the next character, and
// $VAR / ${VAR} / ${VAR:-default} / ${VAR:+alt} / ${VAR-default} / ${VAR+alt}
// are expanded. Quotes are removed.
func Words(s string, escape rune, lookup Lookup) ([]string, error) {
	p := &lexer{src: []rune(s), escape: escape, lookup: lookup}
	return p.words(true)
}

// Expand expands variables in s as a single word (whitespace is kept).
func Expand(s string, escape rune, lookup Lookup) (string, error) {
	p := &lexer{src: []rune(s), escape: escape, lookup: lookup}
	w, err := p.words(false)
	if err != nil {
		return "", err
	}
	return strings.Join(w, ""), nil
}

type lexer struct {
	src    []rune
	pos    int
	escape rune
	lookup Lookup
}

func (l *lexer) peek() (rune, bool) {
	if l.pos >= len(l.src) {
		return 0, false
	}
	return l.src[l.pos], true
}

func (l *lexer) words(split bool) ([]string, error) {
	var out []string
	var cur strings.Builder
	inWord := false
	flush := func() {
		if inWord {
			out = append(out, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	for {
		r, ok := l.peek()
		if !ok {
			break
		}
		switch {
		case split && unicode.IsSpace(r):
			l.pos++
			flush()
		case r == l.escape:
			l.pos++
			inWord = true
			if n, ok := l.peek(); ok {
				cur.WriteRune(n)
				l.pos++
			} else {
				cur.WriteRune(r)
			}
		case r == '\'':
			l.pos++
			inWord = true
			end := l.indexFrom('\'')
			if end < 0 {
				return nil, fmt.Errorf("unterminated single quote in %q", string(l.src))
			}
			cur.WriteString(string(l.src[l.pos:end]))
			l.pos = end + 1
		case r == '"':
			l.pos++
			inWord = true
			if err := l.doubleQuoted(&cur); err != nil {
				return nil, err
			}
		case r == '$':
			inWord = true
			v, err := l.variable()
			if err != nil {
				return nil, err
			}
			cur.WriteString(v)
		default:
			l.pos++
			inWord = true
			cur.WriteRune(r)
		}
	}
	flush()
	return out, nil
}

func (l *lexer) indexFrom(c rune) int {
	for i := l.pos; i < len(l.src); i++ {
		if l.src[i] == c {
			return i
		}
	}
	return -1
}

func (l *lexer) doubleQuoted(cur *strings.Builder) error {
	for {
		r, ok := l.peek()
		if !ok {
			return fmt.Errorf("unterminated double quote in %q", string(l.src))
		}
		switch r {
		case '"':
			l.pos++
			return nil
		case '$':
			v, err := l.variable()
			if err != nil {
				return err
			}
			cur.WriteString(v)
		case l.escape:
			l.pos++
			n, ok := l.peek()
			if !ok {
				cur.WriteRune(r)
				continue
			}
			// Inside double quotes only these are escapable; otherwise keep the escape.
			if n == '"' || n == '$' || n == l.escape {
				cur.WriteRune(n)
			} else {
				cur.WriteRune(r)
				cur.WriteRune(n)
			}
			l.pos++
		default:
			cur.WriteRune(r)
			l.pos++
		}
	}
}

func isNameRune(r rune, first bool) bool {
	if r == '_' || unicode.IsLetter(r) {
		return true
	}
	return !first && unicode.IsDigit(r)
}

// variable parses at '$' and returns the expansion.
func (l *lexer) variable() (string, error) {
	l.pos++ // '$'
	r, ok := l.peek()
	if !ok {
		return "$", nil
	}
	if r != '{' {
		start := l.pos
		for {
			c, ok := l.peek()
			if !ok || !isNameRune(c, l.pos == start) {
				break
			}
			l.pos++
		}
		if l.pos == start {
			return "$", nil // a lone '$' is literal
		}
		v, _ := l.get(string(l.src[start:l.pos]))
		return v, nil
	}

	l.pos++ // '{'
	start := l.pos
	for {
		c, ok := l.peek()
		if !ok || !isNameRune(c, l.pos == start) {
			break
		}
		l.pos++
	}
	name := string(l.src[start:l.pos])
	if name == "" {
		return "", fmt.Errorf("bad substitution in %q", string(l.src))
	}
	c, ok := l.peek()
	if !ok {
		return "", fmt.Errorf("missing '}' in %q", string(l.src))
	}
	if c == '}' {
		l.pos++
		v, _ := l.get(name)
		return v, nil
	}

	// ${name:-word} ${name-word} ${name:+word} ${name+word}
	colon := false
	if c == ':' {
		colon = true
		l.pos++
		c, ok = l.peek()
		if !ok {
			return "", fmt.Errorf("missing '}' in %q", string(l.src))
		}
	}
	if c != '-' && c != '+' {
		return "", fmt.Errorf("unsupported modifier %q in %q", string(c), string(l.src))
	}
	l.pos++
	word, err := l.braceWord()
	if err != nil {
		return "", err
	}
	v, set := l.get(name)
	if colon && v == "" {
		set = false
	}
	if c == '-' {
		if set {
			return v, nil
		}
		return word, nil
	}
	if set {
		return word, nil
	}
	return "", nil
}

// braceWord reads the word of ${name:-word} up to the matching '}', expanding
// nested variables.
func (l *lexer) braceWord() (string, error) {
	var b strings.Builder
	for {
		r, ok := l.peek()
		if !ok {
			return "", fmt.Errorf("missing '}' in %q", string(l.src))
		}
		switch r {
		case '}':
			l.pos++
			return b.String(), nil
		case '$':
			v, err := l.variable()
			if err != nil {
				return "", err
			}
			b.WriteString(v)
		case l.escape:
			l.pos++
			if n, ok := l.peek(); ok {
				b.WriteRune(n)
				l.pos++
			}
		default:
			b.WriteRune(r)
			l.pos++
		}
	}
}

func (l *lexer) get(name string) (string, bool) {
	if l.lookup == nil {
		return "", false
	}
	return l.lookup(name)
}

// KeyValues parses ENV/LABEL/ARG-style arguments. It accepts "k=v k2=v2"
// (each word split on its first '=') and, when legacy is true and the first
// word has no '=', the old "key value with spaces" form.
func KeyValues(raw string, escape rune, lookup Lookup, legacy bool) ([][2]string, error) {
	words, err := Words(raw, escape, lookup)
	if err != nil {
		return nil, err
	}
	if len(words) == 0 {
		return nil, fmt.Errorf("missing arguments")
	}
	if legacy && !strings.Contains(words[0], "=") {
		// Legacy form: ENV key value... (value is the rest, expanded as one word).
		key, rest, _ := strings.Cut(strings.TrimSpace(raw), " ")
		if strings.TrimSpace(rest) == "" {
			return nil, fmt.Errorf("%s: missing value", key)
		}
		v, err := Expand(strings.TrimSpace(rest), escape, lookup)
		if err != nil {
			return nil, err
		}
		return [][2]string{{key, v}}, nil
	}
	out := make([][2]string, 0, len(words))
	for _, w := range words {
		k, v, ok := strings.Cut(w, "=")
		if !ok {
			return nil, fmt.Errorf("%q: expected key=value", w)
		}
		if k == "" {
			return nil, fmt.Errorf("%q: empty key", w)
		}
		out = append(out, [2]string{k, v})
	}
	return out, nil
}
