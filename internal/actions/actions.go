package actions

import (
	"reflect"

	"github.com/sirupsen/logrus"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"github.com/castai/cluster-controller/internal/castai"
	"github.com/castai/cluster-controller/internal/helm"
	"github.com/castai/cluster-controller/internal/informer"
	"github.com/castai/cluster-controller/internal/k8s"
	"github.com/castai/cluster-controller/internal/nodes"
	"github.com/castai/cluster-controller/internal/volume"
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
	podInformer cache.SharedIndexInformer,
	vaWaiter volume.DetachmentWaiter,
) ActionHandlers {
	handlers := ActionHandlers{
		reflect.TypeFor[*castai.ActionDeleteNode]():        NewDeleteNodeHandler(log, clientset),
		reflect.TypeFor[*castai.ActionDrainNode]():         NewDrainNodeHandler(log, clientset, castNamespace, vaWaiter),
		reflect.TypeFor[*castai.ActionPatchNode]():         NewPatchNodeHandler(log, clientset),
		reflect.TypeFor[*castai.ActionCreateEvent]():       NewCreateEventHandler(log, clientset),
		reflect.TypeFor[*castai.ActionChartUpsert]():       NewChartUpsertHandler(log, helmClient),
		reflect.TypeFor[*castai.ActionChartUninstall]():    NewChartUninstallHandler(log, helmClient),
		reflect.TypeFor[*castai.ActionChartRollback]():     NewChartRollbackHandler(log, helmClient, k8sVersion),
		reflect.TypeFor[*castai.ActionDisconnectCluster](): NewDisconnectClusterHandler(log, clientset),
		reflect.TypeFor[*castai.ActionCheckNodeDeleted]():  NewCheckNodeDeletedHandler(log, clientset),
		reflect.TypeFor[*castai.ActionCheckNodeStatus]():   NewCheckNodeStatusHandler(log, clientset),
		reflect.TypeFor[*castai.ActionEvictPod]():          NewEvictPodHandler(log, clientset),
		reflect.TypeFor[*castai.ActionPatch]():             NewPatchHandler(log, dynamicClient),
		reflect.TypeFor[*castai.ActionCreate]():            NewCreateHandler(log, dynamicClient),
		reflect.TypeFor[*castai.ActionDelete]():            NewDeleteHandler(log, dynamicClient),
	}

	if nodeInformer != nil {
		handlers[reflect.TypeFor[*castai.ActionCheckNodeStatus]()] = NewCheckNodeStatusInformerHandler(log, clientset, nodeInformer)
	}

	if podInformer != nil {
		nodeManager := nodes.NewManager(podInformer.GetIndexer(), k8s.NewClient(clientset, log), log, nodes.ManagerConfig{})
		handlers[reflect.TypeFor[*castai.ActionDrainNode]()] = NewDrainNodeInformerHandler(log, clientset, castNamespace, vaWaiter, nodeManager)
	}

	return handlers
}

func (h ActionHandlers) Close() error {
	return h[reflect.TypeFor[*castai.ActionCreateEvent]()].(*CreateEventHandler).Close()
}

// ShouldWaitForVolumeDetach returns whether to wait for VolumeAttachments based on per-action config.
// Returns true only if explicitly enabled via action field; defaults to false (disabled).
func ShouldWaitForVolumeDetach(req *castai.ActionDrainNode) bool {
	if req.WaitForVolumeDetach != nil {
		return *req.WaitForVolumeDetach
	}
	return false
}
