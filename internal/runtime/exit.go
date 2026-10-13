package runtime

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// StartExitError is returned by RunForeground when crun or the workload
// fails: Output is everything written to stderr (the workload's startup log,
// or crun's own error), Err the exit status.
type StartExitError struct {
	Err    error
	Output string
}

func (e *StartExitError) Error() string {
	if s := ExitSummary(e.Output); s != "" {
		return fmt.Sprintf("%v: %s", e.Err, s)
	}
	return e.Err.Error()
}

func (e *StartExitError) Unwrap() error { return e.Err }

// maxSummary caps the summary in an error message.
const maxSummary = 400

// ExitSummary picks the line of a failed process's output that explains the
// failure: the last line that looks like an error (for JSON log lines, its
// "error" or "msg" field), else the last line.
func ExitSummary(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var last string
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l == "" {
			continue
		}
		text, isErr := describeLine(l)
		if last == "" {
			last = text
		}
		if isErr {
			return capRunes(text, maxSummary)
		}
	}
	return capRunes(last, maxSummary)
}

// describeLine returns the readable text of one output line and whether it
// reports an error.
func describeLine(l string) (string, bool) {
	if strings.HasPrefix(l, "{") {
		var m map[string]any
		if json.Unmarshal([]byte(l), &m) == nil {
			str := func(k string) string { s, _ := m[k].(string); return strings.TrimSpace(s) }
			level := strings.ToLower(str("level"))
			if level == "" {
				level = strings.ToLower(str("severity"))
			}
			text := str("msg")
			if text == "" {
				text = str("message")
			}
			if e := str("error"); e != "" {
				if text == "" || strings.Contains(e, text) {
					text = e
				} else if !strings.Contains(text, e) {
					text += ": " + e
				}
			}
			if text == "" {
				text = l
			}
			switch level {
			case "error", "fatal", "panic", "critical", "emerg", "alert", "crit":
				return text, true
			}
			return text, str("error") != ""
		}
	}
	low := strings.ToLower(l)
	for _, w := range []string{"error", "fatal", "panic", "failed", "permission denied", "not found", "cannot", "can't"} {
		if strings.Contains(low, w) {
			return l, true
		}
	}
	return l, false
}

func capRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}
