package gitlab

import (
	"context"
	"sort"
)

// projectSource is the subset of Client the resolver needs. Depending on an
// interface (not *Client) lets the resolver be unit-tested without a live
// instance.
type projectSource interface {
	ListGroupProjects(ctx context.Context, group string) ([]Project, error)
	GetProject(ctx context.Context, path string) (Project, error)
}

var _ projectSource = (*Client)(nil)

// resolveProjects expands product specs into the deduplicated set of projects
// to poll, tagging each with the product(s) that matched it. Per-group and
// per-project failures are collected and returned alongside whatever resolved
// successfully (partial success), so one bad group doesn't sink the refresh.
func resolveProjects(ctx context.Context, src projectSource, specs []ProductSpec) ([]Project, []error) {
	byID := map[int64]*Project{}
	var errs []error

	add := func(p Project, product string) {
		if existing, ok := byID[p.ID]; ok {
			existing.Products = appendUnique(existing.Products, product)
			return
		}
		clone := p
		clone.Products = []string{product}
		byID[p.ID] = &clone
	}

	for _, spec := range specs {
		for _, group := range spec.Groups {
			projects, err := src.ListGroupProjects(ctx, group)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			for _, p := range projects {
				add(p, spec.Name)
			}
		}
		for _, path := range spec.Projects {
			p, err := src.GetProject(ctx, path)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			add(p, spec.Name)
		}
	}

	out := make([]Project, 0, len(byID))
	for _, p := range byID {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, errs
}

func appendUnique(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}
