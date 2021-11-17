package mock

import (
	"context"

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
	Actions []*castai.ClusterAction
	Logs    []*castai.LogEvent
	Acks    []*mockAck
}

func (m *mockClient) GetActions(ctx context.Context) ([]*castai.ClusterAction, error) {
	return m.Actions, nil
}

func (m *mockClient) SendLogs(ctx context.Context, req *castai.LogEvent) error {
	m.Logs = append(m.Logs, req)
	return nil
}

func (m *mockClient) AckAction(ctx context.Context, actionID string, req *castai.AckClusterActionRequest) error {
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
