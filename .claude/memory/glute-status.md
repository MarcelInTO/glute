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
