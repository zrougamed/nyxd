package dockerfile

import (
	"reflect"
	"strings"
	"testing"
)

func mustParse(t *testing.T, src string) *Dockerfile {
	t.Helper()
	df, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	return df
}

func TestParseStagesAndForms(t *testing.T) {
	df := mustParse(t, `# syntax=docker/dockerfile:1
ARG BASE=alpine:3.20
FROM ${BASE} AS build
# a comment
RUN apk add --no-cache \
      gcc \
    # comment inside a continuation
      musl-dev
COPY --chown=1000:1000 --from=x src/ /app/
CMD ["sh", "-c", "echo hi"]
ENTRYPOINT [not json
maintainer Jane <j@x>

from scratch
COPY --from=build /app /app
`)
	if len(df.MetaArgs) != 1 || df.MetaArgs[0].Raw != "BASE=alpine:3.20" {
		t.Fatalf("meta args = %+v", df.MetaArgs)
	}
	if len(df.Stages) != 2 || df.Stages[0].Name != "build" || df.Stages[1].Name != "" {
		t.Fatalf("stages = %+v", df.Stages)
	}
	ins := df.Stages[0].Instructions
	if ins[0].Cmd != "run" || ins[0].Raw != "apk add --no-cache       gcc       musl-dev" || ins[0].Line != 5 {
		t.Fatalf("run = %q (line %d)", ins[0].Raw, ins[0].Line)
	}
	if v, ok := ins[1].Flag("chown"); !ok || v != "1000:1000" {
		t.Fatalf("chown flag = %q %v", v, ok)
	}
	if v, _ := ins[1].Flag("from"); v != "x" || ins[1].Raw != "src/ /app/" {
		t.Fatalf("copy = %+v", ins[1])
	}
	if !ins[2].JSON || !reflect.DeepEqual(ins[2].Args, []string{"sh", "-c", "echo hi"}) {
		t.Fatalf("cmd = %+v", ins[2])
	}
	if ins[3].JSON || ins[3].Raw != "[not json" {
		t.Fatalf("invalid JSON should be shell form: %+v", ins[3])
	}
	if ins[4].Cmd != "label" || ins[4].Raw != `maintainer="Jane <j@x>"` {
		t.Fatalf("maintainer = %+v", ins[4])
	}
}

func TestParseEscapeDirective(t *testing.T) {
	df := mustParse(t, "# escape=`\nFROM x\nRUN echo a `\n  b\nCOPY c:\\src d:\\dst\n")
	if df.Escape != '`' {
		t.Fatal("escape directive ignored")
	}
	ins := df.Stages[0].Instructions
	if ins[0].Raw != "echo a   b" || ins[1].Raw != `c:\src d:\dst` {
		t.Fatalf("instructions = %+v", ins)
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"no from":       "RUN echo\n",
		"cmd before":    "ENV a=b\nFROM x\n",
		"unknown":       "FROM x\nFOO bar\n",
		"bad from":      "FROM x AS\n",
		"heredoc":       "FROM x\nRUN <<EOF\necho\nEOF\n",
		"bad escape":    "# escape=x\nFROM x\n",
		"dup directive": "# escape=\\\n# escape=`\nFROM x\n",
	}
	for name, src := range cases {
		if _, err := Parse(strings.NewReader(src)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestWords(t *testing.T) {
	env := map[string]string{"A": "1", "E": "", "P": "/opt"}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	cases := []struct {
		in   string
		want []string
	}{
		{`a b  c`, []string{"a", "b", "c"}},
		{`$A ${A}x "$A y" '$A'`, []string{"1", "1x", "1 y", "$A"}},
		{`${E:-d} ${E-d} ${N:-d} ${A:+set} ${N:+set}x`, []string{"d", "", "d", "set", "x"}},
		{`${P}/bin\ dir "q\"uote" a\$b`, []string{"/opt/bin dir", `q"uote`, "a$b"}},
		{`${N:-${P}/x}`, []string{"/opt/x"}},
		{`cost $ 5`, []string{"cost", "$", "5"}},
	}
	for _, c := range cases {
		got, err := Words(c.in, '\\', lookup)
		if err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%q = %q, want %q", c.in, got, c.want)
		}
	}
	for _, bad := range []string{`'open`, `"open`, `${A`, `${}`, `${A:?x}`} {
		if _, err := Words(bad, '\\', lookup); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}

func TestKeyValues(t *testing.T) {
	lookup := func(k string) (string, bool) { return "v", k == "X" }
	kv, err := KeyValues(`a=1 b="two words" c=$X`, '\\', lookup, true)
	if err != nil || !reflect.DeepEqual(kv, [][2]string{{"a", "1"}, {"b", "two words"}, {"c", "v"}}) {
		t.Fatalf("kv = %v, %v", kv, err)
	}
	kv, err = KeyValues(`PATH /usr/local/bin:$X`, '\\', lookup, true)
	if err != nil || !reflect.DeepEqual(kv, [][2]string{{"PATH", "/usr/local/bin:v"}}) {
		t.Fatalf("legacy kv = %v, %v", kv, err)
	}
	if _, err := KeyValues(`a b`, '\\', lookup, false); err == nil {
		t.Fatal("expected error without legacy form")
	}
}
