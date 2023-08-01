package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/cenkalti/backoff/v4"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apitypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/client-go/kubernetes"
)

func patchNode(ctx context.Context, clientset kubernetes.Interface, node *v1.Node, changeFn func(*v1.Node) error) error {
	return patchObject[v1.Node](
		ctx, node, changeFn,
		func(ctx context.Context, patchType apitypes.PatchType, data []byte, options metav1.PatchOptions) error {
			_, err := clientset.CoreV1().Nodes().Patch(ctx, node.Name, patchType, data, options)
			return err
		},
	)
}

func patchObject[T any](
	ctx context.Context,
	object *T,
	modify func(*T) error,
	patch func(ctx context.Context, patchType apitypes.PatchType, data []byte, options metav1.PatchOptions) error,
) error {
	oldData, err := json.Marshal(object)
	if err != nil {
		return fmt.Errorf("marshaling old data: %w", err)
	}

	if err := modify(object); err != nil {
		return fmt.Errorf("modifying object: %w", err)
	}

	newData, err := json.Marshal(object)
	if err != nil {
		return fmt.Errorf("marshaling new data: %w", err)
	}

	patchData, err := strategicpatch.CreateTwoWayMergePatch(oldData, newData, object)
	if err != nil {
		return fmt.Errorf("creating patch for %T: %w", reflect.TypeOf(object), err)
	}

	err = backoff.Retry(func() error {
		return patch(ctx, apitypes.StrategicMergePatchType, patchData, metav1.PatchOptions{})
	}, defaultBackoff(ctx))
	if err != nil {
		return fmt.Errorf("patching %T: %w", reflect.TypeOf(object), err)
	}

	return nil
}

func defaultBackoff(ctx context.Context) backoff.BackOffContext {
	return backoff.WithContext(backoff.WithMaxRetries(backoff.NewConstantBackOff(500*time.Millisecond), 5), ctx) // nolint:gomnd
}
