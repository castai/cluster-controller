package main

import (
	"fmt"
	"os"

	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
)

var Version = "local"

func main() {
	ctx := signals.SetupSignalHandler()
	if err := newCommand().ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
