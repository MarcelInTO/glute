---
name: instrument-before-optimizing
description: Measure with real instrumentation before designing a performance fix; let the numbers pick the target
metadata:
  node_type: memory
  type: feedback
---

When work turns to performance, the user wants a measurement step before any
optimization design: instrument the running app, gather real numbers, then
design the scheme from what the data actually shows.

**Why:** On the glute refresh-caching work, the user explicitly asked to
instrument first ("Before we design a scheme, could you instrument…"). The
numbers overturned the standing assumption — CLAUDE.md had called the
per-pipeline *detail* fetch "the expensive call," but instrumentation showed the
cost was the *list* fetch re-downloading 30 days of history every refresh, with
detail-enrichment at ~0s once cached. Designing against the assumed bottleneck
would have optimized the wrong thing.

**How to apply:** For a nontrivial perf task, add lightweight timing/counters
first (per-phase, retained across runs, surfaced somewhere observable), run
against a realistic dataset, and let the breakdown choose the target. Keep the
instrumentation in afterward to confirm the win. Don't trust prior "this is the
slow part" claims — including ones written in the repo's own docs. See
[[glute-status]].
