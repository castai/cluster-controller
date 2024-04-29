package actions

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	"k8s.io/api/policy/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktest "k8s.io/client-go/testing"

	"github.com/castai/cluster-controller/types"
)

func TestDrainNodeHandler(t *testing.T) {
	r := require.New(t)

	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	t.Run("drain successfully", func(t *testing.T) {
		nodeName := "node1"
		podName := "pod1"
		clientset := setupFakeClientWithNodePodEviction(nodeName, podName)
		prependEvictionReaction(t, clientset, true)

		action := &types.ClusterAction{
			ID: uuid.New().String(),
			ActionDrainNode: &types.ActionDrainNode{
				NodeName:            "node1",
				DrainTimeoutSeconds: 1,
				Force:               true,
			},
			CreatedAt: time.Now().UTC(),
		}
		h := drainNodeHandler{
			log:       log,
			clientset: clientset,
			cfg:       drainNodeConfig{},
		}

		err := h.Handle(context.Background(), action)
		r.NoError(err)

		n, err := clientset.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
		r.NoError(err)
		r.True(n.Spec.Unschedulable)

		_, err = clientset.CoreV1().Pods("default").Get(context.Background(), podName, metav1.GetOptions{})
		r.Error(err)
		r.True(apierrors.IsNotFound(err))

		// Daemon set and static pods and job should not be drained.
		_, err = clientset.CoreV1().Pods("default").Get(context.Background(), "ds-pod", metav1.GetOptions{})
		r.NoError(err)
		_, err = clientset.CoreV1().Pods("default").Get(context.Background(), "static-pod", metav1.GetOptions{})
		r.NoError(err)
		_, err = clientset.CoreV1().Pods("default").Get(context.Background(), "job-pod", metav1.GetOptions{})
		r.NoError(err)
	})

	t.Run("skip drain when node not found", func(t *testing.T) {
		nodeName := "node1"
		podName := "pod1"
		clientset := setupFakeClientWithNodePodEviction(nodeName, podName)
		prependEvictionReaction(t, clientset, true)

		action := &types.ClusterAction{
			ID: uuid.New().String(),
			ActionDrainNode: &types.ActionDrainNode{
				NodeName:            "already-deleted-node",
				DrainTimeoutSeconds: 1,
				Force:               true,
			},
			CreatedAt: time.Now().UTC(),
		}

		h := drainNodeHandler{
			log:       log,
			clientset: clientset,
			cfg:       drainNodeConfig{},
		}

		err := h.Handle(context.Background(), action)
		r.NoError(err)

		_, err = clientset.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
		r.NoError(err)
	})

	t.Run("fail to drain when internal pod eviction error occur", func(t *testing.T) {
		nodeName := "node1"
		podName := "pod1"
		clientset := setupFakeClientWithNodePodEviction(nodeName, podName)
		prependEvictionReaction(t, clientset, false)

		action := &types.ClusterAction{
			ID: uuid.New().String(),
			ActionDrainNode: &types.ActionDrainNode{
				NodeName:            "node1",
				DrainTimeoutSeconds: 1,
				Force:               true,
			},
			CreatedAt: time.Now().UTC(),
		}

		h := drainNodeHandler{
			log:       log,
			clientset: clientset,
			cfg:       drainNodeConfig{},
		}

		err := h.Handle(context.Background(), action)
		r.EqualError(err, "eviciting node pods: sending evict pods requests: evicting pod pod1 in namespace default: internal")

		n, err := clientset.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
		r.NoError(err)
		r.True(n.Spec.Unschedulable)

		_, err = clientset.CoreV1().Pods("default").Get(context.Background(), podName, metav1.GetOptions{})
		r.NoError(err)
	})

	t.Run("eviction timeout - force remove pods", func(t *testing.T) {
		r := require.New(t)
		nodeName := "node1"
		podName := "pod1"
		clientset := setupFakeClientWithNodePodEviction(nodeName, podName)

		action := &types.ClusterAction{
			ID: uuid.New().String(),
			ActionDrainNode: &types.ActionDrainNode{
				NodeName:            "node1",
				DrainTimeoutSeconds: 1,
				Force:               true,
			},
			CreatedAt: time.Now().UTC(),
		}

		h := drainNodeHandler{
			log:       log,
			clientset: clientset,
			cfg: drainNodeConfig{
				podsDeleteTimeout:             700 * time.Millisecond,
				podDeleteRetries:              5,
				podDeleteRetryDelay:           500 * time.Millisecond,
				podEvictRetryDelay:            500 * time.Millisecond,
				podsTerminationWaitRetryDelay: 1000 * time.Millisecond,
			}}

		clientset.PrependReactor("delete", "pods", func(action ktest.Action) (handled bool, ret runtime.Object, err error) {
			deleteAction := action.(ktest.DeleteActionImpl)
			if deleteAction.Name == podName {
				if deleteAction.DeleteOptions.GracePeriodSeconds == nil {
					return true, nil, context.DeadlineExceeded
				}
				return false, nil, nil
			}
			return false, nil, nil
		})

		err := h.Handle(context.Background(), action)
		r.NoError(err)

		n, err := clientset.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
		r.NoError(err)
		r.True(n.Spec.Unschedulable)

		_, err = clientset.CoreV1().Pods("default").Get(context.Background(), podName, metav1.GetOptions{})
		r.True(apierrors.IsNotFound(err))
	})

	t.Run("eviction timeout - force remove pods - failure", func(t *testing.T) {
		r := require.New(t)
		nodeName := "node1"
		podName := "pod1"
		clientset := setupFakeClientWithNodePodEviction(nodeName, podName)

		action := &types.ClusterAction{
			ID: uuid.New().String(),
			ActionDrainNode: &types.ActionDrainNode{
				NodeName:            "node1",
				DrainTimeoutSeconds: 1,
				Force:               true,
			},
			CreatedAt: time.Now().UTC(),
		}

		h := drainNodeHandler{
			log:       log,
			clientset: clientset,
			cfg: drainNodeConfig{
				podsDeleteTimeout:             7 * time.Second,
				podDeleteRetries:              5,
				podDeleteRetryDelay:           5 * time.Second,
				podEvictRetryDelay:            5 * time.Second,
				podsTerminationWaitRetryDelay: 10 * time.Second,
			}}

		clientset.PrependReactor("delete", "pods", func(action ktest.Action) (handled bool, ret runtime.Object, err error) {
			deleteAction := action.(ktest.DeleteActionImpl)
			if deleteAction.Name == podName {
				return true, nil, &apierrors.StatusError{ErrStatus: metav1.Status{Reason: metav1.StatusReasonInternalError, Message: "internal"}}
			}
			return false, nil, nil
		})

		err := h.Handle(context.Background(), action)
		r.EqualError(err, "forcefully deleting pods: sending delete pods requests: deleting pod pod1 in namespace default: internal")

		_, err = clientset.CoreV1().Pods("default").Get(context.Background(), podName, metav1.GetOptions{})
		r.NoError(err)
	})

	t.Run("eviction timeout - force remove pods with grace 0 - failure", func(t *testing.T) {
		r := require.New(t)
		nodeName := "node1"
		podName := "pod1"
		clientset := setupFakeClientWithNodePodEviction(nodeName, podName)

		action := &types.ClusterAction{
			ID: uuid.New().String(),
			ActionDrainNode: &types.ActionDrainNode{
				NodeName:            "node1",
				DrainTimeoutSeconds: 1,
				Force:               true,
			},
			CreatedAt: time.Now().UTC(),
		}

		h := drainNodeHandler{
			log:       log,
			clientset: clientset,
			cfg: drainNodeConfig{
				podsDeleteTimeout:             700 * time.Millisecond,
				podDeleteRetries:              5,
				podDeleteRetryDelay:           500 * time.Millisecond,
				podEvictRetryDelay:            500 * time.Millisecond,
				podsTerminationWaitRetryDelay: 1000 * time.Millisecond,
			}}

		clientset.PrependReactor("delete", "pods", func(action ktest.Action) (handled bool, ret runtime.Object, err error) {
			deleteAction := action.(ktest.DeleteActionImpl)
			if deleteAction.Name == podName {
				if deleteAction.DeleteOptions.GracePeriodSeconds == nil {
					return true, nil, context.DeadlineExceeded
				}
				return true, nil, &apierrors.StatusError{ErrStatus: metav1.Status{Reason: metav1.StatusReasonInternalError, Message: "internal"}}
			}
			return false, nil, nil
		})

		err := h.Handle(context.Background(), action)
		r.EqualError(err, "forcefully deleting pods: sending delete pods requests: deleting pod pod1 in namespace default: internal")

		_, err = clientset.CoreV1().Pods("default").Get(context.Background(), podName, metav1.GetOptions{})
		r.NoError(err)
	})
}

