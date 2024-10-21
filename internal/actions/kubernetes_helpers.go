package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/castai/cluster-controller/internal/k8sclient"
	"github.com/castai/cluster-controller/internal/waitext"
	"github.com/samber/lo"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	defaultMaxRetriesK8SOperation = 5
)

func patchNode(ctx context.Context, log logrus.FieldLogger, clientset k8sclient.ClientSet, node *v1.Node, changeFn func(*v1.Node)) error {
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
			err = clientset.PatchNode(ctx, node.Name, patch)
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

func patchNodeStatus(ctx context.Context, log logrus.FieldLogger, clientset k8sclient.ClientSet, name string, patch []byte) error {
	err := waitext.Retry(
		ctx,
		defaultBackoff(),
		defaultMaxRetriesK8SOperation,
		func(ctx context.Context) (bool, error) {
			err := clientset.PatchNodeStatus(ctx, name, patch)
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

func getNodeForPatching(ctx context.Context, log logrus.FieldLogger, clientset k8sclient.ClientSet, nodeName string) (*v1.Node, error) {
	// on GKE we noticed that sometimes the node is not found, even though it is in the cluster
	// as a result was returned from watch. But subsequent get request returns not found.
	// This is likely due to clientset's caching that's meant to alleviate API's load.
	// So we give enough time for cache to sync - ~10s max.

	var node *v1.Node

	boff := waitext.DefaultExponentialBackoff()
	boff.Duration = 5 * time.Second

	err := waitext.Retry(
		ctx,
		boff,
		5,
		func(ctx context.Context) (bool, error) {
			var err error
			node, err = clientset.GetNode(nodeName)
			if err != nil {
				return true, err
			}
			return false, nil
		},
		func(err error) {
			log.Warnf("getting node, will retry: %v", err)
		},
	)
	if err != nil {
		return nil, err
	}
	return node, nil
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
		parallelTasks      = int(lo.Clamp(float64(len(pods)), 30, 10))
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
