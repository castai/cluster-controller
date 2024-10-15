package controller

import (
	"context"
	"github.com/castai/cluster-controller/health"
	mock_castai "github.com/castai/cluster-controller/internal/castai/mock"
	"github.com/castai/cluster-controller/internal/types"
	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"testing"
	"time"
)

func TestController_Run(t *testing.T) {
	pollTimeout := 100 * time.Millisecond
	type fields struct {
		cfg                  Config
		tuneMockCastAIClient func(m *mock_castai.MockCastAIClient)
		k8sVersion           string
	}
	tests := []struct {
		name   string
		fields fields
	}{
		{
			name: "poll handle and ack",
			fields: fields{
				cfg: Config{
					PollWaitInterval: 10 * time.Millisecond,
					PollTimeout:      pollTimeout,
					AckTimeout:       1 * time.Second,
					AckRetriesCount:  3,
					AckRetryWait:     1 * time.Millisecond,
					ClusterID:        uuid.New().String(),
				},
				k8sVersion: "1.20.1",
				tuneMockCastAIClient: func(m *mock_castai.MockCastAIClient) {
					m.EXPECT().GetActions(gomock.Any(), gomock.Any()).Return([]*types.ClusterAction{
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
					}, nil)
				},
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := gomock.NewController(t)
			defer m.Finish()
			client := mock_castai.NewMockCastAIClient(m)
			if tt.fields.tuneMockCastAIClient != nil {
				tt.fields.tuneMockCastAIClient(client)
			}
			s := NewService(
				logrus.New(),
				tt.fields.cfg,
				tt.fields.k8sVersion,
				nil,
				nil,
				client,
				nil,
				health.NewHealthzProvider(health.HealthzCfg{HealthyPollIntervalLimit: pollTimeout}, logrus.New()))
			s.Run(context.Background())
		})
	}
}

