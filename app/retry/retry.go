package retry

import (
	"bytes"
	"fmt"
	"math"
	"math/rand"
	"time"
)

// Config controls retry behavior for an operation type.
type Config struct {
	MaxAttempts int
	InitialWait time.Duration
	MaxWait     time.Duration
	Factor      float64
}

// DefaultK8s is tuned for Helm installs and kubectl operations (slow, heavyweight).
var DefaultK8s = Config{
	MaxAttempts: 4,
	InitialWait: 10 * time.Second,
	MaxWait:     120 * time.Second,
	Factor:      2.0,
}

// DefaultDocker is tuned for docker compose operations.
var DefaultDocker = Config{
	MaxAttempts: 3,
	InitialWait: 5 * time.Second,
	MaxWait:     60 * time.Second,
	Factor:      2.0,
}

// DefaultCloud is tuned for cloud API calls (fast, frequently throttled).
var DefaultCloud = Config{
	MaxAttempts: 5,
	InitialWait: 2 * time.Second,
	MaxWait:     32 * time.Second,
	Factor:      2.0,
}

// Attempt is a function that runs one attempt of the operation.
// The provided buffer receives stderr output for error classification.
type Attempt func(capture *bytes.Buffer) error

// Do runs fn up to cfg.MaxAttempts times with exponential backoff and equal
// jitter. Fatal errors (access denied, bad config, etc.) stop immediately
// without further retries.
func Do(label string, cfg Config, fn Attempt) error {
	var lastErr error

	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		var capture bytes.Buffer
		err := fn(&capture)
		if err == nil {
			return nil
		}
		lastErr = err

		c := Classify(capture.String(), err)
		if c.Class == ClassFatal {
			detail := c.Detail
			if detail != "" {
				detail = " (" + detail + ")"
			}
			return fmt.Errorf("%s: %s%s — not retrying: %w", label, c.Reason, detail, err)
		}

		if attempt == cfg.MaxAttempts {
			break
		}

		wait := equalJitter(cfg.InitialWait, cfg.MaxWait, cfg.Factor, attempt)
		fmt.Printf("  ↻  %s failed (attempt %d/%d, %s",
			label, attempt, cfg.MaxAttempts, c.Reason)
		if c.Detail != "" {
			fmt.Printf(": %q", c.Detail)
		}
		fmt.Printf(") — retrying in %s...\n", wait.Round(time.Second))
		time.Sleep(wait)
	}

	return fmt.Errorf("%s failed after %d attempts: %w", label, cfg.MaxAttempts, lastErr)
}

// equalJitter computes wait = cap/2 + rand(0, cap/2) where cap grows
// exponentially, capped at maxWait. Equal jitter prevents thundering herd
// while still providing meaningful randomisation.
func equalJitter(initial, maxWait time.Duration, factor float64, attempt int) time.Duration {
	cap := float64(initial) * math.Pow(factor, float64(attempt-1))
	if cap > float64(maxWait) {
		cap = float64(maxWait)
	}
	half := cap / 2
	return time.Duration(half + rand.Float64()*half)
}
