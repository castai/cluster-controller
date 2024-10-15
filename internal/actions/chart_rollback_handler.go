package actions

import (
	"context"
	"errors"
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
		return fmt.Errorf("unexpected type %T for chart rollback handler", action.Data())
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
		return errors.New("bad request: releaseName not provided")
	}
	if req.Namespace == "" {
		return errors.New("bad request: namespace not provided")
	}
	if req.Version == "" {
		return errors.New("bad request: version not provided")
	}
	return nil
}
