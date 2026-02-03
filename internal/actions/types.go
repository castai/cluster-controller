//go:generate mockgen -destination ./mock/handler.go . ActionHandler
//go:generate mockgen -package=mock_actions -destination ./mock/kubernetes.go k8s.io/client-go/kubernetes Interface
//go:generate mockgen -package=mock_actions -destination ./mock/corev1.go k8s.io/client-go/kubernetes/typed/core/v1 NodeInterface

package actions

import (
	"context"
	"fmt"

	"github.com/castai/cluster-controller/internal/castai"
	"github.com/castai/cluster-controller/internal/k8s"
)

const (
	// ActionIDLogField is the log field name for action ID.
	// This field is used in backend to detect actions ID in logs.
	ActionIDLogField = "id"
)

func newUnexpectedTypeErr(value, expectedType any) error {
	return fmt.Errorf("unexpected type %T, expected %T %w", value, expectedType, k8s.ErrAction)
}

type ActionHandler interface {
	Handle(ctx context.Context, action *castai.ClusterAction) error
}
