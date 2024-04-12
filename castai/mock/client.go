package mock

import (
	"context"
	"sync"

	"github.com/castai/cluster-controller/castai"
)

var _ castai.Client = (*mockClient)(nil)

func NewMockAPIClient(actions []*castai.ClusterAction) *mockClient {
	return &mockClient{Actions: actions}
}

type mockAck struct {
	ActionID string
	Err      *string
}

type mockClient struct {
	Actions        []*castai.ClusterAction
	GetActionsErr  error
	Acks           []*mockAck
	AKSInitDataReq *castai.AKSInitDataRequest

	mu sync.Mutex
}

func (m *mockClient) SendAKSInitData(ctx context.Context, req *castai.AKSInitDataRequest) error {
	m.mu.Lock()
	m.AKSInitDataReq = req
	m.mu.Unlock()
	return nil
}

func (m *mockClient) GetActions(_ context.Context, _ string) ([]*castai.ClusterAction, error) {
	m.mu.Lock()
	actions := m.Actions
	m.mu.Unlock()
	return actions, m.GetActionsErr
}

func (m *mockClient) AckAction(_ context.Context, actionID string, req *castai.AckClusterActionRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.removeAckedActions(actionID)

	m.Acks = append(m.Acks, &mockAck{ActionID: actionID, Err: req.Error})
	return nil
}

func (m *mockClient) removeAckedActions(actionID string) {
	var remaining []*castai.ClusterAction
	for _, action := range m.Actions {
		if action.ID != actionID {
			remaining = append(remaining, action)
		}
	}
	m.Actions = remaining
}
