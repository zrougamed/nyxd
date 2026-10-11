// Package dockerfile parses Dockerfiles into stages and instructions.
//
// It implements the Dockerfile reference grammar that builds rely on: parser
// directives (escape, syntax), line continuations, comments, JSON (exec) and
// shell forms, leading --flags, multi-stage FROM ... AS, and ARG before the
// first FROM. Heredocs are detected and rejected with a clear error.
package dockerfile

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"
)

// Instruction is one Dockerfile instruction after line joining.
type Instruction struct {
	Cmd   string   // lower-case keyword: "run", "copy", ...
	Flags []string // leading flags, e.g. "--from=build" (unexpanded)
	// Args holds the JSON array for exec form; for shell form it is empty and
	// the text is in Raw.
	Args []string
	Raw  string // instruction text after keyword and flags
	JSON bool   // exec (JSON array) form
	Line int    // 1-based line of the keyword
	// Original is the joined source line, used for build history and cache keys.
	Original string
}

// Flag returns the value of --name=value (or "true" for a bare --name) and
// whether it was present.
func (in Instruction) Flag(name string) (string, bool) {
	for _, f := range in.Flags {
		k, v, hasV := strings.Cut(strings.TrimPrefix(f, "--"), "=")
		if k == name {
			if !hasV {
				return "true", true
			}
			return v, true
		}
	}
	return "", false
}

// Stage is a FROM line and the instructions that follow it.
type Stage struct {
	Index        int
	Name         string // lower-cased "AS" name, may be empty
	From         Instruction
	Instructions []Instruction
}

// Dockerfile is a parsed Dockerfile.
type Dockerfile struct {
	Escape   rune
	MetaArgs []Instruction // ARG instructions before the first FROM
	Stages   []Stage
}

var directiveRe = regexp.MustCompile(`^#\s*([a-zA-Z][a-zA-Z0-9]*)\s*=\s*(.+?)\s*$`)

// heredocRe matches "<<EOF", "<<-EOF", "<<'EOF'", "<<\"EOF\"".
var heredocRe = regexp.MustCompile(`<<-?["']?[A-Za-z_][A-Za-z0-9_]*["']?`)

var jsonForms = map[string]bool{
	"run": true, "cmd": true, "entrypoint": true, "shell": true,
	"copy": true, "add": true, "volume": true,
}

var known = map[string]bool{
	"from": true, "run": true, "cmd": true, "entrypoint": true, "shell": true,
	"copy": true, "add": true, "volume": true, "env": true, "arg": true,
	"label": true, "expose": true, "workdir": true, "user": true,
	"stopsignal": true, "healthcheck": true, "onbuild": true, "maintainer": true,
}

// Parse reads a Dockerfile.
func Parse(r io.Reader) (*Dockerfile, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var lines []string
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	df := &Dockerfile{Escape: '\\'}
	start := 0
	// Parser directives: only at the very top, as consecutive "# key=value" lines.
	seen := map[string]bool{}
	for start < len(lines) {
		m := directiveRe.FindStringSubmatch(lines[start])
		if m == nil {
			break
		}
		key := strings.ToLower(m[1])
		if seen[key] {
			return nil, fmt.Errorf("line %d: directive %q set twice", start+1, key)
		}
		seen[key] = true
		if key == "escape" {
			switch m[2] {
			case "\\":
				df.Escape = '\\'
			case "`":
				df.Escape = '`'
			default:
				return nil, fmt.Errorf("line %d: invalid escape %q (use \\ or `)", start+1, m[2])
			}
		}
		// "syntax" and unknown directives are ignored.
		start++
	}

	insts, err := joinLines(lines, start, df.Escape)
	if err != nil {
		return nil, err
	}

	for _, li := range insts {
		in, err := parseInstruction(li.text, li.line)
		if err != nil {
			return nil, err
		}
		switch {
		case in.Cmd == "from":
			st := Stage{Index: len(df.Stages), From: in}
			words := strings.Fields(in.Raw)
			switch {
			case len(words) == 1:
			case len(words) == 3 && strings.EqualFold(words[1], "as"):
				st.Name = strings.ToLower(words[2])
			default:
				return nil, fmt.Errorf("line %d: FROM expects \"image [AS name]\"", in.Line)
			}
			df.Stages = append(df.Stages, st)
		case len(df.Stages) == 0:
			if in.Cmd != "arg" {
				return nil, fmt.Errorf("line %d: %s before the first FROM (only ARG is allowed)", in.Line, strings.ToUpper(in.Cmd))
			}
			df.MetaArgs = append(df.MetaArgs, in)
		default:
			cur := &df.Stages[len(df.Stages)-1]
			cur.Instructions = append(cur.Instructions, in)
		}
	}
	if len(df.Stages) == 0 {
		return nil, fmt.Errorf("no FROM instruction")
	}
	return df, nil
}

