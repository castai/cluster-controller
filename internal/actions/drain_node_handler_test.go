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
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	storagev1listers "k8s.io/client-go/listers/storage/v1"
	ktest "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"

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

// newTestVAInformer creates a fake informer with VolumeAttachments indexed by node name for testing.
// Returns the lister, indexer, and the fake clientset for dynamic updates during tests.
func newTestVAInformer(t *testing.T, vas ...*storagev1.VolumeAttachment) (storagev1listers.VolumeAttachmentLister, cache.Indexer, kubernetes.Interface) {
	t.Helper()

	// Convert VAs to runtime.Object slice for fake clientset
	objs := make([]runtime.Object, 0, len(vas))
	for _, va := range vas {
		objs = append(objs, va)
	}

	clientset := fake.NewClientset(objs...)
	factory := informers.NewSharedInformerFactory(clientset, 0)
	vaInformer := factory.Storage().V1().VolumeAttachments()

	// Add the node name indexer
	err := vaInformer.Informer().AddIndexers(cache.Indexers{
		vaNodeNameIndexer: func(obj interface{}) ([]string, error) {
			va, ok := obj.(*storagev1.VolumeAttachment)
			if !ok {
				return nil, nil
			}
			return []string{va.Spec.NodeName}, nil
		},
	})
	require.NoError(t, err)

	// Start and sync
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	factory.Start(ctx.Done())
	synced := factory.WaitForCacheSync(ctx.Done())
	for typ, ok := range synced {
		require.True(t, ok, "failed to sync informer for %v", typ)
	}

	return vaInformer.Lister(), vaInformer.Informer().GetIndexer(), clientset
}

