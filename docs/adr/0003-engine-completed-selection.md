# ADR 0003: Select from an engine-completed manifest

- Status: accepted
- Date: 2026-11-04

## Context

Static gdparser discovery can identify files and conventional methods, but Godot must execute registration and provider functions to enumerate dynamic cases and parameter rows. Applying every selector only to the static approximation would make dynamic stable IDs and provider-row IDs impossible to address precisely. Executing case bodies to discover them would violate the API contract and could cause user side effects.

## Decision

The client first narrows candidate files with path-bearing selectors and conservative affected analysis. A persistent authenticated worker then performs discovery only: it may run registration and provider functions, but never lifecycle hooks or case bodies. The resulting immutable, path-neutral manifest is cached by tool/runtime/Godot/configuration/content identity.

All name and exact-ID selection is applied to that completed manifest. The execution plan carries the complete candidate file plans plus an explicit `selection_applied` marker and exact selected IDs. The runtime regenerates the manifest in the leased worker and executes only those IDs. Focus policy is applied to this already-selected plan.

Affected-test analysis remains conservative and file-based because arbitrary dynamic registration can depend on unresolved runtime inputs. Unresolved project metadata or dynamic loads widen selection rather than risk a false negative.

## Consequences

- Dynamic cases and individual provider rows are first-class CLI selectors.
- Excluded files do no registration work, hook work, or case work.
- Absolute worktree paths never enter stable IDs or reusable cache keys.
- Discovery errors are reported before case execution with source locations.
- A content or generation mismatch refuses cached or worker results instead of executing a possibly stale selection.
