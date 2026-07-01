package gitlab

import (
	"context"
	"fmt"
	"testing"
)

type fakeSource struct {
	groups   map[string][]Project
	projects map[string]Project
}

func (f fakeSource) ListGroupProjects(_ context.Context, group string) ([]Project, error) {
	return f.groups[group], nil
}

func (f fakeSource) GetProject(_ context.Context, path string) (Project, error) {
	p, ok := f.projects[path]
	if !ok {
		return Project{}, fmt.Errorf("project not found: %s", path)
	}
	return p, nil
}

func TestResolveProjectsDedupesAndTags(t *testing.T) {
	src := fakeSource{
		groups: map[string][]Project{
			"g1": {{ID: 1, Path: "g1/a"}, {ID: 2, Path: "g1/b"}},
		},
		projects: map[string]Project{
			"solo/repo": {ID: 3, Path: "solo/repo"},
		},
	}
	specs := []ProductSpec{
		{Name: "Payments", Groups: []string{"g1"}, Projects: []string{"solo/repo"}},
		{Name: "Platform", Groups: []string{"g1"}}, // overlaps g1 → project 1 & 2
	}

	projects, errs := resolveProjects(context.Background(), src, specs)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(projects) != 3 {
		t.Fatalf("want 3 unique projects, got %d: %+v", len(projects), projects)
	}

	byID := map[int64]Project{}
	for _, p := range projects {
		byID[p.ID] = p
	}
	if got := byID[1].Products; len(got) != 2 {
		t.Errorf("project 1 should be tagged with both products, got %v", got)
	}
	if got := byID[3].Products; len(got) != 1 || got[0] != "Payments" {
		t.Errorf("solo repo should be tagged Payments only, got %v", got)
	}
}

func TestResolveProjectsCollectsErrorsPartially(t *testing.T) {
	src := fakeSource{
		groups: map[string][]Project{"g1": {{ID: 1, Path: "g1/a"}}},
		// no "missing/repo" in projects → GetProject errors
	}
	specs := []ProductSpec{
		{Name: "P", Groups: []string{"g1"}, Projects: []string{"missing/repo"}},
	}

	projects, errs := resolveProjects(context.Background(), src, specs)
	if len(projects) != 1 {
		t.Fatalf("want 1 resolved project despite the error, got %d", len(projects))
	}
	if len(errs) != 1 {
		t.Fatalf("want 1 collected error, got %d: %v", len(errs), errs)
	}
}
