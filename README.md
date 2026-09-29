# glute

A single-binary terminal dashboard for GitLab CI/CD — pipeline and job
statistics across a configured watchlist of **products** (sets of GitLab groups
and repositories).

Read-only. Cross-platform (Linux, macOS, Windows).

## Commands

- `glute` — launch the interactive dashboard (Current / Work / Infrastructure tabs)
- `glute --sample` — explore the dashboard with built-in fixture data
- `glute auth` — connect to a GitLab instance (validates a `read_api` token)
- `glute auth status` — verify the stored token
- `glute auth logout` — remove the stored token
- `glute refresh` — print a one-shot text snapshot (data-layer preview)
- `glute instances` — list configured instances
- `glute version`

Keys in the dashboard: `Tab`/`Shift-Tab` or `1`/`2`/`3` switch tabs, `↑`/`↓`
scroll the Current tree, `t` cycles the history window (1d / 7d / 30d) that
every Work and Infrastructure panel aggregates over, `r` refreshes, `?` shows
help, `q` quits.

## Install

On macOS and Linux:

```sh
brew install marcelinto/tap/glute
```

On Windows, with [Scoop](https://scoop.sh):

```powershell
scoop bucket add marcelinto https://github.com/MarcelInTO/scoop-bucket
scoop install glute
```

Otherwise grab the archive for your platform from the
[latest release](https://github.com/MarcelInTO/glute/releases/latest), unpack it,
and put `glute` on your `PATH`. The binaries are unsigned, so a browser download
on macOS is quarantined by Gatekeeper — install with Homebrew or `curl`, or clear
the flag with `xattr -d com.apple.quarantine`.

To build from source instead (Go 1.26, no CGO):

```sh
make            # cross-compile binaries into bin/
```

## Getting started

```sh
glute auth      # enter your instance URL + a read_api token
glute           # launch the dashboard
```

## Instances

glute supports multiple GitLab servers as named instances, each with its own
config, token, and log. Select one with `--instance/-i` or the `GLUTE_INSTANCE`
environment variable (handy per shell); the `default` instance is used when none
is given.

```sh
glute auth --instance work      # set up a second server
GLUTE_INSTANCE=work glute        # run the dashboard against it
glute instances                  # see them all (active marked with *)
```

## Configuration

Config for the **default** instance lives in `~/.config/glute/config.toml`;
named instances live in `~/.config/glute/<name>/config.toml`. Paths honor
`$XDG_CONFIG_HOME`, or override the base with `$GLUTE_CONFIG_DIR`. The token is
stored separately (`token`, 0600) beside the config, or supplied via the
`GLUTE_TOKEN` environment variable, which takes precedence.

```toml
gitlab_url       = "https://gitlab.example.com"
refresh_interval = "10s"
recent_window    = "24h0m0s"        # "recent failures & successes" lookback
top_window       = "720h0m0s"       # history lookback (30d); `t` picks 1d/7d within it
concurrency      = 16               # parallel GitLab API calls per refresh
# ca_cert = "/path/to/corp-ca.pem"  # optional, for a self-managed CA

[[product]]
name     = "Payments"
groups   = ["org/payments"]
projects = ["org/legacy-gateway"]
```

## Releasing

Releases are cut from a `vX.Y.Z` tag. The GitLab project on `studio.wevr.com` is
the source of truth; `github.com/MarcelInTO/glute` is a push mirror of it, and the
work is split because the GitLab project is private while a public Homebrew tap
or Scoop bucket needs a URL anyone can reach:

| Where | On a `vX.Y.Z` tag |
| --- | --- |
| GitLab (`.gitlab-ci.yml`) | builds the five archives, uploads them to the project's generic package registry, and creates the GitLab release pointing at them |
| GitHub (`.github/workflows/release.yml`) | builds the same archives, creates the public GitHub release with build provenance, and pushes `Formula/glute.rb` to `MarcelInTO/homebrew-tap` and `bucket/glute.json` to `MarcelInTO/scoop-bucket` |

A prerelease tag (`v1.2.3-rc.1`) is released on both sides but deliberately kept
out of the tap and the bucket — Homebrew versions are numeric, and a tap tracking
a release candidate would push it to everyone running `brew upgrade` (a bucket,
to everyone running `scoop update`). Tags that are not `vX.Y.Z` still run the
tests but publish nothing.

Rehearse the whole thing locally before spending a tag — this is exactly what
both pipelines run:

```sh
make check                          # gofmt, vet, tests
make dist        VERSION=v1.2.3     # archives + SHA256SUMS in dist/
make verify-dist VERSION=v1.2.3     # unpack and assert the binary reports v1.2.3
make formula     VERSION=v1.2.3     # dist/glute.rb (Homebrew) from those checksums
make manifest    VERSION=v1.2.3     # dist/glute.json (Scoop) from the same checksums
```

`.github/workflows/release.yml` also has a `workflow_dispatch` dry run: it builds
the archives and prints the formula and manifest to the step summary without
creating a release or touching the tap or the bucket.

The pushes to the tap and the bucket are opt-in: each needs a repository secret on
the mirror (`HOMEBREW_TAP_TOKEN`, `SCOOP_BUCKET_TOKEN`) holding a token with write
access to that repo. Without one, the step warns and leaves the file in the step
summary to commit by hand — or with `packaging/push-to-repo.sh`.

Then:

```sh
git tag -a v1.2.3 -m "glute 1.2.3" && git push origin v1.2.3
```
