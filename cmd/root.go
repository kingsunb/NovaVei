// root.go defines the NovaVeil root CLI command.
package cmd

import (
	"os"

	"github.com/kingsunb/NovaVeil/internal/conf"
	"github.com/spf13/cobra"
)

var rootCommand = &cobra.Command{
	Use:   conf.APP_NAME,
	Short: conf.APP_DESC,
}

// Execute runs the root command and exits with status 1 on error.
func Execute() {
	err := rootCommand.Execute()
	if err != nil {
		os.Exit(1)
	}
}
