package actions

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"

	"github.com/castai/cluster-controller/internal/castai"
	"github.com/castai/cluster-controller/internal/helm"
)

var _ ActionHandler = &ChartRollbackHandler{}

func NewChartRollbackHandler(log logrus.FieldLogger, helm helm.Client, version string) *ChartRollbackHandler {
	return &ChartRollbackHandler{
		log:     log,
		helm:    helm,
		version: version,
	}
}

type ChartRollbackHandler struct {
	log     logrus.FieldLogger
	helm    helm.Client
	version string
}

func (c *ChartRollbackHandler) Handle(_ context.Context, action *castai.ClusterAction) error {
	req, ok := action.Data().(*castai.ActionChartRollback)
	if !ok {
		return newUnexpectedTypeErr(action.Data(), req)
	}

	if err := c.validateRequest(req); err != nil {
		return err
	}

	// Rollback only from requested version.
	if req.Version != c.version {
		return nil
	}

	return c.helm.Rollback(helm.RollbackOptions{
		ReleaseName: req.ReleaseName,
		Namespace:   req.Namespace,
	})
}

func (c *ChartRollbackHandler) validateRequest(req *castai.ActionChartRollback) error {
	if req.ReleaseName == "" {
		return fmt.Errorf("release name not provided %w", errAction)
	}
	if req.Namespace == "" {
		return fmt.Errorf("namespace not provided %w", errAction)
	}
	if req.Version == "" {
		return fmt.Errorf("version not provided %w", errAction)
	}
	return nil
}
