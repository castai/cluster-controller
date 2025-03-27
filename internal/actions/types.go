//go:generate mockgen -destination ./mock/handler.go . ActionHandler
//go:generate mockgen -package=mock_actions -destination ./mock/kubernetes.go k8s.io/client-go/kubernetes Interface

package actions

import (
	"context"
	"errors"
	"fmt"

	"github.com/castai/cluster-controller/internal/castai"
)

const (
	// ActionIDLogField is the log field name for action ID.
	// This field is used in backend to detect actions ID in logs.
	ActionIDLogField = "id"
)

var errAction = errors.New("not valid action")

func newUnexpectedTypeErr(value, expectedType interface{}) error {
	return fmt.Errorf("unexpected type %T, expected %T %w", value, expectedType, errAction)
}

type ActionHandler interface {
	Handle(ctx context.Context, action *castai.ClusterAction) error
}
