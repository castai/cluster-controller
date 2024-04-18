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
		backoff := WithRetry(NewExponentialBackoff(100*time.Millisecond, 2, 0), retries)
		// There is no "initial" wait so 0 index simulates zero.
		// The rest are calculated as interval * factor^(ix) without jitter for simplicity
		expectedWaitTimes := []time.Duration{
			time.Millisecond,
			100 * time.Millisecond,
			200 * time.Millisecond,
			400 * time.Millisecond,
			800 * time.Millisecond,
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
			r.InDelta(expectedWaitTimes[indexWaitTimes], waitTime, float64(5*time.Millisecond))
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

	// Maxinterval?
	// Respects PermanentERror
	// Returns last encountered error

	// Ctx is respected
}

//
//import (
//	"fmt"
//	"testing"
//	"time"
//
//	"github.com/cenkalti/backoff/v4"
//	"k8s.io/apimachinery/pkg/util/wait"
//)
//
//func TestConstantBackoff(t *testing.T) {
//	// Expect to see
//
//	fn := func() error {
//		t.Logf("Current time: %v", time.Now())
//		return fmt.Errorf("test")
//	}
//
//	wfn := wait.ConditionFunc(func() (done bool, err error) {
//		fn()
//		// TODO: Retry here !
//		return false, nil
//	})
//
//	notify := func(err error, d time.Duration) {
//		t.Logf("err: %v, interval: %v", err, d)
//	}
//
//	bf := backoff.WithMaxRetries(backoff.NewConstantBackOff(500*time.Millisecond), 10)
//	err := backoff.RetryNotify(fn, bf, notify)
//	t.Log("Final result for constant backoff is", err)
//
//	w := wait.Backoff{
//		Duration: 500 * time.Millisecond,
//		Steps:    10,
//	}
//	//w.Step()
//	err = wait.ExponentialBackoff(w, wfn)
//	t.Log("Final result for constant wait is", err)
//}
//
//// TODO: Notify
//
//func TestExponentialBackoff(t *testing.T) {
//	fn := func() error {
//		t.Logf("Current time: %v", time.Now())
//		return fmt.Errorf("test")
//	}
//
//	wfn := wait.ConditionFunc(func() (done bool, err error) {
//		fn()
//		// TODO: Retry here !
//		return false, nil
//	})
//
//	bf := backoff.WithMaxRetries(backoff.NewExponentialBackOff(), 10)
//	err := backoff.Retry(fn, bf)
//	t.Log("Final result for exp backoff is", err)
//
//	w := wait.Backoff{
//		Duration: 500 * time.Millisecond,
//		Factor:   1.5,
//		Jitter:   0,
//		Steps:    10,
//		Cap:      60 * time.Second,
//	}
//	t.Log("Final result for exp wait is", wait.ExponentialBackoff(w, wfn))
//}
//
//func TestPrintBackoffs(t *testing.T) {
//	constantBackoff := backoff.WithMaxRetries(backoff.NewConstantBackOff(1*time.Second), 10)
//	t.Logf("Starting constant backoff at %v", time.Now())
//	for i := 1; ; i++ {
//		d := constantBackoff.NextBackOff()
//		if d == backoff.Stop {
//			t.Logf("Stopping backoff constant timer after %d iterations", i)
//			break
//		}
//		t.Logf("Sleeping constant backoff for %v", d)
//	}
//
//	constantW := wait.Backoff{Duration: 1 * time.Second, Steps: 10}
//	t.Logf("Starting constant wait at %v", time.Now())
//	for i := 1; ; i++ {
//		d := constantW.Step()
//		t.Log(constantW)
//		t.Logf("Sleeping constant wait for %v", d)
//		if constantW.Steps <= 0 {
//			t.Logf("Stopping backoff constant timer after %d iterations", i)
//			break
//		}
//	}
//
//	expBackoff := backoff.NewExponentialBackOff()
//	expBackoff.RandomizationFactor = 0
//	exponentBackoff := backoff.WithMaxRetries(expBackoff, 10)
//	t.Logf("Expontential backoff config: %v", exponentBackoff)
//	t.Logf("Starting exp backoff at %v", time.Now())
//	for i := 1; ; i++ {
//		d := exponentBackoff.NextBackOff()
//		if d == backoff.Stop {
//			t.Logf("Stopping backoff expontent timer after %d iterations", i)
//			break
//		}
//		t.Logf("Sleeping exponentBackoff backoff for %v", d)
//	}
//
//	exponentW := wait.Backoff{Duration: 500 * time.Millisecond, Steps: 10, Factor: 1.5, Jitter: 0}
//	t.Logf("Starting exp wait at %v", time.Now())
//	for i := 1; ; i++ {
//		if exponentW.Steps <= 0 {
//			t.Logf("Stopping backoff exp timer after %d iterations", i)
//			break
//		}
//		t.Log(exponentW)
//		d := exponentW.Step()
//		t.Logf("Sleeping exp wait for %v", d)
//	}
//}
//
//// Do things before/after each retry - custom?
//
//// backoff -> returns the inner error once retries are exhausted
//// wait -> returns TimeOut error if retries are exhausted; not the inner ?
//
//// MaxRetries -> handled via Steps in ExpontentialBackoff
////
//
///*
// backoff lib
//randomized interval =
//     RetryInterval * (random value in range [1 - RandomizationFactor, 1 + RandomizationFactor])
//
//MaxElapsedTime => Cap ?
//
//// Pick NEXT iteration from the randomized/jittered interval
//
//if randomizationFactor == 0 {
//		return currentInterval // make sure no randomness is used when randomizationFactor is 0.
//	}
//	var delta = randomizationFactor * float64(currentInterval)
//	var minInterval = float64(currentInterval) - delta
//	var maxInterval = float64(currentInterval) + delta
//
//	// Get a random value from the range [minInterval, maxInterval].
//	// The formula used below has a +1 because if the minInterval is 1 and the maxInterval is 3 then
//	// we want a 33% chance for selecting either 1, 2 or 3.
//	return time.Duration(minInterval + (random * (maxInterval - minInterval + 1)))
//
//// Move the current interval ahead (randomization does not affect here)
//b.currentInterval = time.Duration(float64(b.currentInterval) * b.Multiplier)
//*/
//
///*
//	wait library
//
//
//	Jitter -> interval + interval*maxFactor
//duration + time.Duration(rand.Float64()*maxFactor*float64(duration))
//*/
//
//// sliding + immediate
