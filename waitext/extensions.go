package waitext

import (
	"context"
	"math"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	DefaultInitialInterval     = 500 * time.Millisecond
	DefaultRandomizationFactor = 0.5
	DefaultMultiplier          = 1.5
	DefaultMaxInterval         = 60 * time.Second

	// Forever should be used to simulate infinite retries or backoff increase.
	// Usually it's wise to have a context with timeout to avoid an infinite loop.
	Forever = math.MaxInt32
)

// NewExponentialBackoff creates a backoff that increases the delay between each step based on a factor.
// If maxInterval is positive, then the wait duration will not exceed its value while increasing.
// Essentially at step N the wait is min(initialInterval*factor^(N-1), maxInterval)
func NewExponentialBackoff(initialInterval time.Duration, factor float64, maxInterval time.Duration) wait.Backoff {
	return wait.Backoff{
		Duration: initialInterval,
		Factor:   factor,
		Cap:      maxInterval,
		Steps:    Forever,
	}
}

// DefaultExponentialBackoff creates an exponential backoff with sensible default values.
// Defaults should match ExponentialBackoff in github.com/cenkalti/backoff
func DefaultExponentialBackoff() wait.Backoff {
	return wait.Backoff{
		Duration: DefaultInitialInterval,
		Factor:   DefaultMultiplier,
		Jitter:   DefaultRandomizationFactor,
		Cap:      DefaultMaxInterval,
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

// Retry acts as RetryWithContext but with context.Background()
func Retry(backoff wait.Backoff, retries int, operation func() (bool, error), errNotify func(error)) error {
	return retryCore(context.Background(), backoff, retries, func(_ context.Context) (bool, error) {
		return operation()
	}, errNotify)
}

// RetryWithContext executes an operation with retries following these semantics:
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
func RetryWithContext(ctx context.Context, backoff wait.Backoff, retries int, operation func(context.Context) (bool, error), retryNotify func(error)) error {
	return retryCore(ctx, backoff, retries, operation, retryNotify)
}

func retryCore(ctx context.Context, backoff wait.Backoff, retries int, operation func(context.Context) (bool, error), retryNotify func(error)) error {
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
