package actions

import (
	"context"
	"errors"
	"fmt"

	"github.com/sirupsen/logrus"

	"github.com/castai/cluster-controller/castai"
	"github.com/castai/cluster-controller/helm"
)

func newChartRollbackHandler(log logrus.FieldLogger, helm helm.Client) ActionHandler {
	return &chartRollbackHandler{
		log:  log,
		helm: helm,
	}
}

type chartRollbackHandler struct {
	log  logrus.FieldLogger
	helm helm.Client
}

func (c *chartRollbackHandler) Handle(_ context.Context, data interface{}) error {
	req, ok := data.(*castai.ActionChartRollback)
	if !ok {
		return fmt.Errorf("unexpected type %T for chart rollback handler", data)
	}

	if err := c.validateRequest(req); err != nil {
		return err
	}

	return c.helm.Rollback(helm.RollbackOptions{
		ReleaseName: req.ReleaseName,
		Namespace:   req.Namespace,
	})
}

func (c *chartRollbackHandler) validateRequest(req *castai.ActionChartRollback) error {
	if req.ReleaseName == "" {
		return errors.New("bad request: releaseName not provided")
	}
	if req.Namespace == "" {
		return errors.New("bad request: namespace not provided")
	}
	return nil
}
