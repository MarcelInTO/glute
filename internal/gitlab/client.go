// Package gitlab wraps the upstream GitLab API client so the rest of glute
// depends on a small, stable, mockable surface rather than the third-party API
// directly. All translation from upstream types to glute's domain types happens
// here.
package gitlab

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	neturl "net/url"
	"os"
	"strconv"
	"strings"
	"time"

	glab "gitlab.com/gitlab-org/api/client-go"
)

// maxJobPages bounds the project-wide jobs pagination used for the bulk backfill.
// That endpoint has no time filter, so we page newest-first and stop once past
// the window; this is a backstop against a runaway on unexpectedly busy projects.
const maxJobPages = 50

// Client is glute's handle to a GitLab instance. It wraps the REST client-go
// client and a small GraphQL client (used only where REST can't reach, e.g. job
// needs: dependencies).
type Client struct {
	api    *glab.Client
	gql    *gqlClient
	url    string
	origin string // scheme://host of url, which GraphQL's page paths are relative to
}

// NewClient builds a GitLab API client for the given instance URL and token.
// caCertPath is optional; when set, its PEM is added to the trusted roots
// (useful for self-managed instances behind a corporate CA).
func NewClient(url, token, caCertPath string) (*Client, error) {
	opts := []glab.ClientOptionFunc{glab.WithBaseURL(url)}

	var httpClient *http.Client
	if caCertPath != "" {
		hc, err := httpClientWithCA(caCertPath)
		if err != nil {
			return nil, err
		}
		httpClient = hc
		opts = append(opts, glab.WithHTTPClient(httpClient))
	}

	api, err := glab.NewClient(token, opts...)
	if err != nil {
		return nil, fmt.Errorf("creating GitLab client: %w", err)
	}
	// The GraphQL client reuses the same CA-aware HTTP client (nil → default).
	return &Client{api: api, gql: newGQLClient(url, token, httpClient), url: url, origin: webOrigin(url)}, nil
}

// webOrigin is the scheme://host of an instance URL. GraphQL returns web pages
// as paths from the host root (Pipeline.path, CiJob.webPath:
// "/grp/app/-/pipelines/9"), and those already include any relative URL root
// the instance is served under — so they're joined to the origin, never to
// the full instance URL, which would double such a prefix.
func webOrigin(instance string) string {
	u, err := neturl.Parse(instance)
	if err != nil || u.Host == "" {
		return strings.TrimRight(instance, "/")
	}
	return u.Scheme + "://" + u.Host
}

// webURL joins a GraphQL page path to the instance origin, or returns "" when
// there's no path (a field the instance didn't return).
func webURL(origin, path string) string {
	if path == "" || origin == "" {
		return ""
	}
	return origin + path
}

// WhoAmI returns the username for the authenticated token, or an error if the
// token is invalid or the instance is unreachable.
func (c *Client) WhoAmI(ctx context.Context) (string, error) {
	user, _, err := c.api.Users.CurrentUser(glab.WithContext(ctx))
	if err != nil {
		return "", err
	}
	return user.Username, nil
}

