package metricexporter

import (
	"context"
	"fmt"
	"time"

	"github.com/castai/cluster-controller/internal/metrics"
	dto "github.com/prometheus/client_model/go"
	"github.com/sirupsen/logrus"
)

type MetricSender interface {
	SendMetrics(ctx context.Context, gatherTime time.Time, metricFamilies []*dto.MetricFamily) error
}

type MetricsExporter struct {
	log            *logrus.Entry
	sender         MetricSender
	exportInterval time.Duration
}

func New(log *logrus.Entry, sender MetricSender, exportInterval time.Duration) *MetricsExporter {
	return &MetricsExporter{
		log:            log.WithField("component", "metrics_exporter"),
		sender:         sender,
		exportInterval: exportInterval,
	}
}

func (me *MetricsExporter) Run(ctx context.Context) {
	t := time.NewTicker(me.exportInterval)
	defer t.Stop()
	defer me.log.Info("metrics exporter stopped")
	me.log.Infof("starting metrics exporter with interval %v", me.exportInterval)
	for {
		select {
		case <-ctx.Done():
			me.log.Infof("stopping down metrics exporter: %v", ctx.Err())
			return
		case <-t.C:
			// TODO: remove, only for testing
			metrics.ActionFinished("drain_node", true)

			if err := me.exportMetrics(ctx); err != nil {
				me.log.Errorf("exporting metrics failed: %v", err)
				continue
			}
			me.log.Info("exported metrics successfully")
		}
	}
}

func (me *MetricsExporter) exportMetrics(ctx context.Context) error {
	families, err := metrics.Gather()
	if err != nil {
		return fmt.Errorf("failed to gather metrics: %w", err)
	}
	gatherTime := time.Now()

	if err := me.sender.SendMetrics(ctx, gatherTime, families); err != nil {
		return fmt.Errorf("failed to send metrics: %w", err)
	}

	return nil
}
