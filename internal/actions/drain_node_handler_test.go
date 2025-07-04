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
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktest "k8s.io/client-go/testing"

	"github.com/castai/cluster-controller/internal/castai"
)

func TestDrainNodeHandler(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	t.Run("drain successfully", func(t *testing.T) {
		t.Parallel()

		nodeName := "node1"
		nodeID := "node-id"
		podName := "pod1"
		clientset := setupFakeClientWithNodePodEviction(nodeName, nodeID, podName)
		prependEvictionReaction(t, clientset, true, false)

		action := &castai.ClusterAction{
			ID: uuid.New().String(),
			ActionDrainNode: &castai.ActionDrainNode{
				NodeName:            "node1",
				NodeID:              nodeID,
				DrainTimeoutSeconds: 1,
				Force:               true,
			},
			CreatedAt: time.Now().UTC(),
		}
		h := DrainNodeHandler{
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
		t.Parallel()

		nodeName := "node1"
		nodeID := "node-id"
		podName := "pod1"
		clientset := setupFakeClientWithNodePodEviction(nodeName, nodeID, podName)

		action := &castai.ClusterAction{
			ID: uuid.New().String(),
			ActionDrainNode: &castai.ActionDrainNode{
				NodeName:            "already-deleted-node",
				NodeID:              nodeID,
				DrainTimeoutSeconds: 1,
				Force:               true,
			},
			CreatedAt: time.Now().UTC(),
		}

		h := DrainNodeHandler{
			log:       log,
			clientset: clientset,
			cfg:       drainNodeConfig{},
		}

		err := h.Handle(context.Background(), action)
		r.NoError(err)

		_, err = clientset.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
		r.NoError(err)
	})

	t.Run("when eviction fails for a pod and force=false, leaves node cordoned and skip deletion", func(t *testing.T) {
		t.Parallel()

		nodeName := "node1"
		nodeID := "node-id"
		podName := "pod1"
		clientset := setupFakeClientWithNodePodEviction(nodeName, nodeID, podName)
		prependEvictionReaction(t, clientset, false, false)

		action := &castai.ClusterAction{
			ID: uuid.New().String(),
			ActionDrainNode: &castai.ActionDrainNode{
				NodeName:            "node1",
				NodeID:              nodeID,
				DrainTimeoutSeconds: 1,
				Force:               false,
			},
			CreatedAt: time.Now().UTC(),
		}

		h := DrainNodeHandler{
			log:       log,
			clientset: clientset,
			cfg:       drainNodeConfig{},
		}

		err := h.Handle(context.Background(), action)

		r.Error(err)
		r.ErrorContains(err, "failed to drain via graceful eviction")

		n, err := clientset.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
		r.NoError(err)
		r.True(n.Spec.Unschedulable)

		_, err = clientset.CoreV1().Pods("default").Get(context.Background(), podName, metav1.GetOptions{})
		r.NoError(err)
	})

	t.Run("when eviction timeout is reached and force=false, leaves node cordoned and skip deletion", func(t *testing.T) {
		t.Parallel()

		nodeName := "node1"
		nodeID := "node-id"
		podName := "pod1"
		clientset := setupFakeClientWithNodePodEviction(nodeName, nodeID, podName)
		prependEvictionReaction(t, clientset, false, true)

		action := &castai.ClusterAction{
			ID: uuid.New().String(),
			ActionDrainNode: &castai.ActionDrainNode{
				NodeName:            "node1",
				NodeID:              nodeID,
				DrainTimeoutSeconds: 0,
				Force:               false,
			},
			CreatedAt: time.Now().UTC(),
		}

		h := DrainNodeHandler{
			log:       log,
			clientset: clientset,
			cfg:       drainNodeConfig{},
		}

		err := h.Handle(context.Background(), action)

		r.Error(err)
		r.ErrorContains(err, "failed to drain via graceful eviction")
		r.ErrorIs(err, context.DeadlineExceeded)

		n, err := clientset.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
		r.NoError(err)
		r.True(n.Spec.Unschedulable)

		_, err = clientset.CoreV1().Pods("default").Get(context.Background(), podName, metav1.GetOptions{})
		r.NoError(err)
	})

	t.Run("eviction fails and force=true, force remove pods", func(t *testing.T) {
		t.Parallel()

		cases := []struct {
			name                    string
			drainTimeoutSeconds     int
			retryablePodEvictionErr bool
		}{
			{
				name:                    "timeout during eviction",
				drainTimeoutSeconds:     0,
				retryablePodEvictionErr: true,
			},
			{
				name:                    "failed pod during eviction",
				drainTimeoutSeconds:     10,
				retryablePodEvictionErr: false,
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				r := require.New(t)
				nodeName := "node1"
				nodeID := "node-id"
				podName := "pod1"
				clientset := setupFakeClientWithNodePodEviction(nodeName, nodeID, podName)
				prependEvictionReaction(t, clientset, false, tc.retryablePodEvictionErr)

				action := &castai.ClusterAction{
					ID: uuid.New().String(),
					ActionDrainNode: &castai.ActionDrainNode{
						NodeName:            "node1",
						NodeID:              nodeID,
						DrainTimeoutSeconds: tc.drainTimeoutSeconds,
						Force:               true,
					},
					CreatedAt: time.Now().UTC(),
				}

				h := DrainNodeHandler{
					log:       log,
					clientset: clientset,
					cfg: drainNodeConfig{
						podsDeleteTimeout:             700 * time.Millisecond,
						podDeleteRetries:              5,
						podDeleteRetryDelay:           500 * time.Millisecond,
						podEvictRetryDelay:            500 * time.Millisecond,
						podsTerminationWaitRetryDelay: 1000 * time.Millisecond,
					},
				}

				actualCalls := 0
				clientset.PrependReactor("delete", "pods", func(action ktest.Action) (handled bool, ret runtime.Object, err error) {
					deleteAction := action.(ktest.DeleteActionImpl)
					if deleteAction.Name == podName {
						actualCalls++
						// First call should be graceful; simulate it failed to validate we'll do the forced part.
						// This relies on us not retrying 404s (or let's say it tests it :) ).
						if deleteAction.DeleteOptions.GracePeriodSeconds == nil {
							return true, nil, &apierrors.StatusError{ErrStatus: metav1.Status{Reason: metav1.StatusReasonNotFound}}
						}
						// Second call should be forced.
						r.Equal(int64(0), *deleteAction.DeleteOptions.GracePeriodSeconds)
						return false, nil, nil
					}
					return false, nil, nil
				})

				err := h.Handle(context.Background(), action)
				r.NoError(err)
				r.Equal(2, actualCalls)

				n, err := clientset.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
				r.NoError(err)
				r.True(n.Spec.Unschedulable)

				_, err = clientset.CoreV1().Pods("default").Get(context.Background(), podName, metav1.GetOptions{})
				r.True(apierrors.IsNotFound(err))
			})
		}
	})

	t.Run("eviction fails and force=true, at least one pod fails to delete due to internal error, should return error", func(t *testing.T) {
		t.Parallel()

		nodeName := "node1"
		nodeID := "node-id"
		podName := "pod1"
		clientset := setupFakeClientWithNodePodEviction(nodeName, nodeID, podName)

		action := &castai.ClusterAction{
			ID: uuid.New().String(),
			ActionDrainNode: &castai.ActionDrainNode{
				NodeName:            "node1",
				NodeID:              nodeID,
				DrainTimeoutSeconds: 1,
				Force:               true,
			},
			CreatedAt: time.Now().UTC(),
		}

		h := DrainNodeHandler{
			log:       log,
			clientset: clientset,
			cfg: drainNodeConfig{
				podsDeleteTimeout:             7 * time.Second,
				podDeleteRetries:              5,
				podDeleteRetryDelay:           5 * time.Second,
				podEvictRetryDelay:            5 * time.Second,
				podsTerminationWaitRetryDelay: 10 * time.Second,
			},
		}

		clientset.PrependReactor("delete", "pods", func(action ktest.Action) (handled bool, ret runtime.Object, err error) {
			deleteAction := action.(ktest.DeleteActionImpl)
			if deleteAction.Name == podName {
				return true, nil, &apierrors.StatusError{ErrStatus: metav1.Status{Reason: metav1.StatusReasonInternalError, Message: "internal"}}
			}
			return false, nil, nil
		})

		err := h.Handle(context.Background(), action)

		var podFailedDeletionErr *podFailedActionError

		r.ErrorAs(err, &podFailedDeletionErr)
		r.Len(podFailedDeletionErr.Errors, 1)
		r.Contains(podFailedDeletionErr.Errors[0].Error(), "default/pod1")
		r.Equal("delete", podFailedDeletionErr.Action)

		_, err = clientset.CoreV1().Pods("default").Get(context.Background(), podName, metav1.GetOptions{})
		r.NoError(err)
	})

	t.Run("eviction fails and force=true, timeout during deletion should be retried and returned", func(t *testing.T) {
		t.Parallel()

		nodeName := "node1"
		nodeID := "node-id"
		podName := "pod1"
		clientset := setupFakeClientWithNodePodEviction(nodeName, nodeID, podName)

		action := &castai.ClusterAction{
			ID: uuid.New().String(),
			ActionDrainNode: &castai.ActionDrainNode{
				NodeName:            "node1",
				NodeID:              nodeID,
				DrainTimeoutSeconds: 1,
				Force:               true,
			},
			CreatedAt: time.Now().UTC(),
		}

		h := DrainNodeHandler{
			log:       log,
			clientset: clientset,
			cfg: drainNodeConfig{
				podsDeleteTimeout:             0, // Force delete to timeout immediately.
				podDeleteRetries:              5,
				podDeleteRetryDelay:           5 * time.Second,
				podEvictRetryDelay:            5 * time.Second,
				podsTerminationWaitRetryDelay: 10 * time.Second,
			},
		}

		actualDeleteCalls := 0
		clientset.PrependReactor("delete", "pods", func(action ktest.Action) (handled bool, ret runtime.Object, err error) {
			deleteAction := action.(ktest.DeleteActionImpl)
			if deleteAction.Name == podName {
				actualDeleteCalls++
				return true, nil, &apierrors.StatusError{ErrStatus: metav1.Status{Reason: metav1.StatusReasonTooManyRequests, Message: "stop hammering"}}
			}
			return false, nil, nil
		})

		err := h.Handle(context.Background(), action)

		r.Equal(2, actualDeleteCalls)
		r.ErrorIs(err, context.DeadlineExceeded)

		_, err = clientset.CoreV1().Pods("default").Get(context.Background(), podName, metav1.GetOptions{})
		r.NoError(err)
	})

	t.Run("force=true, failed eviction for PDBs should be retried until timeout before deleting", func(t *testing.T) {
		t.Parallel()

		// tests specifically that PDB error in eviction is retried and not failed fast.
		nodeName := "node1"
		nodeID := "node-id"
		podName := "pod1"
		clientset := setupFakeClientWithNodePodEviction(nodeName, nodeID, podName)

		clientset.PrependReactor("create", "pods", func(action ktest.Action) (handled bool, ret runtime.Object, err error) {
			if action.GetSubresource() != "eviction" {
				return false, nil, nil
			}

			// PDB error is a bit specific in k8s to reconstruct...
			return true,
				nil,
				&apierrors.StatusError{ErrStatus: metav1.Status{
					Reason: metav1.StatusReasonTooManyRequests,
					Details: &metav1.StatusDetails{
						Causes: []metav1.StatusCause{
							{
								Type: policyv1.DisruptionBudgetCause,
							},
						},
					},
				}}
		})

		action := &castai.ClusterAction{
			ID: uuid.New().String(),
			ActionDrainNode: &castai.ActionDrainNode{
				NodeName:            "node1",
				NodeID:              nodeID,
				DrainTimeoutSeconds: 2,
				Force:               false,
			},
			CreatedAt: time.Now().UTC(),
		}

		h := DrainNodeHandler{
			log:       log,
			clientset: clientset,
			cfg:       drainNodeConfig{},
		}

		err := h.Handle(context.Background(), action)

		r.Error(err)
		r.ErrorContains(err, "failed to drain via graceful eviction")
		r.ErrorIs(err, context.DeadlineExceeded)

		n, err := clientset.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
		r.NoError(err)
		r.True(n.Spec.Unschedulable)

		_, err = clientset.CoreV1().Pods("default").Get(context.Background(), podName, metav1.GetOptions{})
		r.NoError(err)
	})
}

