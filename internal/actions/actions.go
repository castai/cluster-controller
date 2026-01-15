package actions

import (
	"reflect"

	"github.com/sirupsen/logrus"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/internal/castai"
	"github.com/castai/cluster-controller/internal/helm"
	"github.com/castai/cluster-controller/internal/informer"
)

type ActionHandlers map[reflect.Type]ActionHandler

func NewDefaultActionHandlers(
	k8sVersion string,
	castNamespace string,
	log logrus.FieldLogger,
	clientset *kubernetes.Clientset,
	dynamicClient dynamic.Interface,
	helmClient helm.Client,
	nodeInformer informer.NodeInformer,
) ActionHandlers {
	return ActionHandlers{
		reflect.TypeFor[*castai.ActionDeleteNode]():        NewDeleteNodeHandler(log, clientset),
		reflect.TypeFor[*castai.ActionDrainNode]():         NewDrainNodeHandler(log, clientset, castNamespace),
		reflect.TypeFor[*castai.ActionPatchNode]():         NewPatchNodeHandler(log, clientset),
		reflect.TypeFor[*castai.ActionCreateEvent]():       NewCreateEventHandler(log, clientset),
		reflect.TypeFor[*castai.ActionChartUpsert]():       NewChartUpsertHandler(log, helmClient),
		reflect.TypeFor[*castai.ActionChartUninstall]():    NewChartUninstallHandler(log, helmClient),
		reflect.TypeFor[*castai.ActionChartRollback]():     NewChartRollbackHandler(log, helmClient, k8sVersion),
		reflect.TypeFor[*castai.ActionDisconnectCluster](): NewDisconnectClusterHandler(log, clientset),
		reflect.TypeFor[*castai.ActionCheckNodeDeleted]():  NewCheckNodeDeletedHandler(log, clientset),
		reflect.TypeFor[*castai.ActionCheckNodeStatus]():   NewCheckNodeStatusHandler(log, clientset, nodeInformer),
		reflect.TypeFor[*castai.ActionEvictPod]():          NewEvictPodHandler(log, clientset),
		reflect.TypeFor[*castai.ActionPatch]():             NewPatchHandler(log, dynamicClient),
		reflect.TypeFor[*castai.ActionCreate]():            NewCreateHandler(log, dynamicClient),
		reflect.TypeFor[*castai.ActionDelete]():            NewDeleteHandler(log, dynamicClient),
	}
}

func (h ActionHandlers) Close() error {
	return h[reflect.TypeFor[*castai.ActionCreateEvent]()].(*CreateEventHandler).Close()
}
