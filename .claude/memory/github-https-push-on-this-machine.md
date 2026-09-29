---
name: github-https-push-on-this-machine
description: HTTPS git clone/push to GitHub fails on this machine (missing credential helper); use the gh CLI token in the URL
metadata:
  type: project
---

On this machine git is configured with `credential.helper = libsecret`, but the
helper binary is not installed, so any HTTPS clone or push of a GitHub repo dies
with `git: 'credential-libsecret' is not a git command` followed by
`could not read Username for 'https://github.com'`. The gh CLI *is* logged in
(account MarcelInTO, `repo` scope), so embed its token in the URL instead:
`https://x-access-token:$(gh auth token)@github.com/<owner>/<repo>.git`. That is
exactly what `packaging/push-to-repo.sh` does with `PUSH_TOKEN`, and it was how
the scoop-bucket repo was seeded on 2026-09-29. The
`git: 'credential-libsecret' is not a git command` line still prints as noise
when the token URL is used; it is harmless.

**Why:** studio.wevr.com is reached over SSH and never hits this; only the
GitHub-side repos (homebrew-tap, scoop-bucket, mirrortest) do, so it surfaces
rarely and looks like an auth problem when it does.

**How to apply:** for a one-off GitHub push, clone with the token URL into the
scratchpad and remove the clone afterwards (the token lands in its
`.git/config`). Don't "fix" it by installing the helper without asking — the
user may prefer gh-managed auth. Related: [[glute-status]].
