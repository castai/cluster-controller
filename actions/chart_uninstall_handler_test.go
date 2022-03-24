package actions

import (
	"context"
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

	t.Run("uninstall chart", func(t *testing.T) {
		action := chartUninstallAction()

		helmMock.EXPECT().Uninstall(helm.UninstallOptions{
			Namespace:   action.Namespace,
			ReleaseName: action.ReleaseName,
		}).Return(nil, nil)

		r.NoError(handler.Handle(ctx, action))
	})
}

func chartUninstallAction() *castai.ActionChartUninstall {
	return &castai.ActionChartUninstall{
		Namespace:   "test",
		ReleaseName: "new-release",
	}
}
