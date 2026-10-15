// Package logs stores container output: one JSON line per log line in
// {logDir}/{id}.log, rotated by size into {id}.log.1 … {id}.log.N.
// No goroutine leaks: all goroutines are tied to a context.
package logs

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Rotation defaults: at most 3 files of 10 MiB per container.
const (
	DefaultMaxSize  = 10 << 20
	DefaultMaxFiles = 3
)

// maxLine is the longest log line kept in one entry; longer lines are split.
const maxLine = 64 * 1024

// Entry is a single log line from a container.
type Entry struct {
	Time        time.Time `json:"time"`
	ContainerID string    `json:"container_id"`
	Stream      string    `json:"stream"` // stdout or stderr
	Log         string    `json:"log"`
}

// Collector writes container output to size-rotated log files.
type Collector struct {
	logDir   string
	log      *slog.Logger
	maxSize  int64 // rotate when the current file reaches this size; 0 = never
	maxFiles int   // files kept per container, the current one included

	mu    sync.Mutex
	files map[string]*logFile
}

// logFile is a container's current log file, shared by its stdout and
// stderr streams so that rotation happens once and lines never interleave.
type logFile struct {
	mu   sync.Mutex
	f    *os.File
	size int64
	refs int
}

// NewCollector creates a log collector writing to logDir with the default
// rotation (DefaultMaxSize, DefaultMaxFiles).
func NewCollector(logDir string, log *slog.Logger) (*Collector, error) {
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return nil, fmt.Errorf("log dir: %w", err)
	}
	return &Collector{
		logDir:   logDir,
		log:      log,
		maxSize:  DefaultMaxSize,
		maxFiles: DefaultMaxFiles,
		files:    map[string]*logFile{},
	}, nil
}

// SetRotation sets when log files rotate: at maxSize bytes (0 disables
// rotation), keeping maxFiles files per container (at least 1).
func (c *Collector) SetRotation(maxSize int64, maxFiles int) {
	if maxFiles < 1 {
		maxFiles = 1
	}
	c.maxSize, c.maxFiles = maxSize, maxFiles
}

// Path is the current log file of a container.
func (c *Collector) Path(containerID string) string {
	return Path(c.logDir, containerID)
}

// Path is the current log file of a container in logDir.
func Path(logDir, containerID string) string {
	return filepath.Join(logDir, containerID+".log")
}