//
//func TestMain(m *testing.M) {
//	goleak.VerifyTestMain(m, goleak.IgnoreTopFunction("k8s.io/klog/v2.(*loggingT).flushDaemon"))
//}
//
//func TestActions(t *testing.T) {
//	log := logrus.New()
//	log.SetLevel(logrus.DebugLevel)
//	cfg := Config{
//		PollWaitInterval: 10 * time.Millisecond,
//		PollTimeout:      100 * time.Millisecond,
//		AckTimeout:       1 * time.Second,
//		AckRetriesCount:  3,
//		AckRetryWait:     1 * time.Millisecond,
//		ClusterID:        uuid.New().String(),
//	}
//
//	newTestService := func(handler actions.ActionHandler, client castai.CastAIClient) Service {
//		svc := NewService(
//			log,
//			cfg,
//			"1.20.1",
//			nil,
//			nil,
//			client,
//			nil,
//			health.NewHealthzProvider(health.HealthzCfg{HealthyPollIntervalLimit: cfg.PollTimeout}, log),
//		).(*Controller)
//		// Patch handlers with a mock one.
//		for k := range svc.actionHandlers {
//			svc.actionHandlers[k] = handler
//		}
//		return svc
//	}
//
//	t.Run("poll handle and ack", func(t *testing.T) {
//		r := require.New(t)
//
//		apiActions := []*castai.ClusterAction{
//			{
//				ID:        "a1",
//				CreatedAt: time.Now(),
//				ActionDeleteNode: &castai.ActionDeleteNode{
//					NodeName: "n1",
//				},
//			},
//			{
//				ID:        "a2",
//				CreatedAt: time.Now(),
//				ActionDrainNode: &castai.ActionDrainNode{
//					NodeName: "n1",
//				},
//			},
//			{
//				ID:        "a3",
//				CreatedAt: time.Now(),
//				ActionPatchNode: &castai.ActionPatchNode{
//					NodeName: "n1",
//				},
//			},
//		}
//		client := mock.NewMockAPIClient(apiActions)
//		handler := &mockAgentActionHandler{handleDelay: 2 * time.Millisecond}
//		svc := newTestService(handler, client)
//		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
//		defer func() {
//			cancel()
//			svc.startedActionsWg.Wait()
//
//			r.Len(client.Acks, 3)
//			ids := make([]string, len(client.Acks))
//			for i, ack := range client.Acks {
//				ids[i] = ack.ActionID
//			}
//			sort.Strings(ids)
//			r.Equal("a1", ids[0])
//			r.Equal("a2", ids[1])
//			r.Equal("a3", ids[2])
//		}()
//		svc.Run(ctx)
//	})
//
//	t.Run("continue polling on api error", func(t *testing.T) {
//		r := require.New(t)
//
//		client := mock.NewMockAPIClient([]*castai.ClusterAction{})
//		client.GetActionsErr = errors.New("ups")
//		handler := &mockAgentActionHandler{err: errors.New("ups")}
//		svc := newTestService(handler, client)
//		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
//		defer func() {
//			cancel()
//			svc.startedActionsWg.Wait()
//
//			r.Len(client.Acks, 0)
//		}()
//		svc.Run(ctx)
//	})
//
//	t.Run("do not ack action on context canceled error", func(t *testing.T) {
//		r := require.New(t)
//
//		apiActions := []*castai.ClusterAction{
//			{
//				ID:        "a1",
//				CreatedAt: time.Now(),
//				ActionPatchNode: &castai.ActionPatchNode{
//					NodeName: "n1",
//				},
//			},
//		}
//		client := mock.NewMockAPIClient(apiActions)
//		handler := &mockAgentActionHandler{err: context.Canceled}
//		svc := newTestService(handler, client)
//		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
//		defer func() {
//			cancel()
//			svc.startedActionsWg.Wait()
//
//			r.NotEmpty(client.Actions)
//			r.Len(client.Acks, 0)
//		}()
//		svc.Run(ctx)
//	})
//
//	t.Run("ack with error when action handler failed", func(t *testing.T) {
//		r := require.New(t)
//
//		apiActions := []*castai.ClusterAction{
//			{
//				ID:        "a1",
//				CreatedAt: time.Now(),
//				ActionPatchNode: &castai.ActionPatchNode{
//					NodeName: "n1",
//				},
//			},
//		}
//		client := mock.NewMockAPIClient(apiActions)
//		handler := &mockAgentActionHandler{err: errors.New("ups")}
//		svc := newTestService(handler, client)
//		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
//		defer func() {
//			cancel()
//			svc.startedActionsWg.Wait()
//
//			r.Empty(client.Actions)
//			r.Len(client.Acks, 1)
//			r.Equal("a1", client.Acks[0].ActionID)
//			r.Equal("handling action *castai.ActionPatchNode: ups", *client.Acks[0].Err)
//		}()
//		svc.Run(ctx)
//	})
//
//	t.Run("ack with error when action handler panic occurred", func(t *testing.T) {
//		r := require.New(t)
//
//		apiActions := []*castai.ClusterAction{
//			{
//				ID:        "a1",
//				CreatedAt: time.Now(),
//				ActionPatchNode: &castai.ActionPatchNode{
//					NodeName: "n1",
//				},
//			},
//		}
//		client := mock.NewMockAPIClient(apiActions)
//		handler := &mockAgentActionHandler{panicErr: errors.New("ups")}
//		svc := newTestService(handler, client)
//		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
//		defer func() {
//			cancel()
//			svc.startedActionsWg.Wait()
//
//			r.Empty(client.Actions)
//			r.Len(client.Acks, 1)
//			r.Equal("a1", client.Acks[0].ActionID)
//			r.Contains(*client.Acks[0].Err, "panic: handling action *castai.ActionPatchNode: ups: goroutine")
//		}()
//		svc.Run(ctx)
//	})
//}
//
//type mockAgentActionHandler struct {
//	err         error
//	panicErr    error
//	handleDelay time.Duration
//}
//
//func (m *mockAgentActionHandler) Handle(ctx context.Context, action *castai.ClusterAction) error {
//	time.Sleep(m.handleDelay)
//	if m.panicErr != nil {
//		panic(m.panicErr)
//	}
//	return m.err
//}
