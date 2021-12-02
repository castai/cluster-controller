package helm

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFlattenChartReleaseValues(t *testing.T) {
	tests := []struct {
		name     string
		values   map[string]interface{}
		expected map[string]string
	}{
		{
			name: "flatten values with one root",
			values: map[string]interface{}{
				"controller": map[string]interface{}{
					"aaa": map[string]interface{}{
						"bbb": map[string]interface{}{
							"ccc": "ok",
						},
					},
					"replicaCount": 1,
					"service": map[string]interface{}{
						"type": "LoadBalancer",
					},
				},
			},
			expected: map[string]string{
				"controller.aaa.bbb.ccc":  "ok",
				"controller.replicaCount": "1",
				"controller.service.type": "LoadBalancer",
			},
		},

		{
			name: "flatten values with multi root",
			values: map[string]interface{}{
				"one": map[string]interface{}{
					"replicaCount": 1,
					"service": map[string]interface{}{
						"type": "LoadBalancer",
					},
				},
				"two": map[string]interface{}{
					"replicaCount": 2,
					"service": map[string]interface{}{
						"type": "LoadBalancer",
					},
				},
			},
			expected: map[string]string{
				"one.replicaCount": "1",
				"one.service.type": "LoadBalancer",
				"two.replicaCount": "2",
				"two.service.type": "LoadBalancer",
			},
		},
		{
			name: "flatten values with no child",
			values: map[string]interface{}{
				"value": "ok",
				"more":  1,
			},
			expected: map[string]string{
				"value": "ok",
				"more":  "1",
			},
		},
		{
			name:     "no values no money",
			values:   map[string]interface{}{},
			expected: map[string]string{},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			res := FlattenChartValues(test.values)
			require.Equal(t, test.expected, res)
		})
	}
}
