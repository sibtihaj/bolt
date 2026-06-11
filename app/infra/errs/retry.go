package errs

import (
	"context"
	"time"
)

// Do calls fn up to maxAttempts times, retrying only when IsTransient(err) is
// true.  onRetry is called before each sleep so the caller can log progress;
// pass nil to skip logging.  The delay doubles after each attempt, capped at
// 30 s.
func Do(
	ctx context.Context,
	maxAttempts int,
	fn func() error,
	onRetry func(attempt int, delay time.Duration, err error),
) error {
	delay := 2 * time.Second
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		if !IsTransient(lastErr) {
			return lastErr
		}
		if attempt == maxAttempts {
			break
		}
		if onRetry != nil {
			onRetry(attempt, delay, lastErr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		if delay < 30*time.Second {
			delay *= 2
		}
	}
	return lastErr
}
