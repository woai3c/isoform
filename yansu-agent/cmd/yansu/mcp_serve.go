package main

import (
	"github.com/spf13/cobra"

	"yansu-agent/mcpread"
)

func newMCPServeCommand() *cobra.Command {
	var cfg mcpread.Config
	cmd := &cobra.Command{
		Use:          "serve",
		Short:        "Serve read-only Isoform context to an assistant over MCP stdio",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return mcpread.Serve(cmd.Context(), cfg, cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&cfg.Client, "client", "", "Assistant identity: claude-code or codex")
	cmd.Flags().StringVar(&cfg.DataDir, "data-dir", "", "Absolute Isoform data folder shown in Settings")
	_ = cmd.MarkFlagRequired("client")
	_ = cmd.MarkFlagRequired("data-dir")
	return cmd
}

func init() {
	mcpCmd.AddCommand(newMCPServeCommand())
}
