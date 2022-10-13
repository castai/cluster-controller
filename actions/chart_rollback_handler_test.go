package actions

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/castai/cluster-controller/castai"
	"github.com/castai/cluster-controller/helm"
	mock_helm "github.com/castai/cluster-controller/helm/mock"
)

func TestChartRollbackHandler(t *testing.T) {
	r := require.New(t)
	ctrl := gomock.NewController(t)
	helmMock := mock_helm.NewMockClient(ctrl)
	ctx := context.Background()

	handler := newChartRollbackHandler(logrus.New(), helmMock, "v0.20.0")

	t.Run("successfully rollback chart", func(t *testing.T) {
		action := newRollbackAction()

		actionID, _ := uuid.NewUUID()
		helmMock.EXPECT().Rollback(helm.RollbackOptions{
			Namespace:   action.Namespace,
			ReleaseName: action.ReleaseName,
		}).Return(nil)

		r.NoError(handler.Handle(ctx, action, actionID.String()))
	})

	t.Run("skip rollback if version mismatch", func(t *testing.T) {
		action := newRollbackAction()
		action.Version = "v0.21.0"
		actionID := uuid.New().String()
		r.NoError(handler.Handle(ctx, action, actionID))
	})

	t.Run("error when rolling back chart", func(t *testing.T) {
		action := newRollbackAction()
		someError := fmt.Errorf("some error")
		actionID := uuid.New().String()
		helmMock.EXPECT().Rollback(helm.RollbackOptions{
			Namespace:   action.Namespace,
			ReleaseName: action.ReleaseName,
		}).Return(someError)

		r.Error(handler.Handle(ctx, action, actionID), someError)
	})

	t.Run("namespace is missing in rollback action", func(t *testing.T) {
		action := newRollbackAction()
		action.Namespace = ""
		actionID := uuid.New().String()

		r.Error(handler.Handle(ctx, action, actionID))
	})

	t.Run("helm release is missing in rollback action", func(t *testing.T) {
		action := newRollbackAction()
		action.ReleaseName = ""
		actionID := uuid.New().String()

		r.Error(handler.Handle(ctx, action, actionID))
	})
}

func newRollbackAction() *castai.ActionChartRollback {
	return &castai.ActionChartRollback{
		Namespace:   "test",
		ReleaseName: "new-release",
		Version:     "v0.20.0",
	}
}
