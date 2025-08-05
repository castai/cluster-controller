package metricexporter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/castai/cluster-controller/internal/metrics"
	dto "github.com/prometheus/client_model/go"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockMetricSender is a mock implementation of MetricSender
type MockMetricSender struct {
	mock.Mock
}

func (m *MockMetricSender) SendMetrics(ctx context.Context, gatherTime time.Time, metricFamilies []*dto.MetricFamily) error {
	args := m.Called(ctx, gatherTime, metricFamilies)
	return args.Error(0)
}

// MockMetricsGatherer allows us to mock the metrics.Gather function
type MockMetricsGatherer struct {
	mock.Mock
}

func (m *MockMetricsGatherer) Gather() ([]*dto.MetricFamily, error) {
	args := m.Called()
	return args.Get(0).([]*dto.MetricFamily), args.Error(1)
}

// Override the metrics.Gather function for testing
var originalGather = metrics.Gather

func setupTest() (*logrus.Entry, *MockMetricSender) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel) // Reduce log noise in tests
	log := logrus.NewEntry(logger)
	sender := &MockMetricSender{}
	return log, sender
}

func TestRun_ExportsMetricsOnInterval(t *testing.T) {
	log, sender := setupTest()
	exporter := New(log, sender, 10*time.Millisecond)

	// Mock successful metric gathering and sending
	mockFamilies := []*dto.MetricFamily{}
	sender.On("SendMetrics", mock.Anything, mock.Anything, mockFamilies).Return(nil).Twice()

	// Override metrics.Gather to return our mock data
	metrics.Gather = func() ([]*dto.MetricFamily, error) {
		return mockFamilies, nil
	}
	defer func() { metrics.Gather = originalGather }()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	exporter.Run(ctx)

	// Verify SendMetrics was called at least twice (indicating interval behavior)
	sender.AssertExpectations(t)
}

func TestRun_StopsOnContextCancellation(t *testing.T) {
	log, sender := setupTest()
	exporter := New(log, sender, 100*time.Millisecond) // Longer interval

	// Mock metrics gathering
	mockFamilies := []*dto.MetricFamily{}
	metrics.Gather = func() ([]*dto.MetricFamily, error) {
		return mockFamilies, nil
	}
	defer func() { metrics.Gather = originalGather }()

	// Don't expect any calls since we'll cancel before first tick
	sender.On("SendMetrics", mock.Anything, mock.Anything, mockFamilies).Return(nil).Maybe()

	ctx, cancel := context.WithCancel(context.Background())
	
	// Cancel immediately
	cancel()

	start := time.Now()
	exporter.Run(ctx)
	duration := time.Since(start)

	// Should return quickly, not wait for the full interval
	assert.Less(t, duration, 50*time.Millisecond)
}

func TestRun_ContinuesOnSendError(t *testing.T) {
	log, sender := setupTest()
	exporter := New(log, sender, 10*time.Millisecond)

	// Mock metrics gathering
	mockFamilies := []*dto.MetricFamily{}
	metrics.Gather = func() ([]*dto.MetricFamily, error) {
		return mockFamilies, nil
	}
	defer func() { metrics.Gather = originalGather }()

	// First call fails, second succeeds
	sender.On("SendMetrics", mock.Anything, mock.Anything, mockFamilies).Return(errors.New("send error")).Once()
	sender.On("SendMetrics", mock.Anything, mock.Anything, mockFamilies).Return(nil).Once()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	exporter.Run(ctx)

	// Verify both calls were made (exporter continued after error)
	sender.AssertExpectations(t)
}

func TestExportMetrics_Success(t *testing.T) {
	log, sender := setupTest()
	exporter := New(log, sender, time.Minute) // Interval doesn't matter for this test

	// Mock successful metrics gathering
	mockFamilies := []*dto.MetricFamily{
		{Name: stringPtr("test_metric")},
	}
	metrics.Gather = func() ([]*dto.MetricFamily, error) {
		return mockFamilies, nil
	}
	defer func() { metrics.Gather = originalGather }()

	// Expect SendMetrics to be called with the mock families
	sender.On("SendMetrics", mock.Anything, mock.Anything, mockFamilies).Return(nil)

	ctx := context.Background()
	err := exporter.exportMetrics(ctx)

	require.NoError(t, err)
	sender.AssertExpectations(t)
}

func TestExportMetrics_GatherError(t *testing.T) {
	log, sender := setupTest()
	exporter := New(log, sender, time.Minute)

	// Mock metrics gathering error
	gatherError := errors.New("gather failed")
	metrics.Gather = func() ([]*dto.MetricFamily, error) {
		return nil, gatherError
	}
	defer func() { metrics.Gather = originalGather }()

	// SendMetrics should not be called
	sender.AssertNotCalled(t, "SendMetrics")

	ctx := context.Background()
	err := exporter.exportMetrics(ctx)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to gather metrics")
	assert.Contains(t, err.Error(), "gather failed")
}

func TestExportMetrics_SendError(t *testing.T) {
	log, sender := setupTest()
	exporter := New(log, sender, time.Minute)

	// Mock successful metrics gathering
	mockFamilies := []*dto.MetricFamily{}
	metrics.Gather = func() ([]*dto.MetricFamily, error) {
		return mockFamilies, nil
	}
	defer func() { metrics.Gather = originalGather }()

	// Mock SendMetrics error
	sendError := errors.New("send failed")
	sender.On("SendMetrics", mock.Anything, mock.Anything, mockFamilies).Return(sendError)

	ctx := context.Background()
	err := exporter.exportMetrics(ctx)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to send metrics")
	assert.Contains(t, err.Error(), "send failed")
	sender.AssertExpectations(t)
}

// Helper function to create string pointers
func stringPtr(s string) *string {
	return &s
}
