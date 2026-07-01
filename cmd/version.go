package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Version is the build version, overridable at link time:
//
//	go build -ldflags "-X github.com/MarcelInTO/glute/cmd.Version=v0.1.0"
var Version = "dev"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the glute version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("glute %s\n", Version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
