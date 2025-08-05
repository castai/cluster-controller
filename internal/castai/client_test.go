package castai

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	dto "github.com/prometheus/client_model/go"
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

func TestConvertPrometheusMetricFamilies(t *testing.T) {
	gatherTime := time.Date(2023, 9, 13, 10, 30, 0, 0, time.UTC)
	expectedTimestamp := gatherTime.UnixMilli()

	t.Run("empty input", func(t *testing.T) {
		r := require.New(t)

		result := convertPrometheusMetricFamilies(gatherTime, []*dto.MetricFamily{})

		r.NotNil(result)
		r.Empty(result.Timeseries)
	})

	t.Run("single counter with labels", func(t *testing.T) {
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

		result := convertPrometheusMetricFamilies(gatherTime, []*dto.MetricFamily{family})

		r.Len(result.Timeseries, 1)
		ts := result.Timeseries[0]

		// Verify __name__ label is first
		r.Len(ts.Labels, 2)
		r.Equal("__name__", ts.Labels[0].Name)
		r.Equal(metricName, ts.Labels[0].Value)

		// Verify custom label
		r.Equal(labelName, ts.Labels[1].Name)
		r.Equal(labelValue, ts.Labels[1].Value)

		// Verify sample
		r.Len(ts.Samples, 1)
		r.Equal(expectedTimestamp, ts.Samples[0].Timestamp)
		r.Equal(counterValue, ts.Samples[0].Value)
	})

	t.Run("multiple counters in one family", func(t *testing.T) {
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

		result := convertPrometheusMetricFamilies(gatherTime, []*dto.MetricFamily{family})

		r.Len(result.Timeseries, 2)

		// Both should have same __name__ label
		r.Equal("__name__", result.Timeseries[0].Labels[0].Name)
		r.Equal(metricName, result.Timeseries[0].Labels[0].Value)
		r.Equal("__name__", result.Timeseries[1].Labels[0].Name)
		r.Equal(metricName, result.Timeseries[1].Labels[0].Value)

		// Different counter values
		r.Equal(counter1Value, result.Timeseries[0].Samples[0].Value)
		r.Equal(counter2Value, result.Timeseries[1].Samples[0].Value)
	})

	t.Run("multiple metric families", func(t *testing.T) {
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

		result := convertPrometheusMetricFamilies(gatherTime, []*dto.MetricFamily{family1, family2})

		r.Len(result.Timeseries, 2)

		// Verify different metric names
		r.Equal(metric1Name, result.Timeseries[0].Labels[0].Value)
		r.Equal(metric2Name, result.Timeseries[1].Labels[0].Value)

		// Verify values
		r.Equal(value1, result.Timeseries[0].Samples[0].Value)
		r.Equal(value2, result.Timeseries[1].Samples[0].Value)
	})

	t.Run("label edge cases", func(t *testing.T) {
		r := require.New(t)

		metricName := "test_counter"
		counterValue := 5.0
		validLabelName := "valid_label"
		validLabelValue := "valid_value"
		emptyLabelValue := ""

		family := &dto.MetricFamily{
			Name: &metricName,
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{
							Name: nil, // Should be skipped
							Value: &validLabelValue,
						},
						{
							Name:  &validLabelName,
							Value: nil, // Should use empty string via lo.FromPtr
						},
						{
							Name:  &validLabelName,
							Value: &emptyLabelValue, // Should preserve empty string
						},
					},
					Counter: &dto.Counter{Value: &counterValue},
				},
			},
		}

		result := convertPrometheusMetricFamilies(gatherTime, []*dto.MetricFamily{family})

		r.Len(result.Timeseries, 1)
		ts := result.Timeseries[0]

		// Should have __name__ + 2 valid labels (nil name skipped, nil value converted to empty)
		r.Len(ts.Labels, 3)
		r.Equal("__name__", ts.Labels[0].Name)
		r.Equal(metricName, ts.Labels[0].Value)

		// Verify remaining labels handle nil values correctly
		r.Equal(validLabelName, ts.Labels[1].Name)
		r.Equal("", ts.Labels[1].Value) // nil value becomes empty string
		r.Equal(validLabelName, ts.Labels[2].Name)
		r.Equal("", ts.Labels[2].Value) // empty string preserved
	})

	t.Run("counter edge cases", func(t *testing.T) {
		r := require.New(t)

		metricName := "test_counter"
		zeroValue := 0.0
		largeValue := 9999999999.99

		family := &dto.MetricFamily{
			Name: &metricName,
			Metric: []*dto.Metric{
				{
					Counter: &dto.Counter{Value: &zeroValue},
				},
				{
					Counter: &dto.Counter{Value: &largeValue},
				},
				{
					Counter: nil, // Should not produce samples
				},
			},
		}

		result := convertPrometheusMetricFamilies(gatherTime, []*dto.MetricFamily{family})

		// Only 2 timeseries should be created (nil counter skipped)
		r.Len(result.Timeseries, 2)

		// Verify zero value
		r.Equal(zeroValue, result.Timeseries[0].Samples[0].Value)
		r.Equal(expectedTimestamp, result.Timeseries[0].Samples[0].Timestamp)

		// Verify large value
		r.Equal(largeValue, result.Timeseries[1].Samples[0].Value)
		r.Equal(expectedTimestamp, result.Timeseries[1].Samples[0].Timestamp)
	})
}
