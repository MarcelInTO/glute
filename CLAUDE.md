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
official successor to xanzy/go-gitlab) for REST, plus a tiny hand-rolled GraphQL
client (`internal/gitlab/graphql.go`) for what REST can't return (job `needs:`).
Keep it CGO-free so cross-compilation stays trivial.

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
- **Data layer = one `Service.Refresh(ctx) → Snapshot`** that feeds every panel
  and the Current tree, so the UI never shows a half-loaded state. The UI depends
  on the `Service` interface (fake + `SampleSnapshot` for tests).
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
- The pipeline **list** also omits the **triggering user** (like `duration`); the
  **detail** endpoint's `User` and GraphQL's `pipeline.user` carry it, so
  `Pipeline.User` is filled on the detail-enrich path (roots) and the GraphQL path
  (children). Since the triggering user is immutable, the store keeps it sticky —
  a later list-refresh (or a skipped/failed enrich) with a blank user doesn't
  clear a value already learned (`upsertPipe`).
- **Jobs use two paths.** The full-window backfill (cold start + periodic
  resync) uses the REST project-wide jobs list (`ListJobs`): it's cheap (~1 page
  per project) and — crucially — *includes child-pipeline jobs*. The warm delta
  fetches jobs per pipeline via **GraphQL** (`FetchPipelineJobTree`) for only the
  active/changed pipelines, since a job's state changes only as part of its
  pipeline's — that's what keeps warm refreshes off the multi-second project-wide
  sweep. Both paths share `jobFetchScopes` so the panels are consistent. Jobs
  carry `duration` directly (no per-job enrichment), and both paths carry the
  job's runner **tags** (REST `tag_list`, GraphQL `CiJob.tags`) — unlike `needs:`,
  which only GraphQL returns — so `Job.Tags` is populated regardless of which
  path surfaced a job.
- **Why GraphQL on the warm path.** The REST job payload has no `needs:`
  dependencies, and dependencies (not stages) determine execution order in DAG
  pipelines. GraphQL's `CiJob.needs` is the only source, so the Current tree can
  order jobs correctly (see the topological sort in the TUI notes). One GraphQL
  query per pipeline also returns the pipeline's fields, its jobs *with needs*,
  and its downstream children in a single round-trip — collapsing the old warm
  path (per-pipeline jobs + separate bridge calls). GraphQL is a tiny hand-rolled
  client (`graphql.go`, no new module; reuses the token + CA-aware HTTP client);
  bulk/history stays on REST. GraphQL ids are global (`gid://gitlab/Ci::Build/N`)
  — `parseGID` extracts the int; pipelines are looked up by `(project, iid)`, so
  `Pipeline.IID` is populated. GraphQL status enums are UPPERCASE — lowercased to
  match our `Status` constants.
- **Child pipelines** (dynamically-generated, `source=parent_pipeline`) do NOT
  appear in the project pipeline list, and a parent's own jobs are just the
  bridge/trigger job. On the warm path the GraphQL query returns each **BRIDGE**
  job's `downstreamPipeline { iid project { fullPath } }`; we recurse into those
  children (by `(path, iid)`) and fetch their jobs too. Bridge jobs are not job
  rows (only their child ref is used); children stay out of the pipeline
  aggregate store (counting them would double the parent's ref in Top Pipelines)
  — only their jobs surface, tagged with the child's own real project path. A
  root is marked job-complete only once its whole subtree is terminal, so a child
  that outlives its parent (fire-and-forget trigger) keeps being polled. On the
  bulk path children come for free (project-wide `/jobs` already lists them).
  This was a real regression when jobs first moved to per-pipeline — keep it
  covered.

## TUI notes

- **Tabs: Current (default) · Work · Infrastructure.** The split is by *question*,
  not by GitLab noun: Current = "what's happening right now", Work = "which
  projects' pipelines and jobs need attention" (history keyed by project),
  Infrastructure = "how did the CI capacity behave" (history keyed by
  tag/runner). The earlier Pipelines/Jobs tabs split by noun instead, which put
  the two capacity panels on the pipeline tab and left job history stranded on a
  tab whose headline panel (running jobs) duplicated the Current tree; that panel
  was dropped in the regroup. *Current* is the live-monitoring
  view, split top-to-bottom into the **active-pipeline tree** (top ~2/3) and a
  **"recently finished pipelines"** panel (bottom ~1/3). The tree is a single
  indented, scrollable table — each active root pipeline, its jobs (grouped by
  stage), and its downstream child pipelines nested one level deeper (marked `↳`),
  with a subtree jobs-done/total progress column. The tree is built by the pure
  `activePipelines` aggregate over the retained store; the parent→child edges come
  from `childPipes`/`childParent`, which the job-tree walk records for active roots
  (so they're fresh on every warm refresh and self-heal one refresh after a cold
  start / resync, where the bulk job path learns no edges). The finished panel
  (`fillFinishedPipelines`) lists the snapshot's `RecentPipelines` (already
  finished-only, newest-finished first) so a pipeline you were watching keeps its
  outcome after it drops out of the tree — but only finished **root** pipelines
  (children are excluded from the store), within the recent window.
