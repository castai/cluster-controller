package mock

import (
	"context"
	"sync"

	castai2 "github.com/castai/cluster-controller/internal/castai"
)

var _ castai2.ActionsClient = (*mockClient)(nil)

func NewMockAPIClient(actions []*castai2.ClusterAction) *mockClient {
	return &mockClient{Actions: actions}
}

type mockAck struct {
	ActionID string
	Err      *string
}

type mockClient struct {
	Actions        []*castai2.ClusterAction
	GetActionsErr  error
	Acks           []*mockAck
	AKSInitDataReq *castai2.AKSInitDataRequest

	mu sync.Mutex
}

func (m *mockClient) SendAKSInitData(ctx context.Context, req *castai2.AKSInitDataRequest) error {
	m.mu.Lock()
	m.AKSInitDataReq = req
	m.mu.Unlock()
	return nil
}

func (m *mockClient) GetActions(_ context.Context, _ string) ([]*castai2.ClusterAction, error) {
	m.mu.Lock()
	actions := m.Actions
	m.mu.Unlock()
	return actions, m.GetActionsErr
}

func (m *mockClient) AckAction(_ context.Context, actionID string, req *castai2.AckClusterActionRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.removeAckedActions(actionID)

	m.Acks = append(m.Acks, &mockAck{ActionID: actionID, Err: req.Error})
	return nil
}

func (m *mockClient) removeAckedActions(actionID string) {
	var remaining []*castai2.ClusterAction
	for _, action := range m.Actions {
		if action.ID != actionID {
			remaining = append(remaining, action)
		}
	}
	m.Actions = remaining
}
