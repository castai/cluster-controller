package castai

import (
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRestryClient_TLS(t *testing.T) {
	t.Run("should populate tls.Config RootCAs when valid certificate presented", func(t *testing.T) {
		r := require.New(t)

		ca := `
-----BEGIN CERTIFICATE-----
MIIDATCCAemgAwIBAgIUPUS4krHP49SF+yYMLHe4nCllKmEwDQYJKoZIhvcNAQEL
BQAwDzENMAsGA1UECgwEVGVzdDAgFw0yMzA5MTMwODM5MzhaGA8yMjE1MDUxMDA4
MzkzOFowDzENMAsGA1UECgwEVGVzdDCCASIwDQYJKoZIhvcNAQEBBQADggEPADCC
AQoCggEBAOVZbDa4/tf3N3VP4Ezvt18d++xrQ+bzjhuE7MWX36NWZ4wUzgmqQXd0
OQWoxYqRGKyI847v29j2BWG17ZmbqarwZHjR98rn9gNtRJgeURlEyAh1pAprhFwb
IBS9vyyCNJtfFFF+lvWvJcU+VKIqWH/9413xDx+OE8tRWNRkS/1CVJg1Nnm3H/IF
lhWAKOYbeKY9q8RtIhb4xNqIc8nmUjDFIjRTarIuf+jDwfFQAPK5pNci+o9KCDgd
Y4lvnGfvPp9XAHnWzTRWNGJQyefZb/SdJjXlic10njfttzKBXi0x8IuV2x98AEPE
2jLXIvC+UBpvMhscdzPfahp5xkYJWx0CAwEAAaNTMFEwHQYDVR0OBBYEFFE48b+V
4E5PWqjpLcUnqWvDDgsuMB8GA1UdIwQYMBaAFFE48b+V4E5PWqjpLcUnqWvDDgsu
MA8GA1UdEwEB/wQFMAMBAf8wDQYJKoZIhvcNAQELBQADggEBAIe82ddHX61WHmyp
zeSiF25aXBqeOUA0ScArTL0fBGi9xZ/8gVU79BvJMyfkaeBKvV06ka6g9OnleWYB
zhBmHBvCL6PsgwLxgzt/dj5ES0K3Ml+7jGmhCKKryzYj/ZvhSMyLlxZqP/nRccBG
y6G3KK4bjzqY4TcEPNs8H4Akc+0SGcPl+AAe65mXPIQhtMkANFLoRuWxMf5JmJke
dYT1GoOjRJpEWCATM+KCXa3UEpRBcXNLeOHZivuqf7n0e1CUD6+0oK4TLxVsTqti
q276VYI/vYmMLRI/iE7Qjn9uGEeR1LWpVngE9jSzSdzByvzw3DwO4sL5B+rv7O1T
9Qgi/No=
-----END CERTIFICATE-----
		`

		got, err := createTLSConfig(ca)
		r.NoError(err)
		r.NotNil(got)
		r.NotEmpty(got.RootCAs)
	})

	t.Run("should return error and nil for tls.Config when invalid certificate is given", func(t *testing.T) {
		r := require.New(t)

		ca := "certificate"
		got, err := createTLSConfig(ca)
		r.Error(err)
		r.Nil(got)
	})

	t.Run("should return nil if no certificate is set", func(t *testing.T) {
		r := require.New(t)

		got, err := createTLSConfig("")
		r.NoError(err)
		r.Nil(got)
	})
}

func TestConvertPrometheusMetricFamilies_EmptyInput(t *testing.T) {
	gatherTime := time.Date(2023, 9, 13, 10, 30, 0, 0, time.UTC)
	r := require.New(t)

	result := convertPrometheusMetricFamilies(gatherTime, "ctrl_pod", []*dto.MetricFamily{})

	r.NotNil(result)
	r.Empty(result.Timeseries)
}

func TestConvertPrometheusMetricFamilies_SingleCounter(t *testing.T) {
	gatherTime := time.Date(2023, 9, 13, 10, 30, 0, 0, time.UTC)
	expectedTimestamp := gatherTime.UnixMilli()
	r := require.New(t)

	metricName := "test_counter"
	counterValue := 42.5
	labelName := "label1"
	labelValue := "value1"

	family := &dto.MetricFamily{
		Name: &metricName,
		Metric: []*dto.Metric{
			{
				Label: []*dto.LabelPair{
					{
						Name:  &labelName,
						Value: &labelValue,
					},
				},
				Counter: &dto.Counter{
					Value: &counterValue,
				},
			},
		},
	}

	result := convertPrometheusMetricFamilies(gatherTime, "ctrl_pod", []*dto.MetricFamily{family})

	r.Len(result.Timeseries, 1)
	ts := result.Timeseries[0]

	r.Len(ts.Labels, 3)
	assertLabelPresent(t, ts.Labels, "__name__", metricName)
	assertLabelPresent(t, ts.Labels, "pod_name", "ctrl_pod")
	assertLabelPresent(t, ts.Labels, labelName, labelValue)

	// Verify sample
	r.Len(ts.Samples, 1)
	r.Equal(expectedTimestamp, ts.Samples[0].Timestamp)
	r.Equal(counterValue, ts.Samples[0].Value)
}

func TestConvertPrometheusMetricFamilies_MultipleCounters(t *testing.T) {
	gatherTime := time.Date(2023, 9, 13, 10, 30, 0, 0, time.UTC)
	r := require.New(t)

	metricName := "test_counter"
	counter1Value := 10.0
	counter2Value := 20.0

	family := &dto.MetricFamily{
		Name: &metricName,
		Metric: []*dto.Metric{
			{
				Counter: &dto.Counter{Value: &counter1Value},
			},
			{
				Counter: &dto.Counter{Value: &counter2Value},
			},
		},
	}

	result := convertPrometheusMetricFamilies(gatherTime, "ctrl_pod", []*dto.MetricFamily{family})

	r.Len(result.Timeseries, 2)

	assertLabelPresent(t, result.Timeseries[0].Labels, "__name__", metricName)
	assertLabelPresent(t, result.Timeseries[1].Labels, "__name__", metricName)

	r.Equal(counter1Value, result.Timeseries[0].Samples[0].Value)
	r.Equal(counter2Value, result.Timeseries[1].Samples[0].Value)
}

func TestConvertPrometheusMetricFamilies_MultipleFamilies(t *testing.T) {
	gatherTime := time.Date(2023, 9, 13, 10, 30, 0, 0, time.UTC)
	r := require.New(t)

	metric1Name := "counter1"
	metric2Name := "counter2"
	value1 := 1.0
	value2 := 2.0

	family1 := &dto.MetricFamily{
		Name: &metric1Name,
		Metric: []*dto.Metric{
			{Counter: &dto.Counter{Value: &value1}},
		},
	}

	family2 := &dto.MetricFamily{
		Name: &metric2Name,
		Metric: []*dto.Metric{
			{Counter: &dto.Counter{Value: &value2}},
		},
	}

	result := convertPrometheusMetricFamilies(gatherTime, "ctrl_pod", []*dto.MetricFamily{family1, family2})

	r.Len(result.Timeseries, 2)

	// Verify different metric names
	assertLabelPresent(t, result.Timeseries[0].Labels, "__name__", metric1Name)
	assertLabelPresent(t, result.Timeseries[1].Labels, "__name__", metric2Name)

	// Verify values
	r.Equal(value1, result.Timeseries[0].Samples[0].Value)
	r.Equal(value2, result.Timeseries[1].Samples[0].Value)
}

func TestConvertPrometheusMetricFamilies_LabelEdgeCases(t *testing.T) {
	gatherTime := time.Date(2023, 9, 13, 10, 30, 0, 0, time.UTC)
	r := require.New(t)

	metricName := "test_counter"
	counterValue := 5.0
	validLabelName := "valid_label"
	validLabelValue := "valid_value"
	emptyLabelName := "empty_label"
	emptyLabelValue := ""

	family := &dto.MetricFamily{
		Name: &metricName,
		Metric: []*dto.Metric{
			{
				Label: []*dto.LabelPair{
					{
						Name:  nil, // Should be skipped
						Value: &validLabelValue,
					},
					{
						Name:  &validLabelName,
						Value: nil, // Should use empty string
					},
					{
						Name:  &emptyLabelName,
						Value: &emptyLabelValue, // Should preserve empty string
					},
				},
				Counter: &dto.Counter{Value: &counterValue},
			},
		},
	}

	result := convertPrometheusMetricFamilies(gatherTime, "ctrl_pod", []*dto.MetricFamily{family})

	r.Len(result.Timeseries, 1)
	ts := result.Timeseries[0]

	// Should have __name__, pod_name + 2 valid labels (nil name skipped, nil value converted to empty)
	r.Len(ts.Labels, 4)
	assertLabelPresent(t, ts.Labels, "__name__", metricName)
	assertLabelPresent(t, ts.Labels, "pod_name", "ctrl_pod")
	assertLabelPresent(t, ts.Labels, validLabelName, "")
	assertLabelPresent(t, ts.Labels, emptyLabelName, emptyLabelValue)
}

func TestConvertPrometheusMetricFamilies_CounterEdgeCases(t *testing.T) {
	gatherTime := time.Date(2023, 9, 13, 10, 30, 0, 0, time.UTC)
	r := require.New(t)

	metricName := "test_counter"

	family := &dto.MetricFamily{
		Name: &metricName,
		Metric: []*dto.Metric{
			{
				Counter: nil, // Should not produce samples
			},
		},
	}

	result := convertPrometheusMetricFamilies(gatherTime, "ctrl_pod", []*dto.MetricFamily{family})

	r.Len(result.Timeseries, 0)
}

func TestConvertPrometheusMetricFamilies_Histogram(t *testing.T) {
	gatherTime := time.Date(2023, 9, 13, 10, 30, 0, 0, time.UTC)
	expectedTimestamp := gatherTime.UnixMilli()
	r := require.New(t)

	metricName := "test_histogram"
	sampleCount := uint64(100)
	sampleSum := 250.5

	// Create histogram buckets
	buckets := []*dto.Bucket{
		{
			CumulativeCount: lo.ToPtr(uint64(10)),
			UpperBound:      lo.ToPtr(1.0),
		},
		{
			CumulativeCount: lo.ToPtr(uint64(30)),
			UpperBound:      lo.ToPtr(5.0),
		},
		{
			CumulativeCount: lo.ToPtr(uint64(80)),
			UpperBound:      lo.ToPtr(10.0),
		},
		{
			CumulativeCount: lo.ToPtr(uint64(100)),
			UpperBound:      lo.ToPtr(25.0),
		},
	}

	labelName := "method"
	labelValue := "GET"

	family := &dto.MetricFamily{
		Name: &metricName,
		Metric: []*dto.Metric{
			{
				Label: []*dto.LabelPair{
					{
						Name:  &labelName,
						Value: &labelValue,
					},
				},
				Histogram: &dto.Histogram{
					SampleCount: &sampleCount,
					SampleSum:   &sampleSum,
					Bucket:      buckets,
				},
			},
		},
	}

	result := convertPrometheusMetricFamilies(gatherTime, "ctrl_pod", []*dto.MetricFamily{family})

	// Should have 4 bucket timeseries + 1 +Inf bucket + 1 _sum + 1 _count = 7 total
	r.Len(result.Timeseries, 7)

	// Separate timeseries by type
	bucketTimeseries := []PrometheusTimeseries{}
	var sumTimeseries, countTimeseries *PrometheusTimeseries

	for _, ts := range result.Timeseries {
		// Check which type this timeseries is by looking at __name__ label
		var metricNameValue string
		for _, label := range ts.Labels {
			if label.Name == "__name__" {
				metricNameValue = label.Value
				break
			}
		}

		switch metricNameValue {
		case metricName + "_bucket":
			bucketTimeseries = append(bucketTimeseries, ts)
		case metricName + "_sum":
			sumTimeseries = &ts
		case metricName + "_count":
			countTimeseries = &ts
		default:
			t.Fatalf("Unexpected metric name: %s", metricNameValue)
		}

		// Verify common labels are present on all timeseries
		assertLabelPresent(t, ts.Labels, "pod_name", "ctrl_pod")
		assertLabelPresent(t, ts.Labels, labelName, labelValue)

		// Verify timestamp and sample structure
		r.Len(ts.Samples, 1)
		r.Equal(expectedTimestamp, ts.Samples[0].Timestamp)
	}

	// Verify bucket timeseries (4 explicit buckets + 1 +Inf bucket)
	r.Len(bucketTimeseries, 5)

	// Verify each expected bucket exists
	expectedBuckets := map[string]float64{
		"1.000000":  10,
		"5.000000":  30,
		"10.000000": 80,
		"25.000000": 100,
		"+Inf":      100,
	}

	foundBuckets := make(map[string]bool)
	for _, ts := range bucketTimeseries {
		assertLabelPresent(t, ts.Labels, "__name__", metricName+"_bucket")

		// Find the 'le' label value
		var leValue string
		for _, label := range ts.Labels {
			if label.Name == "le" {
				leValue = label.Value
				break
			}
		}

		r.NotEmpty(leValue, "Bucket timeseries should have 'le' label")

		expectedCount, exists := expectedBuckets[leValue]
		r.True(exists, "Unexpected bucket with le=%s", leValue)
		r.Equal(expectedCount, ts.Samples[0].Value)
		foundBuckets[leValue] = true
	}

	// Ensure all expected buckets were found
	r.Len(foundBuckets, len(expectedBuckets))

	// Verify _sum timeseries
	r.NotNil(sumTimeseries)
	assertLabelPresent(t, sumTimeseries.Labels, "__name__", metricName+"_sum")
	r.Equal(sampleSum, sumTimeseries.Samples[0].Value)

	// Verify _sum doesn't have 'le' label
	for _, label := range sumTimeseries.Labels {
		r.NotEqual("le", label.Name, "_sum should not have 'le' label")
	}

	// Verify _count timeseries
	r.NotNil(countTimeseries)
	assertLabelPresent(t, countTimeseries.Labels, "__name__", metricName+"_count")
	r.Equal(float64(sampleCount), countTimeseries.Samples[0].Value)

	// Verify _count doesn't have 'le' label
	for _, label := range countTimeseries.Labels {
		r.NotEqual("le", label.Name, "_count should not have 'le' label")
	}
}

func TestConvertPrometheusMetricFamilies_Summary(t *testing.T) {
	gatherTime := time.Date(2023, 9, 13, 10, 30, 0, 0, time.UTC)
	expectedTimestamp := gatherTime.UnixMilli()
	r := require.New(t)

	metricName := "test_summary"
	sampleCount := uint64(50)
	sampleSum := 125.75

	// Create summary quantiles
	quantiles := []*dto.Quantile{
		{
			Quantile: lo.ToPtr(0.5),
			Value:    lo.ToPtr(2.5),
		},
		{
			Quantile: lo.ToPtr(0.9),
			Value:    lo.ToPtr(8.1),
		},
		{
			Quantile: lo.ToPtr(0.99),
			Value:    lo.ToPtr(15.3),
		},
	}

	labelName := "handler"
	labelValue := "api"

	family := &dto.MetricFamily{
		Name: &metricName,
		Metric: []*dto.Metric{
			{
				Label: []*dto.LabelPair{
					{
						Name:  &labelName,
						Value: &labelValue,
					},
				},
				Summary: &dto.Summary{
					SampleCount: &sampleCount,
					SampleSum:   &sampleSum,
					Quantile:    quantiles,
				},
			},
		},
	}

	result := convertPrometheusMetricFamilies(gatherTime, "ctrl_pod", []*dto.MetricFamily{family})

	// Should have 3 quantile timeseries + 1 _sum + 1 _count = 5 total
	r.Len(result.Timeseries, 5)

	// Separate timeseries by type
	quantileTimeseries := []PrometheusTimeseries{}
	var sumTimeseries, countTimeseries *PrometheusTimeseries

	for _, ts := range result.Timeseries {
		// Check which type this timeseries is by looking at __name__ label
		var metricNameValue string
		for _, label := range ts.Labels {
			if label.Name == "__name__" {
				metricNameValue = label.Value
				break
			}
		}

		switch metricNameValue {
		case metricName + "_quantile":
			quantileTimeseries = append(quantileTimeseries, ts)
		case metricName + "_sum":
			sumTimeseries = &ts
		case metricName + "_count":
			countTimeseries = &ts
		default:
			t.Fatalf("Unexpected metric name: %s", metricNameValue)
		}

		// Verify common labels are present on all timeseries
		assertLabelPresent(t, ts.Labels, "pod_name", "ctrl_pod")
		assertLabelPresent(t, ts.Labels, labelName, labelValue)

		// Verify timestamp and sample structure
		r.Len(ts.Samples, 1)
		r.Equal(expectedTimestamp, ts.Samples[0].Timestamp)
	}

	// Verify quantile timeseries (3 quantiles)
	r.Len(quantileTimeseries, 3)

	// Verify each expected quantile exists
	expectedQuantiles := map[string]float64{
		"0.500000": 2.5,
		"0.900000": 8.1,
		"0.990000": 15.3,
	}

	foundQuantiles := make(map[string]bool)
	for _, ts := range quantileTimeseries {
		assertLabelPresent(t, ts.Labels, "__name__", metricName+"_quantile")

		// Find the 'quantile' label value
		var quantileValue string
		for _, label := range ts.Labels {
			if label.Name == "quantile" {
				quantileValue = label.Value
				break
			}
		}

		r.NotEmpty(quantileValue, "Quantile timeseries should have 'quantile' label")

		expectedValue, exists := expectedQuantiles[quantileValue]
		r.True(exists, "Unexpected quantile with quantile=%s", quantileValue)
		r.Equal(expectedValue, ts.Samples[0].Value)
		foundQuantiles[quantileValue] = true
	}

	// Ensure all expected quantiles were found
	r.Len(foundQuantiles, len(expectedQuantiles))

	// Verify _sum timeseries
	r.NotNil(sumTimeseries)
	assertLabelPresent(t, sumTimeseries.Labels, "__name__", metricName+"_sum")
	r.Equal(sampleSum, sumTimeseries.Samples[0].Value)

	// Verify _sum doesn't have 'quantile' label
	for _, label := range sumTimeseries.Labels {
		r.NotEqual("quantile", label.Name, "_sum should not have 'quantile' label")
	}

	// Verify _count timeseries
	r.NotNil(countTimeseries)
	assertLabelPresent(t, countTimeseries.Labels, "__name__", metricName+"_count")
	r.Equal(float64(sampleCount), countTimeseries.Samples[0].Value)

	// Verify _count doesn't have 'quantile' label
	for _, label := range countTimeseries.Labels {
		r.NotEqual("quantile", label.Name, "_count should not have 'quantile' label")
	}
}

func assertLabelPresent(t *testing.T, labels []PrometheusLabel, name, value string) {
	assert.Contains(t, labels, PrometheusLabel{Name: name, Value: value})
}
