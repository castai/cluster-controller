package metricexporter

import (
	"context"
	"errors"
	"testing"
	"time"

	mock_castai "github.com/castai/cluster-controller/internal/castai/mock"
	"github.com/golang/mock/gomock"
	dto "github.com/prometheus/client_model/go"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestRun_ExportsMetricsOnInterval(t *testing.T) {
	t.Parallel()
	m := gomock.NewController(t)
	defer m.Finish()
	sender := mock_castai.NewMockCastAIClient(m)

	families := []*dto.MetricFamily{}
	gatherTime := time.Now()
	gatherer := func() ([]*dto.MetricFamily, time.Time, error) {
		return families, gatherTime, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
	defer cancel()

	sender.EXPECT().SendMetrics(ctx, gatherTime, families).Return(nil).Times(3)

	exporter := New(
		logrus.NewEntry(logrus.New()),
		sender,
		10*time.Millisecond,
		WithMetricGatherer(gatherer),
	)
	exporter.Run(ctx)
	<-ctx.Done()
}

func TestExportMetrics_GatherError(t *testing.T) {
	t.Parallel()
	m := gomock.NewController(t)
	defer m.Finish()
	sender := mock_castai.NewMockCastAIClient(m)

	gatherCounter := 0
	gatherer := func() ([]*dto.MetricFamily, time.Time, error) {
		gatherCounter++
		return nil, time.Now(), errors.New("error")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	exporter := New(
		logrus.NewEntry(logrus.New()),
		sender,
		10*time.Millisecond,
		WithMetricGatherer(gatherer),
	)
	exporter.Run(ctx)
	<-ctx.Done()

	// assert gather was called twice, but no calls to send the metrics
	require.Equal(t, 2, gatherCounter)
}

func TestExportMetrics_SendError(t *testing.T) {
	t.Parallel()
	m := gomock.NewController(t)
	defer m.Finish()
	sender := mock_castai.NewMockCastAIClient(m)

	families := []*dto.MetricFamily{}
	gatherTime := time.Now()
	gatherer := func() ([]*dto.MetricFamily, time.Time, error) {
		return families, gatherTime, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	sender.EXPECT().SendMetrics(ctx, gatherTime, families).Return(errors.New("error")).Times(2)

	exporter := New(
		logrus.NewEntry(logrus.New()),
		sender,
		10*time.Millisecond,
		WithMetricGatherer(gatherer),
	)
	exporter.Run(ctx)
	<-ctx.Done()
}