func TestGetVolumeAttachmentsForNode(t *testing.T) {
	t.Parallel()

	t.Run("should return error when indexer is nil", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)
		log := logrus.New()

		h := &DrainNodeHandler{
			log:       log,
			vaIndexer: nil,
		}

		vaNames, err := h.getVolumeAttachmentsForNode(context.Background(), log, "node1", nil)
		r.Error(err)
		r.Contains(err.Error(), "indexer not available")
		r.Nil(vaNames)
	})

	t.Run("should return empty when no VolumeAttachments on node", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)
		log := logrus.New()
		vaLister, vaIndexer, _ := newTestVAInformer(t)

		h := &DrainNodeHandler{
			log:       log,
			vaLister:  vaLister,
			vaIndexer: vaIndexer,
		}

		vaNames, err := h.getVolumeAttachmentsForNode(context.Background(), log, "node1", nil)
		r.NoError(err)
		r.Empty(vaNames)
	})

	t.Run("should find VolumeAttachments for node", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)
		log := logrus.New()

		va := &storagev1.VolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{Name: "va1"},
			Spec: storagev1.VolumeAttachmentSpec{
				NodeName: "node1",
				Source:   storagev1.VolumeAttachmentSource{PersistentVolumeName: strPtr("pv1")},
			},
		}

		vaLister, vaIndexer, _ := newTestVAInformer(t, va)

		h := &DrainNodeHandler{
			log:       log,
			vaLister:  vaLister,
			vaIndexer: vaIndexer,
		}

		// No non-evictable pods
		vaNames, err := h.getVolumeAttachmentsForNode(context.Background(), log, "node1", nil)
		r.NoError(err)
		r.Equal([]string{"va1"}, vaNames)
	})

	t.Run("should exclude VAs from DaemonSet pods", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)
		log := logrus.New()

		pvc := &v1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: "pvc-ds", Namespace: "default"},
			Spec:       v1.PersistentVolumeClaimSpec{VolumeName: "pv-ds"},
		}
		vaFromDS := &storagev1.VolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{Name: "va-ds"},
			Spec: storagev1.VolumeAttachmentSpec{
				NodeName: "node1",
				Source:   storagev1.VolumeAttachmentSource{PersistentVolumeName: strPtr("pv-ds")},
			},
		}
		vaFromRegular := &storagev1.VolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{Name: "va-regular"},
			Spec: storagev1.VolumeAttachmentSpec{
				NodeName: "node1",
				Source:   storagev1.VolumeAttachmentSource{PersistentVolumeName: strPtr("pv-regular")},
			},
		}

		controller := true
		dsPod := v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "ds-pod",
				Namespace: "default",
				OwnerReferences: []metav1.OwnerReference{
					{Kind: "DaemonSet", Controller: &controller},
				},
			},
			Spec: v1.PodSpec{
				NodeName: "node1",
				Volumes: []v1.Volume{
					{
						Name: "data",
						VolumeSource: v1.VolumeSource{
							PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{ClaimName: "pvc-ds"},
						},
					},
				},
			},
		}

		vaLister, vaIndexer, _ := newTestVAInformer(t, vaFromDS, vaFromRegular)
		// Create a clientset with the PVC for lookup
		clientset := fake.NewClientset(pvc)

		h := &DrainNodeHandler{
			log:       log,
			clientset: clientset,
			vaLister:  vaLister,
			vaIndexer: vaIndexer,
		}

		// Pass the DaemonSet pod as non-evictable
		vaNames, err := h.getVolumeAttachmentsForNode(context.Background(), log, "node1", []v1.Pod{dsPod})
		r.NoError(err)
		// Should only return va-regular, not va-ds (excluded because owned by DaemonSet)
		r.Equal([]string{"va-regular"}, vaNames)
	})

	t.Run("should exclude VAs from static pods", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)
		log := logrus.New()

		pvc := &v1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: "pvc-static", Namespace: "default"},
			Spec:       v1.PersistentVolumeClaimSpec{VolumeName: "pv-static"},
		}
		vaFromStatic := &storagev1.VolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{Name: "va-static"},
			Spec: storagev1.VolumeAttachmentSpec{
				NodeName: "node1",
				Source:   storagev1.VolumeAttachmentSource{PersistentVolumeName: strPtr("pv-static")},
			},
		}
		vaFromRegular := &storagev1.VolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{Name: "va-regular"},
			Spec: storagev1.VolumeAttachmentSpec{
				NodeName: "node1",
				Source:   storagev1.VolumeAttachmentSource{PersistentVolumeName: strPtr("pv-regular")},
			},
		}

		controller := true
		staticPod := v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "static-pod",
				Namespace: "default",
				OwnerReferences: []metav1.OwnerReference{
					{Kind: "Node", Controller: &controller},
				},
			},
			Spec: v1.PodSpec{
				NodeName: "node1",
				Volumes: []v1.Volume{
					{
						Name: "data",
						VolumeSource: v1.VolumeSource{
							PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{ClaimName: "pvc-static"},
						},
					},
				},
			},
		}

		vaLister, vaIndexer, _ := newTestVAInformer(t, vaFromStatic, vaFromRegular)
		// Create a clientset with the PVC for lookup
		clientset := fake.NewClientset(pvc)

		h := &DrainNodeHandler{
			log:       log,
			clientset: clientset,
			vaLister:  vaLister,
			vaIndexer: vaIndexer,
		}

		// Pass the static pod as non-evictable
		vaNames, err := h.getVolumeAttachmentsForNode(context.Background(), log, "node1", []v1.Pod{staticPod})
		r.NoError(err)
		// Should only return va-regular, not va-static (excluded because owned by Node/static)
		r.Equal([]string{"va-regular"}, vaNames)
	})

	t.Run("should only return VAs for the specified node", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)
		log := logrus.New()

		vaNode1 := &storagev1.VolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{Name: "va-node1"},
			Spec: storagev1.VolumeAttachmentSpec{
				NodeName: "node1",
				Source:   storagev1.VolumeAttachmentSource{PersistentVolumeName: strPtr("pv1")},
			},
		}
		vaNode2 := &storagev1.VolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{Name: "va-node2"},
			Spec: storagev1.VolumeAttachmentSpec{
				NodeName: "node2",
				Source:   storagev1.VolumeAttachmentSource{PersistentVolumeName: strPtr("pv2")},
			},
		}

		vaLister, vaIndexer, _ := newTestVAInformer(t, vaNode1, vaNode2)

		h := &DrainNodeHandler{
			log:       log,
			vaLister:  vaLister,
			vaIndexer: vaIndexer,
		}

		vaNames, err := h.getVolumeAttachmentsForNode(context.Background(), log, "node1", nil)
		r.NoError(err)
		r.Equal([]string{"va-node1"}, vaNames)
	})
}

