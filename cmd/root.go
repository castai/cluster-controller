package cmd

import (
	"context"
	"fmt"
	"os"

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
		for _, b := range os.Args[1:] {
			if a.Name() == b {
				cmdFound = true
				break
			}
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
