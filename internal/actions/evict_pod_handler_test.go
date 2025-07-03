package actions

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktest "k8s.io/client-go/testing"

	"github.com/castai/cluster-controller/internal/castai"
)

func TestEvictPodHandler(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	t.Run("evict successfully", func(t *testing.T) {
		t.Parallel()

		clientset, pod := setupFakeClientWithPodEviction()

		reaction := normalPodEvictionReaction(t, ctx, clientset, 1*time.Second) // Delay is used to verify action handler returns after pod is deleted.
		prependPodEvictionReaction(clientset, reaction)

		h := NewEvictPodHandler(
			log,
			clientset,
		)

		action := newEvictPodAction(&castai.ActionEvictPod{
			Namespace: pod.Namespace,
			PodName:   pod.Name,
		})
		err := h.Handle(ctx, action)
		require.NoError(t, err)

		requirePodNotExists(t, ctx, clientset, pod.Namespace, pod.Name)
	})

	t.Run("retries on TooManyRequests status", func(t *testing.T) {
		t.Parallel()

		clientset, pod := setupFakeClientWithPodEviction()

		reactionErr := &apierrors.StatusError{ErrStatus: metav1.Status{Reason: metav1.StatusReasonTooManyRequests}}
		reaction := failingPodEvictionReaction(reactionErr, 2, normalPodEvictionReaction(t, ctx, clientset, 0))
		prependPodEvictionReaction(clientset, reaction)

		h := &EvictPodHandler{
			log:                           log,
			clientset:                     clientset,
			podEvictRetryDelay:            5 * time.Millisecond,
			podsTerminationWaitRetryDelay: 10 * time.Millisecond,
		}

		action := newEvictPodAction(&castai.ActionEvictPod{
			Namespace: pod.Namespace,
			PodName:   pod.Name,
		})
		err := h.Handle(ctx, action)
		require.NoError(t, err)

		requirePodNotExists(t, ctx, clientset, pod.Namespace, pod.Name)
	})

	t.Run("fails immediately on InternalError status", func(t *testing.T) {
		t.Parallel()

		clientset, pod := setupFakeClientWithPodEviction()

		reactionErr := &apierrors.StatusError{ErrStatus: metav1.Status{Reason: metav1.StatusReasonInternalError}}
		reaction := failingPodEvictionReaction(reactionErr, 1, nil)
		prependPodEvictionReaction(clientset, reaction)

		h := NewEvictPodHandler(
			log,
			clientset,
		)

		action := newEvictPodAction(&castai.ActionEvictPod{
			Namespace: pod.Namespace,
			PodName:   pod.Name,
		})
		err := h.Handle(ctx, action)
		require.Error(t, err)
	})

	t.Run("evict successfully", func(t *testing.T) {
		t.Parallel()

		clientset, pod := setupFakeClientWithPodEviction()
		prependPodEvictionReaction(clientset, normalPodEvictionReaction(t, ctx, clientset, 1*time.Second)) // Delay is used to verify action handler returns after pod is deleted.

		h := NewEvictPodHandler(
			log,
			clientset,
		)

		action := newEvictPodAction(&castai.ActionEvictPod{
			Namespace: pod.Namespace,
			PodName:   pod.Name,
		})
		err := h.Handle(ctx, action)
		require.NoError(t, err)

		requirePodNotExists(t, ctx, clientset, pod.Namespace, pod.Name)
	})

	t.Run("ignores non-existing pod", func(t *testing.T) {
		t.Parallel()

		clientset, pod := setupFakeClientWithPodEviction()
		givenPodIsDeleted(t, ctx, clientset, pod.Namespace, pod.Name)

		h := NewEvictPodHandler(
			log,
			clientset,
		)

		action := newEvictPodAction(&castai.ActionEvictPod{
			Namespace: pod.Namespace,
			PodName:   pod.Name,
		})
		err := h.Handle(ctx, action)
		require.NoError(t, err)
	})
}

func newEvictPodAction(data *castai.ActionEvictPod) *castai.ClusterAction {
	return &castai.ClusterAction{
		ID:             uuid.NewString(),
		ActionEvictPod: data,
		CreatedAt:      time.Now().UTC(),
	}
}

func normalPodEvictionReaction(t *testing.T, ctx context.Context, clientset *fake.Clientset, delay time.Duration) func(string, string) error {
	return func(namespace, name string) error {
		go func() {
			time.Sleep(delay) // Simulated graceful shutdown.
			err := clientset.CoreV1().Pods(namespace).Delete(ctx, name, metav1.DeleteOptions{})
			require.NoError(t, err)
		}()
		return nil
	}
}

func failingPodEvictionReaction(responseErr error, count int, next func(string, string) error) func(string, string) error {
	return func(a0, a1 string) error {
		if count > 0 {
			count--
			return responseErr
		} else {
			return next(a0, a1)
		}
	}
}

func givenPodIsDeleted(t testing.TB, ctx context.Context, clientset *fake.Clientset, namespace, name string) {
	t.Helper()
	err := clientset.CoreV1().Pods(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	require.NoError(t, err)
}

func requirePodNotExists(t testing.TB, ctx context.Context, clientset *fake.Clientset, namespace, pod string) {
	t.Helper()
	_, err := clientset.CoreV1().Pods(namespace).Get(ctx, pod, metav1.GetOptions{})
	require.True(t, apierrors.IsNotFound(err), "pod should not exist")
}

func setupFakeClientWithPodEviction() (*fake.Clientset, *v1.Pod) {
	namespace := "default"
	nodeName := "node-0"
	podName := "pod-0"

	node := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName,
		},
	}
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: namespace,
		},
		Spec: v1.PodSpec{
			NodeName: nodeName,
		},
	}
	clientset := fake.NewSimpleClientset(node, pod)

	addEvictionSupport(clientset)

	return clientset, pod
}

func prependPodEvictionReaction(c *fake.Clientset, reaction func(string, string) error) {
	c.PrependReactor("create", "pods", func(action ktest.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() != "eviction" {
			return false, nil, nil
		}

		var namespace, name string
		if eviction, ok := action.(ktest.CreateAction).GetObject().(*policyv1beta1.Eviction); ok {
			namespace = eviction.GetNamespace()
			name = eviction.GetName()
		}
		if eviction, ok := action.(ktest.CreateAction).GetObject().(*policyv1.Eviction); ok {
			namespace = eviction.GetNamespace()
			name = eviction.GetName()
		}
		return true, nil, reaction(namespace, name)
	})
}
