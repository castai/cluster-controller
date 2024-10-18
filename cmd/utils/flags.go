package utils

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func WithAPIFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().String("api-key", "", "")
	err := viper.BindPFlag("api.key", cmd.PersistentFlags().Lookup("api-key"))
	if err != nil {
		panic(err)
	}

	cmd.PersistentFlags().String("api-url", "", "")
	err = viper.BindPFlag("api.url", cmd.PersistentFlags().Lookup("api-url"))
	if err != nil {
		panic(err)
	}
}
