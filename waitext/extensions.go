package waitext

import (
	"context"
	"math"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	defaultInitialInterval     = 500 * time.Millisecond
	defaultRandomizationFactor = 0.5
	defaultMultiplier          = 1.5
	defaultMaxInterval         = 60 * time.Second

	// Forever should be used to simulate infinite retries or backoff increase.
	// Usually it's wise to have a context with timeout to avoid an infinite loop.
	Forever = math.MaxInt32
)

// DefaultExponentialBackoff creates an exponential backoff with sensible default values.
// Defaults should match ExponentialBackoff in github.com/cenkalti/backoff
func DefaultExponentialBackoff() wait.Backoff {
	return wait.Backoff{
		Duration: defaultInitialInterval,
		Factor:   defaultMultiplier,
		Jitter:   defaultRandomizationFactor,
		Cap:      defaultMaxInterval,
		Steps:    Forever,
	}
}

// NewConstantBackoff creates a backoff that steps at constant intervals.
// This backoff will run "forever", use WithMaxRetries or a context to put a hard cap.
// This works similar to ConstantBackOff in github.com/cenkalti/backoff
func NewConstantBackoff(interval time.Duration) wait.Backoff {
	return wait.Backoff{
		Duration: interval,
		Steps:    Forever,
	}
}

// Retry executes an operation with retries following these semantics:
//
//   - The operation is executed at least once (even if context is cancelled)
//
//   - If operation returns nil error, assumption is that it succeeded
//
//   - If operation returns non-nil error, then the first boolean return value decides whether to retry or not
//
// The operation will not be retried anymore if
//
//   - retries reaches 0
//
//   - the context is cancelled
//
// The end result is the final error observed when calling operation() or nil if successful or context.Err() if the context was cancelled.
// If retryNotify is passed, it is called when making retries.
// Caveat: this function is similar to wait.ExponentialBackoff but has some important behavior differences like at-least-one execution and retryable errors
func Retry(ctx context.Context, backoff wait.Backoff, retries int, operation func(context.Context) (bool, error), retryNotify func(error)) error {
	var lastErr error
	var shouldRetry bool

	shouldRetry, lastErr = operation(ctx)

	// No retry needed
	if lastErr == nil || !shouldRetry {
		return lastErr
	}

	for retries > 0 {
		// Notify about expected retry
		if retryNotify != nil {
			retryNotify(lastErr)
		}

		waitInterval := backoff.Step()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitInterval):
		}

		shouldRetry, lastErr = operation(ctx)
		retries--

		// We are done
		if lastErr == nil || !shouldRetry {
			break
		}
	}

	return lastErr
}
