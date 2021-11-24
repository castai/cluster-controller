package mock

import (
	"context"
	"sync"

	"github.com/castai/cluster-controller/castai"
)

func NewMockAPIClient(actions []*castai.ClusterAction) *mockClient {
	return &mockClient{Actions: actions}
}

type mockAck struct {
	ActionID string
	Err      *string
}

type mockClient struct {
	Actions       []*castai.ClusterAction
	GetActionsErr error
	Logs          []*castai.LogEvent
	Acks          []*mockAck
	mu            sync.Mutex
}

func (m *mockClient) GetActions(_ context.Context) ([]*castai.ClusterAction, error) {
	return m.Actions, m.GetActionsErr
}

func (m *mockClient) SendLogs(_ context.Context, req *castai.LogEvent) error {
	m.mu.Lock()
	m.Logs = append(m.Logs, req)
	m.mu.Unlock()
	return nil
}

func (m *mockClient) AckAction(_ context.Context, actionID string, req *castai.AckClusterActionRequest) error {
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
