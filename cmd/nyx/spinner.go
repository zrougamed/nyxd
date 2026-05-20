package main

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// withStderrSpinner runs fn while showing a small braille spinner on stderr.
// If message is empty, fn runs without a spinner.
func withStderrSpinner(message string, fn func() error) error {
	if message == "" {
		return fn()
	}
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		tick := time.NewTicker(90 * time.Millisecond)
		defer tick.Stop()
		i := 0
		for {
			select {
			case <-done:
				_, _ = fmt.Fprint(os.Stderr, "\r\x1b[2K")
				return
			case <-tick.C:
				f := frames[i%len(frames)]
				i++
				_, _ = fmt.Fprintf(os.Stderr, "\r\x1b[2K%s %s", f, message)
			}
		}
	}()
	err := fn()
	close(done)
	wg.Wait()
	return err
}
