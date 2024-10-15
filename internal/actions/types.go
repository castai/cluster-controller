//go:generate mockgen -destination ./mock/handler.go . ActionHandler
package actions

import (
	"context"
	"fmt"

	"github.com/castai/cluster-controller/internal/castai"
)

const (
	// ActionIDLogField is the log field name for action ID.
	// This field is used in backend to detect actions ID in logs.
	ActionIDLogField = "id"
)

func newUnexpectedTypeErr(value, expectedType interface{}) error {
	return fmt.Errorf("unexpected type %T, expected %T", value, expectedType)
}

type ActionHandler interface {
	Handle(ctx context.Context, action *castai.ClusterAction) error
}
