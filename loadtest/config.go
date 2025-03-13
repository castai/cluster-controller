package loadtest

import (
	"time"
)

// Config for the HTTP server.
type Config struct {
	Port int
}

// TestServerConfig has settings for the mock server instance.
type TestServerConfig struct {
	// MaxActionsPerCall is the upper limit of actions to return in one CastAITestServer.GetActions call.
	MaxActionsPerCall int
	// TimeoutWaitingForActions controls how long to wait for at least 1 action to appear on server side.
	// This mimics CH behavior of not returning early if there are no pending actions and keeping the request "running".
	TimeoutWaitingForActions time.Duration
	// BufferSize controls the input channel size.
	BufferSize int
}
