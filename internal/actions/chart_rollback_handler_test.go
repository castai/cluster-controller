package actions

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/castai/cluster-controller/internal/helm"
	"github.com/castai/cluster-controller/internal/helm/mock"
	"github.com/castai/cluster-controller/internal/types"
)

func TestChartRollbackHandler(t *testing.T) {
	r := require.New(t)
	ctrl := gomock.NewController(t)
	helmMock := mock_helm.NewMockClient(ctrl)
	ctx := context.Background()

	handler := NewChartRollbackHandler(logrus.New(), helmMock, "v0.20.0")

	t.Run("successfully rollback chart", func(t *testing.T) {
		action := &types.ClusterAction{
			ID:                  uuid.New().String(),
			ActionChartRollback: newRollbackAction(),
		}

		helmMock.EXPECT().Rollback(helm.RollbackOptions{
			Namespace:   action.ActionChartRollback.Namespace,
			ReleaseName: action.ActionChartRollback.ReleaseName,
		}).Return(nil)

		r.NoError(handler.Handle(ctx, action))
	})

	t.Run("skip rollback if version mismatch", func(t *testing.T) {
		action := &types.ClusterAction{
			ID:                  uuid.New().String(),
			ActionChartRollback: newRollbackAction(),
		}
		action.ActionChartRollback.Version = "v0.21.0"
		r.NoError(handler.Handle(ctx, action))
	})

	t.Run("error when rolling back chart", func(t *testing.T) {
		action := &types.ClusterAction{
			ID:                  uuid.New().String(),
			ActionChartRollback: newRollbackAction(),
		}
		someError := fmt.Errorf("some error")
		helmMock.EXPECT().Rollback(helm.RollbackOptions{
			Namespace:   action.ActionChartRollback.Namespace,
			ReleaseName: action.ActionChartRollback.ReleaseName,
		}).Return(someError)

		r.Error(handler.Handle(ctx, action), someError)
	})

	t.Run("namespace is missing in rollback action", func(t *testing.T) {
		action := &types.ClusterAction{
			ID:                  uuid.New().String(),
			ActionChartRollback: newRollbackAction(),
		}
		action.ActionChartRollback.Namespace = ""

		r.Error(handler.Handle(ctx, action))
	})

	t.Run("helm release is missing in rollback action", func(t *testing.T) {
		action := &types.ClusterAction{
			ID:                  uuid.New().String(),
			ActionChartRollback: newRollbackAction(),
		}
		action.ActionChartRollback.ReleaseName = ""

		r.Error(handler.Handle(ctx, action))
	})
}

func newRollbackAction() *types.ActionChartRollback {
	return &types.ActionChartRollback{
		Namespace:   "test",
		ReleaseName: "new-release",
		Version:     "v0.20.0",
	}
}
