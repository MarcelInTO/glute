---
name: glute-status
description: "glute (GitLab CI/CD TUI) — build state, next steps, deferrals"
metadata: 
  node_type: memory
  type: project
  originSessionId: 3ea31245-90a6-499d-b976-c840d837c60f
  modified: 2026-09-30T15:49:59.418Z
---

glute is a single-binary, read-only Go TUI dashboard for GitLab CI/CD
(Current, Work, and Infrastructure tabs). Repo: `/mnt/md0/devWevr/glute`; see its
CLAUDE.md for decisions and rationale.

As of 2026-07-04: Phases 1–3 are done and committed on a local `main`
(scaffold + `glute auth`, the `internal/gitlab` data layer, and the tview
dashboard), plus named multi-instance support, last-segment project names with
hover-for-full-path, and pending jobs in the Running panels.

As of 2026-07-09: the data layer is now an **incremental retained store** in
the `Poller` (not a full fetch per refresh). Warm refreshes dropped from ~9.4s
of fetching to <1s on a real 42-project instance; cold start stays ~16s. Jobs
use a hybrid fetch — a cheap project-wide bulk list for the full-window
backfill (cold + periodic resync), and per-pipeline + bridge traversal for the
warm delta, which also captures dynamically-generated **child-pipeline** jobs
(they don't appear in the pipeline list). Also added refresh-timing
instrumentation (`RefreshStats`, printed to stderr on exit; per-refresh
`refresh timing:` log line). See CLAUDE.md's "Poller" decision + GitLab API
notes for the design. Committed and pushed to `main` (`cd143d0`).

As of 2026-07-10 (uncommitted, working tree): added the **Current tab** — a new
first/default tab that renders a hierarchical live-monitoring view: active root
pipelines → their jobs (grouped by stage) → downstream child pipelines (nested,
marked `↳`), in one indented, scrollable, selectable table with a subtree
jobs-done/total progress column. Built by a new pure `activePipelines` aggregate
over the retained store; the parent→child edges come from new
`childPipes`/`childParent` maps the poller's job-tree walk records for active
roots. Two-part plan: (1) this tab — DONE; (2) still TODO — remove the Running
panels from the Pipelines/Jobs tabs and replace them with optimization-oriented
stats (which pipelines/jobs are slow, run too often, fail too often, wait too
long for runners). All tests pass; not yet committed.

As of 2026-07-13 (committed + pushed to `main`): the Current tab's TIME column
now renders fixed-width `hh:mm:ss` (leading zero fields blanked to spaces) via
`format.HMS`, the default refresh interval dropped 30s→10s (warm refreshes are
sub-second), and a one-second UI tick counts the running timers up smoothly
between refreshes (`currentView.tick`, `89ffbb1`). Added a **goroutine-dump
diagnostic**: `kill -USR1 <pid>` writes all stacks to `glute.log`
(`watchDumpSignal`, `0ebc611`).

As of 2026-07-14 (committed + pushed to `main`, `82c6619`): the Current
tab now orders each pipeline's jobs by **execution order honoring `needs:`
dependencies**, not stages — many wevr pipelines (esp. dynamically-generated
child pipelines) drive execution with `needs` and put every job in one stage, so
stage/alphabetical ordering was wrong (e.g. an `Intro` job with no needs sorted
last). `needs` isn't in the REST API, so glute now has a **tiny hand-rolled
GraphQL client** (`internal/gitlab/graphql.go`) used on the **warm path only**:
`Client.FetchPipelineJobTree` fetches a pipeline's fields + jobs (with `needs`) +
downstream children in one query, replacing the old per-pipeline
`ListPipelineJobs` + bridge calls. `sortPipelineJobs` does a stable topological
sort by dependency *depth* (then stage/name), matching the GitLab UI's DAG view;
falls back to stage order when no `needs`. Bulk/history stays REST. Scope was
deliberately **incremental** (not a full data-layer migration) — GraphQL is now
in place to extend to the future stats pages. Verified live against
studio.wevr.com (GitLab 18.9.1-ee). This is the direction to build on for stats.
**Caveat for that stats work:** GitLab's GraphQL does *not* do server-side
aggregation (no group-by/avg/count) — you still pull records and aggregate
client-side, same as REST. Its benefit is precise field selection and fewer
round-trips, not SQL-like querying; don't over-expect it when designing stats.

As of 2026-07-17 (committed + pushed to `main`, `2babfde`): the Current tab is
now **split** — the active tree (top ~2/3) plus a new **"recently finished
pipelines"** panel (bottom ~1/3), newest-first from `RecentPipelines`, so a
pipeline's outcome stays visible after it leaves the tree. Durations use
`format.HMS` (matching the tree's TIME column), MR refs display as `MR <n>`
(`displayRef`), and the panel calls `ScrollToBeginning()` each refresh to defeat
tview's sticky `trackEnd` — a `Table` first rendered empty otherwise pins an
overflowing list to its *oldest* rows (this was the actual bug; the data/sort
were correct all along).

As of 2026-07-31 (committed to `main`, `e956af0`): the **Pipelines tab is
reworked** from live-state panels into a **2×2 grid of historical-analysis
panels** over the Top window (part 2, Pipelines half — DONE). Panels: **Fails
most often** (projects by failure rate, `minFailRuns` floor), **Slowest**
(per-project wall-clock min/mean/p95/max), **Tag performance** and **Runner
performance** (job load — jobs/compute/mean-queue — keyed by the tags jobs were
invoked with, resp. the runner that ran them; two keyings of one `jobStats`
accumulator over the same job population). All ref-dropped, keyed by
project/tag/runner. New pure aggregates in `aggregate.go` (`pipelineStats` →
both Fails+Slowest; `jobStats` → both `tagStats`+`runnerStats`;
`computeByProduct`/`computeByProject`; `percentile`) over the existing window
slices — **no store change**. New Snapshot fields + `PipelineStats`/
`ComputeAgg`/`JobStats` types; **"compute" = Σ `Job.Duration`**, the non-admin
proxy for runner-time (not pipeline wall-clock). Tag keying needed `Job.Tags`,
now carried on **both** job-fetch paths (REST `tag_list`, GraphQL `CiJob.tags`).
`format.Compute` added; `--sample` fixtures + `refresh` dump + CLAUDE.md
updated; new unit tests + updated render tests all pass. The
right-align-headers convention is now applied to Pipelines (via
`rightAlignHeaders`); the ref is dropped so `MR <n>` display is moot there.
**Still deferred to the Jobs-tab rework:** right-align headers + `MR <n>` ref.

As of 2026-09-03 (committed + pushed to `main`, `806de6b` + `217bf99`): the
tabs are now Current / Work / Infrastructure (the Jobs-tab rework landed as a
regroup, `0cbee82`). Added the **`t` history-window key** (1d/7d/30d, capped at
`top_window`; Snapshot carries `Windows []WindowStats`, so it's a UI-only view
switch, session-only). Then a **measured** perf pass on the real watchlist
(`wevr` group, 81 projects, only 16 with CI in 30d, 5 return 403): the ~10s
resync cost was the project-wide job re-list over *all* projects; it now runs
only for projects whose full-window pipeline list returned anything
(`bulkJobProjects`), and `concurrency` is a config key (default 16, was 8).
Cold refresh went 20.4s → 14.2s (jobs 9.9→6.5s, enrich 8.1→5.7s).
Marcel's explicit constraint: **projects alive only via forgotten scheduled
pipelines must not fall through the cracks** — satisfied because the decision
is GitLab's own pipeline list (source-blind), never `last_activity_at`.
**Verified on the instance:** `last_activity_at` is NOT moved by pipelines
(every active project's newest pipeline post-dated it) and would only trim
81→36, not to 16 — so it is rejected as a skip signal, not merely deferred.
Also measured: a group-level GraphQL `projects { pipelines(first:1,
updatedAfter) }` query gives the exact active set in one call but with an
erratic tail (1.6s / 11.6s / 1.7s on identical runs) — fine for a once-per-
resync probe, unfit for the warm sweep.

**KNOWN ISSUE (unresolved):** glute has hit an **intermittent 100%-CPU hang** —
unresponsive to keys and Ctrl-C, no redraw. Seen once on the `b7eb264` build
(pre-timer-work, so not caused by it), after ~1h idle with 0 running pipelines;
exactly one goroutine spinning (likely the tview/tcell event loop, with the
refresh goroutine then blocked on `QueueUpdateDraw`). Root cause unknown — no
live trace was capturable (`ptrace_scope=1` blocks gdb/dlv attach to a
non-child). **If it recurs: `kill -USR1 <pid>` first, then read the dump in
`glute.log` before killing.** A killed run leaves the terminal in raw mode — run
`reset` to restore it.

As of 2026-09-30 (committed to `main`, not pushed): the
**finished-pipeline detail view**. `f`, then ↑/↓ and Enter, or a click on a
finished row, opens a modal with the pipeline's whole tree plus QUEUED/START/TIME
and a per-row timeline bar. Marcel chose the timeline option over "tree
as-is". The tree is fetched on demand (`Service.PipelineTree`, `tree.go`), not
from the store; see CLAUDE.md for why. Committed (`3186628`), then both
follow-ups: the Current tree no longer shows **retried job attempts twice**
(Marcel: "an especial annoyance"; the fix is name-based, because REST jobs carry
no retried flag), and the timeline **cuts idle gaps** that are longer than all
the busy time combined.

As of 2026-09-30 (later): **v0.3.0 released**, tag on `64aecfd`, green on both
sides first (GitLab 235073, GitHub CI 36749231485). It ships the
finished-pipeline detail view (with its timeline, idle-gap cuts, inset panel
and charcoal background), the latest-attempt-only fix for retried jobs, and
**pipeline ids as GitLab links**: an OSC 8 terminal hyperlink, plus glute
launching the browser on a plain click or `o`, but not over SSH. `o` on a job
row opens the job's page. The release pipelines (GitLab 235074, GitHub release
36750047770) were done in about 6 minutes. The tap formula (4 hashes) and the
bucket manifest matched the GitHub `SHA256SUMS`, and both pushes said
*pushed*. The re-downloaded bucket zip and linux-amd64 tarball re-hashed
correctly and report `glute v0.3.0`. Not yet tried on a real desktop: the
browser launch on macOS, Windows or a Linux desktop (it was tested only with a
stand-in opener).

**Why:** Records live status and next-steps that aren't obvious from the code.

**How to apply:**
- **Next perf lever (not started):** the resync/cold job phase is now bounded
  by the *deepest* busy project's sequential page chain on the REST jobs
  endpoint — 0.6–1.7s per 100-job page (300–400KB payloads, no `x-total`
  headers), and 5 projects have ≥100 in-window jobs on page 1 alone. Fetching
  pages 2..k in parallel after page 1 (stop once a page falls past the window)
  would roughly halve it. Also available: `resync_interval` as a config knob,
  and dropping 403 projects from the poll set until the next resolve.
- Follow-ups noted during the Pipelines rework: a compute-by-product panel was dropped as
  unhelpful (dull with a single configured product) but `ComputeByProduct`/
  `ByProject` remain in the Snapshot (used by the `refresh` preview) if a panel
  ever wants them; a like-for-like runner comparison (same job on ≥2 runners) and
  a failure-reason breakdown (`Job.FailureReason`) were scoped out as future panels.
- **Also queued (polish pass):** scroll long tables on the stats tabs, responsive
  column widths, and surface the footer warning's detail in the UI.
- **Deferred:** the Runners tab (dropped — admin-only metric); release
  automation (cross-builds exist via `make`, but no GoReleaser / macOS
  notarization / Windows signing). In-memory incremental caching is now DONE;
  a *persistent* (cross-restart) cache is still deferred. Gating the warm
  per-project sweep on `last_activity_at` is **rejected** (see 2026-09-03: CI
  doesn't move it); the sweep floor is ~1.4s for 81 projects and only
  `concurrency` shrinks it.
- Git: remote `origin` is `git@studio.wevr.com:wevr/tech/glute.git`; work lands
  on `main`.

As of 2026-09-29 (committed + pushed to `main`): **Scoop distribution for Windows**
set up as the twin of the Homebrew tap. `packaging/glute.json.in` → `make manifest`
→ `dist/glute.json`; `release.yml` pushes it to `bucket/glute.json` in the new
public repo `github.com/MarcelInTO/scoop-bucket` (created this session, seeded with
the v0.1.0 manifest, verified against Scoop's schema and the real release zip's
sha256). Both tap and bucket pushes now go through `packaging/push-to-repo.sh`.
The `SCOOP_BUCKET_TOKEN` secret is set on the GitHub mirror (2026-09-29) and
v0.2.0 exercised the bucket push end to end (see below). Not exercised on
a real Windows machine —
`scoop install glute` from the bucket is the first thing to try there. Windows on
ARM is deliberately not offered (no `windows/arm64` in PLATFORMS).

As of 2026-09-29 (later, committed to `main`): **v0.2.0 released** — the first
tag through the whole chain, all green. GitLab pipeline 234929 (test → build →
release) published the GitLab release; the mirror carried the tag in ~5 min,
while the plain `main` pushes before it propagated in ~15s — the 5 minutes is a
minimum spacing between mirror runs (a push to an idle mirror goes at once; the
tag, pushed minutes after the commit, waited), see CLAUDE.md's mirror bullets;
GitHub `release` run 36624526869 created the
release with provenance and pushed `Formula/glute.rb` 0.2.0 to the tap and
`bucket/glute.json` 0.2.0 to the bucket via `packaging/push-to-repo.sh`, both
verified against the release's SHA256SUMS (and the bucket URL re-downloaded and
re-hashed). Also in 0.2.0 (`25f32ea`): **running is DodgerBlue** (queued states
stay yellow) and **both Current panels lead with a pipeline ID column** — the
instance-wide `Pipeline.ID`; per-project `IID` is the one-field swap if the team
turns out to quote that number instead. Job rows show no id on purpose.
