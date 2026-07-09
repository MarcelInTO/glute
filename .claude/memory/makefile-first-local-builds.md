---
name: makefile-first-local-builds
description: Prefer Makefiles to drive local (non-CI) build steps and processes
metadata: 
  node_type: memory
  type: feedback
  originSessionId: 3ea31245-90a6-499d-b976-c840d837c60f
---

For projects with local, non-CI build steps or other processes, drive them
through a Makefile as the first choice — with the default `make` target doing
the project's primary build.

**Why:** Stated cross-project preference. It is conditional on project type — it
applies when there is local build/process automation to run, not universally.

**How to apply:** When a project needs local build/dev/process automation, reach
for a Makefile before ad-hoc scripts, unless there is a concrete reason to do
otherwise. The specifics (targets, outputs) are per-project — e.g. glute's
default `make` cross-compiles single binaries into a gitignored `bin/`.
