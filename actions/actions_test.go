package actions

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"go.uber.org/goleak"

	mock_actions "github.com/castai/cluster-controller/actions/mock"
	"github.com/castai/cluster-controller/health"
	"github.com/castai/cluster-controller/types"
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

	newTestService := func(handler actionHandler, client Client) *Service {
		svc := NewService(
			log,
			cfg,
			"1.20.1",
			nil,
			nil,
			client,
			nil,
			health.NewHealthzProvider(health.HealthzCfg{HealthyPollIntervalLimit: cfg.PollTimeout}, log),
		)
		handlers := svc.actionHandlers
		// Patch handlers with a mock one.
		for k := range handlers {
			handlers[k] = handler
		}
		return svc
	}

	t.Run("poll handle and ack", func(t *testing.T) {
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
		client := mock_actions.NewMockClient(gomock.NewController(t))
		client.EXPECT().GetActions(gomock.Any(), gomock.Any()).Return(apiActions, nil)
		client.EXPECT().AckAction(gomock.Any(), "a1", nil).Return(nil)
		client.EXPECT().AckAction(gomock.Any(), "a2", nil).Return(nil)
		client.EXPECT().AckAction(gomock.Any(), "a3", nil).Return(nil)
		handler := &mockAgentActionHandler{handleDelay: 2 * time.Millisecond}
		svc := newTestService(handler, client)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer func() {
			cancel()
			svc.startedActionsWg.Wait()

		}()
		_ = svc.doWork(ctx)
	})

	t.Run("continue polling on api error", func(t *testing.T) {
		client := mock_actions.NewMockClient(gomock.NewController(t))
		client.EXPECT().GetActions(gomock.Any(), gomock.Any()).Return(nil, errors.New("ups"))
		handler := &mockAgentActionHandler{err: errors.New("ups")}
		svc := newTestService(handler, client)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer func() {
			cancel()
			svc.startedActionsWg.Wait()
		}()
		_ = svc.doWork(ctx)
	})

	t.Run("do not ack action on context canceled error", func(t *testing.T) {
		apiActions := []*types.ClusterAction{
			{
				ID:        "a1",
				CreatedAt: time.Now(),
				ActionPatchNode: &types.ActionPatchNode{
					NodeName: "n1",
				},
			},
		}
		client := mock_actions.NewMockClient(gomock.NewController(t))
		client.EXPECT().GetActions(gomock.Any(), gomock.Any()).Return(apiActions, nil)
		handler := &mockAgentActionHandler{err: context.Canceled}
		svc := newTestService(handler, client)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer func() {
			cancel()
			svc.startedActionsWg.Wait()
		}()
		_ = svc.doWork(ctx)
	})

	t.Run("ack with error when action handler failed", func(t *testing.T) {
		apiActions := []*types.ClusterAction{
			{
				ID:        "a1",
				CreatedAt: time.Now(),
				ActionPatchNode: &types.ActionPatchNode{
					NodeName: "n1",
				},
			},
		}
		client := mock_actions.NewMockClient(gomock.NewController(t))
		client.EXPECT().GetActions(gomock.Any(), gomock.Any()).Return(apiActions, nil)
		client.EXPECT().AckAction(gomock.Any(), "a1", gomock.Not(gomock.Nil())).Return(nil)
		handler := &mockAgentActionHandler{err: errors.New("ups")}
		svc := newTestService(handler, client)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer func() {
			cancel()
			svc.startedActionsWg.Wait()
		}()
		_ = svc.doWork(ctx)
	})

	t.Run("ack with error when action handler panic occurred", func(t *testing.T) {
		apiActions := []*types.ClusterAction{
			{
				ID:        "a1",
				CreatedAt: time.Now(),
				ActionPatchNode: &types.ActionPatchNode{
					NodeName: "n1",
				},
			},
		}
		client := mock_actions.NewMockClient(gomock.NewController(t))
		client.EXPECT().GetActions(gomock.Any(), gomock.Any()).Return(apiActions, nil)
		client.EXPECT().AckAction(gomock.Any(), "a1", gomock.Not(gomock.Nil())).Return(nil)
		handler := &mockAgentActionHandler{panicErr: errors.New("ups")}
		svc := newTestService(handler, client)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer func() {
			cancel()
			svc.startedActionsWg.Wait()
		}()
		_ = svc.doWork(ctx)
	})
}

type mockAgentActionHandler struct {
	err         error
	panicErr    error
	handleDelay time.Duration
}

func (m *mockAgentActionHandler) Handle(_ context.Context, _ *types.ClusterAction) error {
	time.Sleep(m.handleDelay)
	if m.panicErr != nil {
		panic(m.panicErr)
	}
	return m.err
}