func TestGetDrainTimeout(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	t.Run("drain timeout for new action should be the same like in request", func(t *testing.T) {
		r := require.New(t)
		action := &types.ClusterAction{
			ID: uuid.New().String(),
			ActionDrainNode: &types.ActionDrainNode{
				NodeName:            "node1",
				DrainTimeoutSeconds: 100,
				Force:               true,
			},
			CreatedAt: time.Now().UTC(),
		}
		h := drainNodeHandler{
			log: log,
			cfg: drainNodeConfig{},
		}

		timeout := h.getDrainTimeout(action)
		r.Equal(100*time.Second, timeout)
	})

	t.Run("drain timeout for older action should be decreased by time since action creation", func(t *testing.T) {
		r := require.New(t)
		action := &types.ClusterAction{
			ID: uuid.New().String(),
			ActionDrainNode: &types.ActionDrainNode{
				NodeName:            "node1",
				DrainTimeoutSeconds: 600,
				Force:               true,
			},
			CreatedAt: time.Now().UTC().Add(-3 * time.Minute),
		}
		h := drainNodeHandler{
			log: log,
			cfg: drainNodeConfig{},
		}

		timeout := h.getDrainTimeout(action)
		r.Less(int(math.Floor(timeout.Seconds())), 600)
	})

	t.Run("drain timeout min wait timeout should be 0s", func(t *testing.T) {
		r := require.New(t)
		action := &types.ClusterAction{
			ID: uuid.New().String(),
			ActionDrainNode: &types.ActionDrainNode{
				NodeName:            "node1",
				DrainTimeoutSeconds: 600,
				Force:               true,
			},
			CreatedAt: time.Now().UTC().Add(-60 * time.Minute),
		}
		h := drainNodeHandler{
			log: log,
			cfg: drainNodeConfig{},
		}

		timeout := h.getDrainTimeout(action)
		r.Equal(0, int(timeout.Seconds()))
	})
}

