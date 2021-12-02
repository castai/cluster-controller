package actions

import (
	"github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"
)

func newInstallChartHandler(log logrus.FieldLogger, clientset kubernetes.Interface) ActionHandler {
	return &installChartHandler{
		log:       log,
		clientset: clientset,
	}
}

type installChartHandler struct {
	log       logrus.FieldLogger
	clientset kubernetes.Interface
}