func TestWaitForVolumeDetach(t *testing.T) {
	t.Parallel()

	t.Run("should return immediately when no VAs to wait for", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)
		log := logrus.New()

		h := &DrainNodeHandler{
			log: log,
			cfg: drainNodeConfig{
				volumeDetachPollInterval: 100 * time.Millisecond,
			},
		}

		err := h.waitForVolumeDetach(context.Background(), log, "node1", nil)
		r.NoError(err)
	})

	t.Run("should return immediately when vaIndexer is nil", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)
		log := logrus.New()

		h := &DrainNodeHandler{
			log:       log,
			vaIndexer: nil,
			cfg: drainNodeConfig{
				volumeDetachPollInterval: 100 * time.Millisecond,
			},
		}

		// Should skip waiting and return nil when vaIndexer is nil
		err := h.waitForVolumeDetach(context.Background(), log, "node1", []string{"va1"})
		r.NoError(err)
	})

	t.Run("should complete when VAs are deleted", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)
		log := logrus.New()

		va := &storagev1.VolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{Name: "va1"},
			Spec: storagev1.VolumeAttachmentSpec{
				NodeName: "node1",
				Source:   storagev1.VolumeAttachmentSource{PersistentVolumeName: strPtr("pv1")},
			},
		}
		vaLister, vaIndexer, clientset := newTestVAInformer(t, va)

		h := &DrainNodeHandler{
			log:       log,
			vaLister:  vaLister,
			vaIndexer: vaIndexer,
			cfg: drainNodeConfig{
				volumeDetachPollInterval: 50 * time.Millisecond,
			},
		}

		// Delete VA in background using the clientset
		go func() {
			time.Sleep(100 * time.Millisecond)
			err := clientset.StorageV1().VolumeAttachments().Delete(context.Background(), va.Name, metav1.DeleteOptions{})
			r.NoError(err)
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		err := h.waitForVolumeDetach(ctx, log, "node1", []string{"va1"})
		r.NoError(err)
	})

	t.Run("should timeout gracefully", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)
		log := logrus.New()

		va := &storagev1.VolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{Name: "va1"},
			Spec: storagev1.VolumeAttachmentSpec{
				NodeName: "node1",
				Source:   storagev1.VolumeAttachmentSource{PersistentVolumeName: strPtr("pv1")},
			},
		}
		vaLister, vaIndexer, _ := newTestVAInformer(t, va)

		h := &DrainNodeHandler{
			log:       log,
			vaLister:  vaLister,
			vaIndexer: vaIndexer,
			cfg: drainNodeConfig{
				volumeDetachPollInterval: 50 * time.Millisecond,
			},
		}

		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		defer cancel()

		// VA will not be deleted, should timeout but return nil
		err := h.waitForVolumeDetach(ctx, log, "node1", []string{"va1"})
		r.NoError(err) // Timeout is handled gracefully
	})

	t.Run("should respect context cancellation", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)
		log := logrus.New()

		va := &storagev1.VolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{Name: "va1"},
			Spec: storagev1.VolumeAttachmentSpec{
				NodeName: "node1",
				Source:   storagev1.VolumeAttachmentSource{PersistentVolumeName: strPtr("pv1")},
			},
		}
		vaLister, vaIndexer, _ := newTestVAInformer(t, va)

		h := &DrainNodeHandler{
			log:       log,
			vaLister:  vaLister,
			vaIndexer: vaIndexer,
			cfg: drainNodeConfig{
				volumeDetachPollInterval: 50 * time.Millisecond,
			},
		}

		ctx, cancel := context.WithCancel(context.Background())

		// Cancel context immediately
		go func() {
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()

		err := h.waitForVolumeDetach(ctx, log, "node1", []string{"va1"})
		r.ErrorIs(err, context.Canceled)
	})
}