func TestGetDrainTimeout(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	t.Run("drain timeout for new action should be the same like in request", func(t *testing.T) {
		r := require.New(t)
		action := &castai.ClusterAction{
			ID: uuid.New().String(),
			ActionDrainNode: &castai.ActionDrainNode{
				NodeName:            "node1",
				DrainTimeoutSeconds: 100,
				Force:               true,
			},
			CreatedAt: time.Now().UTC(),
		}
		h := DrainNodeHandler{
			log: log,
			cfg: drainNodeConfig{},
		}

		timeout := h.getDrainTimeout(action)

		// We give some wiggle room as the test might get here a few milliseconds late.
		r.InDelta((100 * time.Second).Milliseconds(), timeout.Milliseconds(), 10)
	})

	t.Run("drain timeout for older action should be decreased by time since action creation", func(t *testing.T) {
		r := require.New(t)
		action := &castai.ClusterAction{
			ID: uuid.New().String(),
			ActionDrainNode: &castai.ActionDrainNode{
				NodeName:            "node1",
				DrainTimeoutSeconds: 600,
				Force:               true,
			},
			CreatedAt: time.Now().UTC().Add(-3 * time.Minute),
		}
		h := DrainNodeHandler{
			log: log,
			cfg: drainNodeConfig{},
		}

		timeout := h.getDrainTimeout(action)
		r.Less(int(math.Floor(timeout.Seconds())), 600)
	})

	t.Run("drain timeout min wait timeout should be 0s", func(t *testing.T) {
		r := require.New(t)
		action := &castai.ClusterAction{
			ID: uuid.New().String(),
			ActionDrainNode: &castai.ActionDrainNode{
				NodeName:            "node1",
				DrainTimeoutSeconds: 600,
				Force:               true,
			},
			CreatedAt: time.Now().UTC().Add(-60 * time.Minute),
		}
		h := DrainNodeHandler{
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

func prependEvictionReaction(t testing.TB, c *fake.Clientset, success, retryableFailure bool) {
	prependPodEvictionReaction(c, func(namespace, name string) error {
		if !success {
			if retryableFailure {
				// Simulate failure that should be retried by client.
				return &apierrors.StatusError{ErrStatus: metav1.Status{Reason: metav1.StatusReasonTooManyRequests}}
			}
			return &apierrors.StatusError{ErrStatus: metav1.Status{Reason: metav1.StatusReasonInternalError, Message: "internal"}}
		}
		go func() {
			err := c.CoreV1().Pods(namespace).Delete(context.Background(), name, metav1.DeleteOptions{})
			require.NoError(t, err)
		}()
		return nil
	})
}

// nolint: unparam
func setupFakeClientWithNodePodEviction(nodeName, nodeID, podName string) *fake.Clientset {
	node := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName,
			Labels: map[string]string{
				castai.LabelNodeID: nodeID,
			},
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
		Group:   "policy",
		Version: "v1",
	}
	coreResources := &metav1.APIResourceList{
		GroupVersion: "v1",
		APIResources: []metav1.APIResource{podsEviction},
	}

	c.Resources = append(c.Resources, coreResources)
}
