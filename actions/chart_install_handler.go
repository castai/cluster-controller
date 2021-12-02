package actions

import (
	"context"

	"github.com/sirupsen/logrus"

	"github.com/castai/cluster-controller/helm"
)

func newInstallChartHandler(log logrus.FieldLogger, helm helm.Client) ActionHandler {
	return &installChartHandler{
		log:  log,
		helm: helm,
	}
}

type installChartHandler struct {
	log  logrus.FieldLogger
	helm helm.Client
}

func (i installChartHandler) Handle(ctx context.Context, data interface{}) error {
	panic("implement me")
}