type logicalLine struct {
	text string
	line int
}

// joinLines strips comments and joins continuation lines.
func joinLines(lines []string, start int, escape rune) ([]logicalLine, error) {
	var out []logicalLine
	var cur strings.Builder
	curLine := 0
	cont := false
	for i := start; i < len(lines); i++ {
		raw := lines[i]
		trimmed := strings.TrimLeftFunc(raw, unicode.IsSpace)
		if strings.HasPrefix(trimmed, "#") {
			continue // comments are dropped, also inside continuations
		}
		if strings.TrimSpace(raw) == "" {
			if cont {
				continue // empty continuation lines are skipped
			}
			continue
		}
		if !cont {
			curLine = i + 1
			cur.Reset()
			raw = trimmed
		}
		body := strings.TrimRightFunc(raw, unicode.IsSpace)
		if strings.HasSuffix(body, string(escape)) && !strings.HasSuffix(body, string(escape)+string(escape)) {
			cur.WriteString(strings.TrimSuffix(body, string(escape)))
			cont = true
			continue
		}
		cur.WriteString(raw)
		out = append(out, logicalLine{text: cur.String(), line: curLine})
		cont = false
	}
	if cont {
		out = append(out, logicalLine{text: cur.String(), line: curLine})
	}
	return out, nil
}

func parseInstruction(text string, line int) (Instruction, error) {
	kw, rest, _ := strings.Cut(strings.TrimSpace(text), " ")
	if i := strings.IndexAny(kw, "\t"); i >= 0 {
		rest = kw[i+1:] + " " + rest
		kw = kw[:i]
	}
	cmd := strings.ToLower(kw)
	if !known[cmd] {
		return Instruction{}, fmt.Errorf("line %d: unknown instruction %q", line, kw)
	}
	in := Instruction{Cmd: cmd, Line: line, Original: strings.TrimSpace(text)}
	rest = strings.TrimSpace(rest)

	// Leading --flags (FROM, RUN, COPY, ADD, HEALTHCHECK ...).
	for strings.HasPrefix(rest, "--") {
		f, r, _ := strings.Cut(rest, " ")
		in.Flags = append(in.Flags, f)
		rest = strings.TrimSpace(r)
	}
	in.Raw = rest

	if (cmd == "run" || cmd == "copy" || cmd == "add") && heredocRe.MatchString(rest) && !strings.HasPrefix(rest, "[") {
		return Instruction{}, fmt.Errorf("line %d: heredocs (<<EOF) are not supported yet", line)
	}

	if jsonForms[cmd] && strings.HasPrefix(rest, "[") {
		var arr []string
		if err := json.Unmarshal([]byte(rest), &arr); err == nil {
			in.JSON = true
			in.Args = arr
		}
		// Not a valid JSON string array: Docker treats it as shell form.
	}
	if cmd == "maintainer" {
		in.Cmd = "label"
		in.Raw = "maintainer=" + quote(rest)
	}
	return in, nil
}

// quote wraps s in double quotes so Words returns it as one literal word.
func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`).Replace(s) + `"`
}
