package compose

import "testing"

func TestSubstitute(t *testing.T) {
	env := map[string]string{"FOO": "bar", "EMPTY": ""}
	if got := Substitute("x ${FOO} y", env); got != "x bar y" {
		t.Fatalf("got %q", got)
	}
	if got := Substitute(`a ${EMPTY:-def} b`, env); got != "a def b" {
		t.Fatalf("default: got %q", got)
	}
	if got := Substitute(`$$ ${FOO}`, env); got != "$ bar" {
		t.Fatalf("escape: got %q", got)
	}
	if got := Substitute("$FOO", env); got != "bar" {
		t.Fatalf("bare: got %q", got)
	}
}
