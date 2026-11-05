# ADR 0004: Distribute gdtest as a sliced gpm host tool

- Status: accepted
- Date: 2026-11-04

## Context

gdtest combines a pure GDScript runtime with a Go executable that runs on the developer's host. A game's export platforms are unrelated to that host: an iOS/web project opened on macOS still needs a macOS gdtest executable, not iOS and web executables. Shipping every binary in mandatory core would waste space and make the runtime package platform-dependent.

The released gpm version did not expose an atomic add operation that wrote an explicit empty addon platform override before resolution. Editing `addons.toml` manually before installation would make the documented primary workflow multi-step and error-prone.

## Decision

Release packages contain a binary-free mandatory core and four validated architecture slices: macOS arm64/x86_64 and Linux arm64/x86_64. Windows slices are deferred until a native user-level daemon transport and Windows CI can be validated; publishing cross-compiled binaries whose default daemon mode is known not to work would violate the isolation contract. Packaging always happens from a temporary staged addon tree. The release version is injected into both the Go binary and `version.gd`; authenticated handshakes reject a mismatch.

Consumers install with gpm's `--host-tool` add flag. That operation writes `platforms = []`, selects only core plus the implicit current-host slice for the initial install, and rolls the manifest entry back if installation fails. Nil and explicit-empty platform lists retain identical lock hash semantics so locks remain byte-identical across hosts and selection modes.

The minimal gpm enhancement is isolated in sibling `godot-addon-manager` branch
`feat/host-tool-platforms`, implementation commit `80a6244` plus real sliced-transaction test
commit `67e8265`, based on `3202034`. The command-level test uses the documented
`github-release --host-tool` shape with mobile and web project targets, feeds a real
`gpm package` fixture through the source seam, and verifies that the manifest records
`platforms = []`, the lock pins every published slice, and machine state plus installed files
contain only core and the current host slice. gdtest does not vendor or patch gpm at runtime.

## Consequences

The authoring manifest stays under `packaging/`; running bare `gpm package` at repository root fails instead of accidentally publishing Go sources. Package and test scripts build twice, compare bytes, inspect every archive, exercise default/host-only/all-platform installs, compare locks, check repeat-install cleanliness, and run the installed host binary against the installed runtime. A gpm release containing the sibling change is required for the one-command workflow; older gpm versions can use the documented explicit manifest fallback.
