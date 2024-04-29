package actions

import (
	"context"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"helm.sh/helm/v3/pkg/release"
	helmdriver "helm.sh/helm/v3/pkg/storage/driver"

	"github.com/castai/cluster-controller/helm"
	mock_helm "github.com/castai/cluster-controller/helm/mock"
	"github.com/castai/cluster-controller/types"
)

func TestChartUpsertHandler(t *testing.T) {
	r := require.New(t)
	ctrl := gomock.NewController(t)
	helmMock := mock_helm.NewMockClient(ctrl)
	ctx := context.Background()

	handler := newChartUpsertHandler(logrus.New(), helmMock)

	t.Run("install chart given release is not found", func(_ *testing.T) {
		action := &types.ClusterAction{
			ID:                uuid.New().String(),
			ActionChartUpsert: chartUpsertAction(),
		}

		helmMock.EXPECT().GetRelease(helm.GetReleaseOptions{
			Namespace:   action.ActionChartUpsert.Namespace,
			ReleaseName: action.ActionChartUpsert.ReleaseName,
		}).Return(nil, helmdriver.ErrReleaseNotFound)

		helmMock.EXPECT().Install(ctx, helm.InstallOptions{
			ChartSource:     &action.ActionChartUpsert.ChartSource,
			Namespace:       action.ActionChartUpsert.Namespace,
			ReleaseName:     action.ActionChartUpsert.ReleaseName,
			ValuesOverrides: action.ActionChartUpsert.ValuesOverrides,
		}).Return(nil, nil)

		r.NoError(handler.Handle(ctx, action))
	})

	t.Run("upgrade chart given release is found", func(_ *testing.T) {
		action := &types.ClusterAction{
			ID:                uuid.New().String(),
			ActionChartUpsert: chartUpsertAction(),
		}

		rel := &release.Release{
			Name:      "new-release",
			Version:   1,
			Namespace: "test",
			Info: &release.Info{
				Status: release.StatusDeployed,
			},
		}

		helmMock.EXPECT().GetRelease(helm.GetReleaseOptions{
			Namespace:   action.ActionChartUpsert.Namespace,
			ReleaseName: action.ActionChartUpsert.ReleaseName,
		}).Return(rel, nil)

		helmMock.EXPECT().Upgrade(ctx, helm.UpgradeOptions{
			ChartSource:     &action.ActionChartUpsert.ChartSource,
			Release:         rel,
			ValuesOverrides: action.ActionChartUpsert.ValuesOverrides,
			MaxHistory:      3,
		}).Return(nil, nil)

		r.NoError(handler.Handle(ctx, action))
	})

	t.Run("rollback previous release before upgrade", func(_ *testing.T) {
		action := &types.ClusterAction{
			ID:                uuid.New().String(),
			ActionChartUpsert: chartUpsertAction(),
		}

		rel := &release.Release{
			Name:      "new-release",
			Version:   1,
			Namespace: "test",
			Info: &release.Info{
				Status: release.StatusPendingUpgrade,
			},
		}

		helmMock.EXPECT().GetRelease(gomock.Any()).Return(rel, nil)

		helmMock.EXPECT().Rollback(helm.RollbackOptions{
			Namespace:   action.ActionChartUpsert.Namespace,
			ReleaseName: action.ActionChartUpsert.ReleaseName,
		}).Return(nil)

		helmMock.EXPECT().Upgrade(ctx, gomock.Any()).Return(nil, nil)

		r.NoError(handler.Handle(ctx, action))
	})
}

func chartUpsertAction() *types.ActionChartUpsert {
	return &types.ActionChartUpsert{
		Namespace:       "test",
		ReleaseName:     "new-release",
		ValuesOverrides: map[string]string{"image.tag": "1.0.0"},
		ChartSource: types.ChartSource{
			RepoURL: "https://my-charts.repo",
			Name:    "super-chart",
			Version: "1.5.0",
		},
	}
}
