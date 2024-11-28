package main

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/manager/signals"

	"github.com/castai/cluster-controller/cmd"
	"github.com/castai/cluster-controller/cmd/utils"
	"github.com/castai/cluster-controller/internal/config"
)

// These should be set via `go build` during a release.
var (
	GitCommit = "4a3f219"
	GitRef    = "no-ref"
	Version   = "v0.54.6"
)

func main() {
	ctx := signals.SetupSignalHandler()
	ctx = context.WithValue(ctx, utils.ClusterControllerVersionKey, &config.ClusterControllerVersion{
		GitCommit: GitCommit,
		GitRef:    GitRef,
		Version:   Version,
	})
	cmd.Execute(ctx)
}
