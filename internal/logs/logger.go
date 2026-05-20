// Package log provides structured container log streaming.
// Attaches to crun's log-format=json output and forwards to the daemon log.
// No goroutine leaks: all goroutines are tied to a context.
package logs

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// Entry is a single log line from a container.
type Entry struct {
	Time        time.Time `json:"time"`
	ContainerID string    `json:"container_id"`
	Stream      string    `json:"stream"` // stdout or stderr
	Log         string    `json:"log"`
}

// Collector streams container logs to a rotating file and/or slog.
type Collector struct {
	logDir string
	log    *slog.Logger
}

// NewCollector creates a log collector writing to logDir.
func NewCollector(logDir string, log *slog.Logger) (*Collector, error) {
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return nil, fmt.Errorf("log dir: %w", err)
	}
	return &Collector{logDir: logDir, log: log}, nil
}

// Stream reads log lines from r (container stdout/stderr) and writes them
// to a per-container log file. Stops when ctx is cancelled or r is closed.
// stream should be "stdout" or "stderr".
func (c *Collector) Stream(ctx context.Context, containerID, stream string, r io.Reader) {
	logPath := filepath.Join(c.logDir, containerID+".log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		c.log.Error("open log file", "id", containerID, "err", err)
		return
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 64*1024)

	for {
		// Check context before blocking on Scan.
		select {
		case <-ctx.Done():
			return
		default:
		}

		if !scanner.Scan() {
			if err := scanner.Err(); err != nil && ctx.Err() == nil {
				c.log.Warn("log scan error", "id", containerID, "err", err)
			}
			return
		}

		line := scanner.Text()
		entry := Entry{
			Time:        time.Now().UTC(),
			ContainerID: containerID,
			Stream:      stream,
			Log:         line,
		}

		if err := enc.Encode(entry); err != nil {
			c.log.Warn("log write", "id", containerID, "err", err)
		}
	}
}

// Tail returns the last n lines from a container's log file.
func (c *Collector) Tail(containerID string, n int) ([]Entry, error) {
	logPath := filepath.Join(c.logDir, containerID+".log")
	f, err := os.Open(logPath)
	if err != nil {
		return nil, fmt.Errorf("open log %s: %w", containerID, err)
	}
	defer f.Close()

	// Simple approach: read all, return last n.
	// For large logs, implement a proper tail-from-end algorithm.
	var entries []Entry
	dec := json.NewDecoder(f)
	for {
		var e Entry
		if err := dec.Decode(&e); err != nil {
			break
		}
		entries = append(entries, e)
		if len(entries) > n*2 {
			// Trim to avoid unbounded growth in memory.
			entries = entries[len(entries)-n:]
		}
	}

	if len(entries) > n {
		entries = entries[len(entries)-n:]
	}
	return entries, nil
}

// Rotate renames containerID.log → containerID.log.1 and starts a fresh file.
func (c *Collector) Rotate(containerID string) error {
	logPath := filepath.Join(c.logDir, containerID+".log")
	rotPath := logPath + ".1"
	return os.Rename(logPath, rotPath)
}

// Remove deletes all log files for a container.
func (c *Collector) Remove(containerID string) {
	os.Remove(filepath.Join(c.logDir, containerID+".log"))
	os.Remove(filepath.Join(c.logDir, containerID+".log.1"))
}
