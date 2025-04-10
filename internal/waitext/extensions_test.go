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

func TestDefaultExponentialBackoff(t *testing.T) {
	r := require.New(t)

	val := DefaultExponentialBackoff()

	r.Equal(defaultInitialInterval, val.Duration)
	r.Equal(defaultMultiplier, val.Factor)
	r.Equal(defaultRandomizationFactor, val.Jitter)
	r.Equal(defaultMaxInterval, val.Cap)
}

func TestRetry(t *testing.T) {
	r := require.New(t)

	t.Run("Retrying logic tests", func(t *testing.T) {
		t.Run("Called at least once, even if retries or steps is 0", func(t *testing.T) {
			called := false
			err := Retry(context.Background(), wait.Backoff{Steps: 0}, 0, func(_ context.Context) (bool, error) {
				called = true
				return false, nil
			}, nil)

			r.NoError(err)
			r.True(called)
		})

		t.Run("Respects backoff and retry count", func(t *testing.T) {
			retries := 4
			expectedTotalExecutions := 1 + retries
			backoff := DefaultExponentialBackoff()
			backoff.Duration = 10 * time.Millisecond
			backoff.Factor = 2
			backoff.Jitter = 0

			// There is no "initial" wait so 0 index simulates zero.
			// The rest are calculated as interval * factor^(ix) without jitter for simplicity.
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
			err := Retry(context.Background(), backoff, retries, func(_ context.Context) (bool, error) {
				actualExecutions++
				now := time.Now()
				waitTime := now.Sub(lastExec)
				lastExec = now

				t.Log("wait time", waitTime)

				// We give some tolerance as we can't be precise to the nanosecond here.
				r.InDelta(expectedWaitTimes[indexWaitTimes], waitTime, float64(2*time.Millisecond))
				indexWaitTimes++

				return true, errors.New("dummy")
			}, nil)

			r.Error(err)
			r.Equal(expectedTotalExecutions, actualExecutions)
		})

		t.Run("Returns last encountered error", func(t *testing.T) {
			timesCalled := 0
			expectedErrMessage := "boom 3"

			err := Retry(context.Background(), NewConstantBackoff(10*time.Millisecond), 2,
				func(ctx context.Context) (bool, error) {
					timesCalled++
					return true, fmt.Errorf("boom %d", timesCalled)
				}, nil)

			r.Equal(expectedErrMessage, err.Error())
		})

		t.Run("Does not retry if false is returned as first parameter", func(t *testing.T) {
			expectedErr := errors.New("dummy")
			called := false
			err := Retry(context.Background(), NewConstantBackoff(10*time.Millisecond), 10,
				func(ctx context.Context) (bool, error) {
					r.False(called)
					called = true
					return false, expectedErr
				}, nil)

			r.ErrorIs(err, expectedErr)
		})
	})

	t.Run("Notify callback tests", func(t *testing.T) {
		t.Run("Notify is passed and called", func(t *testing.T) {
			err := Retry(
				context.Background(),
				NewConstantBackoff(10*time.Millisecond),
				2,
				func(_ context.Context) (bool, error) {
					return true, errors.New("dummy")
				},
				func(err error) {
					r.Error(err)
				},
			)
			r.Error(err)
		})

		t.Run("Notify is not passed, no panic", func(t *testing.T) {
			err := Retry(
				context.Background(),
				NewConstantBackoff(10*time.Millisecond),
				2,
				func(_ context.Context) (bool, error) {
					return true, errors.New("dummy")
				},
				nil,
			)
			r.Error(err)
		})
	})

	t.Run("Context tests", func(t *testing.T) {
		t.Run("On context cancel, stops", func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(context.Background())

			innerError := errors.New("from operation")
			cancelCause := errors.New("cancel cause err")
			var overallReturnedErr error

			done := make(chan bool)
			go func() {
				overallReturnedErr = Retry(ctx, NewConstantBackoff(100*time.Millisecond), 1000, func(ctx context.Context) (bool, error) {
					return true, innerError
				}, nil)
				done <- true
			}()

			cancel(cancelCause)
			<-done
			r.ErrorIs(overallReturnedErr, context.Canceled, "Expected context cancelled to be propagated")
			r.ErrorIs(overallReturnedErr, innerError, "Expected inner error by operation be propagated")
			r.ErrorIs(overallReturnedErr, cancelCause, "Expected cancel cause error to be propagated")
		})

		t.Run("Operation is called at least once, even if context is cancelled", func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			called := false
			err := Retry(ctx, NewConstantBackoff(10*time.Millisecond), 1, func(ctx context.Context) (bool, error) {
				called = true
				return true, errors.New("dummy")
			}, nil)

			r.ErrorIs(err, context.Canceled)
			r.True(called)
		})
	})
}
