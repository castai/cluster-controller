package actions

import (
	"context"
	"errors"
	"fmt"

	"github.com/sirupsen/logrus"

	"github.com/castai/cluster-controller/internal/castai"
	"github.com/castai/cluster-controller/internal/helm"
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
		return fmt.Errorf("unexpected type %T for upsert uninstall handler", action.Data())
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
		return errors.New("bad request: releaseName not provided")
	}
	if req.Namespace == "" {
		return errors.New("bad request: namespace not provided")
	}
	return nil
}
