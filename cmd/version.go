// version.go implements the NovaVeil `version` subcommand.
package cmd

import (
	"fmt"
	"os"
	"runtime"

	"github.com/kingsunb/NovaVeil/internal/conf"

	"github.com/spf13/cobra"
)

var versionCommand = &cobra.Command{
	Use:   "version",
	Short: "Show current version of " + conf.APP_NAME,
	Run: func(cmd *cobra.Command, args []string) {
		goVersion := fmt.Sprintf("%s %s/%s", runtime.Version(), runtime.GOOS, runtime.GOARCH)
		fmt.Printf("Built At: %s \nGo Version: %s \nAuthor: %s \nCommit ID: %s \nVersion: %s \n", conf.BuildTime, goVersion, conf.Author, conf.Commit, conf.Version)
		os.Exit(0)
	},
}

func init() {
	rootCommand.AddCommand(versionCommand)
}
