# ADR 0012: Retain bounded warm worker lanes

- Status: accepted
- Date: 2026-11-06

## Context

ADR 0006 retained one primary worker per project session but made every
additional file lane ephemeral. That kept the process count bounded, but a
four-way sharded command started three Godot processes after discovery and
then shut them down before replying. On the Uzir consumer corpus, auxiliary
startup took about eight seconds and three sequential graceful-shutdown
deadlines added about six more. Repeating the same shard plan could not
amortize either cost.

The daemon already owns a user-wide hard worker limit, per-session identity,
generation fencing, age and memory limits, run-count recycling, and idle/LRU
session eviction. An unbounded per-session pool is therefore unnecessary: the
same bounded lanes used during a request can remain attached to their exact
session when idle.

## Decision

After a successful `reuse` multi-file or sharded run, auxiliary lanes remain
idle in that project session. A later run leases those exact lanes before
starting new workers. Reuse requires the same source generation and an
unexpired worker age.

The global worker budget remains authoritative. LRU eviction may discard an
idle session's primary and pooled lanes to serve another session. `fresh` and
`recycle` isolation, taint, reset or protocol failure, generation mismatch,
`worker_max_runs`, age, memory pressure, session reconciliation, and daemon
shutdown retire pooled lanes as well as the primary. Asynchronous recycling
holds each global slot until its exact child exits, so replacement can never
oversubscribe the bound.

## Consequences

- Repeated four-way Uzir single-file shards reuse all four workers: measured
  client time fell from 19.6 seconds before this decision to 2.2 seconds.
- Idle sessions may retain more than one process, but never beyond the existing
  global limit; another session can evict them.
- A first cold shard still pays worker startup. Callers that require predictable
  first-run latency should warm the project, and future work may extend
  `warm_workers` to proactively populate more than the primary lane.
- Each worker remains bound to one canonical project session and generation;
  no process crosses a worktree or session boundary.