func TestWaitForVolumeDetachIfEnabled(t *testing.T) {
	t.Parallel()

	t.Run("should do nothing when WaitForVolumeDetach is nil (default disabled)", func(t *testing.T) {
		t.Parallel()
		log := logrus.New()
		clientset := fake.NewClientset()

		h := &DrainNodeHandler{
			log:       log,
			clientset: clientset,
			cfg:       drainNodeConfig{},
		}

		// Action with nil WaitForVolumeDetach (default behavior = disabled)
		req := &castai.ActionDrainNode{
			NodeName: "node1",
		}

		// Should return without doing anything
		h.waitForVolumeDetachIfEnabled(context.Background(), log, "node1", req, nil)
		// No assertions needed - just ensuring no panic/error
	})

	t.Run("should do nothing when WaitForVolumeDetach is explicitly false", func(t *testing.T) {
		t.Parallel()
		log := logrus.New()
		clientset := fake.NewClientset()

		h := &DrainNodeHandler{
			log:       log,
			clientset: clientset,
			cfg:       drainNodeConfig{},
		}

		// Action with WaitForVolumeDetach explicitly set to false
		waitForVA := false
		req := &castai.ActionDrainNode{
			NodeName:            "node1",
			WaitForVolumeDetach: &waitForVA,
		}

		// Should return without doing anything
		h.waitForVolumeDetachIfEnabled(context.Background(), log, "node1", req, nil)
		// No assertions needed - just ensuring no panic/error
	})

	t.Run("should do nothing when vaIndexer is nil even if enabled", func(t *testing.T) {
		t.Parallel()
		log := logrus.New()
		clientset := fake.NewClientset()

		h := &DrainNodeHandler{
			log:       log,
			clientset: clientset,
			vaIndexer: nil, // No indexer
			cfg: drainNodeConfig{
				volumeDetachTimeout:      1 * time.Second,
				volumeDetachPollInterval: 100 * time.Millisecond,
			},
		}

		// Action with WaitForVolumeDetach enabled
		waitForVA := true
		req := &castai.ActionDrainNode{
			NodeName:            "node1",
			WaitForVolumeDetach: &waitForVA,
		}

		h.waitForVolumeDetachIfEnabled(context.Background(), log, "node1", req, nil)
		// No assertions needed - just ensuring no panic/error
	})

	t.Run("should wait when WaitForVolumeDetach is true and VAs exist", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)
		log := logrus.New()

		va := &storagev1.VolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{Name: "va1"},
			Spec: storagev1.VolumeAttachmentSpec{
				NodeName: "node1",
				Source:   storagev1.VolumeAttachmentSource{PersistentVolumeName: strPtr("pv1")},
			},
		}

		vaLister, vaIndexer, clientset := newTestVAInformer(t, va)

		h := &DrainNodeHandler{
			log:       log,
			clientset: clientset,
			vaLister:  vaLister,
			vaIndexer: vaIndexer,
			cfg: drainNodeConfig{
				volumeDetachTimeout:      2 * time.Second,
				volumeDetachPollInterval: 50 * time.Millisecond,
			},
		}

		// Action with WaitForVolumeDetach enabled
		waitForVA := true
		req := &castai.ActionDrainNode{
			NodeName:            "node1",
			WaitForVolumeDetach: &waitForVA,
		}

		// Delete VA in background
		go func() {
			time.Sleep(100 * time.Millisecond)
			err := clientset.StorageV1().VolumeAttachments().Delete(context.Background(), "va1", metav1.DeleteOptions{})
			r.NoError(err)
		}()

		h.waitForVolumeDetachIfEnabled(context.Background(), log, "node1", req, nil)
		// No assertions needed - just ensuring no panic/error and it completes
	})

	t.Run("should use per-action timeout when specified", func(t *testing.T) {
		t.Parallel()
		log := logrus.New()

		va := &storagev1.VolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{Name: "va1"},
			Spec: storagev1.VolumeAttachmentSpec{
				NodeName: "node1",
				Source:   storagev1.VolumeAttachmentSource{PersistentVolumeName: strPtr("pv1")},
			},
		}

		vaLister, vaIndexer, clientset := newTestVAInformer(t, va)

		h := &DrainNodeHandler{
			log:       log,
			clientset: clientset,
			vaLister:  vaLister,
			vaIndexer: vaIndexer,
			cfg: drainNodeConfig{
				volumeDetachTimeout:      60 * time.Second, // Default timeout (should be overridden)
				volumeDetachPollInterval: 50 * time.Millisecond,
			},
		}

		// Action with custom timeout of 100ms
		waitForVA := true
		customTimeoutSec := 1 // 1 second
		req := &castai.ActionDrainNode{
			NodeName:                   "node1",
			WaitForVolumeDetach:        &waitForVA,
			VolumeDetachTimeoutSeconds: &customTimeoutSec,
		}

		start := time.Now()
		h.waitForVolumeDetachIfEnabled(context.Background(), log, "node1", req, nil)
		elapsed := time.Since(start)

		// Should timeout around custom timeout (1s), not default (60s)
		// Allow some tolerance
		if elapsed > 5*time.Second {
			t.Errorf("expected timeout around 1s, but took %v (default 60s was not overridden)", elapsed)
		}
	})

	t.Run("should use default timeout when per-action timeout is nil", func(t *testing.T) {
		t.Parallel()
		log := logrus.New()

		va := &storagev1.VolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{Name: "va1"},
			Spec: storagev1.VolumeAttachmentSpec{
				NodeName: "node1",
				Source:   storagev1.VolumeAttachmentSource{PersistentVolumeName: strPtr("pv1")},
			},
		}

		vaLister, vaIndexer, clientset := newTestVAInformer(t, va)

		h := &DrainNodeHandler{
			log:       log,
			clientset: clientset,
			vaLister:  vaLister,
			vaIndexer: vaIndexer,
			cfg: drainNodeConfig{
				volumeDetachTimeout:      200 * time.Millisecond, // Short default for testing
				volumeDetachPollInterval: 50 * time.Millisecond,
			},
		}

		// Action with nil VolumeDetachTimeoutSeconds (should use default)
		waitForVA := true
		req := &castai.ActionDrainNode{
			NodeName:                   "node1",
			WaitForVolumeDetach:        &waitForVA,
			VolumeDetachTimeoutSeconds: nil,
		}

		start := time.Now()
		h.waitForVolumeDetachIfEnabled(context.Background(), log, "node1", req, nil)
		elapsed := time.Since(start)

		// Should timeout around 200ms (default)
		if elapsed > 2*time.Second {
			t.Errorf("expected timeout around 200ms (default), but took %v", elapsed)
		}
	})
}

