package actions

import (
	"context"
	"fmt"
	"github.com/castai/cluster-controller/internal/types"
)

const (
	// ActionIDLogField is the log field name for action ID.
	// This field is used in backend to detect actions ID in logs.
	ActionIDLogField = "id"
)

func newUnexpectedTypeErr(value interface{}, expectedType interface{}) error {
	return fmt.Errorf("unexpected type %T, expected %T", value, expectedType)
}

type ActionHandler interface {
	Handle(ctx context.Context, action *types.ClusterAction) error
}
