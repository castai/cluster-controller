package loadtest

import (
	"reflect"
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

type ActionLoadTestConfig struct {
	//  TotalActions is the overall amount of actions of this type that will be created.
	TotalActions int
	// ActionsPerCall controls how many actions of the type to return on a given call.
	// In the real world, this parameter is in cluster-hub side to determine how many max actions to return.
	ActionsPerCall int

	ActionType reflect.Type
}
