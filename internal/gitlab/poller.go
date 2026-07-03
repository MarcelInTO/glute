package gitlab

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// PollOptions tunes a Poller. Zero values fall back to sensible defaults.
type PollOptions struct {
	RecentWindow   time.Duration // "recent failures & successes" lookback
	TopWindow      time.Duration // "top … last month" lookback
	TopLimit       int           // max rows in the Top panels
	Concurrency    int           // max projects/detail-fetches in parallel
	MaxDetailFetch int           // cap on pipeline-detail calls per refresh
}

func (o PollOptions) withDefaults() PollOptions {
	if o.RecentWindow <= 0 {
		o.RecentWindow = 24 * time.Hour
	}
	if o.TopWindow <= 0 {
		o.TopWindow = 30 * 24 * time.Hour
	}
	if o.TopLimit <= 0 {
		o.TopLimit = 20
	}
	if o.Concurrency <= 0 {
		o.Concurrency = 8
	}
	if o.MaxDetailFetch <= 0 {
		o.MaxDetailFetch = 1000
	}
	return o
}

// Poller fetches CI data for the configured products and assembles Snapshots.
// It implements Service.
type Poller struct {
	client *Client
	specs  []ProductSpec
	opts   PollOptions

	mu        sync.Mutex
	pipeCache map[int64]Pipeline // finished-pipeline detail cache (immutable data)
}

// NewPoller builds a Poller for the given client and product specs.
func NewPoller(client *Client, specs []ProductSpec, opts PollOptions) *Poller {
	return &Poller{
		client:    client,
		specs:     specs,
		opts:      opts.withDefaults(),
		pipeCache: map[int64]Pipeline{},
	}
}

// Refresh fetches current data and assembles a Snapshot. Per-project failures
// are folded into Snapshot.Errors rather than aborting the whole refresh, so
// the UI always gets whatever data succeeded.
func (p *Poller) Refresh(ctx context.Context) (Snapshot, error) {
	now := time.Now()
	topSince := now.Add(-p.opts.TopWindow)
	recentSince := now.Add(-p.opts.RecentWindow)

	projects, errs := resolveProjects(ctx, p.client, p.specs)
	if len(projects) == 0 {
		return Snapshot{UpdatedAt: now, Errors: errs}, nil
	}

	allPipes, allJobs, fetchErrs := p.fetchAll(ctx, projects, topSince)
	errs = append(errs, fetchErrs...)

	if skipped := p.enrichDurations(ctx, allPipes); skipped > 0 {
		errs = append(errs, fmt.Errorf(
			"skipped duration lookups for %d pipelines (MaxDetailFetch=%d); Top averages are partial",
			skipped, p.opts.MaxDetailFetch))
	}

	return Snapshot{
		Projects:         len(projects),
		RunningPipelines: runningPipelines(allPipes),
		RecentPipelines:  recentPipelines(allPipes, recentSince),
		TopPipelines:     topPipelines(allPipes, p.opts.TopLimit),
		RunningJobs:      runningJobs(allJobs),
		RecentJobs:       recentJobs(allJobs, recentSince),
		TopJobs:          topJobs(allJobs, p.opts.TopLimit),
		UpdatedAt:        now,
		Errors:           errs,
	}, nil
}

// fetchAll fans out per-project fetches with bounded concurrency and gathers the
// combined pipelines, jobs, and any per-project errors.
func (p *Poller) fetchAll(ctx context.Context, projects []Project, topSince time.Time) ([]Pipeline, []Job, []error) {
	type result struct {
		pipes []Pipeline
		jobs  []Job
		err   error
	}
	results := make([]result, len(projects))

	sem := make(chan struct{}, p.opts.Concurrency)
	var wg sync.WaitGroup
	for i := range projects {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			pipes, jobs, err := p.fetchProject(ctx, projects[i], topSince)
			results[i] = result{pipes: pipes, jobs: jobs, err: err}
		}(i)
	}
	wg.Wait()

	var allPipes []Pipeline
	var allJobs []Job
	var errs []error
	for _, r := range results {
		if r.err != nil {
			errs = append(errs, r.err)
		}
		allPipes = append(allPipes, r.pipes...)
		allJobs = append(allJobs, r.jobs...)
	}
	return allPipes, allJobs, errs
}

// jobFetchScopes lists the job statuses glute pulls per project: the not-yet-
// finished states (created, pending, running) so the Running panel includes
// queued work, plus success and failed for the Recent and Top panels.
var jobFetchScopes = []Status{
	StatusCreated,
	StatusPending,
	StatusRunning,
	StatusSuccess,
	StatusFailed,
}

// fetchProject fetches one project's pipelines and jobs, tagging each with the
// project path. A failure in one call doesn't discard the other's data.
func (p *Poller) fetchProject(ctx context.Context, proj Project, topSince time.Time) ([]Pipeline, []Job, error) {
	var errList []error

	pipes, err := p.client.ListPipelines(ctx, proj.ID, topSince)
	if err != nil {
		errList = append(errList, err)
	}
	for i := range pipes {
		pipes[i].ProjectPath = proj.Path
	}

	jobs, err := p.client.ListJobs(ctx, proj.ID, jobFetchScopes, topSince)
	if err != nil {
		errList = append(errList, err)
	}
	for i := range jobs {
		jobs[i].ProjectPath = proj.Path
	}

	return pipes, jobs, errors.Join(errList...)
}

// enrichDurations fills in pipeline durations from the detail endpoint, serving
// finished pipelines from an immutable cache and bounding network work by
// MaxDetailFetch. It returns the number of pipelines left unenriched due to the
// cap (newest are enriched first). Distinct slice indices are written from
// separate goroutines, which is data-race-free.
func (p *Poller) enrichDurations(ctx context.Context, pipes []Pipeline) int {
	sort.Slice(pipes, func(i, j int) bool { return pipes[i].Updated.After(pipes[j].Updated) })

	var toFetch []int
	for i := range pipes {
		if cached, ok := p.cachedPipe(pipes[i].ID); ok {
			applyDetail(&pipes[i], cached)
			continue
		}
		toFetch = append(toFetch, i)
	}

	skipped := 0
	if len(toFetch) > p.opts.MaxDetailFetch {
		skipped = len(toFetch) - p.opts.MaxDetailFetch
		toFetch = toFetch[:p.opts.MaxDetailFetch]
	}

	sem := make(chan struct{}, p.opts.Concurrency)
	var wg sync.WaitGroup
	for _, idx := range toFetch {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int) {
			defer wg.Done()
			defer func() { <-sem }()
			detail, err := p.client.GetPipeline(ctx, pipes[idx].ProjectID, pipes[idx].ID)
			if err != nil {
				return // leave unenriched; non-fatal
			}
			applyDetail(&pipes[idx], detail)
			if pipes[idx].Status.IsFinished() {
				p.cachePipe(pipes[idx])
			}
		}(idx)
	}
	wg.Wait()
	return skipped
}

func applyDetail(dst *Pipeline, detail Pipeline) {
	dst.Duration = detail.Duration
	dst.Started = detail.Started
	dst.Finished = detail.Finished
}

func (p *Poller) cachedPipe(id int64) (Pipeline, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	v, ok := p.pipeCache[id]
	return v, ok
}

func (p *Poller) cachePipe(pipe Pipeline) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pipeCache[pipe.ID] = pipe
}
