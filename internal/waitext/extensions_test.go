package waitext

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/util/wait"
)

func TestNewConstantBackoff(t *testing.T) {
	r := require.New(t)
	expectedSleepDuration := 10 * time.Second
	backoff := NewConstantBackoff(expectedSleepDuration)

	for i := 0; i < 10; i++ {
		r.Equal(expectedSleepDuration, backoff.Step())
	}
}

func TestWithRetry(t *testing.T) {
	r := require.New(t)

	retries := 10
	expectedTotalExecutions := 1 + 10 // Initial is not counted as retry

	backoff := WithRetry(DefaultExponentialBackoff(), retries)

	r.Equal(expectedTotalExecutions, backoff.Steps)
}

func TestWithJitter(t *testing.T) {
	r := require.New(t)

	backoff := WithJitter(DefaultExponentialBackoff(), 0.5)

	r.Equal(0.5, backoff.Jitter)
}

func TestExponentialBackoff(t *testing.T) {
	r := require.New(t)

	interval := 100 * time.Millisecond
	factor := 10.0
	maxInterval := 1 * time.Second
	backoff := NewExponentialBackoff(interval, factor, maxInterval)

	r.Equal(interval, backoff.Duration)
	r.Equal(factor, backoff.Factor)
	r.Equal(maxInterval, backoff.Cap)
}

func TestRetryCore(t *testing.T) {
	r := require.New(t)

	t.Run("Retrying logic tests", func(t *testing.T) {
		t.Run("Called at least once, even if steps is 0", func(t *testing.T) {
			called := false
			err := retryCore(context.Background(), wait.Backoff{Steps: 0}, func(_ context.Context) error {
				called = true
				return nil
			}, nil)
			r.NoError(err)
			r.True(called)
		})

		t.Run("Respects backoff and retry count", func(t *testing.T) {
			retries := 4
			expectedTotalExecutions := 1 + retries
			backoff := WithRetry(NewExponentialBackoff(10*time.Millisecond, 2, 0), retries)
			// There is no "initial" wait so 0 index simulates zero.
			// The rest are calculated as interval * factor^(ix) without jitter for simplicity
			expectedWaitTimes := []time.Duration{
				time.Millisecond,
				10 * time.Millisecond,
				20 * time.Millisecond,
				40 * time.Millisecond,
				80 * time.Millisecond,
			}
			indexWaitTimes := 0

			actualExecutions := 0
			lastExec := time.Now()
			err := retryCore(context.Background(), backoff, func(_ context.Context) error {
				actualExecutions++
				now := time.Now()
				waitTime := now.Sub(lastExec)
				lastExec = now

				// We give some tolerance as we can't be precise to the nanosecond here
				r.InDelta(expectedWaitTimes[indexWaitTimes], waitTime, float64(2*time.Millisecond))
				indexWaitTimes++

				return errors.New("dummy")
			}, nil)

			r.Error(err)
			r.Equal(expectedTotalExecutions, actualExecutions)
		})

		t.Run("Returns last encountered error", func(t *testing.T) {
			timesCalled := 0
			expectedErrMessage := "boom 3"

			err := retryCore(context.Background(), WithRetry(NewConstantBackoff(10*time.Millisecond), 2),
				func(ctx context.Context) error {
					timesCalled++
					return fmt.Errorf("boom %d", timesCalled)
				}, nil)

			r.Equal(expectedErrMessage, err.Error())
		})

		t.Run("Does not retry instances of NonTransientError", func(t *testing.T) {
			expectedErr := errors.New("dummy")
			called := false
			err := retryCore(context.Background(), WithRetry(NewConstantBackoff(10*time.Millisecond), 10),
				func(ctx context.Context) error {
					r.False(called)
					called = true
					return NewNonTransientError(expectedErr)
				}, nil)

			r.ErrorIs(err, expectedErr)
		})
	})

	t.Run("Notify callback tests", func(t *testing.T) {
		t.Run("Notify is passed and called", func(t *testing.T) {
			err := retryCore(context.Background(), WithRetry(NewConstantBackoff(10*time.Millisecond), 2), func(_ context.Context) error {
				return errors.New("dummy")
			}, func(err error) {
				r.Error(err)
			})
			r.Error(err)
		})

		t.Run("Notify is not passed, no panic", func(t *testing.T) {
			err := retryCore(context.Background(), WithRetry(NewConstantBackoff(10*time.Millisecond), 2),
				func(_ context.Context) error {
					return errors.New("dummy")
				}, nil)
			r.Error(err)
		})
	})

	t.Run("Context tests", func(t *testing.T) {
		t.Run("On context cancel, stops", func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())

			var err error
			done := make(chan bool)
			go func() {
				err = retryCore(ctx, WithRetry(NewConstantBackoff(100*time.Millisecond), 1000), func(ctx context.Context) error {
					return errors.New("dummy")
				}, nil)
				done <- true
			}()

			cancel()
			<-done
			r.ErrorIs(err, context.Canceled, "Expected context cancelled to be propagated")
		})

		t.Run("Operation is called at least once, even if context is cancelled", func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			called := false
			err := retryCore(ctx, WithRetry(NewConstantBackoff(10*time.Millisecond), 1), func(ctx context.Context) error {
				called = true
				return errors.New("dummy")
			}, nil)

			r.ErrorIs(err, context.Canceled)
			r.True(called)
		})
	})
}
