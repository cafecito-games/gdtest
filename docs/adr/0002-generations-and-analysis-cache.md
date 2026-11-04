# ADR 0002: Keep live generations path-bound and analysis caches content-bound

Status: accepted

Each daemon session owns a reconciler for one canonical worktree path. Its generation is monotonic
within that session and never crosses into another worktree, even when two worktrees currently have
identical files. Periodic authoritative scans recover from lost or coalesced filesystem notifications.
A run completed after its requested generation advances remains visible but its terminal event is
marked stale.

Reusable discovery and dependency artifacts use hashes of normalized project-relative inputs,
configuration, gdtest build identity, and gdparser version. Absolute worktree paths are excluded from
cache keys. Entries are checksummed and atomically published outside the project.

Only a change whose parsed top-level structure is unchanged is eligible for an in-place worker
generation advance. Structural changes, parse failures, runtime-addon changes, additions, removals,
and unknown changes replace the worker. This deliberately favors an unnecessary replacement over a
worker observing stale Godot class or import state.
