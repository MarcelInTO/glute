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
  panels from a single fetch pass, so the UI never shows a half-loaded state.
  The UI depends on the `Service` interface (fake + `SampleSnapshot` for tests).
- **Deliberately deferred (small instance — don't pre-optimize):** the
  group→project resolver re-resolves every refresh and there is no persistent
  cache. Add caching/activity-trimming only when a real instance demands it.

## GitLab API notes

- The pipeline **list** endpoint omits `duration` — computing "avg length"
  needs a per-pipeline **detail** fetch. Finished pipelines are cached in the
  poller (their duration is immutable); this is the expensive call.
- The **jobs** list endpoint *does* include duration (cheaper).
- The jobs endpoint has **no time filter**; the client paginates newest-first
  and stops once past the window.
- The Running panels include pending/queued work via `Status.IsActive()`. Jobs
  must therefore be fetched with `created`/`pending` scopes (see
  `jobFetchScopes`), not just `running`, or pending jobs never appear.
  Pipelines are fetched without a status filter, so they already include them.

## TUI notes

- tview has no native tooltip — "hover" reveals a row's full project path in
  the footer via a mouse-motion capture (falls back to click).
- A TUI owns the screen, so logs go to `<instance dir>/glute.log`, never stdout.
- Muted text uses `silver` (not `gray`) so it stays legible on dark terminals.
