---
name: memory-is-public
description: This memory directory and CLAUDE.md are published on the public GitHub mirror; keep anything sensitive, client-identifying or unrelated out
metadata:
  type: feedback
---

This memory directory is `.claude/memory` in the glute repo (symlinked from
`~/.claude/projects/...`), so every file in it is committed and pushed to
`github.com/MarcelInTO/glute`, which is **public**. So is CLAUDE.md, and
everything else in the repo.

**Why:** On 2026-10-21 Marcel had both scrubbed. An untracked note about another
project (its cloud account names, a DNS zone id, a cloud profile's exact
permissions) was one `git add` away from publication. The status note named
the GitLab instance's exact version and edition, which anyone can match against
GitLab's CVE list. CLAUDE.md named internal projects (client work among them)
and CI runner machines in its real-instance examples.

**How to apply:** Before writing a memory or a CLAUDE.md note, ask whether it
would be fine on a public web page. Keep out secrets and tokens, software
versions of internal servers, runner and machine names, internal project
names, cloud account/subscription/zone ids, the scopes of Marcel's own tokens,
local filesystem paths, and facts about other projects. Cite a real-instance
example by its pipeline id ("pipeline #235448"), not its project. The instance
hostname `studio.wevr.com` is already public throughout the repo. Another
project's facts go in that project's own memory store, even when the session
that learned them was started here. Removing a line doesn't remove it from the
public history. Related: [[glute-status]].
