package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apitypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/waitext"
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
				// permissions might be of older version that can't patch node/status
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

func getNodeForPatching(ctx context.Context, log logrus.FieldLogger, clientset kubernetes.Interface, nodeName string) (*v1.Node, error) {
	// on GKE we noticed that sometimes the node is not found, even though it is in the cluster
	// as a result was returned from watch. But subsequent get request returns not found.
	// This is likely due to clientset's caching that's meant to alleviate API's load.
	// So we give enough time for cache to sync - ~10s max.

	var node *v1.Node

	boff := waitext.DefaultExponentialBackoff()

	err := waitext.Retry(
		ctx,
		boff,
		5,
		func(ctx context.Context) (bool, error) {
			var err error
			node, err = clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
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

func defaultBackoff() wait.Backoff {
	return waitext.NewConstantBackoff(500 * time.Millisecond)
}
