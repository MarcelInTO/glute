# glute

A single-binary terminal dashboard for GitLab CI/CD — pipeline and job
statistics across a configured watchlist of **products** (sets of GitLab groups
and repositories).

Read-only. Cross-platform (Linux, macOS, Windows).

## Status

Early scaffold. Working today:

- `glute auth` — connect to a GitLab instance (validates a `read_api` token)
- `glute auth status` — verify the stored token
- `glute auth logout` — remove the stored token
- `glute version`

The Pipelines and Jobs dashboard tabs are next.

## Getting started

```sh
go run . auth          # enter your instance URL + a read_api token
go run . auth status   # confirm it works
```

## Configuration

Non-secret config lives in `~/.config/glute/config.toml` (honors
`$XDG_CONFIG_HOME`, or override with `$GLUTE_CONFIG_DIR`). The token is stored
separately in `~/.config/glute/token` (0600), or supplied via the `GLUTE_TOKEN`
environment variable, which takes precedence.

```toml
gitlab_url       = "https://gitlab.example.com"
refresh_interval = "30s"
# ca_cert = "/path/to/corp-ca.pem"   # optional, for a self-managed CA

[[product]]
name     = "Payments"
groups   = ["org/payments"]
projects = ["org/legacy-gateway"]
```