func TestShouldWaitForVolumeDetach(t *testing.T) {
	t.Parallel()

	t.Run("returns false when WaitForVolumeDetach is nil", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		h := &DrainNodeHandler{}
		req := &castai.ActionDrainNode{}

		r.False(h.shouldWaitForVolumeDetach(req))
	})

	t.Run("returns true when WaitForVolumeDetach is true", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		h := &DrainNodeHandler{}
		waitForVA := true
		req := &castai.ActionDrainNode{
			WaitForVolumeDetach: &waitForVA,
		}

		r.True(h.shouldWaitForVolumeDetach(req))
	})

	t.Run("returns false when WaitForVolumeDetach is false", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		h := &DrainNodeHandler{}
		waitForVA := false
		req := &castai.ActionDrainNode{
			WaitForVolumeDetach: &waitForVA,
		}

		r.False(h.shouldWaitForVolumeDetach(req))
	})
}

func TestGetVolumeDetachTimeout(t *testing.T) {
	t.Parallel()

	t.Run("returns default when VolumeDetachTimeoutSeconds is nil", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		h := &DrainNodeHandler{
			cfg: drainNodeConfig{
				volumeDetachTimeout: 60 * time.Second,
			},
		}
		req := &castai.ActionDrainNode{}

		r.Equal(60*time.Second, h.getVolumeDetachTimeout(req))
	})

	t.Run("returns default when VolumeDetachTimeoutSeconds is 0", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		h := &DrainNodeHandler{
			cfg: drainNodeConfig{
				volumeDetachTimeout: 60 * time.Second,
			},
		}
		zeroTimeout := 0
		req := &castai.ActionDrainNode{
			VolumeDetachTimeoutSeconds: &zeroTimeout,
		}

		r.Equal(60*time.Second, h.getVolumeDetachTimeout(req))
	})

	t.Run("returns per-action timeout when set", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		h := &DrainNodeHandler{
			cfg: drainNodeConfig{
				volumeDetachTimeout: 60 * time.Second,
			},
		}
		customTimeout := 120
		req := &castai.ActionDrainNode{
			VolumeDetachTimeoutSeconds: &customTimeout,
		}

		r.Equal(120*time.Second, h.getVolumeDetachTimeout(req))
	})
}

func strPtr(s string) *string {
	return &s
}
