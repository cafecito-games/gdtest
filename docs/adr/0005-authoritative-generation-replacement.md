# ADR 0005: Replace workers on every source-generation change

- Status: accepted
- Date: 2026-11-04

## Context

A Godot worker owns one loaded project generation. In-process script reload cannot prove that
static variables, resource-cache entries, autoloads, inherited scripts, scene dependencies, or
engine registrations all reflect one coherent new generation. The earlier generation-counter-only
reload path could therefore label old loaded code as current.

## Decision

Any relevant source-content change advances the authoritative session generation and makes the
existing worker ineligible. The daemon rejects plans discovered against the old generation and
starts a replacement worker for the new generation. The client may repeat discovery and selection
once against the authoritative generation.

## Consequences

Replacement adds startup latency after a source change but preserves isolation. Real-Godot
generation coverage changes a test body during a run, observes the stale result, and verifies that
a different worker executes the changed body. A future in-process reload optimization requires
evidence that every Godot source/resource/cache category remains generation-consistent.
