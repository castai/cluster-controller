package metricexporter

import (
	"context"
	"fmt"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/sirupsen/logrus"

	"github.com/castai/cluster-controller/internal/metrics"
)

type Sender interface {
	SendMetrics(ctx context.Context, gatherTime time.Time, metricFamilies []*dto.MetricFamily) error
}

type Gatherer func() ([]*dto.MetricFamily, time.Time, error)

func DefaultMetricGatherer() ([]*dto.MetricFamily, time.Time, error) {
	families, err := metrics.Gather()
	return families, time.Now(), err
}

type Exporter struct {
	log            *logrus.Entry
	sender         Sender
	gatherer       Gatherer
	exportInterval time.Duration
}

func New(
	log *logrus.Entry,
	sender Sender,
	exportInterval time.Duration,
	opts ...func(*Exporter),
) *Exporter {
	exp := &Exporter{
		log:            log.WithField("component", "metrics_exporter"),
		sender:         sender,
		gatherer:       DefaultMetricGatherer,
		exportInterval: exportInterval,
	}
	for _, opt := range opts {
		opt(exp)
	}
	return exp
}

func WithMetricGatherer(g Gatherer) func(*Exporter) {
	return func(me *Exporter) {
		if g == nil {
			return
		}
		me.gatherer = g
	}
}

func (me *Exporter) Run(ctx context.Context) {
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
			if err := me.exportMetrics(ctx); err != nil {
				me.log.Errorf("exporting metrics failed: %v", err)
				continue
			}
			me.log.Info("exported metrics successfully")
		}
	}
}

func (me *Exporter) exportMetrics(ctx context.Context) error {
	families, gatherTime, err := me.gatherer()
	if err != nil {
		return fmt.Errorf("failed to gather metrics: %w", err)
	}

	if err := me.sender.SendMetrics(ctx, gatherTime, families); err != nil {
		return fmt.Errorf("failed to send metrics: %w", err)
	}

	return nil
}
