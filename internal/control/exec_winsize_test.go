package control

import (
	"testing"
)

func TestExecTTYWinSize(t *testing.T) {
	if execTTYWinSize(execRequest{TTY: false}) != nil {
		t.Fatal("non-tty")
	}
	if execTTYWinSize(execRequest{TTY: true}) != nil {
		t.Fatal("expected nil when rows/cols unset (runtime defaults)")
	}
	w := execTTYWinSize(execRequest{TTY: true, Rows: 30, Cols: 120})
	if w == nil || w.Rows != 30 || w.Cols != 120 {
		t.Fatalf("got %+v", w)
	}
	partial := execTTYWinSize(execRequest{TTY: true, Rows: 0, Cols: 100})
	if partial == nil || partial.Rows != 24 || partial.Cols != 100 {
		t.Fatalf("partial: got %+v want rows=24 cols=100", partial)
	}
}
