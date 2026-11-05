# ADR 0006: Pool bounded worker lanes and isolate worker user data

- Status: accepted
- Date: 2026-11-05

## Context

A multi-file request originally started one Godot process per file. Large projects therefore paid
hundreds of startup costs, while starting several cold workers concurrently could race during the
first project import. Reusing workers exposed two further isolation requirements: Godot processes
for one project otherwise share `user://`, and engine-global state such as `AudioServer` survives
between files.

## Decision

A multi-file request owns bounded worker lanes. The primary lane bootstraps first and is retained
for later requests; additional lanes start lazily up to the request, scheduler, and global worker
limits and close when that request finishes. Every file still acquires its own fair scheduler lease,
and run-count, age, memory, taint, cancellation, and generation policies apply to each lane.

Each process receives a dedicated Godot custom user-data directory under
`gdtest/workers/<opaque-session-hash>/<worker-id>`. The daemon creates the exact leaf with private
permissions before launch and removes it when the child exits, including failed startup attempts.
Neither project paths nor caller-controlled session identifiers appear in the directory name.

Worker reset restores the startup `AudioBusLayout` in addition to clearing gdtest's per-run state.
Source-generation changes still replace the process as required by ADR 0005; reset is not treated
as proof that arbitrary Godot or project global state can be made fresh.

## Consequences

The Uzir customer corpus executes 682 files and 8,238 cases through four bounded lanes with no
cross-file contamination. A fresh isolated run completed in 96.13 seconds, a same-configuration
follow-up reused the retained primary worker, and stopping the daemon left no worker user-data leaf
directories. Focused tests cover serialized initial bootstrap, lane reuse, maximum-run recycling,
audio-state restoration in real Godot, opaque data paths, failed-start cleanup, and the global
worker bound.

The primary worker occupies one session slot between requests until normal idle or lifecycle
recycling. Additional request lanes remain intentionally ephemeral, avoiding an unbounded
per-session pool while retaining the dominant warm-worker benefit.
