package e2e

import (
	"context"
	"log"
	"os"
	"testing"

	"github.com/kelseyhightower/envconfig"
	"github.com/stretchr/testify/require"

	"github.com/castai/cluster-controller/e2e/suites"
)

var cfg suites.Config

func TestMain(m *testing.M) {
	if err := envconfig.Process("", &cfg); err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	exitCode := m.Run()
	os.Exit(exitCode)
}

func TestClusterController_GKEUpgrade(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skip test in short mode")
	}

	ctx := context.Background()

	ts, err := suites.NewGKETestSuite(t, &cfg)
	require.NoError(t, err)

	ts.Run(ctx, t)
}
