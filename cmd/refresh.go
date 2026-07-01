package cmd

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/MarcelInTO/glute/internal/auth"
	"github.com/MarcelInTO/glute/internal/config"
	"github.com/MarcelInTO/glute/internal/gitlab"
	"github.com/spf13/cobra"
)

var refreshSample bool

var refreshCmd = &cobra.Command{
	Use:   "refresh",
	Short: "Fetch a CI snapshot and print it (text preview of the data layer)",
	Long: `refresh fetches one snapshot for the configured products and prints a text
summary. It's a preview of the data that the Pipelines and Jobs tabs will show;
the interactive dashboard comes in a later phase.

Use --sample to print built-in fixture data without contacting GitLab.`,
	RunE: runRefresh,
}

func init() {
	refreshCmd.Flags().BoolVar(&refreshSample, "sample", false, "print built-in sample data instead of fetching")
	rootCmd.AddCommand(refreshCmd)
}

func runRefresh(cmd *cobra.Command, args []string) error {
	if refreshSample {
		printSnapshot(gitlab.SampleSnapshot())
		return nil
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	token, err := auth.LoadToken()
	if err != nil {
		return err
	}
	if cfg.GitLabURL == "" || token == "" {
		return errors.New("not configured; run `glute auth` first")
	}
	if len(cfg.Products) == 0 {
		return fmt.Errorf("no products configured; add a [[product]] block to %s", config.Path())
	}

	client, err := gitlab.NewClient(cfg.GitLabURL, token, cfg.CACert)
	if err != nil {
		return err
	}

	specs := make([]gitlab.ProductSpec, 0, len(cfg.Products))
	for _, p := range cfg.Products {
		specs = append(specs, gitlab.ProductSpec{Name: p.Name, Groups: p.Groups, Projects: p.Projects})
	}
	poller := gitlab.NewPoller(client, specs, gitlab.PollOptions{
		RecentWindow: time.Duration(cfg.RecentWindow),
		TopWindow:    time.Duration(cfg.TopWindow),
	})

	fmt.Fprintf(os.Stderr, "Fetching from %s ...\n", cfg.GitLabURL)
	snap, err := poller.Refresh(cmd.Context())
	if err != nil {
		return err
	}
	printSnapshot(snap)
	return nil
}

func printSnapshot(s gitlab.Snapshot) {
	fmt.Printf("Snapshot @ %s — %d project(s) polled\n", s.UpdatedAt.Format("15:04:05"), s.Projects)

	fmt.Printf("\n[running pipelines: %d]\n", len(s.RunningPipelines))
	for _, p := range head(s.RunningPipelines, 10) {
		fmt.Printf("  %-30s %-18s %-9s  %s\n", trunc(p.ProjectPath, 30), trunc(p.Ref, 18), p.Status, elapsed(p.Started, p.Created))
	}

	fmt.Printf("\n[recent pipelines: %d]\n", len(s.RecentPipelines))
	for _, p := range head(s.RecentPipelines, 10) {
		fmt.Printf("  %-30s %-18s %-9s %9s  %s ago\n", trunc(p.ProjectPath, 30), trunc(p.Ref, 18), p.Status, fmtDur(p.Duration), ago(p.Finished))
	}

	fmt.Printf("\n[top pipelines / avg length: %d]\n", len(s.TopPipelines))
	for _, a := range head(s.TopPipelines, 10) {
		fmt.Printf("  %-30s %-18s runs=%-5d avg=%9s  success=%3.0f%%\n", trunc(a.ProjectPath, 30), trunc(a.Ref, 18), a.Count, fmtDur(a.AvgDuration), a.SuccessRate()*100)
	}

	fmt.Printf("\n[running jobs: %d]\n", len(s.RunningJobs))
	for _, j := range head(s.RunningJobs, 10) {
		fmt.Printf("  %-30s %-18s %-9s  %s\n", trunc(j.ProjectPath, 30), trunc(j.Name, 18), j.Status, elapsed(j.Started, j.Created))
	}

	fmt.Printf("\n[recent jobs: %d]\n", len(s.RecentJobs))
	for _, j := range head(s.RecentJobs, 10) {
		fmt.Printf("  %-30s %-18s %-9s %9s  %s ago\n", trunc(j.ProjectPath, 30), trunc(j.Name, 18), j.Status, fmtDur(j.Duration), ago(j.Finished))
	}

	fmt.Printf("\n[top jobs / avg length: %d]\n", len(s.TopJobs))
	for _, a := range head(s.TopJobs, 10) {
		fmt.Printf("  %-30s %-18s runs=%-5d avg=%9s  success=%3.0f%%\n", trunc(a.ProjectPath, 30), trunc(a.Name, 18), a.Count, fmtDur(a.AvgDuration), a.SuccessRate()*100)
	}

	if len(s.Errors) > 0 {
		fmt.Printf("\n[%d warning(s)]\n", len(s.Errors))
		for _, e := range s.Errors {
			fmt.Printf("  - %s\n", e)
		}
	}
}

func head[T any](s []T, n int) []T {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

func fmtDur(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	return d.Round(time.Second).String()
}

// elapsed formats how long ago work started, preferring started over created.
func elapsed(started, created time.Time) string {
	t := started
	if t.IsZero() {
		t = created
	}
	return ago(t)
}

func ago(t time.Time) string {
	if t.IsZero() {
		return "?"
	}
	d := max(time.Since(t), 0)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
