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
	"os"
	"time"

	glab "gitlab.com/gitlab-org/api/client-go"
)

// maxJobPages bounds the jobs pagination per project. The jobs endpoint has no
// time filter, so we page newest-first and stop early once past the window;
// this is a backstop against a runaway on unexpectedly busy projects.
const maxJobPages = 50

// Client is glute's handle to a GitLab instance.
type Client struct {
	api *glab.Client
	url string
}

// NewClient builds a GitLab API client for the given instance URL and token.
// caCertPath is optional; when set, its PEM is added to the trusted roots
// (useful for self-managed instances behind a corporate CA).
func NewClient(url, token, caCertPath string) (*Client, error) {
	opts := []glab.ClientOptionFunc{glab.WithBaseURL(url)}

	if caCertPath != "" {
		httpClient, err := httpClientWithCA(caCertPath)
		if err != nil {
			return nil, err
		}
		opts = append(opts, glab.WithHTTPClient(httpClient))
	}

	api, err := glab.NewClient(token, opts...)
	if err != nil {
		return nil, fmt.Errorf("creating GitLab client: %w", err)
	}
	return &Client{api: api, url: url}, nil
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

// ListJobs returns jobs for a project matching the given scopes, back to since.
// The jobs endpoint carries durations directly, so no per-job enrichment is
// needed.
func (c *Client) ListJobs(ctx context.Context, projectID int64, scopes []Status, since time.Time) ([]Job, error) {
	scopeVals := make([]glab.BuildStateValue, 0, len(scopes))
	for _, s := range scopes {
		scopeVals = append(scopeVals, glab.BuildStateValue(s))
	}
	opt := &glab.ListJobsOptions{}
	if len(scopeVals) > 0 {
		opt.Scope = &scopeVals
	}
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
		ProjectID: projectID,
		Ref:       p.Ref,
		SHA:       p.SHA,
		Status:    Status(p.Status),
		Source:    string(p.Source),
		WebURL:    p.WebURL,
		Created:   derefTime(p.CreatedAt),
		Updated:   derefTime(p.UpdatedAt),
		Started:   derefTime(p.StartedAt),
		Finished:  derefTime(p.FinishedAt),
		Duration:  time.Duration(p.Duration) * time.Second,
	}
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
		Created:       derefTime(j.CreatedAt),
		Started:       derefTime(j.StartedAt),
		Finished:      derefTime(j.FinishedAt),
		Duration:      secondsToDuration(j.Duration),
		Queued:        secondsToDuration(j.QueuedDuration),
	}
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
