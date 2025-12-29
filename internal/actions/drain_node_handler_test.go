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
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	ktest "k8s.io/client-go/testing"

	"github.com/castai/cluster-controller/internal/castai"
)

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
func setupFakeClientWithNodePodEviction(nodeName, nodeID, providerID, podName string) *fake.Clientset {
	node := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName,
			Labels: map[string]string{
				castai.LabelNodeID: nodeID,
			},
		},
		Spec: v1.NodeSpec{
			ProviderID: providerID,
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

	clientset := fake.NewClientset(node, pod, daemonSetPod, staticPod, terminatedPod, jobCompleted)

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

// nolint: gocognit
func TestDrainNodeHandler_Handle(t *testing.T) {
	podFailedDeletionErr := &podFailedActionError{}
	t.Parallel()
	type fields struct {
		clientSet func(t *testing.T) *fake.Clientset
	}
	type args struct {
		action *castai.ClusterAction
		cfg    drainNodeConfig
	}
	tests := []struct {
		name                string
		fields              fields
		args                args
		wantErr             error
		wantErrorContains   string
		wantPodIsNotFound   bool
		wantNodeNotCordoned bool
	}{
		{
			name: "nil",
			args: args{},
			fields: fields{
				clientSet: func(t *testing.T) *fake.Clientset {
					return fake.NewClientset()
				},
			},
			wantErr: errAction,
		},
		{
			name: "wrong action type",
			args: args{
				action: &castai.ClusterAction{
					ActionDeleteNode: &castai.ActionDeleteNode{},
				},
			},
			fields: fields{
				clientSet: func(t *testing.T) *fake.Clientset {
					return fake.NewClientset()
				},
			},
			wantErr: errAction,
		},
		{
			name: "empty node name",
			args: args{
				action: newActionDrainNode("", nodeID, providerID, 1, true),
			},
			fields: fields{
				clientSet: func(t *testing.T) *fake.Clientset {
					return setupFakeClientWithNodePodEviction(nodeName, nodeID, providerID, podName)
				},
			},
			wantErr: errAction,
		},
		{
			name: "empty node ID and provider ID",
			args: args{
				action: newPatchNodeAction(nodeName, "", "",
					nil, nil, nil, nil, nil),
			},
			fields: fields{
				clientSet: func(t *testing.T) *fake.Clientset {
					return setupFakeClientWithNodePodEviction(nodeName, nodeID, providerID, podName)
				},
			},
			wantErr: errAction,
		},
		{
			name: "action with another node id and provider id - skip drain",
			fields: fields{
				clientSet: func(t *testing.T) *fake.Clientset {
					return setupFakeClientWithNodePodEviction(nodeName, nodeID, providerID, podName)
				},
			},
			args: args{
				action: newActionDrainNode(nodeName, "another-node-id", "another-provider-id", 1, true),
			},
			wantNodeNotCordoned: true,
		},
		{
			name: "action with proper node id and another provider id - skip drain",
			fields: fields{
				clientSet: func(t *testing.T) *fake.Clientset {
					return setupFakeClientWithNodePodEviction(nodeName, nodeID, providerID, podName)
				},
			},
			args: args{
				action: newActionDrainNode(nodeName, nodeID, "another-provider-id", 1, true),
			},
			wantNodeNotCordoned: true,
		},
		{
			name: "action with another node id and proper provider id - skip drain",
			fields: fields{
				clientSet: func(t *testing.T) *fake.Clientset {
					return setupFakeClientWithNodePodEviction(nodeName, nodeID, providerID, podName)
				},
			},
			args: args{
				action: newActionDrainNode(nodeName, nodeID, "another-provider-id", 1, true),
			},
			wantNodeNotCordoned: true,
		},
		{
			name: "drain node successfully",
			fields: fields{
				clientSet: func(t *testing.T) *fake.Clientset {
					c := setupFakeClientWithNodePodEviction(nodeName, nodeID, providerID, podName)
					prependEvictionReaction(t, c, true, false)
					return c
				},
			},
			args: args{
				cfg:    drainNodeConfig{},
				action: newActionDrainNode(nodeName, nodeID, providerID, 1, true),
			},
			wantPodIsNotFound: true,
		},
		{
			name: "skip drain when node not found",
			fields: fields{
				clientSet: func(t *testing.T) *fake.Clientset {
					return setupFakeClientWithNodePodEviction(nodeName, nodeID, providerID, podName)
				},
			},
			args: args{
				cfg:    drainNodeConfig{},
				action: newActionDrainNode("already-deleted-node", nodeID, providerID, 1, true),
			},
		},
		{
			name: "when eviction fails for a pod and force=false, leaves node cordoned and skip deletion",
			fields: fields{
				clientSet: func(t *testing.T) *fake.Clientset {
					c := setupFakeClientWithNodePodEviction(nodeName, nodeID, providerID, podName)
					prependEvictionReaction(t, c, false, false)
					return c
				},
			},
			args: args{
				cfg:    drainNodeConfig{},
				action: newActionDrainNode(nodeName, nodeID, providerID, 1, false),
			},
			wantErr:           context.DeadlineExceeded,
			wantErrorContains: "failed to drain via graceful eviction",
		},
		{
			name: "when eviction timeout is reached and force=false, leaves node cordoned and skip deletion",
			fields: fields{
				clientSet: func(t *testing.T) *fake.Clientset {
					c := setupFakeClientWithNodePodEviction(nodeName, nodeID, providerID, podName)
					prependEvictionReaction(t, c, false, true)
					return c
				},
			},
			args: args{
				cfg:    drainNodeConfig{},
				action: newActionDrainNode(nodeName, nodeID, providerID, 0, false),
			},
			wantErr:           context.DeadlineExceeded,
			wantErrorContains: "failed to drain via graceful eviction",
		},
		{
			name: "eviction fails and force=true, force remove pods: timeout during eviction",
			fields: fields{
				clientSet: func(*testing.T) *fake.Clientset {
					c := setupFakeClientWithNodePodEviction(nodeName, nodeID, providerID, podName)
					prependEvictionReaction(t, c, false, true)
					actualCalls := 0
					c.PrependReactor("delete", "pods", func(action ktest.Action) (handled bool, ret runtime.Object, err error) {
						deleteAction := action.(ktest.DeleteActionImpl)
						if deleteAction.Name == podName {
							actualCalls++
							// First call should be graceful; simulate it failed to validate we'll do the forced part.
							// This relies on us not retrying 404s (or let's say it tests it :) ).
							if deleteAction.DeleteOptions.GracePeriodSeconds == nil {
								return true, nil, &apierrors.StatusError{ErrStatus: metav1.Status{Reason: metav1.StatusReasonNotFound}}
							}
							// Second call should be forced.
							require.Equal(t, int64(0), *deleteAction.DeleteOptions.GracePeriodSeconds)
							require.True(t, actualCalls <= 2, "actual calls to delete pod should be at most 2, got %d", actualCalls)
							return false, nil, nil
						}
						return false, nil, nil
					})
					return c
				},
			},
			args: args{
				cfg: drainNodeConfig{
					podsDeleteTimeout:             700 * time.Millisecond,
					podDeleteRetries:              5,
					podDeleteRetryDelay:           500 * time.Millisecond,
					podEvictRetryDelay:            500 * time.Millisecond,
					podsTerminationWaitRetryDelay: 1000 * time.Millisecond,
				},
				action: newActionDrainNode(nodeName, nodeID, providerID, 0, true),
			},
			wantPodIsNotFound: true,
		},
		{
			name: "eviction fails and force=true, force remove pods: failed pod during eviction",
			fields: fields{
				clientSet: func(t *testing.T) *fake.Clientset {
					c := setupFakeClientWithNodePodEviction(nodeName, nodeID, providerID, podName)
					prependEvictionReaction(t, c, false, false)
					actualCalls := 0
					c.PrependReactor("delete", "pods", func(action ktest.Action) (handled bool, ret runtime.Object, err error) {
						deleteAction := action.(ktest.DeleteActionImpl)
						if deleteAction.Name == podName {
							actualCalls++
							// First call should be graceful; simulate it failed to validate we'll do the forced part.
							// This relies on us not retrying 404s (or let's say it tests it :) ).
							if deleteAction.DeleteOptions.GracePeriodSeconds == nil {
								return true, nil, &apierrors.StatusError{ErrStatus: metav1.Status{Reason: metav1.StatusReasonNotFound}}
							}
							// Second call should be forced.
							require.Equal(t, int64(0), *deleteAction.DeleteOptions.GracePeriodSeconds)
							require.True(t, actualCalls <= 2, "actual calls to delete pod should be at most 2, got %d", actualCalls)
							return false, nil, nil
						}
						return false, nil, nil
					})
					return c
				},
			},
			args: args{
				cfg: drainNodeConfig{
					podsDeleteTimeout:             700 * time.Millisecond,
					podDeleteRetries:              5,
					podDeleteRetryDelay:           500 * time.Millisecond,
					podEvictRetryDelay:            500 * time.Millisecond,
					podsTerminationWaitRetryDelay: 1000 * time.Millisecond,
				},
				action: newActionDrainNode(nodeName, nodeID, providerID, 10, true),
			},
			wantPodIsNotFound: true,
		},
		{
			name: "eviction fails and force=true, at least one pod fails to delete due to internal error, should return error",
			fields: fields{
				clientSet: func(t *testing.T) *fake.Clientset {
					c := setupFakeClientWithNodePodEviction(nodeName, nodeID, providerID, podName)
					c.PrependReactor("delete", "pods", func(action ktest.Action) (handled bool, ret runtime.Object, err error) {
						deleteAction := action.(ktest.DeleteActionImpl)
						if deleteAction.Name == podName {
							return true, nil, &apierrors.StatusError{ErrStatus: metav1.Status{Reason: metav1.StatusReasonInternalError, Message: "internal"}}
						}
						return false, nil, nil
					})
					return c
				},
			},
			args: args{
				cfg: drainNodeConfig{
					podsDeleteTimeout:             7 * time.Second,
					podDeleteRetries:              5,
					podDeleteRetryDelay:           5 * time.Second,
					podEvictRetryDelay:            5 * time.Second,
					podsTerminationWaitRetryDelay: 10 * time.Second,
				},
				action: newActionDrainNode(nodeName, nodeID, providerID, 0, true),
			},
			wantErr:           podFailedDeletionErr,
			wantErrorContains: "pod default/pod1 failed deletion: deleting pod pod1 in namespace default: internal",
		},
		{
			name: "eviction fails and force=true, timeout during deletion should be retried and returned",
			fields: fields{
				clientSet: func(t *testing.T) *fake.Clientset {
					c := setupFakeClientWithNodePodEviction(nodeName, nodeID, providerID, podName)
					actualDeleteCalls := 0
					c.PrependReactor("delete", "pods", func(action ktest.Action) (handled bool, ret runtime.Object, err error) {
						deleteAction := action.(ktest.DeleteActionImpl)
						if deleteAction.Name == podName {
							actualDeleteCalls++
							return true, nil, &apierrors.StatusError{ErrStatus: metav1.Status{Reason: metav1.StatusReasonTooManyRequests, Message: "stop hammering"}}
						}
						return false, nil, nil
					})
					return c
				},
			},
			args: args{
				cfg: drainNodeConfig{
					podsDeleteTimeout:             0, // Force delete to timeout immediately.
					podDeleteRetries:              5,
					podDeleteRetryDelay:           5 * time.Second,
					podEvictRetryDelay:            5 * time.Second,
					podsTerminationWaitRetryDelay: 10 * time.Second,
				},
				action: newActionDrainNode(nodeName, nodeID, providerID, 1, true),
			},
			wantErr: context.DeadlineExceeded,
		},
		{
			name: "force=true, failed eviction for PDBs should be retried until timeout before deleting",
			fields: fields{
				clientSet: func(t *testing.T) *fake.Clientset {
					c := setupFakeClientWithNodePodEviction(nodeName, nodeID, providerID, podName)
					c.PrependReactor("create", "pods", func(action ktest.Action) (handled bool, ret runtime.Object, err error) {
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
					return c
				},
			},
			args: args{
				cfg:    drainNodeConfig{},
				action: newActionDrainNode(nodeName, nodeID, providerID, 2, false),
			},
			wantErr:           context.DeadlineExceeded,
			wantErrorContains: "failed to drain via graceful eviction",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := &DrainNodeHandler{
				log:       logrus.New(),
				clientset: tt.fields.clientSet(t),
				cfg:       tt.args.cfg,
			}
			err := h.Handle(context.Background(), tt.args.action)
			require.Equal(t, tt.wantErr != nil, err != nil, "expected error: %v, got: %v", tt.wantErr, err)
			if tt.wantErr != nil {
				require.ErrorAs(t, err, &tt.wantErr)
				require.ErrorContains(t, err, tt.wantErrorContains)
			}

			if err != nil {
				return
			}

			n, err := h.clientset.CoreV1().Nodes().Get(context.Background(), tt.args.action.ActionDrainNode.NodeName, metav1.GetOptions{})
			require.True(t, (err != nil && apierrors.IsNotFound(err)) ||
				(err == nil && n.Spec.Unschedulable == !tt.wantNodeNotCordoned),
				"expected node to be not found or cordoned, got: %v", err)

			_, err = h.clientset.CoreV1().Pods("default").Get(context.Background(), podName, metav1.GetOptions{})
			require.True(t, (tt.wantPodIsNotFound && apierrors.IsNotFound(err)) || (!tt.wantPodIsNotFound && err == nil), "expected pod to be not found, got: %v", err)

			checkPods(t, h.clientset, "ds-pod", "static-pod", "job-pod")
		})
	}
}

func checkPods(t *testing.T, clientset kubernetes.Interface, podNames ...string) {
	t.Helper()
	for _, podName := range podNames {
		_, err := clientset.CoreV1().Pods("default").Get(context.Background(), podName, metav1.GetOptions{})
		require.NoError(t, err, "expected pod %s to be found", podName)
	}
}

func newActionDrainNode(nodeName, nodeID, providerID string, drainTimeoutSeconds int, force bool) *castai.ClusterAction {
	return &castai.ClusterAction{
		ID: uuid.New().String(),
		ActionDrainNode: &castai.ActionDrainNode{
			NodeName:            nodeName,
			NodeID:              nodeID,
			ProviderId:          providerID,
			DrainTimeoutSeconds: drainTimeoutSeconds,
			Force:               force,
		},
		CreatedAt: time.Now().UTC(),
	}
}
