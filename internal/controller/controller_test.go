package controller

import (
	"context"
	"github.com/castai/cluster-controller/health"
	"github.com/castai/cluster-controller/internal/actions"
	"github.com/castai/cluster-controller/internal/castai"
	"github.com/sirupsen/logrus"
	"reflect"
	"sync"
	"testing"
)

func TestController_Run(t *testing.T) {
	type fields struct {
		log              logrus.FieldLogger
		cfg              Config
		castAIClient     castai.CastAIClient
		k8sVersion       string
		actionHandlers   map[reflect.Type]actions.ActionHandler
		startedActionsWg sync.WaitGroup
		startedActions   map[string]struct{}
		startedActionsMu sync.Mutex
		healthCheck      *health.HealthzProvider
	}
	type args struct {
		ctx context.Context
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Controller{
				log:              tt.fields.log,
				cfg:              tt.fields.cfg,
				castAIClient:     tt.fields.castAIClient,
				k8sVersion:       tt.fields.k8sVersion,
				actionHandlers:   tt.fields.actionHandlers,
				startedActionsWg: tt.fields.startedActionsWg,
				startedActions:   tt.fields.startedActions,
				startedActionsMu: tt.fields.startedActionsMu,
				healthCheck:      tt.fields.healthCheck,
			}
			s.Run(tt.args.ctx)
		})
	}
}
