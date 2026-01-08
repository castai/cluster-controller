package controller

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/castai/cluster-controller/health"
	"github.com/castai/cluster-controller/internal/actions"
	mock_actions "github.com/castai/cluster-controller/internal/actions/mock"
	"github.com/castai/cluster-controller/internal/castai"
	mock_castai "github.com/castai/cluster-controller/internal/castai/mock"
)

// nolint: govet
func TestController_Run(t *testing.T) {
	t.Parallel()
	pollTimeout := 100 * time.Millisecond
	cfg := Config{
		PollWaitInterval:     10 * time.Millisecond,
		PollTimeout:          pollTimeout,
		AckTimeout:           1 * time.Second,
		AckRetriesCount:      3,
		AckRetryWait:         1 * time.Millisecond,
		ClusterID:            uuid.New().String(),
		MaxActionsInProgress: 100,
	}
	type fields struct {
		cfg                  Config
		tuneMockCastAIClient func(m *mock_castai.MockCastAIClient)
		tuneMockHandler      func(m *mock_actions.MockActionHandler)
		k8sVersion           string
	}
	type args struct {
		ctx func() context.Context
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{
			name: "no actions",
			args: args{
				ctx: func() context.Context {
					ctx, _ := context.WithTimeout(context.Background(), time.Second)
					return ctx
				},
			},
			fields: fields{
				cfg: cfg,
				tuneMockCastAIClient: func(m *mock_castai.MockCastAIClient) {
					m.EXPECT().GetActions(gomock.Any(), gomock.Any()).Return(nil, nil).MinTimes(1)
				},
			},
		},
		{
			name: "poll handle and ack",
			args: args{
				ctx: func() context.Context {
					ctx, _ := context.WithTimeout(context.Background(), time.Second)
					return ctx
				},
			},
			fields: fields{
				cfg:        cfg,
				k8sVersion: "1.20.1",
				tuneMockHandler: func(m *mock_actions.MockActionHandler) {
					m.EXPECT().Handle(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				},
				tuneMockCastAIClient: func(m *mock_castai.MockCastAIClient) {
					m.EXPECT().GetActions(gomock.Any(), gomock.Any()).Return([]*castai.ClusterAction{
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
					}, nil).Times(1).MinTimes(1)
					m.EXPECT().AckAction(gomock.Any(), "a1", &castai.AckClusterActionRequest{}).Return(nil).MinTimes(1)
					m.EXPECT().AckAction(gomock.Any(), "a2", &castai.AckClusterActionRequest{}).Return(nil).MinTimes(1)
					m.EXPECT().AckAction(gomock.Any(), "a3", &castai.AckClusterActionRequest{}).Return(nil).MinTimes(1)
				},
			},
		},
		{
			name: "handle action error",
			args: args{
				ctx: func() context.Context {
					ctx, _ := context.WithTimeout(context.Background(), time.Second)
					return ctx
				},
			},
			fields: fields{
				cfg:        cfg,
				k8sVersion: "1.20.1",
				tuneMockHandler: func(m *mock_actions.MockActionHandler) {
					m.EXPECT().Handle(gomock.Any(), gomock.Any()).Return(fmt.Errorf("test handle action error")).MinTimes(1)
				},
				tuneMockCastAIClient: func(m *mock_castai.MockCastAIClient) {
					m.EXPECT().GetActions(gomock.Any(), gomock.Any()).Return([]*castai.ClusterAction{
						{
							ID:        "a1",
							CreatedAt: time.Now(),
							ActionDeleteNode: &castai.ActionDeleteNode{
								NodeName: "n1",
							},
						},
					}, nil).Times(1).MinTimes(1)
					m.EXPECT().AckAction(gomock.Any(), "a1", gomock.Any()).
						DoAndReturn(func(ctx context.Context, actionID string, req *castai.AckClusterActionRequest) error {
							require.NotNil(t, req.Error)
							return nil
						}).MinTimes(1)
				},
			},
		},
		{
			name: "handle action: context canceled error",
			args: args{
				ctx: func() context.Context {
					ctx, _ := context.WithTimeout(context.Background(), time.Second)
					return ctx
				},
			},
			fields: fields{
				cfg:        cfg,
				k8sVersion: "1.20.1",
				tuneMockHandler: func(m *mock_actions.MockActionHandler) {
					m.EXPECT().Handle(gomock.Any(), gomock.Any()).Return(context.Canceled).MinTimes(1)
				},
				tuneMockCastAIClient: func(m *mock_castai.MockCastAIClient) {
					m.EXPECT().GetActions(gomock.Any(), gomock.Any()).Return([]*castai.ClusterAction{
						{
							ID:        "a1",
							CreatedAt: time.Now(),
							ActionDeleteNode: &castai.ActionDeleteNode{
								NodeName: "n1",
							},
						},
					}, nil).Times(1).MinTimes(1)
				},
			},
		},
		{
			name: "ack with error when action handler panic occurred",
			args: args{
				ctx: func() context.Context {
					ctx, _ := context.WithTimeout(context.Background(), time.Second)
					return ctx
				},
			},
			fields: fields{
				cfg:        cfg,
				k8sVersion: "1.20.1",
				tuneMockHandler: func(m *mock_actions.MockActionHandler) {
					m.EXPECT().Handle(gomock.Any(), gomock.Any()).
						DoAndReturn(func(ctx context.Context, action *castai.ClusterAction) error {
							panic("ups")
						}).
						MinTimes(1)
				},
				tuneMockCastAIClient: func(m *mock_castai.MockCastAIClient) {
					m.EXPECT().GetActions(gomock.Any(), gomock.Any()).Return([]*castai.ClusterAction{
						{
							ID:        "a1",
							CreatedAt: time.Now(),
							ActionDeleteNode: &castai.ActionDeleteNode{
								NodeName: "n1",
							},
						},
					}, nil).Times(1).MinTimes(1)
					m.EXPECT().AckAction(gomock.Any(), "a1", gomock.Any()).
						DoAndReturn(func(ctx context.Context, actionID string, req *castai.AckClusterActionRequest) error {
							require.NotNil(t, req.Error)
							return nil
						}).MinTimes(1)
				},
			},
		},
		{
			name: "unknown action type, should surface ack action with error",
			args: args{
				ctx: func() context.Context {
					ctx, _ := context.WithTimeout(context.Background(), time.Second)
					return ctx
				},
			},
			fields: fields{
				cfg:             cfg,
				k8sVersion:      "1.20.1",
				tuneMockHandler: func(m *mock_actions.MockActionHandler) {},
				tuneMockCastAIClient: func(m *mock_castai.MockCastAIClient) {
					m.EXPECT().GetActions(gomock.Any(), gomock.Any()).Return([]*castai.ClusterAction{
						{
							ID:        "a1",
							CreatedAt: time.Now(),
						},
					}, nil).Times(1).MinTimes(1)
					m.EXPECT().AckAction(gomock.Any(), "a1", gomock.Any()).
						DoAndReturn(func(ctx context.Context, actionID string, req *castai.AckClusterActionRequest) error {
							require.NotNil(t, req.Error)
							require.NotContains(t, *req.Error, "panic") // We don't want to rely on panic, error should be handled cleanly.
							require.Contains(t, *req.Error, "invalid action")
							return nil
						}).MinTimes(1)
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := gomock.NewController(t)
			defer m.Finish()
			client := mock_castai.NewMockCastAIClient(m)
			if tt.fields.tuneMockCastAIClient != nil {
				tt.fields.tuneMockCastAIClient(client)
			}

			handler := mock_actions.NewMockActionHandler(m)
			if tt.fields.tuneMockHandler != nil {
				tt.fields.tuneMockHandler(handler)
			}
			testActionHandlers := map[reflect.Type]actions.ActionHandler{
				reflect.TypeFor[*castai.ActionDeleteNode](): handler,
				reflect.TypeFor[*castai.ActionDrainNode]():  handler,
				reflect.TypeFor[*castai.ActionPatchNode]():  handler,
			}

			s := NewService(
				logrus.New(),
				tt.fields.cfg,
				tt.fields.k8sVersion,
				client,
				health.NewHealthzProvider(health.HealthzCfg{HealthyPollIntervalLimit: pollTimeout}, logrus.New()),
				testActionHandlers)

			s.Run(tt.args.ctx())
		})
	}
}

func TestController_ParallelExecutionTest(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		cfg := Config{
			PollWaitInterval:     time.Second,
			PollTimeout:          50 * time.Millisecond,
			AckTimeout:           time.Second,
			AckRetriesCount:      2,
			AckRetryWait:         time.Millisecond,
			ClusterID:            uuid.New().String(),
			MaxActionsInProgress: 2,
		}

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		client := mock_castai.NewMockCastAIClient(ctrl)
		handler := mock_actions.NewMockActionHandler(ctrl)

		testActionHandlers := map[reflect.Type]actions.ActionHandler{
			reflect.TypeFor[*castai.ActionCreateEvent](): handler,
		}

		const maxActions = 4
		actions := make([]*castai.ClusterAction, 0, maxActions)
		for i := range maxActions {
			actions = append(actions, &castai.ClusterAction{
				ID:        "action-" + strconv.Itoa(i),
				CreatedAt: time.Now(),
				ActionCreateEvent: &castai.ActionCreateEvent{
					EventType: "fake",
				},
			})
		}
		actionsWithAckErr := map[string]struct{}{
			actions[2].ID: {},
		}

		var (
			mu                   sync.Mutex
			currentlyExecuting   int
			maxExecutingObserved int
			executionCounts      = make(map[string]int)
		)

		handler.EXPECT().Handle(gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, action *castai.ClusterAction) error {
				mu.Lock()
				currentlyExecuting++
				executionCounts[action.ID]++
				if currentlyExecuting > maxExecutingObserved {
					maxExecutingObserved = currentlyExecuting
				}
				mu.Unlock()

				time.Sleep(100 * time.Millisecond)

				mu.Lock()
				currentlyExecuting--
				mu.Unlock()

				return nil
			},
		).AnyTimes()

		client.EXPECT().GetActions(gomock.Any(), gomock.Any()).Return(actions, nil).Times(1)
		client.EXPECT().AckAction(gomock.Any(), gomock.Any(), &castai.AckClusterActionRequest{}).
			DoAndReturn(func(ctx context.Context, actionID string, req *castai.AckClusterActionRequest) error {
				if _, ok := actionsWithAckErr[actionID]; ok {
					return assert.AnError
				}
				return nil
			}).AnyTimes()

		client.EXPECT().GetActions(gomock.Any(), gomock.Any()).Return(actions, nil).Times(3)

		client.EXPECT().GetActions(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()

		logger := logrus.New()
		svc := NewService(
			logger,
			cfg,
			"v0",
			client,
			health.NewHealthzProvider(health.HealthzCfg{HealthyPollIntervalLimit: cfg.PollTimeout}, logger),
			testActionHandlers,
		)

		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()

		go svc.Run(ctx)

		synctest.Wait()
		<-ctx.Done()
		svc.startedActionsWg.Wait()

		require.LessOrEqual(t, maxExecutingObserved, 2, "Expected no more than 2 actions to execute concurrently, but observed %d", maxExecutingObserved)

		for _, action := range actions {
			count := executionCounts[action.ID]
			if _, ok := actionsWithAckErr[action.ID]; ok {
				assert.Equal(t, 3, count, "Expected action %s to be executed three times because of ack errors, but it was executed %d times", action.ID, count)
				continue
			}
			assert.Equal(t, 1, count, "Expected action %s to be executed exactly once, but it was executed %d times", action.ID, count)
		}
	})
}

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m, goleak.IgnoreTopFunction("k8s.io/klog/v2.(*loggingT).flushDaemon"))
}
