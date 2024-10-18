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
	GitCommit = "undefined"
	GitRef    = "no-ref"
	Version   = "local"
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
