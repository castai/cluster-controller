package actions

import (
	"context"
	"fmt"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/castai/cluster-controller/internal/castai"
	"github.com/castai/cluster-controller/internal/helm"
	"github.com/castai/cluster-controller/internal/helm/mock"
)

func TestChartUninstallHandler(t *testing.T) {
	r := require.New(t)
	ctrl := gomock.NewController(t)
	helmMock := mock_helm.NewMockClient(ctrl)
	ctx := context.Background()

	handler := NewChartUninstallHandler(logrus.New(), helmMock)

	t.Run("successfully uninstall chart", func(t *testing.T) {
		action := &castai.ClusterAction{
			ID:                   uuid.New().String(),
			ActionChartUninstall: newUninstallAction(),
		}

		helmMock.EXPECT().Uninstall(helm.UninstallOptions{
			Namespace:   action.ActionChartUninstall.Namespace,
			ReleaseName: action.ActionChartUninstall.ReleaseName,
		}).Return(nil, nil)

		r.NoError(handler.Handle(ctx, action))
	})

	t.Run("error when uninstalling chart", func(t *testing.T) {
		action := &castai.ClusterAction{
			ID:                   uuid.New().String(),
			ActionChartUninstall: newUninstallAction(),
		}
		someError := fmt.Errorf("some error")

		helmMock.EXPECT().Uninstall(helm.UninstallOptions{
			Namespace:   action.ActionChartUninstall.Namespace,
			ReleaseName: action.ActionChartUninstall.ReleaseName,
		}).Return(nil, someError)

		r.Error(handler.Handle(ctx, action), someError)
	})

	t.Run("namespace is missing in uninstall action", func(t *testing.T) {
		action := &castai.ClusterAction{
			ID:                   uuid.New().String(),
			ActionChartUninstall: newUninstallAction(),
		}
		action.ActionChartUninstall.Namespace = ""

		r.Error(handler.Handle(ctx, action))
	})

	t.Run("helm release is missing in uninstall action", func(t *testing.T) {
		action := &castai.ClusterAction{
			ID:                   uuid.New().String(),
			ActionChartUninstall: newUninstallAction(),
		}
		action.ActionChartUninstall.ReleaseName = ""

		r.Error(handler.Handle(ctx, action))
	})
}

func newUninstallAction() *castai.ActionChartUninstall {
	return &castai.ActionChartUninstall{
		Namespace:   "test",
		ReleaseName: "new-release",
	}
}