// ListGroupProjects returns every non-archived project in the group, recursing
// into subgroups.
func (c *Client) ListGroupProjects(ctx context.Context, group string) ([]Project, error) {
	opt := &glab.ListGroupProjectsOptions{
		IncludeSubGroups: glab.Ptr(true),
		Archived:         glab.Ptr(false),
		Simple:           glab.Ptr(true),
	}
	opt.PerPage = 100
	opt.Page = 1

	var out []Project
	for {
		projects, resp, err := c.api.Groups.ListGroupProjects(group, opt, glab.WithContext(ctx))
		if err != nil {
			return nil, fmt.Errorf("listing projects for group %q: %w", group, err)
		}
		for _, p := range projects {
			out = append(out, mapProject(p))
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return out, nil
}

// GetProject resolves a single project by its "namespace/path".
func (c *Client) GetProject(ctx context.Context, path string) (Project, error) {
	p, _, err := c.api.Projects.GetProject(path, nil, glab.WithContext(ctx))
	if err != nil {
		return Project{}, fmt.Errorf("getting project %q: %w", path, err)
	}
	return mapProject(p), nil
}

// ListPipelines returns pipelines for a project updated at or after
// updatedAfter, newest first. The results carry no duration (the list endpoint
// omits it); use GetPipeline to enrich.
func (c *Client) ListPipelines(ctx context.Context, projectID int64, updatedAfter time.Time) ([]Pipeline, error) {
	opt := &glab.ListProjectPipelinesOptions{
		UpdatedAfter: &updatedAfter,
		OrderBy:      glab.Ptr("updated_at"),
		Sort:         glab.Ptr("desc"),
	}
	opt.PerPage = 100
	opt.Page = 1

	var out []Pipeline
	for {
		infos, resp, err := c.api.Pipelines.ListProjectPipelines(projectID, opt, glab.WithContext(ctx))
		if err != nil {
			return nil, fmt.Errorf("listing pipelines for project %d: %w", projectID, err)
		}
		for _, pi := range infos {
			out = append(out, mapPipelineInfo(pi))
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return out, nil
}

// GetPipeline fetches a single pipeline's detail, including its duration.
func (c *Client) GetPipeline(ctx context.Context, projectID, pipelineID int64) (Pipeline, error) {
	p, _, err := c.api.Pipelines.GetPipeline(projectID, pipelineID, glab.WithContext(ctx))
	if err != nil {
		return Pipeline{}, fmt.Errorf("getting pipeline %d of project %d: %w", pipelineID, projectID, err)
	}
	return mapPipelineDetail(projectID, p), nil
}

// ListJobs returns a project's jobs matching the given scopes, back to since,
// newest-first. It's the bulk backfill used for the full-window fetch: the
// project-wide endpoint includes child-pipeline jobs and pages efficiently
// (~one page per project on a typical instance), so it's far cheaper than
// walking every pipeline. The jobs endpoint carries durations directly.
func (c *Client) ListJobs(ctx context.Context, projectID int64, scopes []Status, since time.Time) ([]Job, error) {
	opt := &glab.ListJobsOptions{Scope: scopeValues(scopes)}
	opt.PerPage = 100
	opt.Page = 1

	var out []Job
	for range maxJobPages {
		jobs, resp, err := c.api.Jobs.ListProjectJobs(projectID, opt, glab.WithContext(ctx))
		if err != nil {
			return nil, fmt.Errorf("listing jobs for project %d: %w", projectID, err)
		}
		pastWindow := false
		for _, j := range jobs {
			job := mapJob(j)
			if !since.IsZero() && !job.Created.IsZero() && job.Created.Before(since) {
				pastWindow = true
				continue
			}
			out = append(out, job)
		}
		// Jobs come newest-first, so once a page dips below the window we're done.
		if resp.NextPage == 0 || pastWindow {
			break
		}
		opt.Page = resp.NextPage
	}
	return out, nil
}

// scopeValues converts glute Statuses to the upstream scope filter, or nil (all
// scopes) when none are given.
func scopeValues(scopes []Status) *[]glab.BuildStateValue {
	if len(scopes) == 0 {
		return nil
	}
	vals := make([]glab.BuildStateValue, 0, len(scopes))
	for _, s := range scopes {
		vals = append(vals, glab.BuildStateValue(s))
	}
	return &vals
}

// pipelineJobsQuery fetches one pipeline's own fields plus its jobs — each with
// its needs: dependencies, stage, runner, tags, and (for bridge jobs) the
// downstream child pipeline it triggers. The jobs connection is paginated via
// $cursor. It returns retried (superseded) attempts alongside the latest ones —
// `retried` marks which is which — so the store sees every run that consumed a
// runner, and a view that wants only the latest attempt filters on the flag.
const pipelineJobsQuery = `query PipelineJobs($path: ID!, $iid: ID!, $cursor: String) {
  project(fullPath: $path) {
    pipeline(iid: $iid) {
      id
      status
      ref
      createdAt
      startedAt
      finishedAt
      duration
      path
      user { username }
      jobs(first: 100, after: $cursor) {
        pageInfo { hasNextPage endCursor }
        nodes {
          id
          name
          kind
          status
          retried
          createdAt
          startedAt
          finishedAt
          duration
          queuedDuration
          stage { name }
          tags
          needs { nodes { name } }
          runnerManager { runner { description } }
          webPath
          downstreamPipeline { iid project { fullPath } }
        }
      }
    }
  }
}`

type gqlJobNode struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Kind           string     `json:"kind"`
	Status         string     `json:"status"`
	Retried        bool       `json:"retried"`
	CreatedAt      *time.Time `json:"createdAt"`
	StartedAt      *time.Time `json:"startedAt"`
	FinishedAt     *time.Time `json:"finishedAt"`
	Duration       *float64   `json:"duration"`
	QueuedDuration *float64   `json:"queuedDuration"`
	Stage          *struct {
		Name string `json:"name"`
	} `json:"stage"`
	Tags  []string `json:"tags"`
	Needs struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"needs"`
	RunnerManager *struct {
		Runner *struct {
			Description string `json:"description"`
		} `json:"runner"`
	} `json:"runnerManager"`
	WebPath            string `json:"webPath"`
	DownstreamPipeline *struct {
		IID     string `json:"iid"`
		Project struct {
			FullPath string `json:"fullPath"`
		} `json:"project"`
	} `json:"downstreamPipeline"`
}

type gqlPipelineNode struct {
	ID         string     `json:"id"`
	Status     string     `json:"status"`
	Ref        string     `json:"ref"`
	CreatedAt  *time.Time `json:"createdAt"`
	StartedAt  *time.Time `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt"`
	Duration   *float64   `json:"duration"`
	Path       string     `json:"path"`
	User       *struct {
		Username string `json:"username"`
	} `json:"user"`
	Jobs struct {
		PageInfo struct {
			HasNextPage bool   `json:"hasNextPage"`
			EndCursor   string `json:"endCursor"`
		} `json:"pageInfo"`
		Nodes []gqlJobNode `json:"nodes"`
	} `json:"jobs"`
}

type gqlPipelineResp struct {
	Project *struct {
		Pipeline *gqlPipelineNode `json:"pipeline"`
	} `json:"project"`
}

// childRef is a downstream child pipeline discovered while fetching a pipeline's
// jobs — enough to fetch it in turn (its project path and iid). retried marks a
// child triggered by a bridge attempt that was later retried: a superseded
// run, which the store still follows (its jobs used runners) but a view of the
// pipeline as it finished leaves out.
type childRef struct {
	projectPath string
	iid         int64
	retried     bool
}

// FetchPipelineJobTree fetches, via GraphQL, one pipeline's own fields, its jobs
// (each with needs:), and the downstream child pipelines it triggers. BUILD jobs
// become domain Jobs (tagged with projectPath, filtered to scopes); BRIDGE jobs
// aren't job rows but yield child refs. The returned Pipeline carries the queried
// pipeline's id/ref/status/timing (ProjectPath set to projectPath) so the caller
// can record child metadata. A missing pipeline (deleted/inaccessible) returns
// zero values and no error.
func (c *Client) FetchPipelineJobTree(ctx context.Context, projectPath string, iid int64, scopes []Status) (Pipeline, []Job, []childRef, error) {
	scopeSet := statusSet(scopes)
	var (
		pipe     Pipeline
		jobs     []Job
		children []childRef
		cursor   string
		gotPipe  bool
	)
	for {
		vars := map[string]any{"path": projectPath, "iid": strconv.FormatInt(iid, 10)}
		if cursor != "" {
			vars["cursor"] = cursor
		}
		var resp gqlPipelineResp
		if err := c.gql.query(ctx, pipelineJobsQuery, vars, &resp); err != nil {
			return Pipeline{}, nil, nil, fmt.Errorf("graphql jobs for %s!%d: %w", projectPath, iid, err)
		}
		if resp.Project == nil || resp.Project.Pipeline == nil {
			break // pipeline not found (deleted/inaccessible) — treat as no jobs
		}
		pn := resp.Project.Pipeline
		if !gotPipe {
			pipe = mapGQLPipeline(pn, projectPath, c.origin)
			gotPipe = true
		}
		js, kids := collectPipelineNode(pn, projectPath, c.origin, scopeSet)
		jobs = append(jobs, js...)
		children = append(children, kids...)
		if !pn.Jobs.PageInfo.HasNextPage || pn.Jobs.PageInfo.EndCursor == "" {
			break
		}
		cursor = pn.Jobs.PageInfo.EndCursor
	}
	return pipe, jobs, children, nil
}

// collectPipelineNode maps one fetched pipeline node's jobs into domain Jobs
// (BUILD jobs only, tagged with projectPath and filtered to scopeSet, their web
// pages joined to origin) and its downstream child pipeline refs (from BRIDGE
// jobs). Pure, so it's unit-tested against a captured payload without touching
// the network.
func collectPipelineNode(pn *gqlPipelineNode, projectPath, origin string, scopeSet map[Status]bool) (jobs []Job, children []childRef) {
	pipelineID := parseGID(pn.ID)
	for i := range pn.Jobs.Nodes {
		n := &pn.Jobs.Nodes[i]
		if n.DownstreamPipeline != nil {
			children = append(children, childRef{
				projectPath: n.DownstreamPipeline.Project.FullPath,
				iid:         parseIID(n.DownstreamPipeline.IID),
				retried:     n.Retried,
			})
		}
		if strings.EqualFold(n.Kind, "BRIDGE") {
			continue // bridges aren't job rows; they yielded the child ref above
		}
		job := mapGQLJob(n, projectPath, origin, pipelineID)
		if scopeSet != nil && !scopeSet[job.Status] {
			continue
		}
		jobs = append(jobs, job)
	}
	return jobs, children
}

func mapGQLPipeline(pn *gqlPipelineNode, projectPath, origin string) Pipeline {
	pipe := Pipeline{
		ID:          parseGID(pn.ID),
		ProjectPath: projectPath,
		WebURL:      webURL(origin, pn.Path),
		Ref:         pn.Ref,
		Status:      Status(strings.ToLower(pn.Status)),
		Created:     derefTime(pn.CreatedAt),
		Started:     derefTime(pn.StartedAt),
		Finished:    derefTime(pn.FinishedAt),
		Duration:    secondsPtr(pn.Duration),
	}
	if pn.User != nil {
		pipe.User = pn.User.Username
	}
	return pipe
}

func mapGQLJob(n *gqlJobNode, projectPath, origin string, pipelineID int64) Job {
	j := Job{
		ID:          parseGID(n.ID),
		Name:        n.Name,
		Status:      Status(strings.ToLower(n.Status)),
		ProjectPath: projectPath,
		PipelineID:  pipelineID,
		WebURL:      webURL(origin, n.WebPath),
		Tags:        n.Tags,
		Retried:     n.Retried,
		Created:     derefTime(n.CreatedAt),
		Started:     derefTime(n.StartedAt),
		Finished:    derefTime(n.FinishedAt),
		Duration:    secondsPtr(n.Duration),
		Queued:      secondsPtr(n.QueuedDuration),
	}
	if n.Stage != nil {
		j.Stage = n.Stage.Name
	}
	if n.RunnerManager != nil && n.RunnerManager.Runner != nil {
		j.Runner = n.RunnerManager.Runner.Description
	}
	for _, need := range n.Needs.Nodes {
		j.Needs = append(j.Needs, need.Name)
	}
	return j
}

// parseGID extracts the trailing numeric id from a GitLab global id such as
// "gid://gitlab/Ci::Build/727460" (works for Ci::Bridge and Ci::Pipeline too).
// Returns 0 if there's no numeric suffix.
func parseGID(gid string) int64 {
	i := strings.LastIndex(gid, "/")
	if i < 0 {
		return 0
	}
	return parseIID(gid[i+1:])
}

func parseIID(s string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func secondsPtr(s *float64) time.Duration {
	if s == nil {
		return 0
	}
	return secondsToDuration(*s)
}

func statusSet(scopes []Status) map[Status]bool {
	if len(scopes) == 0 {
		return nil
	}
	m := make(map[Status]bool, len(scopes))
	for _, s := range scopes {
		m[s] = true
	}
	return m
}

func mapProject(p *glab.Project) Project {
	return Project{
		ID:       p.ID,
		Path:     p.PathWithNamespace,
		Name:     p.Name,
		WebURL:   p.WebURL,
		Archived: p.Archived,
	}
}

func mapPipelineInfo(pi *glab.PipelineInfo) Pipeline {
	return Pipeline{
		ID:        pi.ID,
		IID:       pi.IID,
		ProjectID: pi.ProjectID,
		Ref:       pi.Ref,
		SHA:       pi.SHA,
		Status:    Status(pi.Status),
		Source:    pi.Source,
		WebURL:    pi.WebURL,
		Created:   derefTime(pi.CreatedAt),
		Updated:   derefTime(pi.UpdatedAt),
	}
}

func mapPipelineDetail(projectID int64, p *glab.Pipeline) Pipeline {
	return Pipeline{
		ID:        p.ID,
		IID:       p.IID,
		ProjectID: projectID,
		Ref:       p.Ref,
		SHA:       p.SHA,
		Status:    Status(p.Status),
		Source:    string(p.Source),
		User:      basicUserName(p.User),
		WebURL:    p.WebURL,
		Created:   derefTime(p.CreatedAt),
		Updated:   derefTime(p.UpdatedAt),
		Started:   derefTime(p.StartedAt),
		Finished:  derefTime(p.FinishedAt),
		Duration:  time.Duration(p.Duration) * time.Second,
	}
}

// basicUserName returns a user's username, or "" when unset (the pipeline list
// endpoint omits the user, and some system-triggered pipelines have none).
func basicUserName(u *glab.BasicUser) string {
	if u == nil {
		return ""
	}
	return u.Username
}

func mapJob(j *glab.Job) Job {
	return Job{
		ID:            j.ID,
		Name:          j.Name,
		Stage:         j.Stage,
		Status:        Status(j.Status),
		Ref:           j.Ref,
		PipelineID:    j.Pipeline.ID,
		WebURL:        j.WebURL,
		FailureReason: j.FailureReason,
		Runner:        runnerName(j.Runner),
		Tags:          j.TagList,
		Created:       derefTime(j.CreatedAt),
		Started:       derefTime(j.StartedAt),
		Finished:      derefTime(j.FinishedAt),
		Duration:      secondsToDuration(j.Duration),
		Queued:        secondsToDuration(j.QueuedDuration),
	}
}

// runnerName picks the human-facing label for a job's runner: GitLab shows the
// runner's description in its UI, so prefer that, falling back to the name (and
// "" when no runner is assigned yet, e.g. a still-queued job).
func runnerName(r glab.JobRunner) string {
	if r.Description != "" {
		return r.Description
	}
	return r.Name
}

func derefTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

func secondsToDuration(s float64) time.Duration {
	return time.Duration(s * float64(time.Second))
}

func httpClientWithCA(caCertPath string) (*http.Client, error) {
	pem, err := os.ReadFile(caCertPath)
	if err != nil {
		return nil, fmt.Errorf("reading CA cert: %w", err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("no valid certificates found in %s", caCertPath)
	}
	return &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}},
	}, nil
}
