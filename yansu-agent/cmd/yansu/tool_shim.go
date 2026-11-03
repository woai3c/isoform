package main

import (
	"github.com/spf13/cobra"
	"yansu-agent/agent/toolserver"
)

func init() {
	var ticketPath string
	cmd := &cobra.Command{
		Use:          "tool-shim",
		Short:        "Connect an assistant to its Isoform run tools",
		Hidden:       true,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return toolserver.Relay(cmd.Context(), ticketPath, cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&ticketPath, "ticket-file", "", "Per-run tool server ticket")
	_ = cmd.MarkFlagRequired("ticket-file")
	rootCmd.AddCommand(cmd)
}
