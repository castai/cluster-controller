package actions

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"

	"github.com/castai/cluster-controller/internal/castai"
	"github.com/castai/cluster-controller/internal/helm"
	"github.com/castai/cluster-controller/internal/k8s"
)

var _ ActionHandler = &ChartUninstallHandler{}

func NewChartUninstallHandler(log logrus.FieldLogger, helm helm.Client) *ChartUninstallHandler {
	return &ChartUninstallHandler{
		log:  log,
		helm: helm,
	}
}

type ChartUninstallHandler struct {
	log  logrus.FieldLogger
	helm helm.Client
}

func (c *ChartUninstallHandler) Handle(_ context.Context, action *castai.ClusterAction) error {
	req, ok := action.Data().(*castai.ActionChartUninstall)
	if !ok {
		return newUnexpectedTypeErr(action.Data(), req)
	}

	if err := c.validateRequest(req); err != nil {
		return err
	}
	_, err := c.helm.Uninstall(helm.UninstallOptions{
		ReleaseName: req.ReleaseName,
		Namespace:   req.Namespace,
	})
	return err
}

func (c *ChartUninstallHandler) validateRequest(req *castai.ActionChartUninstall) error {
	if req.ReleaseName == "" {
		return fmt.Errorf("release name not provided %w", k8s.ErrAction)
	}
	if req.Namespace == "" {
		return fmt.Errorf("namespace not provided %w", k8s.ErrAction)
	}
	return nil
}
