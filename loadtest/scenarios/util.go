package scenarios

import (
	"context"
	"time"
)

func WaitUntil(ctx context.Context, duration time.Duration, condition func(ctx context.Context) bool) bool {
	start := time.Now()
	for {
		select {
		case <-ctx.Done():
			return false
		default:
		}
		if time.Since(start) > duration {
			return false
		}
		if condition(ctx) {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
}
