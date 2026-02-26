package actions

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/castai/cluster-controller/internal/castai"
	"github.com/castai/cluster-controller/internal/informer"
	"github.com/castai/cluster-controller/internal/k8s"
	mock_k8s "github.com/castai/cluster-controller/internal/k8s/mock"
	mock_nodes "github.com/castai/cluster-controller/internal/nodes/mock"
	"github.com/castai/cluster-controller/internal/volume"
)

// stubNodeInformer is a simple test implementation of informer.NodeInformer.
type stubNodeInformer struct {
	node *v1.Node
	err  error
}

func (s *stubNodeInformer) Get(_ string) (*v1.Node, error) {
	return s.node, s.err
}

func (s *stubNodeInformer) List() ([]*v1.Node, error) {
	return nil, nil
}

func (s *stubNodeInformer) Wait(_ context.Context, _ string, _ informer.Predicate) chan error {
	return make(chan error, 1)
}

func newDrainTestNode() *v1.Node {
	return &v1.Node{
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
}

// nolint
func newActionDrainNodeWithVolumeDetach(name, nID, pID string, drainTimeoutSeconds int, force bool) *castai.ClusterAction {
	action := newActionDrainNode(name, nID, pID, drainTimeoutSeconds, force)
	waitForVA := true
	action.ActionDrainNode.WaitForVolumeDetach = &waitForVA
	return action
}

func TestDrainNodeInformerHandler_Handle(t *testing.T) {
	t.Parallel()

	type setupFn func(mockClient *mock_k8s.MockClient, mockDrainer *mock_nodes.MockDrainer)

	tests := []struct {
		name              string
		action            *castai.ClusterAction
		cfg               drainNodeConfig
		nodeInformer      informer.NodeInformer
		vaWaiter          volume.DetachmentWaiter
		setup             setupFn
		wantErrIs         error
		wantErrorContains string
		wantVolumeWait    bool
	}{
		{
			name:         "nil action returns error",
			nodeInformer: &stubNodeInformer{},
			wantErrIs:    k8s.ErrAction,
		},
		{
			name: "wrong action type returns error",
			action: &castai.ClusterAction{
				ActionDeleteNode: &castai.ActionDeleteNode{},
			},
			nodeInformer: &stubNodeInformer{},
			wantErrIs:    k8s.ErrAction,
		},
		{
			name:         "empty node name returns error",
			action:       newActionDrainNode("", nodeID, providerID, 1, true),
			nodeInformer: &stubNodeInformer{},
			wantErrIs:    k8s.ErrAction,
		},
		{
			name:         "empty node ID and provider ID returns error",
			action:       newActionDrainNode(nodeName, "", "", 1, true),
			nodeInformer: &stubNodeInformer{},
			wantErrIs:    k8s.ErrAction,
		},
		{
			name:   "node found in informer cache but both IDs do not match, skip drain",
			action: newActionDrainNode(nodeName, "another-node-id", "another-provider-id", 1, true),
			nodeInformer: &stubNodeInformer{
				node: newDrainTestNode(),
			},
		},
		{
			name:   "node found in informer cache, nodeID matches but providerID does not, skip drain",
			action: newActionDrainNode(nodeName, nodeID, "another-provider-id", 1, true),
			nodeInformer: &stubNodeInformer{
				node: newDrainTestNode(),
			},
		},
		{
			name:   "node found in informer cache, providerID matches but nodeID does not, skip drain",
			action: newActionDrainNode(nodeName, "another-node-id", providerID, 1, true),
			nodeInformer: &stubNodeInformer{
				node: newDrainTestNode(),
			},
		},
		{
			name:   "informer returns unexpected non-NotFound error, propagate error",
			action: newActionDrainNode(nodeName, nodeID, providerID, 1, true),
			nodeInformer: &stubNodeInformer{
				err: errors.New("internal informer error"),
			},
			wantErrorContains: "internal informer error",
		},
		{
			name:         "informer returns nil node with no error, skip drain",
			action:       newActionDrainNode(nodeName, nodeID, providerID, 1, true),
			nodeInformer: &stubNodeInformer{},
		},
		{
			name:   "node not found in informer, fallback to API returns not found, skip drain",
			action: newActionDrainNode(nodeName, nodeID, providerID, 1, true),
			nodeInformer: &stubNodeInformer{
				err: k8serrors.NewNotFound(v1.Resource("nodes"), nodeName),
			},
			setup: func(mockClient *mock_k8s.MockClient, _ *mock_nodes.MockDrainer) {
				mockClient.EXPECT().
					GetNodeByIDs(gomock.Any(), nodeName, nodeID, providerID).
					Return(nil, k8s.ErrNodeNotFound)
			},
		},
		{
			name:   "node not found in informer, fallback to API IDs do not match, skip drain",
			action: newActionDrainNode(nodeName, nodeID, providerID, 1, true),
			nodeInformer: &stubNodeInformer{
				err: k8serrors.NewNotFound(v1.Resource("nodes"), nodeName),
			},
			setup: func(mockClient *mock_k8s.MockClient, _ *mock_nodes.MockDrainer) {
				mockClient.EXPECT().
					GetNodeByIDs(gomock.Any(), nodeName, nodeID, providerID).
					Return(nil, k8s.ErrNodeDoesNotMatch)
			},
		},
		{
			name:   "node not found in informer, API returns unexpected error, propagate error",
			action: newActionDrainNode(nodeName, nodeID, providerID, 1, true),
			nodeInformer: &stubNodeInformer{
				err: k8serrors.NewNotFound(v1.Resource("nodes"), nodeName),
			},
			setup: func(mockClient *mock_k8s.MockClient, _ *mock_nodes.MockDrainer) {
				mockClient.EXPECT().
					GetNodeByIDs(gomock.Any(), nodeName, nodeID, providerID).
					Return(nil, errors.New("api connectivity error"))
			},
			wantErrorContains: "failed to get node from API",
		},
		{
			name:   "node not found in informer, found via API fallback, drain succeeds",
			action: newActionDrainNode(nodeName, nodeID, providerID, 10, true),
			nodeInformer: &stubNodeInformer{
				err: k8serrors.NewNotFound(v1.Resource("nodes"), nodeName),
			},
			cfg: drainNodeConfig{
				podsDeleteTimeout: 10 * time.Second,
			},
			setup: func(mockClient *mock_k8s.MockClient, mockDrainer *mock_nodes.MockDrainer) {
				mockClient.EXPECT().
					GetNodeByIDs(gomock.Any(), nodeName, nodeID, providerID).
					Return(newDrainTestNode(), nil)
				mockClient.EXPECT().CordonNode(gomock.Any(), gomock.Any()).Return(nil)
				mockDrainer.EXPECT().Evict(gomock.Any(), gomock.Any()).Return(nil, nil)
			},
		},
		{
			name:   "cordon node fails, return error",
			action: newActionDrainNode(nodeName, nodeID, providerID, 10, true),
			nodeInformer: &stubNodeInformer{
				node: newDrainTestNode(),
			},
			setup: func(mockClient *mock_k8s.MockClient, _ *mock_nodes.MockDrainer) {
				mockClient.EXPECT().CordonNode(gomock.Any(), gomock.Any()).
					Return(errors.New("cordon failed"))
			},
			wantErrorContains: "cordoning node",
		},
		{
			name:   "drain node successfully via eviction",
			action: newActionDrainNode(nodeName, nodeID, providerID, 10, true),
			nodeInformer: &stubNodeInformer{
				node: newDrainTestNode(),
			},
			cfg: drainNodeConfig{
				podsDeleteTimeout: 10 * time.Second,
			},
			setup: func(mockClient *mock_k8s.MockClient, mockDrainer *mock_nodes.MockDrainer) {
				mockClient.EXPECT().CordonNode(gomock.Any(), gomock.Any()).Return(nil)
				mockDrainer.EXPECT().Evict(gomock.Any(), gomock.Any()).Return(nil, nil)
			},
		},
		{
			name:   "drain node successfully via eviction with volume detach wait",
			action: newActionDrainNodeWithVolumeDetach(nodeName, nodeID, providerID, 10, true),
			nodeInformer: &stubNodeInformer{
				node: newDrainTestNode(),
			},
			cfg: drainNodeConfig{
				podsDeleteTimeout: 10 * time.Second,
			},
			vaWaiter: &mockVolumeDetachmentWaiter{},
			setup: func(mockClient *mock_k8s.MockClient, mockDrainer *mock_nodes.MockDrainer) {
				mockClient.EXPECT().CordonNode(gomock.Any(), gomock.Any()).Return(nil)
				mockDrainer.EXPECT().Evict(gomock.Any(), gomock.Any()).Return(nil, nil)
			},
			wantVolumeWait: true,
		},
		{
			name:   "eviction fails with pod failure, force=false returns error",
			action: newActionDrainNode(nodeName, nodeID, providerID, 10, false),
			nodeInformer: &stubNodeInformer{
				node: newDrainTestNode(),
			},
			setup: func(mockClient *mock_k8s.MockClient, mockDrainer *mock_nodes.MockDrainer) {
				mockClient.EXPECT().CordonNode(gomock.Any(), gomock.Any()).Return(nil)
				mockDrainer.EXPECT().Evict(gomock.Any(), gomock.Any()).
					Return(nil, &k8s.PodFailedActionError{Action: "evict", Errors: []error{errors.New("evict failed")}})
			},
			wantErrorContains: "node failed to drain via graceful eviction",
		},
		{
			name:   "eviction timeout, force=false returns error",
			action: newActionDrainNode(nodeName, nodeID, providerID, 0, false),
			nodeInformer: &stubNodeInformer{
				node: newDrainTestNode(),
			},
			setup: func(mockClient *mock_k8s.MockClient, mockDrainer *mock_nodes.MockDrainer) {
				mockClient.EXPECT().CordonNode(gomock.Any(), gomock.Any()).Return(nil)
				mockDrainer.EXPECT().Evict(gomock.Any(), gomock.Any()).
					Return(nil, context.DeadlineExceeded)
			},
			wantErrIs:         context.DeadlineExceeded,
			wantErrorContains: "node failed to drain via graceful eviction",
		},
		{
			name:   "eviction timeout, force=true, force drain succeeds",
			action: newActionDrainNode(nodeName, nodeID, providerID, 0, true),
			nodeInformer: &stubNodeInformer{
				node: newDrainTestNode(),
			},
			cfg: drainNodeConfig{
				podsDeleteTimeout: 10 * time.Second,
			},
			setup: func(mockClient *mock_k8s.MockClient, mockDrainer *mock_nodes.MockDrainer) {
				mockClient.EXPECT().CordonNode(gomock.Any(), gomock.Any()).Return(nil)
				mockDrainer.EXPECT().Evict(gomock.Any(), gomock.Any()).
					Return(nil, context.DeadlineExceeded)
				mockDrainer.EXPECT().Drain(gomock.Any(), gomock.Any()).Return(nil, nil)
			},
		},
		{
			name:   "eviction fails with pod failure, force=true, force drain succeeds",
			action: newActionDrainNode(nodeName, nodeID, providerID, 10, true),
			nodeInformer: &stubNodeInformer{
				node: newDrainTestNode(),
			},
			cfg: drainNodeConfig{
				podsDeleteTimeout: 10 * time.Second,
			},
			setup: func(mockClient *mock_k8s.MockClient, mockDrainer *mock_nodes.MockDrainer) {
				mockClient.EXPECT().CordonNode(gomock.Any(), gomock.Any()).Return(nil)
				mockDrainer.EXPECT().Evict(gomock.Any(), gomock.Any()).
					Return(nil, &k8s.PodFailedActionError{Action: "evict"})
				mockDrainer.EXPECT().Drain(gomock.Any(), gomock.Any()).Return(nil, nil)
			},
		},
		{
			name:   "force drain both attempts time out, return error",
			action: newActionDrainNode(nodeName, nodeID, providerID, 0, true),
			nodeInformer: &stubNodeInformer{
				node: newDrainTestNode(),
			},
			cfg: drainNodeConfig{
				podsDeleteTimeout: 10 * time.Second,
			},
			setup: func(mockClient *mock_k8s.MockClient, mockDrainer *mock_nodes.MockDrainer) {
				mockClient.EXPECT().CordonNode(gomock.Any(), gomock.Any()).Return(nil)
				mockDrainer.EXPECT().Evict(gomock.Any(), gomock.Any()).
					Return(nil, context.DeadlineExceeded)
				first := mockDrainer.EXPECT().Drain(gomock.Any(), gomock.Any()).
					Return(nil, context.DeadlineExceeded)
				second := mockDrainer.EXPECT().Drain(gomock.Any(), gomock.Any()).
					Return(nil, context.DeadlineExceeded)
				gomock.InOrder(first, second)
			},
			wantErrIs: context.DeadlineExceeded,
		},
		{
			name:   "force drain first attempt fails with pod deletion failure, second attempt succeeds",
			action: newActionDrainNode(nodeName, nodeID, providerID, 0, true),
			nodeInformer: &stubNodeInformer{
				node: newDrainTestNode(),
			},
			cfg: drainNodeConfig{
				podsDeleteTimeout: 10 * time.Second,
			},
			setup: func(mockClient *mock_k8s.MockClient, mockDrainer *mock_nodes.MockDrainer) {
				mockClient.EXPECT().CordonNode(gomock.Any(), gomock.Any()).Return(nil)
				mockDrainer.EXPECT().Evict(gomock.Any(), gomock.Any()).
					Return(nil, context.DeadlineExceeded)
				first := mockDrainer.EXPECT().Drain(gomock.Any(), gomock.Any()).
					Return(nil, &k8s.PodFailedActionError{Action: "delete"})
				second := mockDrainer.EXPECT().Drain(gomock.Any(), gomock.Any()).
					Return(nil, nil)
				gomock.InOrder(first, second)
			},
		},
		{
			name:   "force drain first attempt times out, second attempt with forced grace period succeeds",
			action: newActionDrainNode(nodeName, nodeID, providerID, 0, true),
			nodeInformer: &stubNodeInformer{
				node: newDrainTestNode(),
			},
			cfg: drainNodeConfig{
				podsDeleteTimeout: 10 * time.Second,
			},
			setup: func(mockClient *mock_k8s.MockClient, mockDrainer *mock_nodes.MockDrainer) {
				mockClient.EXPECT().CordonNode(gomock.Any(), gomock.Any()).Return(nil)
				mockDrainer.EXPECT().Evict(gomock.Any(), gomock.Any()).
					Return(nil, context.DeadlineExceeded)
				first := mockDrainer.EXPECT().Drain(gomock.Any(), gomock.Any()).
					Return(nil, context.DeadlineExceeded)
				second := mockDrainer.EXPECT().Drain(gomock.Any(), gomock.Any()).
					Return(nil, nil)
				gomock.InOrder(first, second)
			},
		},
		{
			name:   "eviction returns unrecoverable error, force=true does not proceed to force drain",
			action: newActionDrainNode(nodeName, nodeID, providerID, 10, true),
			nodeInformer: &stubNodeInformer{
				node: newDrainTestNode(),
			},
			setup: func(mockClient *mock_k8s.MockClient, mockDrainer *mock_nodes.MockDrainer) {
				mockClient.EXPECT().CordonNode(gomock.Any(), gomock.Any()).Return(nil)
				mockDrainer.EXPECT().Evict(gomock.Any(), gomock.Any()).
					Return(nil, errors.New("connection refused"))
			},
			wantErrorContains: "evicting node pods",
		},
		{
			name:   "force drain fails with unrecoverable error returns error",
			action: newActionDrainNode(nodeName, nodeID, providerID, 0, true),
			nodeInformer: &stubNodeInformer{
				node: newDrainTestNode(),
			},
			cfg: drainNodeConfig{
				podsDeleteTimeout: 10 * time.Second,
			},
			setup: func(mockClient *mock_k8s.MockClient, mockDrainer *mock_nodes.MockDrainer) {
				mockClient.EXPECT().CordonNode(gomock.Any(), gomock.Any()).Return(nil)
				mockDrainer.EXPECT().Evict(gomock.Any(), gomock.Any()).
					Return(nil, context.DeadlineExceeded)
				mockDrainer.EXPECT().Drain(gomock.Any(), gomock.Any()).
					Return(nil, errors.New("internal error"))
			},
			wantErrorContains: "forcefully deleting pods",
		},
		{
			name:   "force drain succeeds with volume detach wait enabled",
			action: newActionDrainNodeWithVolumeDetach(nodeName, nodeID, providerID, 0, true),
			nodeInformer: &stubNodeInformer{
				node: newDrainTestNode(),
			},
			cfg: drainNodeConfig{
				podsDeleteTimeout: 10 * time.Second,
			},
			vaWaiter: &mockVolumeDetachmentWaiter{},
			setup: func(mockClient *mock_k8s.MockClient, mockDrainer *mock_nodes.MockDrainer) {
				mockClient.EXPECT().CordonNode(gomock.Any(), gomock.Any()).Return(nil)
				mockDrainer.EXPECT().Evict(gomock.Any(), gomock.Any()).
					Return(nil, context.DeadlineExceeded)
				mockDrainer.EXPECT().Drain(gomock.Any(), gomock.Any()).Return(nil, nil)
			},
			wantVolumeWait: true,
		},
		{
			name:   "volume detach wait not called when vaWaiter is nil even if enabled in request",
			action: newActionDrainNodeWithVolumeDetach(nodeName, nodeID, providerID, 10, true),
			nodeInformer: &stubNodeInformer{
				node: newDrainTestNode(),
			},
			cfg: drainNodeConfig{
				podsDeleteTimeout: 10 * time.Second,
			},
			// vaWaiter intentionally left nil
			setup: func(mockClient *mock_k8s.MockClient, mockDrainer *mock_nodes.MockDrainer) {
				mockClient.EXPECT().CordonNode(gomock.Any(), gomock.Any()).Return(nil)
				mockDrainer.EXPECT().Evict(gomock.Any(), gomock.Any()).Return(nil, nil)
			},
		},
		{
			name: "volume detach wait called with custom timeout",
			action: func() *castai.ClusterAction {
				a := newActionDrainNodeWithVolumeDetach(nodeName, nodeID, providerID, 10, true)
				timeout := 120
				a.ActionDrainNode.VolumeDetachTimeoutSeconds = &timeout
				return a
			}(),
			nodeInformer: &stubNodeInformer{
				node: newDrainTestNode(),
			},
			cfg: drainNodeConfig{
				podsDeleteTimeout: 10 * time.Second,
			},
			vaWaiter: &mockVolumeDetachmentWaiter{},
			setup: func(mockClient *mock_k8s.MockClient, mockDrainer *mock_nodes.MockDrainer) {
				mockClient.EXPECT().CordonNode(gomock.Any(), gomock.Any()).Return(nil)
				mockDrainer.EXPECT().Evict(gomock.Any(), gomock.Any()).Return(nil, nil)
			},
			wantVolumeWait: true,
		},
		{
			name:   "volume detach wait error is logged but not returned to caller",
			action: newActionDrainNodeWithVolumeDetach(nodeName, nodeID, providerID, 10, true),
			nodeInformer: &stubNodeInformer{
				node: newDrainTestNode(),
			},
			cfg: drainNodeConfig{
				podsDeleteTimeout: 10 * time.Second,
			},
			vaWaiter: &mockVolumeDetachmentWaiter{waitErr: errors.New("detach failed")},
			setup: func(mockClient *mock_k8s.MockClient, mockDrainer *mock_nodes.MockDrainer) {
				mockClient.EXPECT().CordonNode(gomock.Any(), gomock.Any()).Return(nil)
				mockDrainer.EXPECT().Evict(gomock.Any(), gomock.Any()).Return(nil, nil)
			},
			wantVolumeWait: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			mockClient := mock_k8s.NewMockClient(ctrl)
			mockDrainer := mock_nodes.NewMockDrainer(ctrl)

			if tt.setup != nil {
				tt.setup(mockClient, mockDrainer)
			}

			h := &DrainNodeInfomerHandler{
				log:          logrus.New(),
				nodeInformer: tt.nodeInformer,
				nodeManager:  mockDrainer,
				client:       mockClient,
				cfg:          tt.cfg,
				vaWaiter:     tt.vaWaiter,
			}

			err := h.Handle(context.Background(), tt.action)

			switch {
			case tt.wantErrIs != nil:
				require.ErrorIs(t, err, tt.wantErrIs)
				if tt.wantErrorContains != "" {
					require.ErrorContains(t, err, tt.wantErrorContains)
				}
			case tt.wantErrorContains != "":
				require.Error(t, err)
				require.ErrorContains(t, err, tt.wantErrorContains)
			default:
				require.NoError(t, err)
			}

			if tt.wantVolumeWait {
				waiter, ok := tt.vaWaiter.(*mockVolumeDetachmentWaiter)
				require.True(t, ok)
				require.True(t, waiter.waitCalled, "expected volume detachment Wait to be called")
			}
		})
	}
}
