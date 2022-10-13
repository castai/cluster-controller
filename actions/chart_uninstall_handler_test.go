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

func TestChartUninstallHandler(t *testing.T) {
	r := require.New(t)
	ctrl := gomock.NewController(t)
	helmMock := mock_helm.NewMockClient(ctrl)
	ctx := context.Background()

	handler := newChartUninstallHandler(logrus.New(), helmMock)

	t.Run("successfully uninstall chart", func(t *testing.T) {
		action := newUninstallAction()
		actionID := uuid.New().String()

		helmMock.EXPECT().Uninstall(helm.UninstallOptions{
			Namespace:   action.Namespace,
			ReleaseName: action.ReleaseName,
		}).Return(nil, nil)

		r.NoError(handler.Handle(ctx, action, actionID))
	})

	t.Run("error when uninstalling chart", func(t *testing.T) {
		action := newUninstallAction()
		someError := fmt.Errorf("some error")
		actionID := uuid.New().String()

		helmMock.EXPECT().Uninstall(helm.UninstallOptions{
			Namespace:   action.Namespace,
			ReleaseName: action.ReleaseName,
		}).Return(nil, someError)

		r.Error(handler.Handle(ctx, action, actionID), someError)
	})

	t.Run("namespace is missing in uninstall action", func(t *testing.T) {
		action := newUninstallAction()
		action.Namespace = ""
		actionID := uuid.New().String()

		r.Error(handler.Handle(ctx, action, actionID))
	})

	t.Run("helm release is missing in uninstall action", func(t *testing.T) {
		action := newUninstallAction()
		action.ReleaseName = ""
		actionID := uuid.New().String()

		r.Error(handler.Handle(ctx, action, actionID))
	})
}

func newUninstallAction() *castai.ActionChartUninstall {
	return &castai.ActionChartUninstall{
		Namespace:   "test",
		ReleaseName: "new-release",
	}
}
