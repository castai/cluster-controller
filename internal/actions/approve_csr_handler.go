package actions

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/internal/castai"
)

const (
	approveCSRTimeout = 4 * time.Minute
)

var _ ActionHandler = &ApproveCSRHandler{}

func NewApproveCSRHandler(log logrus.FieldLogger, clientset kubernetes.Interface) *ApproveCSRHandler {
	return &ApproveCSRHandler{
		log:                    log,
		clientset:              clientset,
		initialCSRFetchTimeout: 5 * time.Minute,
		csrFetchInterval:       5 * time.Second,
	}
}

type ApproveCSRHandler struct {
	log                    logrus.FieldLogger
	clientset              kubernetes.Interface
	initialCSRFetchTimeout time.Duration
	csrFetchInterval       time.Duration
}

func (h *ApproveCSRHandler) Handle(_ context.Context, _ *castai.ClusterAction) error {
	return nil
}
