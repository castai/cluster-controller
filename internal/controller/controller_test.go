package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/health"
	mock_actions "github.com/castai/cluster-controller/internal/actions/mock"
	"github.com/castai/cluster-controller/internal/castai"
	"github.com/castai/cluster-controller/internal/castai/mock"
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
					m.EXPECT().AckAction(gomock.Any(), "a1", gomock.Any()).Return(nil).MinTimes(1)
					m.EXPECT().AckAction(gomock.Any(), "a2", gomock.Any()).Return(nil).MinTimes(1)
					m.EXPECT().AckAction(gomock.Any(), "a3", gomock.Any()).Return(nil).MinTimes(1)
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
				kubernetes.New(nil),
				nil,
				client,
				nil,
				health.NewHealthzProvider(health.HealthzCfg{HealthyPollIntervalLimit: pollTimeout}, logrus.New()))
			handler := mock_actions.NewMockActionHandler(m)
			if tt.fields.tuneMockHandler != nil {
				tt.fields.tuneMockHandler(handler)
			}
			for k := range s.actionHandlers {
				s.actionHandlers[k] = handler
			}
			s.Run(tt.args.ctx())
		})
	}
}

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m, goleak.IgnoreTopFunction("k8s.io/klog/v2.(*loggingT).flushDaemon"))
}