func TestLogCastPodsToEvict(t *testing.T) {
	t.Run("should log pods to evict", func(t *testing.T) {
		r := require.New(t)
		log, hook := test.NewNullLogger()
		pods := []v1.Pod{
			{ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "ns1"}},
			{ObjectMeta: metav1.ObjectMeta{Name: "pod2", Namespace: "ns2"}},
		}

		logCastPodsToEvict(log, pods)

		r.Len(hook.Entries, 1)
	})

	t.Run("should skip logs when no pods to evict", func(t *testing.T) {
		r := require.New(t)
		log, hook := test.NewNullLogger()

		var pods []v1.Pod
		logCastPodsToEvict(log, pods)

		r.Len(hook.Entries, 0)
	})

}

func prependEvictionReaction(t *testing.T, c *fake.Clientset, success bool) {
	c.PrependReactor("create", "pods", func(action ktest.Action) (handled bool, ret runtime.Object, err error) {
		if action.GetSubresource() != "eviction" {
			return false, nil, nil
		}

		if success {
			eviction := action.(ktest.CreateAction).GetObject().(*v1beta1.Eviction)

			go func() {
				err := c.CoreV1().Pods(eviction.Namespace).Delete(context.Background(), eviction.Name, metav1.DeleteOptions{})
				require.NoError(t, err)
			}()

			return true, nil, nil
		}

		return true, nil, &apierrors.StatusError{ErrStatus: metav1.Status{Reason: metav1.StatusReasonInternalError, Message: "internal"}}
	})
}

func setupFakeClientWithNodePodEviction(nodeName, podName string) *fake.Clientset {
	node := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName,
		},
	}
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: "default",
		},
		Spec: v1.PodSpec{
			NodeName: nodeName,
		},
	}
	controller := true
	daemonSetPod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ds-pod",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind:       "DaemonSet",
					Controller: &controller,
				},
			},
		},
		Spec: v1.PodSpec{
			NodeName: nodeName,
		},
	}
	staticPod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "static-pod",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind:       "Node",
					Controller: &controller,
				},
			},
		},
		Spec: v1.PodSpec{
			NodeName: nodeName,
		},
	}
	terminatedPod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "removed-pod",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind:       "Node",
					Controller: &controller,
				},
			},
			DeletionTimestamp: &metav1.Time{Time: time.Now().UTC()},
		},
		Spec: v1.PodSpec{
			NodeName: nodeName,
		},
	}
	jobCompleted := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "job-pod",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind:       "Node",
					Controller: &controller,
				},
			},
		},
		Spec: v1.PodSpec{
			NodeName: nodeName,
		},
		Status: v1.PodStatus{
			Phase: v1.PodSucceeded,
		},
	}

	clientset := fake.NewSimpleClientset(node, pod, daemonSetPod, staticPod, terminatedPod, jobCompleted)

	addEvictionSupport(clientset)

	return clientset
}

func addEvictionSupport(c *fake.Clientset) {
	podsEviction := metav1.APIResource{
		Name:    "pods/eviction",
		Kind:    "Eviction",
		Group:   "",
		Version: "v1",
	}
	coreResources := &metav1.APIResourceList{
		GroupVersion: "v1",
		APIResources: []metav1.APIResource{podsEviction},
	}

	c.Resources = append(c.Resources, coreResources)
}
