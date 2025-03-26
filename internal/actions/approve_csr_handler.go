package actions

import (
	"context"

	"github.com/castai/cluster-controller/internal/castai"
)

// // TODO clean up after proper handling unknown actions https://castai.atlassian.net/browse/KUBE-1036.

var _ ActionHandler = &ApproveCSRHandlerDeprecated{}

func NewApproveCSRHandler() *ApproveCSRHandlerDeprecated {
	return &ApproveCSRHandlerDeprecated{}
}

type ApproveCSRHandlerDeprecated struct{}

func (h *ApproveCSRHandlerDeprecated) Handle(_ context.Context, _ *castai.ClusterAction) error {
	return nil
}
