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

	"github.com/castai/cluster-controller/actions/types"
	"github.com/castai/cluster-controller/health"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m, goleak.IgnoreTopFunction("k8s.io/klog/v2.(*loggingT).flushDaemon"))
}

func TestActions(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)
	cfg := Config{
		PollWaitInterval: 10 * time.Millisecond,
		PollTimeout:      100 * time.Millisecond,
		AckTimeout:       1 * time.Second,
		AckRetriesCount:  3,
		AckRetryWait:     1 * time.Millisecond,
		ClusterID:        uuid.New().String(),
	}

	newTestService := func(handler ActionHandler, client Client) *service {
		svc := NewService(
			log,
			cfg,
			"1.20.1",
			nil,
			nil,
			client,
			nil,
			health.NewHealthzProvider(health.HealthzCfg{HealthyPollIntervalLimit: cfg.PollTimeout}, log),
		).(*service)
		handlers := svc.actionHandlers
		// Patch handlers with a mock one.
		for k := range handlers {
			handlers[k] = handler
		}
		return svc
	}

	t.Run("poll handle and ack", func(t *testing.T) {
		r := require.New(t)

		apiActions := []*types.ClusterAction{
			{
				ID:        "a1",
				CreatedAt: time.Now(),
				ActionDeleteNode: &types.ActionDeleteNode{
					NodeName: "n1",
				},
			},
			{
				ID:        "a2",
				CreatedAt: time.Now(),
				ActionDrainNode: &types.ActionDrainNode{
					NodeName: "n1",
				},
			},
			{
				ID:        "a3",
				CreatedAt: time.Now(),
				ActionPatchNode: &types.ActionPatchNode{
					NodeName: "n1",
				},
			},
		}
		client := newMockAPIClient(apiActions)
		handler := &mockAgentActionHandler{handleDelay: 2 * time.Millisecond}
		svc := newTestService(handler, client)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer func() {
			cancel()
			svc.startedActionsWg.Wait()

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
		r := require.New(t)

		client := newMockAPIClient([]*types.ClusterAction{})
		client.GetActionsErr = errors.New("ups")
		handler := &mockAgentActionHandler{err: errors.New("ups")}
		svc := newTestService(handler, client)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer func() {
			cancel()
			svc.startedActionsWg.Wait()

			r.Len(client.Acks, 0)
		}()
		svc.Run(ctx)
	})

	t.Run("do not ack action on context canceled error", func(t *testing.T) {
		r := require.New(t)

		apiActions := []*types.ClusterAction{
			{
				ID:        "a1",
				CreatedAt: time.Now(),
				ActionPatchNode: &types.ActionPatchNode{
					NodeName: "n1",
				},
			},
		}
		client := newMockAPIClient(apiActions)
		handler := &mockAgentActionHandler{err: context.Canceled}
		svc := newTestService(handler, client)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer func() {
			cancel()
			svc.startedActionsWg.Wait()

			r.NotEmpty(client.Actions)
			r.Len(client.Acks, 0)
		}()
		svc.Run(ctx)
	})

	t.Run("ack with error when action handler failed", func(t *testing.T) {
		r := require.New(t)

		apiActions := []*types.ClusterAction{
			{
				ID:        "a1",
				CreatedAt: time.Now(),
				ActionPatchNode: &types.ActionPatchNode{
					NodeName: "n1",
				},
			},
		}
		client := newMockAPIClient(apiActions)
		handler := &mockAgentActionHandler{err: errors.New("ups")}
		svc := newTestService(handler, client)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer func() {
			cancel()
			svc.startedActionsWg.Wait()

			r.Empty(client.Actions)
			r.Len(client.Acks, 1)
			r.Equal("a1", client.Acks[0].ActionID)
			r.Equal("handling action *ActionPatchNode: ups", *client.Acks[0].Err)
		}()
		svc.Run(ctx)
	})

	t.Run("ack with error when action handler panic occurred", func(t *testing.T) {
		r := require.New(t)

		apiActions := []*types.ClusterAction{
			{
				ID:        "a1",
				CreatedAt: time.Now(),
				ActionPatchNode: &types.ActionPatchNode{
					NodeName: "n1",
				},
			},
		}
		client := newMockAPIClient(apiActions)
		handler := &mockAgentActionHandler{panicErr: errors.New("ups")}
		svc := newTestService(handler, client)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer func() {
			cancel()
			svc.startedActionsWg.Wait()

			r.Empty(client.Actions)
			r.Len(client.Acks, 1)
			r.Equal("a1", client.Acks[0].ActionID)
			r.Contains(*client.Acks[0].Err, "panic: handling action *ActionPatchNode: ups: goroutine")
		}()
		svc.Run(ctx)
	})
}

type mockAgentActionHandler struct {
	err         error
	panicErr    error
	handleDelay time.Duration
}

func (m *mockAgentActionHandler) Handle(ctx context.Context, action *types.ClusterAction) error {
	time.Sleep(m.handleDelay)
	if m.panicErr != nil {
		panic(m.panicErr)
	}
	return m.err
}
