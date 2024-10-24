package actions

import (
	"context"
	"errors"
	"fmt"

	"github.com/sirupsen/logrus"
	"helm.sh/helm/v3/pkg/release"
	helmdriver "helm.sh/helm/v3/pkg/storage/driver"

	"github.com/castai/cluster-controller/internal/castai"
	"github.com/castai/cluster-controller/internal/helm"
)

var _ ActionHandler = &ChartUpsertHandler{}

func NewChartUpsertHandler(log logrus.FieldLogger, helm helm.Client) *ChartUpsertHandler {
	return &ChartUpsertHandler{
		log:  log,
		helm: helm,
	}
}

type ChartUpsertHandler struct {
	log  logrus.FieldLogger
	helm helm.Client
}

func (c *ChartUpsertHandler) Handle(ctx context.Context, action *castai.ClusterAction) error {
	req, ok := action.Data().(*castai.ActionChartUpsert)
	if !ok {
		return newUnexpectedTypeErr(action.Data(), req)
	}

	if err := c.validateRequest(req); err != nil {
		return err
	}

	rel, err := c.helm.GetRelease(helm.GetReleaseOptions{
		Namespace:   req.Namespace,
		ReleaseName: req.ReleaseName,
	})
	if err != nil {
		if !errors.Is(err, helmdriver.ErrReleaseNotFound) {
			return fmt.Errorf("getting helm release %q in namespace %q: %w", req.ReleaseName, req.Namespace, err)
		}
		_, err := c.helm.Install(ctx, helm.InstallOptions{
			ChartSource:     &req.ChartSource,
			Namespace:       req.Namespace,
			CreateNamespace: req.CreateNamespace,
			ReleaseName:     req.ReleaseName,
			ValuesOverrides: req.ValuesOverrides,
		})
		return err
	}

	// In case previous update stuck we should rollback it.
	if rel.Info.Status == release.StatusPendingUpgrade {
		err = c.helm.Rollback(helm.RollbackOptions{
			Namespace:   rel.Namespace,
			ReleaseName: rel.Name,
		})
		if err != nil {
			return err
		}
	}

	c.log.Debugf("upgrading release %q in namespace %q with resetThenReuseValues %t", req.ReleaseName, req.Namespace, req.ResetThenReuseValues)
	_, err = c.helm.Upgrade(ctx, helm.UpgradeOptions{
		ChartSource:          &req.ChartSource,
		Release:              rel,
		ValuesOverrides:      req.ValuesOverrides,
		MaxHistory:           3, // Keep last 3 releases history.
		ResetThenReuseValues: req.ResetThenReuseValues,
	})
	return err
}

func (c *ChartUpsertHandler) validateRequest(req *castai.ActionChartUpsert) error {
	if req.ReleaseName == "" {
		return fmt.Errorf("release name not provided %w", errAction)
	}
	if req.Namespace == "" {
		return fmt.Errorf("namespace not provided %w", errAction)
	}
	if err := req.ChartSource.Validate(); err != nil {
		return fmt.Errorf("validating chart source: %w", err)
	}
	return nil
}
