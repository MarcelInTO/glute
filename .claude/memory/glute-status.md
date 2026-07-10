---
name: glute-status
description: "glute (GitLab CI/CD TUI) — build state, next steps, deferrals"
metadata: 
  node_type: memory
  type: project
  originSessionId: 3ea31245-90a6-499d-b976-c840d837c60f
---

glute is a single-binary, read-only Go TUI dashboard for GitLab CI/CD
(Pipelines and Jobs tabs). Repo: `~/devMisc/glute`; see its CLAUDE.md for
decisions and rationale.

As of 2026-07-04: Phases 1–3 are done and committed on a local `main`
(scaffold + `glute auth`, the `internal/gitlab` data layer, and the tview
dashboard), plus named multi-instance support, last-segment project names with
hover-for-full-path, and pending jobs in the Running panels.

As of 2026-07-09: the data layer is now an **incremental retained store** in
the `Poller` (not a full fetch per refresh). Measured on a real 42-project
instance, this took warm refreshes from ~16s to <1s. Also added refresh-timing
instrumentation (`RefreshStats`, printed to stderr on exit; per-refresh
`refresh timing:` log line). See CLAUDE.md's "Poller" decision + GitLab API
notes for the design. Change is committed-pending on local `main` (uncommitted
working tree at time of writing).

**Why:** Records live status and next-steps that aren't obvious from the code.

**How to apply:**
- **Next (polish pass):** scroll long tables (Top-jobs overflows its panel),
  responsive column widths, and surface the footer warning's detail in the UI.
- **Deferred:** the Runners tab (dropped — admin-only metric); release
  automation (cross-builds exist via `make`, but no GoReleaser / macOS
  notarization / Windows signing). In-memory incremental caching is now DONE;
  a *persistent* (cross-restart) cache is still deferred, as is gating the warm
  ~770ms floor (the 42 empty `updated_after` calls) on project `last_activity_at`.
- Git is **local-only — no remote configured yet** (by request).
