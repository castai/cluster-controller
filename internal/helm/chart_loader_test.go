package helm

import (
	"context"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/castai/cluster-controller/internal/castai"
)

func TestIntegration_ChartLoader(t *testing.T) {
	r := require.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	chart := &castai.ChartSource{
		RepoURL: "https://castai.github.io/helm-charts",
		Name:    "castai-cluster-controller",
		Version: "0.4.3",
	}

	loader := NewChartLoader(logrus.New())
	c, err := loader.Load(ctx, chart)
	r.NoError(err)
	r.Equal(chart.Name, c.Name())
	r.Equal(chart.Version, c.Metadata.Version)
}