// open returns the container's shared log file, opening it on first use.
func (c *Collector) open(containerID string) (*logFile, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if lf := c.files[containerID]; lf != nil {
		lf.refs++
		return lf, nil
	}
	f, err := os.OpenFile(c.Path(containerID), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	lf := &logFile{f: f, size: st.Size(), refs: 1}
	c.files[containerID] = lf
	return lf, nil
}

func (c *Collector) release(containerID string, lf *logFile) {
	c.mu.Lock()
	defer c.mu.Unlock()
	lf.refs--
	if lf.refs > 0 {
		return
	}
	delete(c.files, containerID)
	lf.mu.Lock()
	lf.f.Close()
	lf.mu.Unlock()
}

// write appends one encoded entry, rotating first when the file is full.
func (c *Collector) write(containerID string, lf *logFile, line []byte) error {
	lf.mu.Lock()
	defer lf.mu.Unlock()
	if c.maxSize > 0 && lf.size > 0 && lf.size+int64(len(line)) > c.maxSize {
		if err := c.rotate(containerID, lf); err != nil {
			c.log.Warn("log rotate", "id", containerID, "err", err)
		}
	}
	n, err := lf.f.Write(line)
	lf.size += int64(n)
	return err
}

// rotate shifts id.log → id.log.1 → … → id.log.N-1 (dropping the oldest)
// and reopens an empty id.log. lf.mu is held.
func (c *Collector) rotate(containerID string, lf *logFile) error {
	cur := c.Path(containerID)
	if err := lf.f.Close(); err != nil {
		c.log.Warn("log close", "id", containerID, "err", err)
	}
	var err error
	if c.maxFiles <= 1 {
		err = os.Remove(cur)
	} else {
		os.Remove(cur + "." + strconv.Itoa(c.maxFiles-1))
		for i := c.maxFiles - 2; i >= 1; i-- {
			os.Rename(cur+"."+strconv.Itoa(i), cur+"."+strconv.Itoa(i+1))
		}
		err = os.Rename(cur, cur+".1")
	}
	f, oerr := os.OpenFile(cur, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if oerr != nil {
		// Keep writing somewhere: the old file is gone or renamed, so this
		// only fails when the directory is unusable.
		return errors.Join(err, oerr)
	}
	st, serr := f.Stat()
	lf.f, lf.size = f, 0
	if serr == nil {
		lf.size = st.Size()
	}
	return err
}

// Stream reads log lines from r (container stdout/stderr) and appends them
// to the container's log file until r is closed or ctx is cancelled.
// Lines longer than 64 KiB are split into several entries.
// stream should be "stdout" or "stderr".
func (c *Collector) Stream(ctx context.Context, containerID, stream string, r io.Reader) {
	lf, err := c.open(containerID)
	if err != nil {
		c.log.Error("open log file", "id", containerID, "err", err)
		return
	}
	defer c.release(containerID, lf)

	br := bufio.NewReaderSize(r, maxLine)
	for {
		if ctx.Err() != nil {
			return
		}
		chunk, err := br.ReadSlice('\n')
		if len(chunk) > 0 {
			line, _ := json.Marshal(Entry{
				Time:        time.Now().UTC(),
				ContainerID: containerID,
				Stream:      stream,
				Log:         strings.TrimSuffix(string(chunk), "\n"),
			})
			if werr := c.write(containerID, lf, append(line, '\n')); werr != nil {
				c.log.Warn("log write", "id", containerID, "err", werr)
			}
		}
		switch {
		case err == nil, errors.Is(err, bufio.ErrBufferFull):
		case errors.Is(err, io.EOF), ctx.Err() != nil:
			return
		default:
			c.log.Warn("log read error", "id", containerID, "err", err)
			return
		}
	}
}

// Tail returns the last n entries of a container's log, across rotated
// files. Lines that are not valid entries are skipped.
func (c *Collector) Tail(containerID string, n int) ([]Entry, error) {
	for want := n; ; want *= 2 {
		lines, _, err := TailLines(c.logDir, containerID, want)
		if err != nil {
			return nil, err
		}
		entries := make([]Entry, 0, len(lines))
		for _, ln := range lines {
			var e Entry
			if json.Unmarshal(ln, &e) == nil {
				entries = append(entries, e)
			}
		}
		if len(entries) >= n || len(lines) < want {
			if len(entries) > n {
				entries = entries[len(entries)-n:]
			}
			return entries, nil
		}
	}
}

// TailLines returns the last n raw lines of a container's log, oldest first,
// reading backwards from the end of id.log and, when it holds fewer than n
// lines, of id.log.1, id.log.2 … It never loads whole files. end is the size
// of id.log when it was read, where a follower continues.
func TailLines(logDir, containerID string, n int) (lines [][]byte, end int64, err error) {
	cur := Path(logDir, containerID)
	f, err := os.Open(cur)
	if err != nil {
		return nil, 0, fmt.Errorf("open log %s: %w", containerID, err)
	}
	lines, end, err = tailFile(f, n)
	f.Close()
	if err != nil {
		return nil, 0, err
	}
	for i := 1; len(lines) < n; i++ {
		rf, err := os.Open(cur + "." + strconv.Itoa(i))
		if err != nil {
			break
		}
		older, _, err := tailFile(rf, n-len(lines))
		rf.Close()
		if err != nil {
			break
		}
		lines = append(older, lines...)
	}
	return lines, end, nil
}

// tailFile returns the last n complete lines of f and the file size.
func tailFile(f *os.File, n int) ([][]byte, int64, error) {
	st, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	if !st.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("%s: not a regular file", f.Name())
	}
	size := st.Size()
	if n <= 0 || size == 0 {
		return nil, size, nil
	}
	const block = 32 * 1024
	var buf []byte // the tail of the file read so far
	pos := size
	for pos > 0 && countLines(buf) <= n {
		step := int64(block)
		if pos < step {
			step = pos
		}
		pos -= step
		chunk := make([]byte, step)
		if _, err := f.ReadAt(chunk, pos); err != nil && !errors.Is(err, io.EOF) {
			return nil, 0, err
		}
		buf = append(chunk, buf...)
	}
	text := strings.TrimSuffix(string(buf), "\n")
	parts := strings.Split(text, "\n")
	if pos > 0 && len(parts) > 0 {
		parts = parts[1:] // the first line may be cut off
	}
	if len(parts) > n {
		parts = parts[len(parts)-n:]
	}
	out := make([][]byte, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, []byte(p))
		}
	}
	return out, size, nil
}

func countLines(b []byte) int {
	n := 0
	for _, c := range b {
		if c == '\n' {
			n++
		}
	}
	return n
}

// Remove deletes all log files of a container, rotated ones included.
func (c *Collector) Remove(containerID string) {
	Remove(c.logDir, containerID)
}

// Remove deletes all log files of a container in logDir: id.log and every
// rotated id.log.N. It reports whether there was any.
func Remove(logDir, containerID string) bool {
	cur := Path(logDir, containerID)
	found := os.Remove(cur) == nil
	rotated, _ := filepath.Glob(cur + ".*")
	for _, p := range rotated {
		if _, err := strconv.Atoi(strings.TrimPrefix(p, cur+".")); err == nil && os.Remove(p) == nil {
			found = true
		}
	}
	return found
}

// ParseSize parses a log size such as "10m", "512k", "1g" or "1048576"
// (bytes; k, m and g are powers of 1024, an optional trailing "b" is
// ignored). "0" disables rotation.
func ParseSize(s string) (int64, error) {
	v := strings.ToLower(strings.TrimSpace(s))
	v = strings.TrimSuffix(v, "b")
	mult := int64(1)
	switch {
	case strings.HasSuffix(v, "k"):
		mult, v = 1<<10, strings.TrimSuffix(v, "k")
	case strings.HasSuffix(v, "m"):
		mult, v = 1<<20, strings.TrimSuffix(v, "m")
	case strings.HasSuffix(v, "g"):
		mult, v = 1<<30, strings.TrimSuffix(v, "g")
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid size %q (want e.g. 10m, 512k, 1g or 0)", s)
	}
	return n * mult, nil
}
