package controller

import (
	"github.com/castai/cluster-controller/cmd/utils"
	"github.com/spf13/cobra"
)

const Use = "controller"

func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: Use,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd.Context())
		},
	}

	utils.WithAPIFlags(cmd)

	return cmd
}
