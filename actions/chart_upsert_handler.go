package actions

import (
	"context"
	"errors"
	"fmt"

	"github.com/sirupsen/logrus"
	"helm.sh/helm/v3/pkg/release"
	helmdriver "helm.sh/helm/v3/pkg/storage/driver"

	"github.com/castai/cluster-controller/castai"
	"github.com/castai/cluster-controller/helm"
)

func newChartUpsertHandler(log logrus.FieldLogger, helm helm.Client) ActionHandler {
	return &chartUpsertHandler{
		log:  log,
		helm: helm,
	}
}

type chartUpsertHandler struct {
	log  logrus.FieldLogger
	helm helm.Client
}

func (c *chartUpsertHandler) Handle(ctx context.Context, action *castai.ClusterAction) error {
	req, ok := action.Data().(*castai.ActionChartUpsert)
	if !ok {
		return fmt.Errorf("unexpected type %T for upsert chart handler", action.Data())
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

	_, err = c.helm.Upgrade(ctx, helm.UpgradeOptions{
		ChartSource:          &req.ChartSource,
		Release:              rel,
		ValuesOverrides:      req.ValuesOverrides,
		MaxHistory:           3, // Keep last 3 releases history.
		ResetThenReuseValues: req.ResetThenReuseValues,
	})
	return err
}

func (c *chartUpsertHandler) validateRequest(req *castai.ActionChartUpsert) error {
	if req.ReleaseName == "" {
		return errors.New("bad request: releaseName not provided")
	}
	if req.Namespace == "" {
		return errors.New("bad request: namespace not provided")
	}
	if err := req.ChartSource.Validate(); err != nil {
		return fmt.Errorf("validating chart source: %w", err)
	}
	return nil
}
