package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
)

// errExecReported means the exec failure was already written to stdout in a user-facing
// form; main must exit non-zero without prefixing "nyx:" again.
var errExecReported = errors.New("exec error already printed")

// execFailureMarker is written by nyxd on the exec response stream after headers
// when crun exec fails (HTTP 200 was already sent so the client can stream stdout).
const execFailureMarker = "nyxd: exec:"

// execTailRing records the tail of the raw exec byte stream for [execFailureFromTail].
type execTailRing struct {
	max int
	buf []byte
}

func (r *execTailRing) Write(p []byte) (int, error) {
	const defaultMax = 16384
	if r.max == 0 {
		r.max = defaultMax
	}
	r.buf = append(r.buf, p...)
	if len(r.buf) > r.max {
		r.buf = r.buf[len(r.buf)-r.max:]
	}
	return len(p), nil
}

func (r *execTailRing) Bytes() []byte {
	return append([]byte(nil), r.buf...)
}

// bashExecCleanFilter rewrites crun's "missing bash" line to drop timestamp prefixes and
// drops the nyxd exec trailer on stdout so the user only sees the executable-file line.
type bashExecCleanFilter struct {
	dst io.Writer
	buf []byte
}

func newBashExecCleanFilter(dst io.Writer) *bashExecCleanFilter {
	return &bashExecCleanFilter{dst: dst}
}

// nyxdExecTrailerFilter drops lines the daemon appends to the exec byte stream on
// failure (`nyxd: exec: …`) so the same text is not shown on the PTY and again as
// `nyx: …` on stderr.
//
// It is line-oriented: do not use it for interactive `-t` sessions (raw TTY echo
// is often byte-at-a-time without newlines, so output would stay buffered until Enter).
type nyxdExecTrailerFilter struct {
	dst io.Writer
	buf []byte
}

func newNyxdExecTrailerFilter(dst io.Writer) *nyxdExecTrailerFilter {
	return &nyxdExecTrailerFilter{dst: dst}
}

func (f *nyxdExecTrailerFilter) emitLine(line []byte) error {
	raw := bytes.TrimRight(line, "\r\n")
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	low := bytes.ToLower(bytes.TrimSpace(raw))
	if bytes.HasPrefix(low, []byte("nyxd: exec:")) {
		return nil
	}
	_, err := f.dst.Write(line)
	return err
}

func (f *nyxdExecTrailerFilter) Write(p []byte) (int, error) {
	f.buf = append(f.buf, p...)
	for {
		idx := bytes.IndexByte(f.buf, '\n')
		if idx < 0 {
			return len(p), nil
		}
		line := f.buf[:idx+1]
		f.buf = f.buf[idx+1:]
		if err := f.emitLine(line); err != nil {
			return 0, err
		}
	}
}

func (f *nyxdExecTrailerFilter) Flush() error {
	if len(f.buf) == 0 {
		return nil
	}
	line := f.buf
	f.buf = nil
	return f.emitLine(append(line, '\n'))
}

func stripToExecutableFileLine(line []byte) []byte {
	low := bytes.ToLower(line)
	idx := bytes.Index(low, []byte("executable file"))
	if idx < 0 {
		return bytes.TrimSpace(line)
	}
	return bytes.TrimSpace(line[idx:])
}

func (f *bashExecCleanFilter) handleLine(line []byte) error {
	if len(line) == 0 {
		return nil
	}
	raw := bytes.TrimRight(line, "\r\n")
	if len(raw) == 0 {
		return nil
	}
	low := bytes.ToLower(bytes.TrimSpace(raw))
	if bytes.HasPrefix(low, []byte("nyxd: exec:")) {
		return nil
	}
	if bytes.Contains(low, []byte("executable file")) && bytes.Contains(low, []byte("bash")) {
		out := stripToExecutableFileLine(raw)
		if len(out) == 0 {
			return nil
		}
		if _, err := f.dst.Write(out); err != nil {
			return err
		}
		_, err := f.dst.Write([]byte("\n"))
		return err
	}
	_, err := f.dst.Write(line)
	return err
}

func (f *bashExecCleanFilter) Write(p []byte) (int, error) {
	f.buf = append(f.buf, p...)
	for {
		idx := bytes.IndexByte(f.buf, '\n')
		if idx < 0 {
			return len(p), nil
		}
		line := f.buf[:idx+1]
		f.buf = f.buf[idx+1:]
		if err := f.handleLine(line); err != nil {
			return 0, err
		}
	}
}

func (f *bashExecCleanFilter) Flush() error {
	if len(f.buf) == 0 {
		return nil
	}
	line := f.buf
	f.buf = nil
	return f.handleLine(append(line, '\n'))
}

func execFailureFromTail(tail []byte) error {
	idx := bytes.LastIndex(tail, []byte(execFailureMarker))
	if idx < 0 {
		return nil
	}
	line := bytes.TrimSpace(tail[idx:])
	if nl := bytes.IndexByte(line, '\n'); nl >= 0 {
		line = line[:nl]
	}
	msg := strings.TrimSpace(strings.TrimPrefix(string(line), execFailureMarker))
	if msg == "" {
		return errors.New("exec failed")
	}
	return errors.New(msg)
}
