package actions

import (
	"reflect"

	"github.com/sirupsen/logrus"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/internal/castai"
	"github.com/castai/cluster-controller/internal/helm"
)

type ActionHandlers map[reflect.Type]ActionHandler

func NewDefaultActionHandlers(
	k8sVersion string,
	castNamespace string,
	log logrus.FieldLogger,
	clientset *kubernetes.Clientset,
	dynamicClient dynamic.Interface,
	helmClient helm.Client,
) ActionHandlers {
	return ActionHandlers{
		reflect.TypeOf(&castai.ActionDeleteNode{}):        NewDeleteNodeHandler(log, clientset),
		reflect.TypeOf(&castai.ActionDrainNode{}):         NewDrainNodeHandler(log, clientset, castNamespace),
		reflect.TypeOf(&castai.ActionPatchNode{}):         NewPatchNodeHandler(log, clientset),
		reflect.TypeOf(&castai.ActionCreateEvent{}):       NewCreateEventHandler(log, clientset),
		reflect.TypeOf(&castai.ActionChartUpsert{}):       NewChartUpsertHandler(log, helmClient),
		reflect.TypeOf(&castai.ActionChartUninstall{}):    NewChartUninstallHandler(log, helmClient),
		reflect.TypeOf(&castai.ActionChartRollback{}):     NewChartRollbackHandler(log, helmClient, k8sVersion),
		reflect.TypeOf(&castai.ActionDisconnectCluster{}): NewDisconnectClusterHandler(log, clientset),
		reflect.TypeOf(&castai.ActionCheckNodeDeleted{}):  NewCheckNodeDeletedHandler(log, clientset),
		reflect.TypeOf(&castai.ActionCheckNodeStatus{}):   NewCheckNodeStatusHandler(log, clientset),
		reflect.TypeOf(&castai.ActionEvictPod{}):          NewEvictPodHandler(log, clientset),
		reflect.TypeOf(&castai.ActionPatch{}):             NewPatchHandler(log, dynamicClient),
		reflect.TypeOf(&castai.ActionCreate{}):            NewCreateHandler(log, dynamicClient),
		reflect.TypeOf(&castai.ActionDelete{}):            NewDeleteHandler(log, dynamicClient),
	}
}

func (h ActionHandlers) Close() error {
	return h[reflect.TypeOf(&castai.ActionCreateEvent{})].(*CreateEventHandler).Close()
}
