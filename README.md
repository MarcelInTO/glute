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

## Getting started

```sh
make            # cross-compile binaries into bin/
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
