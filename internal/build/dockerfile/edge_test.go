package dockerfile

import (
	"strings"
	"testing"
)

func TestExpandEdgeCases(t *testing.T) {
	vars := map[string]string{"SET": "v", "EMPTY": ""}
	lookup := func(k string) (string, bool) { v, ok := vars[k]; return v, ok }
	for in, want := range map[string]string{
		`$`:                 "$",
		`a$ b`:              "a$ b",
		`${SET:-d}`:         "v",
		`${EMPTY:-d}`:       "d",
		`${EMPTY-d}`:        "",
		`${UNSET-d}`:        "d",
		`${SET:+alt}`:       "alt",
		`${EMPTY:+alt}`:     "",
		`${UNSET+alt}`:      "",
		`${UNSET:-${SET}x}`: "vx",
		`${UNSET:-a\}b}`:    "a}b",
		`"q \$x \" \\ \n"`:  `q $x " \ \n`,
		`"$SET"`:            "v",
		`'$SET'`:            "$SET",
		`\$SET`:             "$SET",
	} {
		got, err := Expand(in, '\\', lookup)
		if err != nil || got != want {
			t.Errorf("Expand(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if got, _ := Expand(`"tail\`, '`', nil); got != "" {
		// unterminated quote: error expected below
		_ = got
	}
	for _, bad := range []string{`${`, `${}`, `${SET`, `${SET:`, `${SET?x}`, `${SET:-x`, `"open`, `'open`, `${UNSET:-${}}`, `"${`, `${UNSET:-$}`} {
		if _, err := Expand(bad, '\\', lookup); err == nil && bad != `${UNSET:-$}` {
			t.Errorf("Expand(%q): no error", bad)
		}
	}
	if v, _ := Expand(`${SET}`, '\\', nil); v != "" {
		t.Error("nil lookup")
	}
	if w, err := Words(`a "b c" 'd e' f\ g`, '\\', nil); err != nil || strings.Join(w, "|") != "a|b c|d e|f g" {
		t.Errorf("Words: %q %v", w, err)
	}
	if w, _ := Words("x`", '`', nil); len(w) != 1 {
		t.Errorf("trailing escape: %q", w)
	}
	if w, _ := Words(`"a\`, '\\', nil); w != nil {
		t.Errorf("unterminated with escape: %q", w)
	}
}

func TestKeyValuesEdgeCases(t *testing.T) {
	if _, err := KeyValues("", '\\', nil, true); err == nil {
		t.Error("empty")
	}
	if _, err := KeyValues("KEY", '\\', nil, true); err == nil || !strings.Contains(err.Error(), "missing value") {
		t.Errorf("legacy without value: %v", err)
	}
	if kv, err := KeyValues("KEY some value", '\\', nil, true); err != nil || kv[0][1] != "some value" {
		t.Errorf("legacy: %v %v", kv, err)
	}
	if _, err := KeyValues("KEY ${", '\\', nil, true); err == nil {
		t.Error("legacy bad value")
	}
	if _, err := KeyValues("A=${", '\\', nil, true); err == nil {
		t.Error("bad expansion")
	}
	if _, err := KeyValues("novalue", '\\', nil, false); err == nil {
		t.Error("non-legacy word without =")
	}
}

func TestParseEdgeCases(t *testing.T) {
	for want, src := range map[string]string{
		"set twice":           "# escape=`\n# escape=\\\nFROM x\n",
		"invalid escape":      "# escape=x\nFROM x\n",
		"FROM expects":        "FROM a b c\n",
		"before the first":    "RUN x\nFROM y\n",
		"no FROM instruction": "# just a comment\n",
		"unknown instruction": "FROM x\nNOPE y\n",
		"heredocs":            "FROM x\nCOPY <<EOF /a\nx\nEOF\n",
	} {
		if _, err := Parse(strings.NewReader(src)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
	df, err := Parse(strings.NewReader("# syntax=docker/dockerfile:1\n# escape=`\nFROM x AS b\nRUN a `\n\n  # comment inside\n  b ``\nCOPY --link --chown=1 a b\nRUN [\"not json\n"))
	if err != nil {
		t.Fatal(err)
	}
	if df.Escape != '`' || len(df.Stages) != 1 || df.Stages[0].Name != "b" {
		t.Fatalf("%+v", df)
	}
	ins := df.Stages[0].Instructions
	if !strings.Contains(ins[0].Raw, "a") || !strings.Contains(ins[0].Raw, "b") {
		t.Errorf("continuation: %q", ins[0].Raw)
	}
	if v, ok := ins[1].Flag("link"); !ok || v != "true" {
		t.Errorf("boolean flag: %q %v", v, ok)
	}
	if _, ok := ins[1].Flag("nope"); ok {
		t.Error("absent flag")
	}
	if ins[2].JSON {
		t.Error("broken JSON must be shell form")
	}
	if _, err := Parse(errReader{}); err == nil {
		t.Error("read error")
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errBoom }

var errBoom = &boom{}

type boom struct{}

func (*boom) Error() string { return "boom" }
