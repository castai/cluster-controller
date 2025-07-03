package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/samber/lo"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apitypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"

	"github.com/castai/cluster-controller/internal/castai"
	"github.com/castai/cluster-controller/internal/waitext"
)

const (
	defaultMaxRetriesK8SOperation = 5
)

func patchNode(ctx context.Context, log logrus.FieldLogger, clientset kubernetes.Interface, node *v1.Node, changeFn func(*v1.Node)) error {
	oldData, err := json.Marshal(node)
	if err != nil {
		return fmt.Errorf("marshaling old data: %w", err)
	}

	changeFn(node)

	newData, err := json.Marshal(node)
	if err != nil {
		return fmt.Errorf("marshaling new data: %w", err)
	}

	patch, err := strategicpatch.CreateTwoWayMergePatch(oldData, newData, node)
	if err != nil {
		return fmt.Errorf("creating patch for node: %w", err)
	}

	err = waitext.Retry(
		ctx,
		defaultBackoff(),
		defaultMaxRetriesK8SOperation,
		func(ctx context.Context) (bool, error) {
			_, err = clientset.CoreV1().Nodes().Patch(ctx, node.Name, apitypes.StrategicMergePatchType, patch, metav1.PatchOptions{})
			return true, err
		},
		func(err error) {
			log.Warnf("patch node, will retry: %v", err)
		},
	)
	if err != nil {
		return fmt.Errorf("patching node: %w", err)
	}

	return nil
}

func patchNodeStatus(ctx context.Context, log logrus.FieldLogger, clientset kubernetes.Interface, name string, patch []byte) error {
	err := waitext.Retry(
		ctx,
		defaultBackoff(),
		defaultMaxRetriesK8SOperation,
		func(ctx context.Context) (bool, error) {
			_, err := clientset.CoreV1().Nodes().PatchStatus(ctx, name, patch)
			if k8serrors.IsForbidden(err) {
				// permissions might be of older version that can't patch node/status.
				log.WithField("node", name).WithError(err).Warn("skip patch node/status")
				return false, nil
			}
			return true, err
		},
		func(err error) {
			log.Warnf("patch node status, will retry: %v", err)
		},
	)
	if err != nil {
		return fmt.Errorf("patch status: %w", err)
	}
	return nil
}

func getNodeByIDs(ctx context.Context, clientSet corev1.NodeInterface, nodeName, nodeID, providerID string) (*v1.Node, error) {
	if nodeID == "" && providerID == "" {
		return nil, fmt.Errorf("node and provider IDs are empty %w", errAction)
	}

	n, err := clientSet.Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil && k8serrors.IsNotFound(err) {
		return nil, errNodeNotFound
	}
	if err != nil {
		return nil, err
	}

	if n == nil {
		return nil, errNodeNotFound
	}

	if err := isNodeIDProviderIDValid(n, nodeID, providerID); err != nil {
		return nil, fmt.Errorf("requested node ID %s, provider ID %s for node name: %s %w",
			nodeID, providerID, n.Name, err)
	}

	return n, nil
}

var errNodeNotValid = fmt.Errorf("node does not match")

func isNodeIDProviderIDValid(node *v1.Node, nodeID, providerID string) error {
	var currentNodeID string
	if nodeID != "" {
		if currentNodeID, ok := node.Labels[castai.LabelNodeID]; ok {
			if currentNodeID == nodeID {
				return nil
			}
		}
	}

	if providerID != "" && node.Spec.ProviderID != "" && node.Spec.ProviderID == providerID {
		return nil
	}

	return fmt.Errorf("node %v has ID %s and provider ID %s %w", node.Name, currentNodeID, node.Spec.ProviderID, errNodeNotValid)
}

// executeBatchPodActions executes the action for each pod in the list.
// It does internal throttling to avoid spawning a goroutine-per-pod on large lists.
// Returns two sets of pods - the ones that successfully executed the action and the ones that failed.
// actionName might be used to distinguish what is the operation (for logs, debugging, etc.) but is optional.
func executeBatchPodActions(
	ctx context.Context,
	log logrus.FieldLogger,
	pods []v1.Pod,
	action func(context.Context, v1.Pod) error,
	actionName string,
) (successfulPods []*v1.Pod, failedPods []podActionFailure) {
	if actionName == "" {
		actionName = "unspecified"
	}
	log = log.WithField("actionName", actionName)

	if len(pods) == 0 {
		log.Debug("empty list of pods to execute action against")
		return []*v1.Pod{}, nil
	}

	var (
		parallelTasks      = lo.Clamp(len(pods), 1, 50)
		taskChan           = make(chan v1.Pod, len(pods))
		successfulPodsChan = make(chan *v1.Pod, len(pods))
		failedPodsChan     = make(chan podActionFailure, len(pods))
		wg                 sync.WaitGroup
	)

	log.Debugf("Starting %d parallel tasks for %d pods: [%v]", parallelTasks, len(pods), lo.Map(pods, func(t v1.Pod, i int) string {
		return fmt.Sprintf("%s/%s", t.Namespace, t.Name)
	}))

	worker := func(taskChan <-chan v1.Pod) {
		for pod := range taskChan {
			if err := action(ctx, pod); err != nil {
				failedPodsChan <- podActionFailure{
					actionName: actionName,
					pod:        &pod,
					err:        err,
				}
			} else {
				successfulPodsChan <- &pod
			}
		}
		wg.Done()
	}

	for range parallelTasks {
		wg.Add(1)
		go worker(taskChan)
	}

	for _, pod := range pods {
		taskChan <- pod
	}

	close(taskChan)
	wg.Wait()
	close(failedPodsChan)
	close(successfulPodsChan)

	for pod := range successfulPodsChan {
		successfulPods = append(successfulPods, pod)
	}

	for failure := range failedPodsChan {
		failedPods = append(failedPods, failure)
	}

	return
}

func defaultBackoff() wait.Backoff {
	return waitext.NewConstantBackoff(500 * time.Millisecond)
}

type podActionFailure struct {
	actionName string
	pod        *v1.Pod
	err        error
}
