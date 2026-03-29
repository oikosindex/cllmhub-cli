package main

import (
	"fmt"
	"os"

	"github.com/cllmhub/cllmhub-cli/internal/daemon"
	"github.com/spf13/cobra"
)

var daemonCmd = &cobra.Command{
	Use:    "__daemon",
	Hidden: true,
	Short:  "Run the daemon process (internal use only)",
	RunE: func(cmd *cobra.Command, args []string) error {
		d := daemon.New()
		if err := d.Run(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return err
		}
		return nil
	},
}