- **The Work tab (`work.go`) is a 2×2 grid of history keyed by project** —
  pipelines on the top row, the jobs inside them on the bottom — over the Top
  window (labelled "last 30d"); it says nothing about what's running now (that's
  the Current tab). Everything **drops the ref**; that's how this analysis is done
  by hand. **Fails most often** (`fillFailsPanel`): projects by failure *rate*,
  with a `minFailRuns` floor so a tiny sample can't top a panel meant for chronic
  failures. **Slowest** (`fillSlowestPanel`): each project's wall-clock spread
  min/mean/p95/max, by mean desc — the full spread, not just the mean, is what
  flags build-process work. Both come from one `pipelineStats` grouping, sorted
  two ways. Below them the job-level counterparts moved over from the old Jobs
  tab: **Recent jobs** (`fillRecentJobs`, the recent window — which job actually
  failed, not just which project) and **Top jobs** (`fillTopJobs`, runs/avg/success
  over the Top window). All four are project-keyed, so all four record hover paths.
- **The Infrastructure tab (`infra.go`) is the capacity view**: **Tag
  performance** and **Runner performance** side by side, full height (one shared
  `fillJobStatsPanel`). Job load — jobs / compute / mean queue-wait (`Job.Queued`,
  a saturation signal) — grouped by the runner tags jobs were invoked with
  (`Job.Tags`), resp. the runner that ran them. They're two keyings of one
  `jobStats` accumulator over the same job population (only jobs a runner picked
  up), so the panes are directly comparable and are meant to be read against each
  other — a tag whose queue wait dwarfs its runners' points at under-provided
  capacity. That comparability is why they're columns, not stacked. A multi-tagged
  job counts toward each of its tags (tag rows overlap) and tagless jobs land in
  `(untagged)`. Neither panel is project-keyed, so this tab has no hover reveal.
  `JobStats` also carries `Failed`, `MeanDuration` and `P95Queue`, which nothing
  displays yet — the obvious next columns now that the panels are full-height.
  Tag performance replaced an earlier compute-by-product panel (a product rollup
  wasn't useful here); the `computeByProduct`/`ByProject` aggregates survive in the
  Snapshot for the `refresh` text preview, where product shares can still exceed
  100% under overlapping products. **"Compute" = Σ job durations**
  (`Job.Duration`), the honest non-admin proxy for runner-time — *not* pipeline
  wall-clock (which includes parallelism and idle gaps). It includes
  child-pipeline jobs and is bounded by the `maxJobPages` job-fetch cap on
  unusually busy projects. Every panel on both tabs is a pure aggregate in
  `aggregate.go` over the same window slices, so the regroup needed no store
  change — only the UI moved.
- **Job order within a pipeline** follows execution order, matching the GitLab UI:
  `sortPipelineJobs` orders by job **dependency** (`needs:`), not stage, because
  many pipelines drive execution with `needs` and dependencies override stages.
  It's a stable topological sort — jobs ordered by dependency *depth* (longest
  needs-chain), then stage-then-name within a layer — so it groups dependency
  layers like the UI's DAG view and never reshuffles as jobs start (it depends
  only on structure, not status/time). With no `needs` it falls back to stage
  execution order: stages ranked by their lowest job ID (GitLab creates jobs
  stage by stage), then name. `needs` is only available via GraphQL (warm path);
  bulk-fetched jobs have none, so a just-resynced active pipeline uses the stage
  fallback for one refresh until the warm delta repopulates `needs`.
