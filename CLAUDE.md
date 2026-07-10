# glute

A single-binary, read-only terminal (TUI) dashboard for GitLab CI/CD: pipeline
and job stats across a configured watchlist of "products". Go, cross-platform.

## Build & test

- `make` — cross-compile Linux/macOS/Windows binaries into `bin/` (gitignored)
- `make build` — host-only build to `bin/glute`
- `make run ARGS="--sample"` — build and run
- `make test` / `go test ./...`

Stack: Go 1.26, cobra (CLI), rivo/tview + gdamore/tcell (TUI),
pelletier/go-toml/v2 (config), `gitlab.com/gitlab-org/api/client-go` (the
official successor to xanzy/go-gitlab). Keep it CGO-free so cross-compilation
stays trivial.

## Key decisions (why)

- **"product" is the config noun** for a named set of GitLab groups/repos.
  Chosen over "workspace"/"context" (overloaded for this team) and avoids
  collision with GitLab's own "project" (a repo) and "group".
- **Read-only, `read_api` scope only.** No plans to perform write actions.
- **Scope = a configured watchlist**, aggregated across all products in v1
  (per-product filtering is future). There is no cheap instance-wide
  "everything running" GitLab endpoint for non-admins — you must enumerate
  projects per group and poll each.
- **No Runners tab.** Runner utilization isn't a first-class API metric and
  `/runners/all` needs admin; dropping it keeps glute entirely non-admin.
- **Auth = a 0600 token file + `GLUTE_TOKEN` env override; OS keychain
  deferred.** glute is expected to run on headless Linux/SSH (by some users)
  where a keychain is unavailable, so file/env (like `gh`/`glab`) is the
  default. Secrets live beside config, never in `config.toml`.
- **Instances**: the `default` instance uses the base config dir directly;
  named instances get a subdirectory. Zero migration — an existing
  single-instance setup keeps working as `default`.
- **Data layer = one `Service.Refresh(ctx) → Snapshot`** that feeds all six
  panels, so the UI never shows a half-loaded state. The UI depends on the
  `Service` interface (fake + `SampleSnapshot` for tests).
- **Incremental retained store (the `Poller`), not a full fetch per refresh.**
  The Poller keeps an in-memory store of the pipelines and jobs within the Top
  window and updates it incrementally: each refresh fetches only pipelines
  changed since the last (`updated_after` + a small overlap) and the jobs of the
  few pipelines that are active or just changed; terminal pipelines/jobs are
  immutable, learned once and kept until they age out. A periodic full re-list
  (`ResyncInterval`, default 10m) reconciles drift; the group→project resolve is
  cached (`ResolveTTL`, default 10m). This took a real 42-project instance from
  ~9.4s of fetching every refresh to <1s warm (see the `RefreshStats` exit
  summary and the per-refresh `refresh timing:` log line). Snapshots are still
  built by the pure aggregate functions over the whole store, so the UI is
  unchanged. `--sample`/`refresh` build a fresh Poller (cold path) each run.

## GitLab API notes

- The pipeline **list** endpoint omits `duration` — computing "avg length"
  needs a per-pipeline **detail** fetch. The retained store keeps the immutable
  durations of finished pipelines, so enrichment only ever fetches details for
  newly-changed pipelines (cheap once warm; measured ~0s on an idle instance).
- The pipeline list supports `updated_after` and orders by `updated_at desc`, so
  the incremental delta = "pipelines whose status changed since last refresh."
- **Jobs use two paths.** The full-window backfill (cold start + periodic
  resync) uses the project-wide jobs list (`ListJobs`): it's cheap (~1 page per
  project) and — crucially — *includes child-pipeline jobs*. The warm delta
  fetches jobs per pipeline (`ListPipelineJobs`) for only the active/changed
  pipelines, since a job's state changes only as part of its pipeline's — that's
  what keeps warm refreshes off the multi-second project-wide sweep. Both paths
  share `jobFetchScopes` so the panels are consistent. The jobs endpoint carries
  `duration` directly (no per-job enrichment).
- **Child pipelines** (dynamically-generated, `source=parent_pipeline`) do NOT
  appear in the project pipeline list, and a parent's own jobs are just the
  bridge/trigger job. So on the warm path we follow each pipeline's *bridges*
  (`ListDownstreamPipelines`) into its child pipelines and fetch their jobs too,
  recursively. Children stay out of the pipeline aggregate store (counting them
  would double the parent's ref in Top Pipelines); only their jobs surface. A
  root is marked job-complete only once its whole subtree is terminal, so a
  child that outlives its parent (fire-and-forget trigger) keeps being polled.
  On the bulk path children come for free (project-wide `/jobs` already lists
  them). This was a real regression when jobs first moved to per-pipeline —
  keep it covered.

## TUI notes

- tview has no native tooltip — "hover" reveals a row's full project path in
  the footer via a mouse-motion capture (falls back to click).
- A TUI owns the screen, so logs go to `<instance dir>/glute.log`, never stdout.
- Muted text uses `silver` (not `gray`) so it stays legible on dark terminals.
