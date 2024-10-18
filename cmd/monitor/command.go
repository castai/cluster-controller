package monitor

import (
	"github.com/spf13/cobra"
)

const Use = "monitor"

func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: Use,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd.Context())
		},
	}

	return cmd
}
