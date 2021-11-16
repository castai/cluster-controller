package actions

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/castai/cluster-controller/castai"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m, goleak.IgnoreTopFunction("k8s.io/klog/v2.(*loggingT).flushDaemon"))
}

func TestActions(t *testing.T) {
	r := require.New(t)

	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)
	cfg := Config{
		PollInterval:    1 * time.Millisecond,
		PollTimeout:     100 * time.Millisecond,
		AckTimeout:      1 * time.Second,
		AckRetriesCount: 3,
		AckRetryWait:    1 * time.Millisecond,
		ClusterID:       uuid.New().String(),
	}

	newTestService := func(handler ActionHandler, telemetryClient castai.Client) Service {
		svc := NewService(log, cfg, nil, telemetryClient)
		handlers := svc.(*service).actionHandlers
		// Patch handlers with a mock one.
		for k := range handlers {
			handlers[k] = handler
		}
		return svc
	}

	t.Run("poll, handle and ack", func(t *testing.T) {
		apiActions := []*castai.ClusterAction{
			{
				ID:        "a1",
				CreatedAt: time.Now(),
				ActionDeleteNode: &castai.ActionDeleteNode{
					NodeName: "n1",
				},
			},
			{
				ID:        "a2",
				CreatedAt: time.Now(),
				ActionDrainNode: &castai.ActionDrainNode{
					NodeName: "n1",
				},
			},
			{
				ID:        "a3",
				CreatedAt: time.Now(),
				ActionPatchNode: &castai.ActionPatchNode{
					NodeName: "n1",
				},
			},
		}
		telemetryClient := newMockTelemetryClient(apiActions)
		handler := &mockAgentActionHandler{}
		svc := newTestService(handler, telemetryClient)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
		defer func() {
			cancel()
			r.Len(telemetryClient.acks, 3)
			r.Equal("a1", telemetryClient.acks[0].actionID)
			r.Equal("a2", telemetryClient.acks[1].actionID)
			r.Equal("a3", telemetryClient.acks[2].actionID)
		}()
		svc.Run(ctx)
	})

	t.Run("ack with error when action handler failed", func(t *testing.T) {
		apiActions := []*castai.ClusterAction{
			{
				ID:        "a1",
				CreatedAt: time.Now(),
				ActionPatchNode: &castai.ActionPatchNode{
					NodeName: "n1",
				},
			},
		}
		telemetryClient := newMockTelemetryClient(apiActions)
		handler := &mockAgentActionHandler{err: errors.New("ups")}
		svc := newTestService(handler, telemetryClient)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
		defer func() {
			cancel()
			r.Empty(telemetryClient.actions)
			r.Len(telemetryClient.acks, 1)
			r.Equal("a1", telemetryClient.acks[0].actionID)
			r.Equal("ups", *telemetryClient.acks[0].err)
		}()
		svc.Run(ctx)
	})
}

type mockAgentActionHandler struct {
	err error
}

func (m *mockAgentActionHandler) Handle(ctx context.Context, data interface{}) error {
	return m.err
}

func newMockTelemetryClient(actions []*castai.ClusterAction) *mockTelemetryClient {
	return &mockTelemetryClient{actions: actions}
}

type mockAck struct {
	actionID string
	err      *string
}

type mockTelemetryClient struct {
	actions []*castai.ClusterAction
	acks    []*mockAck
}

func (m *mockTelemetryClient) GetActions(ctx context.Context, clusterID string) ([]*castai.ClusterAction, error) {
	return m.actions, nil
}

func (m *mockTelemetryClient) AckAction(ctx context.Context, clusterID, actionID string, req *castai.AckClusterActionRequest) error {
	m.removeAckedActions(actionID)

	m.acks = append(m.acks, &mockAck{actionID: actionID, err: req.Error})
	return nil
}

func (m *mockTelemetryClient) removeAckedActions(actionID string) {
	var remaining []*castai.ClusterAction
	for _, action := range m.actions {
		if action.ID != actionID {
			remaining = append(remaining, action)
		}
	}
	m.actions = remaining
}
