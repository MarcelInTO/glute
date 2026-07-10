---
name: glute-status
description: "glute (GitLab CI/CD TUI) — build state, next steps, deferrals"
metadata: 
  node_type: memory
  type: project
  originSessionId: 3ea31245-90a6-499d-b976-c840d837c60f
---

glute is a single-binary, read-only Go TUI dashboard for GitLab CI/CD
(Pipelines and Jobs tabs). Repo: `/mnt/md0/devWevr/glute`; see its CLAUDE.md
for decisions and rationale.

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

**Why:** Records live status and next-steps that aren't obvious from the code.

**How to apply:**
- **Next (polish pass):** scroll long tables (Top-jobs overflows its panel),
  responsive column widths, and surface the footer warning's detail in the UI.
- **Deferred:** the Runners tab (dropped — admin-only metric); release
  automation (cross-builds exist via `make`, but no GoReleaser / macOS
  notarization / Windows signing). In-memory incremental caching is now DONE;
  a *persistent* (cross-restart) cache is still deferred, as is gating the warm
  ~770ms floor (the 42 empty `updated_after` calls) on project `last_activity_at`.
- Git: remote `origin` is `git@studio.wevr.com:wevr/tech/glute.git`; work lands
  on `main`.
