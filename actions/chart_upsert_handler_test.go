package actions

import (
	"context"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"helm.sh/helm/v3/pkg/release"
	helmdriver "helm.sh/helm/v3/pkg/storage/driver"

	"github.com/castai/cluster-controller/castai"
	"github.com/castai/cluster-controller/helm"
	mock_helm "github.com/castai/cluster-controller/helm/mock"
)

func TestChartUpsertHandler(t *testing.T) {
	r := require.New(t)
	ctrl := gomock.NewController(t)
	helmMock := mock_helm.NewMockClient(ctrl)
	ctx := context.Background()

	handler := newChartUpsertHandler(logrus.New(), helmMock)

	t.Run("install chart given release is not found", func(t *testing.T) {
		action := chartUpsertAction()

		helmMock.EXPECT().GetRelease(helm.GetReleaseOptions{
			Namespace:   action.Namespace,
			ReleaseName: action.ReleaseName,
		}).Return(nil, helmdriver.ErrReleaseNotFound)

		helmMock.EXPECT().Install(ctx, helm.InstallOptions{
			ChartSource:     &action.ChartSource,
			Namespace:       action.Namespace,
			ReleaseName:     action.ReleaseName,
			ValuesOverrides: action.ValuesOverrides,
		}).Return(nil, nil)

		r.NoError(handler.Handle(ctx, action))
	})

	t.Run("upgrade chart given release is found", func(t *testing.T) {
		action := chartUpsertAction()

		rel := &release.Release{
			Name:      "new-release",
			Version:   1,
			Namespace: "test",
		}

		helmMock.EXPECT().GetRelease(helm.GetReleaseOptions{
			Namespace:   action.Namespace,
			ReleaseName: action.ReleaseName,
		}).Return(rel, nil)

		helmMock.EXPECT().Upgrade(ctx, helm.UpgradeOptions{
			ChartSource:     &action.ChartSource,
			Release:         rel,
			ValuesOverrides: action.ValuesOverrides,
		}).Return(nil, nil)

		r.NoError(handler.Handle(ctx, action))
	})

	t.Run("rollback previous release before upgrade", func(t *testing.T) {
		action := chartUpsertAction()

		rel := &release.Release{
			Name:      "new-release",
			Version:   1,
			Namespace: "test",
			Info: &release.Info{
				Status: release.StatusPendingUpgrade,
			},
		}

		helmMock.EXPECT().GetRelease(helm.GetReleaseOptions{
			Namespace:   action.Namespace,
			ReleaseName: action.ReleaseName,
		}).Return(rel, nil)

		helmMock.EXPECT().Rollback(helm.RollbackOptions{
			Namespace:   action.Namespace,
			ReleaseName: action.ReleaseName,
		}).Return(nil)

		helmMock.EXPECT().Upgrade(ctx, helm.UpgradeOptions{
			ChartSource:     &action.ChartSource,
			Release:         rel,
			ValuesOverrides: action.ValuesOverrides,
			MaxHistory:      3,
		}).Return(nil, nil)

		r.NoError(handler.Handle(ctx, action))
	})
}

func chartUpsertAction() *castai.ActionChartUpsert {
	return &castai.ActionChartUpsert{
		Namespace:       "test",
		ReleaseName:     "new-release",
		ValuesOverrides: map[string]string{"image.tag": "1.0.0"},
		ChartSource: castai.ChartSource{
			RepoURL: "https://my-charts.repo",
			Name:    "super-chart",
			Version: "1.5.0",
		},
	}
}
