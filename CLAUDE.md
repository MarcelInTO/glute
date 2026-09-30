# glute

A single-binary, read-only terminal (TUI) dashboard for GitLab CI/CD: pipeline
and job stats across a configured watchlist of "products". Go, cross-platform.

## Build & test

- `make` — cross-compile all five release platforms into `bin/` (gitignored)
- `make build` — host-only build to `bin/glute`
- `make run ARGS="--sample"` — build and run
- `make check` — the CI gate: `fmt-check`, `vet`, `test`
- `make test` / `go test ./...`
- `make smoke` — build, then `glute version` + `glute refresh --sample`
- `make drive-all` — play every TUI scenario (`tools/ptydrive/scenarios/`)
  against the real binary in a pty; `make drive SCENARIO=<file>` plays one and
  prints its `show` steps
- `make dist VERSION=v1.2.3` — release archives + `SHA256SUMS` into `dist/`
- `make verify-dist` / `make formula` — see the release section at the end

**Check interaction changes in the real binary, not only in unit tests.**
The UI tests call `onKey`/`onMouse` directly, and that skips tview's own event
dispatch. So they can't catch a click that never arrives (tview sends a mouse
move ahead of the press in one shared event, and swallowing the move drops the
press), a key that goes to the wrong widget (Enter via `onKey` never reaches a
table without a running app), or a footer that overflows at a real width. All
three passed the unit tests and failed in the real binary. So a change to keys,
mouse handling or layout gets a scenario, or an existing one extended, and a
`make drive-all` run. To check a change against the real instance, use a
throwaway `zz_live_test.go` gated on an env var: it loads glute's own config
and token (`config.Load`, `auth.LoadToken`), runs the real client, and is
deleted before committing. A colour choice was made from an image, not in the
abstract: a throwaway test dumped the simulated screen's cells with their
colours, and Pillow rendered them to a PNG, with the candidate colours side by
side.

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
  on the `Service` interface (fake + `SampleSnapshot` for tests). The Snapshot
  carries the history aggregates at *every* selectable window (`Windows`, see the
  `t` key below), so a window change is a UI-only re-render, never a fetch.
  **One exception: `Service.PipelineTree`**, the on-demand fetch behind the
  finished-pipeline detail view (see the TUI notes). The retained store can't
  rebuild a finished tree: parent→child edges exist only for pipelines glute
  watched while they ran (so, just after startup, most of the finished panel
  would come up with its children missing), `jobFetchScopes` omits the skipped,
  canceled and manual jobs a failed run leaves behind, and bulk jobs have no
  `needs:`. `walkPipelineTree` (`tree.go`) instead issues one GraphQL query per
  pipeline in the tree, a level at a time, with no scope filter. Measured on the
  real instance at ~0.5–0.7s for a two-level tree. A tree whose every pipeline
  has finished is immutable, so the Poller caches it (`trees`, under its own
  `treeMu`, since the call runs concurrently with `Refresh`; pruned with the
  store). A finished root with a still-running child isn't cached. The walk
  fails as a whole on any fetch error rather than show a tree missing a child.
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
  Fetch phases fan out with bounded parallelism — `concurrency` in
  `config.toml` (default 16, was a fixed 8): each phase's wall-clock is roughly
  calls ÷ concurrency × latency, and the request *count* per refresh doesn't
  change, so raising it is safe against per-minute rate limits. The per-project
  pipeline sweep is the floor of every refresh (~1.4s for 81 projects at 8-way).

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
  per project) and — crucially — *includes child-pipeline jobs*. It runs only
  for projects that had **any pipeline in the window** (`bulkJobProjects`, decided
  from the full-window pipeline list the same refresh just did, kept per project
  as `projectPipelines`): a job created in the window belongs to a pipeline
  updated in it, so a project with an empty pipeline list has no jobs to re-list
  and skipping it loses nothing. On the real 81-project watchlist only 16 had a
  pipeline in 30d, and this sweep was ~10s of the ~12s resync. The decision is
  blind to pipeline `source` and made on the raw list (before children are
  dropped), so a project alive only via a forgotten **scheduled** pipeline, a
  trigger/API one, or a cross-project downstream child is still re-listed; a
  project whose list call failed is kept, not skipped. Don't replace this with
  `last_activity_at`: GitLab bumps that on repo/issue/MR events, never on
  pipelines, so CI-only projects would fall through (measured: every active
  project's newest pipeline post-dated its `last_activity_at`). The warm delta
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
- **Both job paths return retried attempts, and only one marks them.** GraphQL
  `pipeline.jobs` returns every attempt, with `retried` on the superseded ones.
  On the instance, tlp-sz!438, a `SUCCESS` pipeline, lists two `FAILED` attempts
  of a job that later passed, plus retried *bridges* whose superseded child
  pipelines still exist. The REST project jobs list (the bulk path) returns
  every attempt too, but with **no** retried field: sems-hosting pipeline 234953
  lists four `validate` runs. The store keeps every attempt, since they consumed
  runners and count for compute and failure stats. Both trees (Current and
  detail) show only the latest attempt: `latestAttempts` in the shared
  `treeBuilder` keeps the highest id per job name within a pipeline, and drops
  anything GraphQL marked `Retried`. The name test is what survives a resync,
  because a flag-only filter would bring the duplicates back every 10 minutes
  when the bulk path overwrote the store's jobs. It relies on job names being
  unique within a pipeline except for retries, which GitLab guarantees
  (`parallel:` and matrix jobs get suffixed names). A retried bridge's child
  pipeline is *superseded*: the warm walk still fetches it (its jobs used
  runners) but records no edge for it or anything beneath it, and it deletes an
  edge learned before the retry. The detail walk doesn't follow it at all.
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
  pipelines on the top row, the jobs inside them on the bottom — over the selected
  history window (the `t` key; the full Top window until changed); it says nothing
  about what's running now (that's the Current tab). Everything **drops the
  ref**; that's how this analysis is done
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
- **The `t` key cycles the history window** (1d → 7d → 30d → 1d…) for every
  windowed panel at once — the Work tab's fails/slowest/top-jobs and both
  Infrastructure panels — and is session-only (not persisted). It's a pure view
  switch: `Refresh` computes `WindowStats` (the same aggregates, via one
  `windowStats` helper) at each `selectableWindows(TopWindow)` lookback and the
  Snapshot carries them all in `Windows`, so `cycleWindow` just re-fills the
  panels from the held snapshot (`renderHistory`) — no fetch, no re-aggregation,
  and the cold-start cost is milliseconds of extra aggregation per refresh. The
  selectable set is the standard steps (`windowSteps`: 1d/7d/30d) that are
  shorter than the configured `top_window`, plus the full window itself, so the
  labels are always honest about how much history the store actually holds
  (`top_window = 14d` yields 1d/7d/14d). The full window is the default and the
  last step; it's also embedded flat in the Snapshot (`Snapshot.WindowStats`) so
  single-window readers like the `refresh` preview needn't pick. Sub-windows
  filter the store on the same timestamps eviction uses (`windowSlices`:
  pipeline `updated_at`, job `created_at`), so a 7d view is exactly what a 7d
  store would hold. Panel titles take their "· last Nd" suffix from the
  displayed window (`windowSuffix`; bare until the first snapshot names the
  windows) and the header shows `window last Nd` beside the tabs, so the change
  is visible from the Current tab too. The **recent** panels (Current's finished
  list, Work's recent jobs) are deliberately *not* windowed — they're
  newest-first event lists over `recent_window`, not aggregates, so `t` leaves
  them alone. Under `--sample` the shorter windows are scaled down from the full
  fixture (`scaleWindowStats`) so the key visibly changes the numbers.
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
- **The finished-pipeline detail view** (`detail.go`) opens over the Current tab
  when a finished row is chosen, by `f` (focus the finished list), ↑/↓ and Enter,
  or by a click on the row. Esc, `q` (which closes it rather than quitting) or a
  click outside closes it, and the panel's own bottom line says so, since a hint
  only in the footer went unnoticed. The panel is inset about 1/12 of the width
  and 1/8 of the height (`detailInset`, floors of 4 columns and 3 rows, giving
  way on small screens), so the tab shows all round it and it reads as a panel
  over the tab, not a new screen. A Flex can't express a margin that scales
  with a floor, so a small `insetView` lays it out and, like a Flex, clears
  nothing. The panel has its own background (`detailBackground`, #262626),
  chosen from a rendered colour preview against #1c1c1c (the step from black
  barely showed) and #303030 (palette green `success` text started to fade).
  It degrades to plain black on a 16-colour terminal. Table cells are
  transparent, so they sit on the table's fill. The TextViews and the frame
  (whose Flex paints only its border and title row) need it set explicitly, or
  they leave black patches. **Mouse handling while it's open** (`onMouse`) has two traps, both
  from how tview dispatches one terminal event. (1) A move is sent ahead of the
  press or release, sharing one event, so swallowing the move (returning nil)
  silently drops the press after it. Moves outside the panel must always pass;
  a unit test that calls `onMouse` for a lone press won't notice. (2) tview
  builds a click only from a release that got through. So a *press* outside
  dismisses the panel, and `dismissing` swallows that gesture's release, which
  keeps the click off the tab beneath. Everything else outside the panel (the
  wheel) is swallowed, since `Pages` hands an unconsumed event to the page
  underneath. It exists for two questions about a finished run:
  which jobs failed, and where the time went. So it lays out the tree the way the
  active tree does (`flattenActive`, the shared `treeBuilder`, the same job
  order) and swaps the live columns for QUEUED (the runner wait, `Job.Queued`),
  START (offset from the root's creation), TIME, and a per-row **timeline bar**.
  In a DAG pipeline the durations don't add up to the wall-clock time, and the
  bars show what overlapped and which chain the run waited on. The axis starts
  at the root's `Created` and runs to the latest finish anywhere in the tree (a
  child can outlive its root). A job draws `░` for its queue, then `█` for its
  run in the status color. A pipeline draws `─` for created→started, then `━`,
  so it reads as a bracket over its jobs. Every row that ran gets at least one
  cell. TIME is GitLab's `duration` (which excludes gaps), while the bar is wall
  clock, so they can differ. The bars are rendered in the table's draw hook at
  the real width: the name column keeps its natural width if the timeline still
  gets `minTimelineWidth`, and is elided otherwise. The durations drop HMS's
  blank padding, which only exists to stop ticking timers jittering, and show a
  zero as `0`, not `—`. The fetch runs off the UI goroutine. `detailSeq` drops a
  result whose view was closed or replaced meanwhile.
- **The timeline cuts idle stretches that would swamp it** (`idleGaps`,
  `timeAxis`). A stretch is idle when no job is queued or running; pipeline
  spans don't count as busy, since they cover their own gaps. It's cut when it
  lasts at least `minIdleGap` (1m) and **longer than all the busy time
  combined**. That rule is what keeps a run's shape honest: sems-platform's
  5½-minute wait before `release` in a 12-minute run is part of where the time
  went, and stays to scale. tlp-sz!438, whose job was retried 3½ days after the
  rest, had a 95h axis with its 48-minute child in one column; three cuts
  remove 94h and spread the child's jobs across the bar. A cut takes one cell,
  drawn `┆` on every row as an axis break. Each busy stretch gets at least one
  cell (a 4-second retry after the last cut otherwise vanished under the `┆`),
  and the rest of the width is shared by length. The header keeps the true
  wall-clock total and names how much was cut (`┆ cuts 94:16:46 idle`). QUEUED,
  START and TIME stay real numbers. The axis stays linear when it's too narrow
  for a cut to leave room.
- **Pipeline ids are links to GitLab** (`browser.go`, `linkCell`). Users often
  run glute in a terminal on their desktop, and sometimes over SSH on a box
  with no browser at all, so two mechanisms cover each other. (1) Every
  pipeline id (tree, finished panel, detail view, and the detail title's `#N`)
  is underlined and carries an OSC 8 terminal hyperlink (tcell `Style.Url`).
  The user's *own* terminal opens it, on their own machine, which is what
  works over SSH. glute captures the mouse, so terminals only take a
  modifier-click as theirs (Cmd, Ctrl or Shift, depending on the terminal).
  tmux passes OSC 8 only with `set -as terminal-features ",*:hyperlinks"`
  (tmux 3.4+). (2) A plain click on an id, or `o` on the selected row, has
  glute launch the default browser itself: `xdg-open`, `open`, or `rundll32
  url.dll,FileProtocolHandler` (not `cmd /c start`, whose quoting breaks on
  `&`). The opener runs with no stdio, because the terminal is in raw mode and
  xdg-open's helpers print, and it isn't waited on. glute doesn't launch
  anything where the browser wouldn't reach the user (`browserUnreachable`:
  SSH env vars, or on Linux/BSD no `DISPLAY`/`WAYLAND_DISPLAY`). Instead the
  footer shows the URL and points at the modifier-click. `o` on a **job** row
  opens the job's own page (its log), since that's the next step after "which
  job failed". The URLs come from GitLab: REST `web_url`, or GraphQL's
  host-relative `Pipeline.path`/`CiJob.webPath` joined to the instance's
  **origin** (`webOrigin`), not its full URL, because those paths already
  include any relative URL root. Checked against REST `web_url` on the
  instance for a child pipeline and a job. The id cell keeps its URL in its
  `Reference` (a `link`), since tcell has no getter for a style's URL and a
  click has to find it (`urlAt`).
- **Which Current panel owns the keyboard** (`currentView.finishedActive`) is
  set only by intent: the `f` key, or a mouse press on a panel. It is never set
  from focus events, because tview moves focus incidentally too.
  `Pages.HidePage` re-focuses the page's default item (the tree), so closing the
  help or the detail view would otherwise forget the user was on the finished
  list. Only the focused panel is selectable, because selectability is the
  highlight switch: tview highlights a selected row whether or not its table has
  focus, so otherwise nothing shows where ↑/↓ will go. A refresh keeps the
  finished selection on the same **pipeline ID**, not row index, since newly
  finished runs push in on top and Enter would otherwise open a different
  pipeline. The view follows the usual rule for a newest-first list. At the top
  it stays at the top, so each arrival shows up, and the selection follows its
  pipeline only as far as the last visible row. (tview scrolls to keep a
  selection in view, so following it past the edge would pull the view off the
  top.) Scrolled down, the view holds still: its offset moves with the arrivals,
  so the rows being read don't shift. Scrolling back to the top resumes
  following. v0.3.0 held the view still *whenever the panel had the keyboard*,
  even at the top. That hid every new arrival just above the view, from the
  first `f`, click or opened pipeline onward, since nothing gives the keyboard
  back. A short list hides this, because tview ignores a scroll offset when
  everything fits: the tests use 30 rows. Its key hint sits in the
  panel title and changes with focus, because the footer line is already full at
  common widths. Because the list now scrolls, its hover reveal goes through
  `Table.CellAt`, which counts the scroll offset (`panelTable.rowAt`).
- The Current table is *selectable* (so it scrolls with ↑/↓); it reveals the
  selected row's full project path in the footer via `SetSelectionChangedFunc`,
  because it's scrolled from the keyboard: the row that matters is the selected
  one, not the one under the mouse.
- tview has no native tooltip — "hover" reveals a row's full project path in the
  footer via a mouse-motion capture (falls back to click). The Current tab's
  finished panel gets this hover reveal too, but only while the cursor is over
  one of its rows, so it doesn't clobber the tree's selection-derived footer
  path. That panel scrolls now (it's selectable), so its row math goes through
  `Table.CellAt`, which counts the scroll offset (`panelTable.rowAt`).
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
  data (`setCurrentHeader` does this for ID/TIME/DONE, `fillFinishedPipelines`
  for ID/DURATION/WHEN, and the Work/Infrastructure tabs via the shared
  `rightAlignHeaders` in `format.go` for every numeric column); neither stats tab
  shows a ref at all. The moved job panels picked up both conventions — plus the
  `ScrollToBeginning()` above, which they had been missing — in the regroup.
- **Identity columns are sized at draw time, not capped by a constant**
  (`flexcol.go`). Each panel declares which columns say *which row this is* —
  project, job, tag/runner key, ref, the Current tree's label — and on every draw
  they take whatever width is left after the other columns get their natural
  (content) widths. Two flexible columns split that budget **max-min fair**:
  whichever needs less than an equal share keeps all of it and releases the rest,
  so a short JOB shows whole while a long PROJECT beside it absorbs the shortfall.
  A constant cap was wrong in *both* directions: on a wide terminal it clipped
  names while the numbers drifted apart in whitespace, and on a narrow one it
  could still overrun — and tview lays columns out left to right and drops the
  ones on the **right** that no longer fit, so an oversized column 0 doesn't
  crowd the stats, it deletes them (measured: at 64 columns the Current tree lost
  RUNNER/TIME/DONE entirely). The cells are built by `nameCell`, which keeps the
  untruncated string in the cell's `Reference` — the cell's own text can't be the
  source once it has been elided once — and `flexColumns` installs a
  `SetDrawFunc` hook that re-elides from it, since only at draw time is the
  pane's real width known. `MaxWidth` backs the elision up for cells with no
  recorded full text (the `(none)` placeholder) and for runes wider than one
  cell, which `format.Elide` counts as one. Tables with a flexible column set
  `SetEvaluateAllRows(true)` so widths don't shift as the table scrolls and so
  tview's measurement agrees with the budget the hook just computed.
- **Truncation, when it's needed, cuts the middle** (`format.Elide`,
  "wevr-pla…ne-ingest"), not the tail (`format.Trunc`). Our repos share long
  conventional prefixes, so a tail cut renders a whole column as the same string
  — the last segment is what identifies the project, and it's exactly what a tail
  cut drops; the odd rune goes to the tail for the same reason. `Trunc` is still
  right for prose and for genuinely bounded fields (USER, the tree's RUNNER,
  error text, the `refresh` preview's fixed-width columns). With flexible columns
  in place this is now the last resort rather than the normal case.
- A TUI owns the screen, so logs go to `<instance dir>/glute.log`, never stdout.
- **Diagnosing a hang:** `kill -USR1 <pid>` dumps every goroutine's stack to
  `glute.log` (`watchDumpSignal` in `app.go`; SIGUSR1 is Unix-only, no-op on
  Windows via the `dumpsignal_*` build-tag split). Preferred over SIGQUIT (which
  writes to stderr, i.e. the raw-mode terminal) and over a debugger, since
  `ptrace_scope=1` blocks attaching to a non-child process. If glute wedges,
  grab a dump *before* killing it — a wedged TUI is unresponsive to keys and
  Ctrl-C because the event loop itself is stuck.
- Muted text uses `silver` (not `gray`) so it stays legible on dark terminals.
- **Status colors** (`statusColor`): success green, failed red, canceled/skipped
  silver, **running DodgerBlue**, and every other active state (created, pending,
  preparing, waiting-for-resource, scheduled) yellow — so on a busy server the
  rows actually executing are told apart from the ones queued for a runner,
  which was the request that split them. DodgerBlue rather than `tcell.ColorBlue`
  for the same reason as silver over gray (the ANSI blue is near-illegible on
  dark terminals), and not aqua, which the headers own.
- **Both Current panels lead with an ID column**: the pipeline's instance-wide
  id (`Pipeline.ID` — the `#N` GitLab shows and the number in its URL), which
  the team quotes as a build number. Filled on pipeline rows only, roots and `↳`
  children alike; job rows leave it blank on purpose — nobody quotes job ids and
  a column of them reads as noise. It sits *before* the name column so the ids
  line up regardless of tree depth, and it is content-sized in both panels (the
  finished panel's other columns all expand, so an expanding ID column would
  park the ids behind a gutter). Not the per-project `IID`: that's the other
  candidate if "the number people quote" ever turns out to be the small
  per-project one, and it's a one-field swap.

## CI & releases

- **Two pipelines, one Makefile.** `studio.wevr.com` is the source of truth and
  `github.com/MarcelInTO/glute` is a push mirror of it. The split exists for one
  reason: the GitLab project is **private**, and a formula in a public Homebrew
  tap (or a manifest in a public Scoop bucket) has to download from a URL anyone
  can reach. So GitLab builds the archives, uploads them to its own generic
  package registry and creates the GitLab release (what people with
  `studio.wevr.com` access download); the mirror's `release.yml` publishes the
  public GitHub release and pushes `Formula/glute.rb` to `MarcelInTO/homebrew-tap`
  and `bucket/glute.json` to `MarcelInTO/scoop-bucket`. Both sides invoke the
  *same* `make` targets, so they cannot drift in how a binary is produced — and
  `make dist verify-dist formula manifest VERSION=v1.2.3` is that same rehearsal
  on a laptop. Nothing about the release lives only in a CI file.
- **Setting the push mirror up (GitLab → Settings → Repository → Mirroring
  repositories).** The GitHub username has to go in **both** the URL *and* the
  separate Username field — `https://MarcelInTO@github.com/MarcelInTO/glute.git`
  with `MarcelInTO` in Username and the PAT in Password. Filling only one of the
  two fails, and the errors do not name the missing field: no username anywhere
  gives "could not read Username ... terminal prompts disabled", and a username in
  just one place gives GitHub's "No anonymous write access", which reads like a
  token-permission problem and is not one. Leave "mirror only protected branches"
  off, or tags never reach GitHub and no release is ever built. SSH + a write-enabled
  GitHub deploy key is the alternative if the password path ever regresses.
- **The mirror throttles to once per 5 minutes, tags included.** So a tag produces
  the GitLab release within ~2 minutes but the GitHub release (and therefore
  `brew upgrade`) roughly 5 minutes later — measured on v0.0.1: tag pushed, tag
  visible on GitHub 5 min later, release workflow done 29s after that. The gap is
  the mirror's cadence, not a failure; don't go hunting for a broken workflow
  during it. The 5 minutes is a *minimum spacing between mirror runs*, not a
  fixed delay: on v0.2.0 two commit pushes to an idle mirror were on GitHub in
  ~15s, while the tag, pushed a few minutes after the commit it sat on, waited
  the full 5. Verifying CI on `main` before tagging (the practice, next bullet)
  therefore means the tag usually waits out the interval; pushing tag and commit
  together would ride one mirror run but forfeits that check.
- **Cutting a release: verify green on both sides before tagging.** Push `main`,
  wait for the GitLab pipeline *and* the GitHub `ci.yml` run on the mirrored
  commit (the only native macOS/Windows test), then tag. The tag is what publishes
  on both sides, and a red commit under a tag burns a version number. Then watch
  both release pipelines (`glab api projects/<enc>/pipelines?ref=vX.Y.Z` and
  `gh run list --branch vX.Y.Z`) and confirm the outputs against the release's
  `SHA256SUMS`: the tap formula's four sha256 values and the bucket manifest's
  hash, and that the "Push formula"/"Push manifest" steps say *pushed*, not
  skipped. Done this way for v0.2.0; the bucket's URL was also re-downloaded and
  re-hashed once, which is the check that proves a manifest, not just the file.
  **The two sides' `SHA256SUMS` differ, by design.** Each pipeline builds its
  own archives from the same `make` targets, and tar and zip record file
  timestamps, so each side's archives hash differently. Check each side
  against its own sums: the tap and the bucket against the **GitHub**
  release's, since that's where they download from, and a GitLab package
  against GitLab's. Seen on v0.3.0: every archive differed between the two
  sides, and each one matched its own side's list. Both releases were done
  about 6 minutes after the tag was pushed, the mirror's spacing being most of
  that.
- **The mirror's token needs the `workflow` scope.** The push mirror carries
  `.github/workflows/` along with everything else, and GitHub refuses a PAT-authed
  push that creates or updates a workflow file unless the token has `workflow`
  scope (classic) / Workflows: write (fine-grained) on top of repo write. The
  failure surfaces as a mirror error about "refusing to allow a Personal Access
  Token to create or update workflow", which does not obviously name the scope.
- **Only `vX.Y.Z` tags publish.** Any other tag still runs the tests but builds
  nothing: GitLab's generic package registry requires a semver version, so a tag
  that cannot be a version cannot be a release. Prereleases (`v1.2.3-rc.1`) are
  released on both sides but kept **out of the tap and the bucket** — Homebrew
  compares versions numerically, and a tap tracking a release candidate would push
  it to everyone running `brew upgrade`; a bucket would do the same to
  `scoop update`.
- **Two spellings of the version.** The tag and the binary keep the leading `v`
  (`git describe` produces it, so a dev build and a release build agree and
  `glute version` prints `glute v1.2.3`); archive names, the formula and the Scoop
  manifest take the bare `1.2.3`, because a leading `v` breaks Homebrew's upgrade
  ordering. Hence
  `VERSION` vs `DIST_VERSION` in the Makefile, and the explicit `"glute v#{version}"`
  in the formula's `test do` block.
- **`verify-dist` unpacks the archive and asserts the binary inside reports the
  tag.** It runs against the exact bytes that ship, not `go run` — a
  version/packaging mismatch is the one release bug a green test suite cannot
  catch. Both pipelines run it right after `make dist`.
- **The formula is a template** (`packaging/glute.rb.in`) substituted from
  `dist/SHA256SUMS`, not a heredoc inside a workflow, so it is reviewable in the
  repo and generatable locally before a tag is spent. `make formula` strips the
  template's own header comment and emits a provenance one — a guard fails the
  build on any unsubstituted `@TOKEN@`, which is what catches a platform silently
  dropped from `PLATFORMS`. The four non-Windows rows of `PLATFORMS` are exactly
  the formula's `on_macos`/`on_linux` × `on_arm`/`on_intel` matrix; Windows is
  absent from the formula because Homebrew has no Windows support — it is the
  Scoop manifest's job.
- **The Scoop manifest is the same idea for Windows** (`packaging/glute.json.in`
  → `make manifest` → `dist/glute.json` → `bucket/glute.json` in
  `MarcelInTO/scoop-bucket`, which `scoop bucket add marcelinto <url>` subscribes
  to). Scoop was picked over winget and Chocolatey because a bucket is just a git
  repo of JSON under our control — no submission queue, no moderation, and
  `brew`-like upgrades. JSON has no comments, so `make manifest` drops the
  template's header at the opening brace and the provenance note rides in Scoop's
  `"##"` comment key. The release zip nests everything under
  `glute-X.Y.Z-windows-amd64/`, so the manifest's `extract_dir` names that folder
  and `bin` is a bare `glute.exe`; Scoop checks the sha256 on install. The
  manifest also carries `checkver`/`autoupdate` against GitHub's
  `releases/latest` (which excludes prereleases): the workflow doesn't use them
  (it pushes a complete manifest), but they let Scoop's own `checkver -u` tooling
  bump it and they document the URL scheme. Only `64bit` is offered because
  `windows/amd64` is the only Windows row in `PLATFORMS`. The bucket is a plain
  repo (`bucket/glute.json` + README), not the ScoopInstaller BucketTemplate,
  whose Excavator/Pester tooling would duplicate what the release workflow
  already does. `make manifest` also parses its output (jq or python3, whichever
  exists) because a malformed manifest breaks `scoop install glute` for every
  bucket user.
- **Pushing to the tap and the bucket is one script**, `packaging/push-to-repo.sh`
  (clone, copy, commit, push HEAD), called from both `release.yml` steps. Each
  step is opt-in behind its own secret — `HOMEBREW_TAP_TOKEN`, `SCOOP_BUCKET_TOKEN`
  — because the job token cannot write to another repository; one PAT with write
  access to both repos can back both secrets. Without a secret the step warns and
  the file is in the step summary, ready to paste.
- **The GitLab release is created with `curl` + `CI_JOB_TOKEN`, not the `release:`
  keyword.** The keyword pulls `registry.gitlab.com/gitlab-org/release-cli`, an
  extra image a self-managed instance has to be able to reach; the API underneath
  takes the same job token. GitLab releases cannot host uploaded files — they link
  to URLs — which is why the archives go to the generic package registry first.
- **The GitHub `ci.yml` is not redundant with GitLab's.** It earns its place by
  running the tests natively on **macOS and Windows**, which the GitLab pipeline
  (Linux docker runners) does not — glute has a real platform split in the
  `dumpsignal_*` build tags. It uses `go` directly rather than `make` on that
  matrix because the Windows runner has no dependable GNU make.
- **`make fmt-check` grades `$(GOFILES)`, not `.`** — `gofmt` descends into
  dot-directories, and `.gitlab-ci.yml` sets `GOPATH=$CI_PROJECT_DIR/.go`
  because GitLab can only cache paths inside the project directory. So on a
  cache *hit* every dependency's source became gofmt's to grade, and tcell and
  pflag aren't gofmt-clean: the job failed with a list of files nobody in this
  repo wrote. It stayed hidden for a week because the runner's cache is local
  (no shared cache server) and the first pipelines logged "Failed to extract
  cache" — `.go/` simply didn't exist yet when `fmt-check` ran. A green
  `fmt-check` that depends on a cold cache is the failure mode to watch for
  here; don't "fix" it by moving the module cache, which has to stay inside the
  project dir.
- **Runner tags on `studio.wevr.com`**: instance docker runners take untagged jobs
  (what this pipeline uses — verified against `wevr-public/cli-tester`, whose
  untagged `test:linux` lands on the "Braque/Bazille - Linux Docker" runners and
  pulls an external image); native runners exist behind the tags `macos` and
  `wevrCodeBuildWin` if a job ever needs them. Go cross-compiles CGO-free, so one
  Linux runner produces every platform and no build matrix is needed.
