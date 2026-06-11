package infra

import (
	"fmt"
	"os"
	"sync"
	"time"
)

var spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type spin struct {
	mu     sync.Mutex
	msg    string
	active bool
	stopCh chan struct{}
	doneCh chan struct{}
	tty    bool
}

var globalSpinner = &spin{tty: detectTTY()}

func detectTTY() bool {
	fi, err := os.Stdout.Stat()
	return err == nil && (fi.Mode()&os.ModeCharDevice != 0)
}

func (s *spin) start(msg string) {
	if !s.tty {
		fmt.Fprintf(os.Stdout, "  ⋯  %s\n", msg)
		return
	}
	s.mu.Lock()
	s.msg = msg
	if s.active {
		s.mu.Unlock()
		return // goroutine already running; message updated above
	}
	s.active = true
	s.stopCh = make(chan struct{})
	s.doneCh = make(chan struct{})
	s.mu.Unlock()

	go func() {
		defer close(s.doneCh)
		i := 0
		for {
			// Check for stop before printing.
			select {
			case <-s.stopCh:
				s.mu.Lock()
				fmt.Fprint(os.Stdout, "\r\033[K") // clear spinner line
				s.mu.Unlock()
				return
			default:
			}

			s.mu.Lock()
			fmt.Fprintf(os.Stdout, "\r  %s  %s  ", spinFrames[i%len(spinFrames)], s.msg)
			s.mu.Unlock()
			i++

			select {
			case <-s.stopCh:
				s.mu.Lock()
				fmt.Fprint(os.Stdout, "\r\033[K")
				s.mu.Unlock()
				return
			case <-time.After(80 * time.Millisecond):
			}
		}
	}()
}

func (s *spin) stop() {
	if !s.tty {
		return
	}
	s.mu.Lock()
	if !s.active {
		s.mu.Unlock()
		return
	}
	s.active = false
	close(s.stopCh)
	doneCh := s.doneCh
	s.mu.Unlock()
	<-doneCh
}

func (s *spin) success(msg string) {
	s.stop()
	fmt.Fprintf(os.Stdout, "  ✓  %s\n", msg)
}

// info prints a message above the running spinner without stopping it.
// Thread-safe: holds the mutex while writing so the spinner goroutine cannot
// interleave.  After info() the cursor is on a new line and the spinner goroutine
// will resume on that line on its next tick.
func (s *spin) info(msg string) {
	if !s.tty {
		fmt.Fprintf(os.Stdout, "%s\n", msg)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active {
		fmt.Fprint(os.Stdout, "\r\033[K") // clear current spinner line
	}
	fmt.Fprintf(os.Stdout, "%s\n", msg)
	// Spinner goroutine will redraw on the new line on its next tick.
}

// ── Exported helpers for cmd package ─────────────────────────────────────────

// StartSpinner starts (or updates the message of) the global spinner.
func StartSpinner(msg string) { globalSpinner.start(msg) }

// StopSpinner stops the global spinner and clears the spinner line.
func StopSpinner() { globalSpinner.stop() }

// SpinnerSuccess stops the spinner and prints a success message.
func SpinnerSuccess(msg string) { globalSpinner.success(msg) }

// SpinnerInfo prints a message above the running spinner without stopping it.
func SpinnerInfo(msg string) { globalSpinner.info(msg) }
