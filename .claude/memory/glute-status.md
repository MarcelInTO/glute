---
name: glute-status
description: "glute (GitLab CI/CD TUI) — build state, next steps, deferrals"
metadata: 
  node_type: memory
  type: project
  originSessionId: 3ea31245-90a6-499d-b976-c840d837c60f
---

glute is a single-binary, read-only Go TUI dashboard for GitLab CI/CD
(Current, Pipelines, and Jobs tabs). Repo: `/mnt/md0/devWevr/glute`; see its
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
were correct all along). **Deferred:** the right-align-numeric-headers fix and
the `MR <n>` ref display aren't yet applied to the Pipelines/Jobs tabs — do both
during the stats rework (part 2 below).

**KNOWN ISSUE (unresolved):** glute has hit an **intermittent 100%-CPU hang** —
unresponsive to keys and Ctrl-C, no redraw. Seen once on the `b7eb264` build
(pre-timer-work, so not caused by it), after ~1h idle with 0 running pipelines;
exactly one goroutine spinning (likely the tview/tcell event loop, with the
refresh goroutine then blocked on `QueueUpdateDraw`). Root cause unknown — no
live trace was capturable (`ptrace_scope=1` blocks gdb/dlv attach to a
non-child). **If it recurs: `kill -USR1 <pid>` first, then read the dump in
`glute.log` before killing.** A killed run leaves the terminal in raw mode — run
`reset` to restore it.

**Why:** Records live status and next-steps that aren't obvious from the code.

**How to apply:**
- **Next:** part (2) above — rework the Pipelines/Jobs tabs into stats panels
  (drop their now-redundant Running panels, since Current supersedes them).
- **Also queued (polish pass):** scroll long tables on the stats tabs, responsive
  column widths, and surface the footer warning's detail in the UI.
- **Deferred:** the Runners tab (dropped — admin-only metric); release
  automation (cross-builds exist via `make`, but no GoReleaser / macOS
  notarization / Windows signing). In-memory incremental caching is now DONE;
  a *persistent* (cross-restart) cache is still deferred, as is gating the warm
  ~770ms floor (the 42 empty `updated_after` calls) on project `last_activity_at`.
- Git: remote `origin` is `git@studio.wevr.com:wevr/tech/glute.git`; work lands
  on `main`.
