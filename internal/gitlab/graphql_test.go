package gitlab

import (
	"encoding/json"
	"testing"
	"time"
)

func TestParseGID(t *testing.T) {
	cases := map[string]int64{
		"gid://gitlab/Ci::Build/727460":  727460,
		"gid://gitlab/Ci::Bridge/1":      1,
		"gid://gitlab/Ci::Pipeline/9999": 9999,
		"":                               0,
		"garbage":                        0,
		"gid://gitlab/Ci::Build/notnum":  0,
	}
	for in, want := range cases {
		if got := parseGID(in); got != want {
			t.Errorf("parseGID(%q) = %d, want %d", in, got, want)
		}
	}
}

// A captured-shape GraphQL payload: two BUILD jobs (one with a need), a BRIDGE
// with a downstream child, and a CANCELED job that the scope filter drops.
const samplePipelinePayload = `{
  "project": { "pipeline": {
    "id": "gid://gitlab/Ci::Pipeline/999",
    "status": "RUNNING",
    "ref": "main",
    "createdAt": "2026-07-14T13:00:00Z",
    "startedAt": "2026-07-14T13:00:05Z",
    "duration": 42,
    "user": { "username": "alice" },
    "jobs": {
      "pageInfo": { "hasNextPage": false, "endCursor": "" },
      "nodes": [
        {"id":"gid://gitlab/Ci::Build/100","name":"build","kind":"BUILD","status":"SUCCESS",
         "createdAt":"2026-07-14T13:00:00Z","startedAt":"2026-07-14T13:00:05Z","finishedAt":"2026-07-14T13:00:20Z",
         "duration":15,"queuedDuration":2,"stage":{"name":"build"},"tags":["linux","docker"],"needs":{"nodes":[]},
         "runnerManager":{"runner":{"description":"linux-docker-1"}},"downstreamPipeline":null},
        {"id":"gid://gitlab/Ci::Build/101","name":"test","kind":"BUILD","status":"RUNNING",
         "startedAt":"2026-07-14T13:00:25Z","stage":{"name":"test"},"needs":{"nodes":[{"name":"build"}]},
         "runnerManager":{"runner":{"description":"linux-docker-2"}},"downstreamPipeline":null},
        {"id":"gid://gitlab/Ci::Bridge/102","name":"trigger-deploy","kind":"BRIDGE","status":"SUCCESS",
         "needs":{"nodes":[{"name":"test"}]},"downstreamPipeline":{"iid":"77","project":{"fullPath":"grp/deploy"}}},
        {"id":"gid://gitlab/Ci::Build/103","name":"stale","kind":"BUILD","status":"CANCELED",
         "stage":{"name":"test"},"needs":{"nodes":[]},"downstreamPipeline":null}
      ]
    }
  }}
}`

func TestCollectPipelineNodeMapsJobsAndChildren(t *testing.T) {
	var resp gqlPipelineResp
	if err := json.Unmarshal([]byte(samplePipelinePayload), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	pn := resp.Project.Pipeline

	pipe := mapGQLPipeline(pn, "grp/app")
	if pipe.ID != 999 || pipe.Status != StatusRunning || pipe.Ref != "main" {
		t.Errorf("pipeline mapped wrong: %+v", pipe)
	}
	if pipe.Duration != 42*time.Second {
		t.Errorf("pipeline duration = %s, want 42s", pipe.Duration)
	}
	if pipe.User != "alice" {
		t.Errorf("pipeline user = %q, want alice", pipe.User)
	}

	jobs, children := collectPipelineNode(pn, "grp/app", statusSet(jobFetchScopes))

	// BRIDGE and the CANCELED (out-of-scope) job are excluded → only build, test.
	if jobNames(jobs) != "build,test" {
		t.Fatalf("jobs = %q, want build,test", jobNames(jobs))
	}
	build, testJob := jobs[0], jobs[1]
	if build.ID != 100 || build.PipelineID != 999 || build.Stage != "build" ||
		build.Runner != "linux-docker-1" || build.Status != StatusSuccess ||
		build.Duration != 15*time.Second || build.Queued != 2*time.Second || len(build.Needs) != 0 {
		t.Errorf("build job mapped wrong: %+v", build)
	}
	if len(build.Tags) != 2 || build.Tags[0] != "linux" || build.Tags[1] != "docker" {
		t.Errorf("build job tags = %v, want [linux docker]", build.Tags)
	}
	if testJob.ID != 101 || testJob.Status != StatusRunning || testJob.Runner != "linux-docker-2" ||
		len(testJob.Needs) != 1 || testJob.Needs[0] != "build" || len(testJob.Tags) != 0 {
		t.Errorf("test job mapped wrong: %+v", testJob)
	}

	// The bridge's downstream becomes a child ref (path + iid); the bridge itself
	// is not a job row.
	if len(children) != 1 || children[0].projectPath != "grp/deploy" || children[0].iid != 77 {
		t.Errorf("children = %+v, want one {grp/deploy, 77}", children)
	}
}
