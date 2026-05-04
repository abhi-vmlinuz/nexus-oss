package cmd

import (
	"fmt"
	"github.com/spf13/cobra"
)

// Version is set at build time via -ldflags
var Version = "v0.0.0-dev"

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version number of nexus-cli",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("Nexus CLI %s\n", Version)
		},
	}
}
