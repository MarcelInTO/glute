package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/MarcelInTO/glute/internal/auth"
	"github.com/MarcelInTO/glute/internal/config"
	"github.com/MarcelInTO/glute/internal/gitlab"
	"github.com/MarcelInTO/glute/internal/ui"
	"github.com/spf13/cobra"
)

var (
	instanceFlag    string
	dashboardSample bool
)

var rootCmd = &cobra.Command{
	Use:   "glute",
	Short: "A TUI dashboard for GitLab CI/CD pipelines and jobs",
	Long: `glute is a terminal dashboard that surfaces pipeline and job statistics
across a configured watchlist of "products" (sets of GitLab groups and repos).

Run "glute auth" to connect to your GitLab instance, then run "glute" to launch
the dashboard. Use --sample to explore the UI with built-in fixture data.

Multiple GitLab servers can be kept as named instances via --instance/-i (or the
GLUTE_INSTANCE environment variable); each has its own config, token, and log.`,
	// RunE errors are printed by cobra; don't also dump usage on them.
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDashboard()
	},
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&instanceFlag, "instance", "i", "",
		`instance profile name (default: $GLUTE_INSTANCE or "default")`)
	rootCmd.Flags().BoolVar(&dashboardSample, "sample", false,
		"run the dashboard against built-in sample data (no GitLab needed)")
}

// Execute runs the root command; it is the single entry point from main.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runDashboard() error {
	instance, err := config.ResolveInstance(instanceFlag)
	if err != nil {
		return err
	}
	cfg, err := config.Load(instance)
	if err != nil {
		return err
	}

	var (
		svc    gitlab.Service
		poller *gitlab.Poller
		title  string
	)
	if dashboardSample {
		svc = gitlab.FakeService{Snap: gitlab.SampleSnapshot()}
		title = "sample data"
	} else {
		token, err := auth.LoadToken(instance)
		if err != nil {
			return err
		}
		if cfg.GitLabURL == "" || token == "" {
			fmt.Printf("Instance %q isn't connected yet. Run `glute auth%s` to set it up.\n", instance, instanceHint(instance))
			return nil
		}
		if len(cfg.Products) == 0 {
			fmt.Printf("No products configured for %q. Add a [[product]] block to %s (see the comments there),\n"+
				"or explore the UI now with: glute --sample\n", instance, config.Path(instance))
			return nil
		}
		client, err := gitlab.NewClient(cfg.GitLabURL, token, cfg.CACert)
		if err != nil {
			return err
		}
		poller = gitlab.NewPoller(client, productSpecs(cfg), gitlab.PollOptions{
			RecentWindow: time.Duration(cfg.RecentWindow),
			TopWindow:    time.Duration(cfg.TopWindow),
		})
		svc = poller
		title = instanceTitle(instance, cfg.GitLabURL)
	}

	err = ui.NewDashboard(svc, ui.Options{
		RefreshInterval: time.Duration(cfg.RefreshInterval),
		Title:           title,
		LogPath:         logPath(instance),
		RunnerAliases:   cfg.RunnerAliases,
	}).Run()

	// The TUI has torn down and restored the terminal by the time Run returns,
	// so it's safe to print the refresh-timing summary to stderr here.
	if poller != nil {
		printRefreshStats(poller.RefreshStats())
	}
	return err
}

// printRefreshStats writes the average per-phase refresh cost to stderr on exit.
// Diagnostic instrumentation to inform a caching design: it contrasts the group
// scan (project resolution, re-run every refresh) against the pipeline/job fetch.
func printRefreshStats(s gitlab.RefreshStats) {
	if s.Count == 0 {
		return
	}
	ms := func(d time.Duration) time.Duration { return d.Round(time.Millisecond) }
	fmt.Fprintf(os.Stderr, "\nrefresh timing (avg over %d refresh(es)):\n", s.Count)
	fmt.Fprintf(os.Stderr, "  group scan (resolve projects): %s\n", ms(s.AvgResolve()))
	fmt.Fprintf(os.Stderr, "  pipeline + job fetch:          %s  (list %s + detail-enrich %s)\n",
		ms(s.AvgFetch()+s.AvgEnrich()), ms(s.AvgFetch()), ms(s.AvgEnrich()))
}

// productSpecs maps the config's products to the data layer's spec type.
func productSpecs(cfg config.Config) []gitlab.ProductSpec {
	specs := make([]gitlab.ProductSpec, 0, len(cfg.Products))
	for _, p := range cfg.Products {
		specs = append(specs, gitlab.ProductSpec{Name: p.Name, Groups: p.Groups, Projects: p.Projects})
	}
	return specs
}

// instanceHint returns " --instance <name>" for non-default instances, so
// suggested commands carry the right instance.
func instanceHint(instance string) string {
	if instance == config.DefaultInstance {
		return ""
	}
	return " --instance " + instance
}

// instanceTitle labels the footer with the instance (unless it's the default).
func instanceTitle(instance, url string) string {
	if instance == config.DefaultInstance {
		return url
	}
	return instance + " · " + url
}

// logPath is the per-instance log file; falls back to a bare name on error.
func logPath(instance string) string {
	dir, err := config.InstanceDir(instance)
	if err != nil {
		return "glute.log"
	}
	return filepath.Join(dir, "glute.log")
}
