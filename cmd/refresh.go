package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/MarcelInTO/glute/internal/auth"
	"github.com/MarcelInTO/glute/internal/config"
	"github.com/MarcelInTO/glute/internal/format"
	"github.com/MarcelInTO/glute/internal/gitlab"
	"github.com/spf13/cobra"
)

var refreshSample bool

var refreshCmd = &cobra.Command{
	Use:   "refresh",
	Short: "Fetch a CI snapshot and print it (text preview of the data layer)",
	Long: `refresh fetches one snapshot for the configured products and prints a text
summary. It's a preview of the data that the Pipelines and Jobs tabs will show;
the interactive dashboard is launched by running glute with no arguments.

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

	instance, err := config.ResolveInstance(instanceFlag)
	if err != nil {
		return err
	}
	cfg, err := config.Load(instance)
	if err != nil {
		return err
	}
	token, err := auth.LoadToken(instance)
	if err != nil {
		return err
	}
	if cfg.GitLabURL == "" || token == "" {
		return fmt.Errorf("instance %q not configured; run `glute auth%s` first", instance, instanceHint(instance))
	}
	if len(cfg.Products) == 0 {
		return fmt.Errorf("no products configured for %q; add a [[product]] block to %s", instance, config.Path(instance))
	}

	client, err := gitlab.NewClient(cfg.GitLabURL, token, cfg.CACert)
	if err != nil {
		return err
	}

	poller := gitlab.NewPoller(client, productSpecs(cfg), gitlab.PollOptions{
		RecentWindow: time.Duration(cfg.RecentWindow),
		TopWindow:    time.Duration(cfg.TopWindow),
		Concurrency:  cfg.Concurrency,
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

	fmt.Printf("\n[current · active pipelines: %d]\n", len(s.Current))
	for _, ap := range s.Current {
		printActive(ap, 0)
	}

	fmt.Printf("\n[recent pipelines: %d]\n", len(s.RecentPipelines))
	for _, p := range head(s.RecentPipelines, 10) {
		fmt.Printf("  %-30s %-18s %-9s %9s  %s ago\n", format.Elide(p.ProjectPath, 30), format.Trunc(p.Ref, 18), p.Status, format.Duration(p.Duration), format.Ago(p.Finished))
	}

	fmt.Printf("\n[unresolved failures: %d]\n", len(s.FailingRefs))
	for _, f := range s.FailingRefs {
		last := f.Latest.Finished
		if last.IsZero() {
			last = f.Latest.Updated // its detail fetch hasn't happened yet (MaxDetailFetch)
		}
		fmt.Printf("  %-30s %-18s failing %-4s latest #%d %s ago\n", format.Elide(f.ProjectPath, 30), format.Trunc(f.Ref, 18),
			format.Ago(f.Since), f.Latest.ID, format.Ago(last))
	}

	// The Pipelines tab's historical analytics, aggregated by project/product/runner.
	fmt.Printf("\n[pipeline stats · by project: %d]\n", len(s.PipelineStats))
	for _, a := range head(s.PipelineStats, 10) {
		fmt.Printf("  %-30s runs=%-5d fail=%-4d(%3.0f%%) dur min/mean/p95/max = %s / %s / %s / %s\n",
			format.Elide(a.ProjectPath, 30), a.Runs, a.Failed, a.FailRate()*100,
			format.Duration(a.DurMin), format.Duration(a.DurMean), format.Duration(a.DurP95), format.Duration(a.DurMax))
	}

	fmt.Printf("\n[compute · by product: %d]\n", len(s.ComputeByProduct))
	for _, a := range head(s.ComputeByProduct, 10) {
		fmt.Printf("  %-24s compute=%8s (%3.0f%%)  jobs=%d\n", format.Trunc(a.Key, 24), format.Compute(a.Compute), a.Pct, a.Runs)
	}

	fmt.Printf("\n[compute · by project: %d]\n", len(s.ComputeByProject))
	for _, a := range head(s.ComputeByProject, 10) {
		fmt.Printf("  %-30s compute=%8s (%3.0f%%)  jobs=%d\n", format.Elide(a.Key, 30), format.Compute(a.Compute), a.Pct, a.Runs)
	}

	fmt.Printf("\n[tag performance: %d]\n", len(s.TagStats))
	for _, a := range head(s.TagStats, 10) {
		fmt.Printf("  %-30s jobs=%-5d compute=%8s mean=%8s queue mean/p95 = %s / %s  fail=%d\n",
			format.Trunc(a.Key, 30), a.Jobs, format.Compute(a.Compute), format.Duration(a.MeanDuration),
			format.Duration(a.MeanQueue), format.Duration(a.P95Queue), a.Failed)
	}

	fmt.Printf("\n[runner performance: %d]\n", len(s.RunnerStats))
	for _, a := range head(s.RunnerStats, 10) {
		fmt.Printf("  %-30s jobs=%-5d compute=%8s mean=%8s queue mean/p95 = %s / %s  fail=%d\n",
			format.Trunc(a.Key, 30), a.Jobs, format.Compute(a.Compute), format.Duration(a.MeanDuration),
			format.Duration(a.MeanQueue), format.Duration(a.P95Queue), a.Failed)
	}

	fmt.Printf("\n[running jobs: %d]\n", len(s.RunningJobs))
	for _, j := range head(s.RunningJobs, 10) {
		fmt.Printf("  %-30s %-18s %-9s  %s\n", format.Elide(j.ProjectPath, 30), format.Trunc(j.Name, 18), j.Status, format.Elapsed(j.Started, j.Created))
	}

	fmt.Printf("\n[recent jobs: %d]\n", len(s.RecentJobs))
	for _, j := range head(s.RecentJobs, 10) {
		fmt.Printf("  %-30s %-18s %-9s %9s  %s ago\n", format.Elide(j.ProjectPath, 30), format.Trunc(j.Name, 18), j.Status, format.Duration(j.Duration), format.Ago(j.Finished))
	}

	fmt.Printf("\n[top jobs / avg length: %d]\n", len(s.TopJobs))
	for _, a := range head(s.TopJobs, 10) {
		fmt.Printf("  %-30s %-18s runs=%-5d avg=%9s  success=%3.0f%%\n", format.Elide(a.ProjectPath, 30), format.Trunc(a.Name, 18), a.Count, format.Duration(a.AvgDuration), a.SuccessRate()*100)
	}

	if len(s.Errors) > 0 {
		fmt.Printf("\n[%d warning(s)]\n", len(s.Errors))
		for _, e := range s.Errors {
			fmt.Printf("  - %s\n", e)
		}
	}
}

// printActive renders one active-pipeline subtree indented by depth: the
// pipeline, then its jobs, then each downstream child pipeline recursively.
func printActive(ap gitlab.ActivePipeline, depth int) {
	indent := strings.Repeat("  ", depth)
	done, total := ap.Progress()
	when := format.Elapsed(ap.Started, ap.Created)
	if ap.Status.IsFinished() {
		when = format.Duration(ap.Duration)
	}
	fmt.Printf("  %s%-40s %-16s %-9s %8s  %d/%d\n",
		indent, format.Trunc(format.Base(ap.ProjectPath)+" · "+ap.Ref, 40), "", ap.Status, when, done, total)
	for _, j := range ap.Jobs {
		jwhen := format.Elapsed(j.Started, j.Created)
		if j.Status.IsFinished() {
			jwhen = format.Duration(j.Duration)
		}
		fmt.Printf("  %s  %-38s %-16s %-9s %8s\n",
			indent, format.Trunc(j.Stage+" · "+j.Name, 38), format.Trunc(j.Runner, 16), j.Status, jwhen)
	}
	for _, c := range ap.Children {
		printActive(c, depth+1)
	}
}

func head[T any](s []T, n int) []T {
	if len(s) > n {
		return s[:n]
	}
	return s
}
