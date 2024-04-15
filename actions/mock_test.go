package actions

import (
	"context"
	"sync"

	"github.com/castai/cluster-controller/actions/types"
)

func newMockAPIClient(actions []*types.ClusterAction) *mockClient {
	return &mockClient{Actions: actions}
}

type mockAck struct {
	ActionID string
	Err      *string
}

type mockClient struct {
	Actions        []*types.ClusterAction
	GetActionsErr  error
	Acks           []*mockAck
	AKSInitDataReq *types.AKSInitDataRequest

	mu sync.Mutex
}

func (m *mockClient) SendAKSInitData(ctx context.Context, req *types.AKSInitDataRequest) error {
	m.mu.Lock()
	m.AKSInitDataReq = req
	m.mu.Unlock()
	return nil
}

func (m *mockClient) GetActions(_ context.Context, _ string) ([]*types.ClusterAction, error) {
	m.mu.Lock()
	actions := m.Actions
	m.mu.Unlock()
	return actions, m.GetActionsErr
}

func (m *mockClient) AckAction(_ context.Context, actionID string, req *types.AckClusterActionRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.removeAckedActions(actionID)

	m.Acks = append(m.Acks, &mockAck{ActionID: actionID, Err: req.Error})
	return nil
}

func (m *mockClient) removeAckedActions(actionID string) {
	var remaining []*types.ClusterAction
	for _, action := range m.Actions {
		if action.ID != actionID {
			remaining = append(remaining, action)
		}
	}
	m.Actions = remaining
}
