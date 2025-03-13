package scenarios

import (
	"context"
	"time"
)

func WaitUntil(ctx context.Context, duration time.Duration, condition func() bool) bool {
	start := time.Now()
	for {
		select {
		case <-ctx.Done():
			return false
		default:
		}
		if time.Now().Sub(start) > duration {
			return false
		}
		if condition() {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
}
