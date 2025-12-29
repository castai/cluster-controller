package cmd

import (
	"context"
	"fmt"
	"os"
	"slices"

	"github.com/spf13/cobra"

	"github.com/castai/cluster-controller/cmd/controller"
	"github.com/castai/cluster-controller/cmd/monitor"
	"github.com/castai/cluster-controller/cmd/testserver"
)

var rootCmd = &cobra.Command{
	Use: "castai-cluster-controller",
}

func Execute(ctx context.Context) {
	var cmdFound bool
	cmd := rootCmd.Commands()

	for _, a := range cmd {
		if slices.Contains(os.Args[1:], a.Name()) {
			cmdFound = true
		}
	}
	if !cmdFound {
		args := append([]string{controller.Use}, os.Args[1:]...)
		rootCmd.SetArgs(args)
	}

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		fatal(err)
	}
}

func init() {
	rootCmd.AddCommand(controller.NewCmd())
	rootCmd.AddCommand(monitor.NewCmd())
	rootCmd.AddCommand(testserver.NewCmd())
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
