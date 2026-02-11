package loadtest

import (
	"github.com/prometheus/client_golang/prometheus"
)

// testRunCounter tracks test run iterations by status.
var testRunCounter = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "test_run_total",
		Help: "Count of test run iterations by status (success/failed).",
	},
	[]string{"status"},
)

// testRunErrorsCounter tracks total number of errors in failed test runs.
var testRunErrorsCounter = prometheus.NewCounter(
	prometheus.CounterOpts{
		Name: "test_run_errors_total",
		Help: "Total count of errors across all failed test runs.",
	},
)

func IncrementTestRunSuccess() {
	testRunCounter.With(prometheus.Labels{"status": "success"}).Inc()
}

func IncrementTestRunFailure(errorCount int) {
	testRunCounter.With(prometheus.Labels{"status": "failed"}).Inc()
	testRunErrorsCounter.Add(float64(errorCount))
}

func RegisterTestMetrics(registry prometheus.Registerer) {
	registry.MustRegister(
		testRunCounter,
		testRunErrorsCounter,
	)
}
