package waitext

import (
	"context"
	"errors"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	DefaultInitialInterval     = 500 * time.Millisecond
	DefaultRandomizationFactor = 0.5
	DefaultMultiplier          = 1.5
	DefaultMaxInterval         = 60 * time.Second
)

// NewExponentialBackoff creates a backoff that increases the delay between each step based on a factor
// If maxInterval is positive, then the wait duration will not exceed its value
// Essentially at step N the wait is min(initialInterval*factor^(N-1), maxInterval)
func NewExponentialBackoff(initialInterval time.Duration, factor float64, maxInterval time.Duration) wait.Backoff {
	return wait.Backoff{
		Duration: initialInterval,
		Factor:   factor,
		Cap:      maxInterval,
	}
}

// DefaultExponentialBackoff creates an exponential backoff with sensible default values
// Defaults should match ExponentialBackoff in github.com/cenkalti/backoff
func DefaultExponentialBackoff() wait.Backoff {
	return wait.Backoff{
		Duration: DefaultInitialInterval,
		Factor:   DefaultMultiplier,
		Jitter:   DefaultRandomizationFactor,
		Cap:      DefaultMaxInterval,
	}
}

// NewConstantBackoff creates a backoff that steps at constant intervals.
// The returned backoff can be passed to wait.ExponentialBackoff and it will actually do constant backoff, despite what the function name says.
// This works similar to ConstantBackOff in github.com/cenkalti/backoff
func NewConstantBackoff(interval time.Duration) wait.Backoff {
	return wait.Backoff{
		Duration: interval,
	}
}

// WithRetry creates a new backoff that has all the same settings as the input except for backoff.Steps
// This will cause it to retry when passed to wait.ExponentialBackoff
// Combine with RetryWithContext or Retry
func WithRetry(backoff wait.Backoff, times int) wait.Backoff {
	return wait.Backoff{
		Duration: backoff.Duration,
		Factor:   backoff.Factor,
		Jitter:   backoff.Jitter,
		Steps:    times + 1, // Initial execution should not count as retry so we add it as a step
		Cap:      backoff.Cap,
	}
}

// WithJitter will do randomization on every step on the backoff, causing the wait value to be in [duration, duration+jitter*duration]
// Use when many clients could use be calling the same operation concurrently as this spreads out the calls a bit instead of converging on the same value
func WithJitter(backoff wait.Backoff, randomizationFactor float64) wait.Backoff {
	return wait.Backoff{
		Duration: backoff.Duration,
		Factor:   backoff.Factor,
		Jitter:   randomizationFactor,
		Steps:    backoff.Steps,
		Cap:      backoff.Cap,
	}
}

// Retry acts as RetryWithContext but with context.Background()
func Retry(backoff wait.Backoff, operation func() error, errNotify func(error)) error {
	return retryCore(context.Background(), backoff, func(_ context.Context) error {
		return operation()
	}, errNotify, false)
}

// RetryWithContext executes an operation with retries following these semantics:
//
//   - The operation is executed at least once (even if context is cancelled)
//
//   - If operation returns an error that is _not_ NonTransientError, the operation might be retried (see below for more info when)
//
//   - If operation returns an error that is NonTransientError, the operation is not retried and underlying error is unwrapped
//
// The operation will not be retried anymore if
//
//   - backoff.Steps reaches 0
//
//   - the context is cancelled
//
// The end result is the final error observed when calling operation() or nil if successful or context.Err() if the context was cancelled.
// If retryNotify is passed, it is called when making retries.
// Caveat: this function is similar to wait.ExponentialBackoff but has some important behavior differences like at-least-one execution and retryable errors
func RetryWithContext(ctx context.Context, backoff wait.Backoff, operation func(context.Context) error, retryNotify func(error)) error {
	return retryCore(ctx, backoff, operation, retryNotify, false)
}

// RetryForever acts as RetryWithContext but ignores the max retries set on backoff.
// Use with care: The provided context should have a deadline to avoid an infinite loop.
func RetryForever(ctx context.Context, backoff wait.Backoff, operation func(context.Context) error, retryNotify func(error)) error {
	return retryCore(ctx, backoff, operation, retryNotify, true)
}

func retryCore(ctx context.Context, backoff wait.Backoff, operation func(context.Context) error, retryNotify func(error), runForever bool) error {
	//lastErr := operation(ctx)
	//if lastErr == nil {
	//	return nil
	//}

	var lastErr error

	for {
		//select {
		//case <-ctx.Done():
		//	return ctx.Err()
		//case <-time.After(waitInterval):
		//}
		//
		// TODO: check here and execute once before
		lastErr = operation(ctx)

		// Happy path
		if lastErr == nil {
			return nil
		}

		// Not-so-happy path
		var nonTransientError *NonTransientError
		if errors.As(lastErr, &nonTransientError) {
			// We don't call retryNotify here since we won't retry
			return nonTransientError.Unwrap()
		}

		// Transient error path

		// Check if we have a retry path at all
		if backoff.Steps <= 1 && !runForever {
			// Don't do anything if we won't retry (steps would reach <= 0 on backoff.Step())
			break
		}

		// Notify about expected retry
		if retryNotify != nil {
			retryNotify(lastErr)
		}

		waitInterval := backoff.Step() // This updates backoff.Steps internally

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitInterval):
		}
	}

	return lastErr
}
