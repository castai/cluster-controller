package main

import (
	"context"
	"github.com/castai/cluster-controller/cmd"
	"github.com/castai/cluster-controller/internal/config"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
)

// These should be set via `go build` during a release.
var (
	GitCommit = "undefined"
	GitRef    = "no-ref"
	Version   = "local"
)

func main() {
	ctx := signals.SetupSignalHandler()
	ctx = context.WithValue(ctx, "agentVersion", &config.ClusterControllerVersion{
		GitCommit: GitCommit,
		GitRef:    GitRef,
		Version:   Version,
	})
	cmd.Execute(ctx)
}
