package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/MarcelInTO/glute/internal/auth"
	"github.com/MarcelInTO/glute/internal/config"
	"github.com/MarcelInTO/glute/internal/gitlab"
	"github.com/MarcelInTO/glute/internal/ui"
	"github.com/spf13/cobra"
)

var dashboardSample bool

var rootCmd = &cobra.Command{
	Use:   "glute",
	Short: "A TUI dashboard for GitLab CI/CD pipelines and jobs",
	Long: `glute is a terminal dashboard that surfaces pipeline and job statistics
across a configured watchlist of "products" (sets of GitLab groups and repos).

Run "glute auth" to connect to your GitLab instance, then run "glute" to launch
the dashboard. Use --sample to explore the UI with built-in fixture data.`,
	// RunE errors are printed by cobra; don't also dump usage on them.
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDashboard()
	},
}

func init() {
	rootCmd.Flags().BoolVar(&dashboardSample, "sample", false, "run the dashboard against built-in sample data (no GitLab needed)")
}

// Execute runs the root command; it is the single entry point from main.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runDashboard() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	var (
		svc   gitlab.Service
		title string
	)
	if dashboardSample {
		svc = gitlab.FakeService{Snap: gitlab.SampleSnapshot()}
		title = "sample data"
	} else {
		token, err := auth.LoadToken()
		if err != nil {
			return err
		}
		if cfg.GitLabURL == "" || token == "" {
			fmt.Println("glute isn't connected yet. Run `glute auth` to set up access to your GitLab instance.")
			return nil
		}
		if len(cfg.Products) == 0 {
			fmt.Printf("No products configured yet. Add a [[product]] block to %s (see the comments there),\n"+
				"or explore the UI now with: glute --sample\n", config.Path())
			return nil
		}
		client, err := gitlab.NewClient(cfg.GitLabURL, token, cfg.CACert)
		if err != nil {
			return err
		}
		svc = gitlab.NewPoller(client, productSpecs(cfg), gitlab.PollOptions{
			RecentWindow: time.Duration(cfg.RecentWindow),
			TopWindow:    time.Duration(cfg.TopWindow),
		})
		title = cfg.GitLabURL
	}

	return ui.NewDashboard(svc, ui.Options{
		RefreshInterval: time.Duration(cfg.RefreshInterval),
		Title:           title,
	}).Run()
}

// productSpecs maps the config's products to the data layer's spec type.
func productSpecs(cfg config.Config) []gitlab.ProductSpec {
	specs := make([]gitlab.ProductSpec, 0, len(cfg.Products))
	for _, p := range cfg.Products {
		specs = append(specs, gitlab.ProductSpec{Name: p.Name, Groups: p.Groups, Projects: p.Projects})
	}
	return specs
}
