package loadtest

import (
	"reflect"
)

type Config struct {
	Port int
}

type ActionLoadTestConfig struct {
	//  TotalActions is the overall amount of actions of this type that will be created.
	TotalActions int
	// ActionsPerCall controls how many actions of the type to return on a given call.
	// In the real world, this parameter is in cluster-hub side to determine how many max actions to return.
	ActionsPerCall int

	ActionType reflect.Type
}
