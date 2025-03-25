package actions

import (
	"context"

	"github.com/castai/cluster-controller/internal/castai"
)

// TODO deprecated action

var _ ActionHandler = &ApproveCSRHandler{}

func NewApproveCSRHandler() *ApproveCSRHandler {
	return &ApproveCSRHandler{}
}

type ApproveCSRHandler struct{}

func (h *ApproveCSRHandler) Handle(_ context.Context, _ *castai.ClusterAction) error {
	return nil
}
