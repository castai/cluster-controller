package actions

import (
	"context"
	"errors"
	"fmt"

	"github.com/sirupsen/logrus"

	"github.com/castai/cluster-controller/actions/types"
	"github.com/castai/cluster-controller/helm"
)

func newChartUninstallHandler(log logrus.FieldLogger, helm HelmClient) ActionHandler {
	return &chartUninstallHandler{
		log:  log,
		helm: helm,
	}
}

type chartUninstallHandler struct {
	log  logrus.FieldLogger
	helm HelmClient
}

func (c *chartUninstallHandler) Handle(_ context.Context, action *types.ClusterAction) error {
	req, ok := action.Data().(*types.ActionChartUninstall)
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

func (c *chartUninstallHandler) validateRequest(req *types.ActionChartUninstall) error {
	if req.ReleaseName == "" {
		return errors.New("bad request: releaseName not provided")
	}
	if req.Namespace == "" {
		return errors.New("bad request: namespace not provided")
	}
	return nil
}
