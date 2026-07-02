package cmd

import (
	"fmt"

	"github.com/MarcelInTO/glute/internal/auth"
	"github.com/MarcelInTO/glute/internal/config"
	"github.com/spf13/cobra"
)

var instancesCmd = &cobra.Command{
	Use:   "instances",
	Short: "List configured instances",
	Long: `instances lists every configured GitLab instance profile, its URL, and
whether it has a stored token. The active instance (from --instance,
$GLUTE_INSTANCE, or the default) is marked with '*'.`,
	RunE: runInstances,
}

func init() {
	rootCmd.AddCommand(instancesCmd)
}

func runInstances(cmd *cobra.Command, args []string) error {
	active, err := config.ResolveInstance(instanceFlag)
	if err != nil {
		return err
	}

	names, err := config.Instances()
	if err != nil {
		return err
	}
	if len(names) == 0 {
		fmt.Println("No instances configured yet. Run `glute auth` (optionally with --instance <name>).")
		return nil
	}

	for _, name := range names {
		cfg, _ := config.Load(name)
		token, _ := auth.LoadToken(name)

		marker := "  "
		if name == active {
			marker = "* "
		}
		state := "no token"
		if token != "" {
			state = "authenticated"
		}
		url := cfg.GitLabURL
		if url == "" {
			url = "(no url)"
		}
		fmt.Printf("%s%-16s %-36s %s\n", marker, name, url, state)
	}
	return nil
}
