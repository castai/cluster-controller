package actions

import (
	"context"
	"fmt"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/castai/cluster-controller/castai"
	"github.com/castai/cluster-controller/helm"
	mock_helm "github.com/castai/cluster-controller/helm/mock"
)

func TestUninstallActionValidator(t *testing.T) {
	r := require.New(t)
	ctrl := gomock.NewController(t)
	helmMock := mock_helm.NewMockClient(ctrl)
	handler := newChartUninstallHandler(logrus.New(), helmMock).(*chartUninstallHandler)

	t.Run("correct uninstall action", func(t *testing.T) {
		r.NoError(handler.validateRequest(newUninstallAction()))
	})

	t.Run("namespace is missing in uninstall action", func(t *testing.T) {
		action := newUninstallAction()
		action.Namespace = ""

		r.Error(handler.validateRequest(action))
	})

	t.Run("helm release is missing in uninstall action", func(t *testing.T) {
		action := newUninstallAction()
		action.ReleaseName = ""

		r.Error(handler.validateRequest(action))
	})
}

func TestChartUninstallHandler(t *testing.T) {
	r := require.New(t)
	ctrl := gomock.NewController(t)
	helmMock := mock_helm.NewMockClient(ctrl)
	ctx := context.Background()

	handler := newChartUninstallHandler(logrus.New(), helmMock)

	t.Run("successfully uninstall chart", func(t *testing.T) {
		action := newUninstallAction()

		helmMock.EXPECT().Uninstall(helm.UninstallOptions{
			Namespace:   action.Namespace,
			ReleaseName: action.ReleaseName,
		}).Return(nil, nil)

		r.NoError(handler.Handle(ctx, action))
	})

	t.Run("error when uninstalling chart", func(t *testing.T) {
		action := newUninstallAction()
		someError := fmt.Errorf("some error")

		helmMock.EXPECT().Uninstall(helm.UninstallOptions{
			Namespace:   action.Namespace,
			ReleaseName: action.ReleaseName,
		}).Return(nil, someError)

		r.Error(handler.Handle(ctx, action), someError)
	})
}

func newUninstallAction() *castai.ActionChartUninstall {
	return &castai.ActionChartUninstall{
		Namespace:   "test",
		ReleaseName: "new-release",
	}
}