- The Current tree's **pipeline** rows show a **USER** column — the username that
  triggered the pipeline, from `Pipeline.User` (blank on job rows, which show
  RUNNER instead; the two attribution columns sit adjacent and never coincide).
- The Current tree's job rows show a **RUNNER** column (the job's runner
  description, from `Job.Runner`). Runner descriptions are long, so
  `config.toml`'s optional `[runner_aliases]` table remaps a runner's full name
  to a short display label; the remap is display-only (applied in the UI via
  `Options.RunnerAliases` → `currentView.displayRunner`), so the data layer keeps
  the true name. Unlisted runners show their real name.
- The Current table is *selectable* (so it scrolls with ↑/↓); it reveals the
  selected row's full project path in the footer via `SetSelectionChangedFunc`,
  because the mouse-hover reveal below assumes fixed, non-scrolling row math and
  would point at the wrong row once scrolled.
- tview has no native tooltip — on the non-scrolling tabs, "hover" reveals a
  row's full project path in the footer via a mouse-motion capture (falls back to
  click). The Current tab's finished panel (non-scrolling, stable row math) gets
  this hover reveal too, but only while the cursor is over one of its rows, so it
  doesn't clobber the tree's selection-derived footer path.
- **tview `Table` parks at the bottom of an overflowing list if it first renders
  empty.** When a `Table` (no cell borders) first draws with content that fits its
  pane — which every panel does on the empty first render, before the initial
  refresh lands — tview sets a sticky `trackEnd`, and once the list later overflows
  it keeps the view pinned to the *bottom*, hiding the newest rows (this was a real
  bug in the finished panel: it showed the oldest completions, not the newest). Any
  panel that populates after an empty render must call `ScrollToBeginning()` after
  (re)filling — see `fillFinishedPipelines` and every Work/Infrastructure fill,
  which all do this.
- **Display conventions.** Merge-request pipeline refs
  (`refs/merge-requests/<n>/head|merge`) render as `MR <n>` via `displayRef` (used
  by both the Current tree label and the finished panel); branches/tags pass
  through. A numeric column right-aligns its **header** to match its right-aligned
  data (`setCurrentHeader` does this for TIME/DONE, `fillFinishedPipelines` for
  DURATION/WHEN, and the Work/Infrastructure tabs via the shared
  `rightAlignHeaders` in `format.go` for every numeric column); neither stats tab
  shows a ref at all. The moved job panels picked up both conventions — plus the
  `ScrollToBeginning()` above, which they had been missing — in the regroup.
- A TUI owns the screen, so logs go to `<instance dir>/glute.log`, never stdout.
- **Diagnosing a hang:** `kill -USR1 <pid>` dumps every goroutine's stack to
  `glute.log` (`watchDumpSignal` in `app.go`; SIGUSR1 is Unix-only, no-op on
  Windows via the `dumpsignal_*` build-tag split). Preferred over SIGQUIT (which
  writes to stderr, i.e. the raw-mode terminal) and over a debugger, since
  `ptrace_scope=1` blocks attaching to a non-child process. If glute wedges,
  grab a dump *before* killing it — a wedged TUI is unresponsive to keys and
  Ctrl-C because the event loop itself is stuck.
- Muted text uses `silver` (not `gray`) so it stays legible on dark terminals.
