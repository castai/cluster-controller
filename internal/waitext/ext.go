package waitext

import (
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
// Combine with TODO to enable transient retries
func WithRetry(backoff wait.Backoff, times int) wait.Backoff {
	return wait.Backoff{
		Duration: backoff.Duration,
		Factor:   backoff.Factor,
		Jitter:   backoff.Jitter,
		Steps:    times,
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

func WithLogging(cond wait.ConditionFunc) wait.ConditionFunc {
	return func() (done bool, err error) {

		done, err = cond()
		if err != nil {
			// Log
		}
		return
	}
}

// WithTransientRetryCndFn follows the semantics of WithTransientRetries but for wait.ConditionFunc
func WithTransientRetryCndFn(cond wait.ConditionFunc, errNotify func(error)) wait.ConditionFunc {
	return func() (done bool, err error) {
		done, err = cond()
		if err != nil {
			var nonTransientError *NonTransientError
			if errors.As(err, &nonTransientError) {
				// We don't call errNotify here since we don't expect retry
				return false, nonTransientError.Unwrap()
			}

			if errNotify != nil {
				errNotify(err)
			}

			// We don't surface the error here as the convention for wait.ConditionFunc is that any error should stop retries
			// (and we assume the operation wanted a retry since it did not return NonTransientError)
			return false, nil
		}

		return done, nil
	}
}

// WithTransientRetries converts operation to a wait.ConditionFunc with the following semantics:
// - if no error is returned, operation is done
// - if an error is returned, and it is instance of NonTransientError, it is surfaced immediately and operation should not be retried
// - for other errors, call errNotify (if not nil) and swallow the error but signal to caller than operation was not successful
// Note that wait.ExponentialBackoff controls the overall retry behavior and what to return when all retry attempts are exceeded.
func WithTransientRetries(operation func() error, errNotify func(error)) wait.ConditionFunc {
	return WithTransientRetryCndFn(func() (bool, error) {
		err := operation()
		return err == nil, err
	}, errNotify)
}

func WithRetries(cond wait.ConditionFunc) wait.ConditionFunc {
	// Retry up to Step times
	return func() (done bool, err error) {
		done, err = cond()
		if err != nil {
			if errors.As(err, &NonTransientError{}) {
				return false, err // TODO
			}

			// Log?

			// We do not propagate the error here because it would cause calls to wait.ExponentialBackoff() to stop retrying
			return false, nil
		}
		return done, err
	}
}
