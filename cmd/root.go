package cmd

import (
	"fmt"
	"os"

	"github.com/MarcelInTO/glute/internal/auth"
	"github.com/MarcelInTO/glute/internal/config"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "glute",
	Short: "A TUI dashboard for GitLab CI/CD pipelines and jobs",
	Long: `glute is a terminal dashboard that surfaces pipeline and job statistics
across a configured watchlist of "products" (sets of GitLab groups and repos).

Run "glute auth" to connect to your GitLab instance before launching the
dashboard.`,
	// RunE errors are printed by cobra; don't also dump usage on them.
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDashboard()
	},
}

// Execute runs the root command; it is the single entry point from main.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// runDashboard is a Phase 1 placeholder. The tview dashboard replaces this in a
// later phase; for now it verifies setup and points the user at `glute auth`.
func runDashboard() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	token, err := auth.LoadToken()
	if err != nil {
		return err
	}
	if cfg.GitLabURL == "" || token == "" {
		fmt.Println("glute isn't connected yet. Run `glute auth` to set up access to your GitLab instance.")
		return nil
	}
	fmt.Println("Setup looks good — the dashboard TUI arrives in the next phase.")
	fmt.Println("Run `glute auth status` to verify your connection.")
	return nil
}
