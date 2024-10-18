package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/castai/cluster-controller/cmd/controller"
	"github.com/castai/cluster-controller/cmd/monitor"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use: "castai-cluster-controller",
}

func Execute(ctx context.Context) {
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		fatal(err)
	}
}

func init() {
	rootCmd.AddCommand(controller.NewCmd())
	rootCmd.AddCommand(monitor.NewCmd())
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
