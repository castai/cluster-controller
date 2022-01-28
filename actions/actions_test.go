package actions

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/castai/cluster-controller/castai"
	"github.com/castai/cluster-controller/castai/mock"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m, goleak.IgnoreTopFunction("k8s.io/klog/v2.(*loggingT).flushDaemon"))
}

func TestActions(t *testing.T) {
	r := require.New(t)

	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)
	cfg := Config{
		PollWaitInterval: 1 * time.Millisecond,
		PollTimeout:      100 * time.Millisecond,
		AckTimeout:       1 * time.Second,
		AckRetriesCount:  3,
		AckRetryWait:     1 * time.Millisecond,
		ClusterID:        uuid.New().String(),
	}

	newTestService := func(handler ActionHandler, client castai.Client) Service {
		svc := NewService(log, cfg, nil, client, nil)
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
		client := mock.NewMockAPIClient(apiActions)
		handler := &mockAgentActionHandler{}
		svc := newTestService(handler, client)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
		defer func() {
			cancel()
			r.Len(client.Acks, 3)
			ids := make([]string, len(client.Acks))
			for i, ack := range client.Acks {
				ids[i] = ack.ActionID
			}
			sort.Strings(ids)
			r.Equal("a1", ids[0])
			r.Equal("a2", ids[1])
			r.Equal("a3", ids[2])
		}()
		svc.Run(ctx)
	})

	t.Run("continue polling on api error", func(t *testing.T) {
		client := mock.NewMockAPIClient([]*castai.ClusterAction{})
		client.GetActionsErr = errors.New("ups")
		handler := &mockAgentActionHandler{err: errors.New("ups")}
		svc := newTestService(handler, client)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
		defer func() {
			cancel()
			r.Len(client.Acks, 0)
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
		client := mock.NewMockAPIClient(apiActions)
		handler := &mockAgentActionHandler{err: errors.New("ups")}
		svc := newTestService(handler, client)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
		defer func() {
			cancel()
			r.Empty(client.Actions)
			r.Len(client.Acks, 1)
			r.Equal("a1", client.Acks[0].ActionID)
			r.Equal("ups", *client.Acks[0].Err)
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
