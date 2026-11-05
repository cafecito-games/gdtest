# Continuous integration

Commit `addons.toml` and `addons.lock`, ignore `.gpm-state.toml`, and install pinned addon bytes with `gpm install`. Never use `gpm update` in CI.

Minimal Unix job:

```sh
gpm install --host-only
./addons/gdtest/gdtest run --json | tee gdtest-results.jsonl
```

Windows release/CI support is deferred until gdtest has a validated native
user-level daemon transport and a Windows CI runner. The package index does not
advertise Windows executable slices in the meantime.

`--host-only` materializes mandatory core and the implicit current-host executable. It does not alter the byte-identical lockfile. A normal install is also valid because the gdtest addon entry carries `platforms = []`.

Use `--only forbid` to reject focused tests in CI. Add `--fail-on-skip`, `--fail-on-todo`, or `--fail-on-void` when those otherwise non-failing outcomes should fail the job. Strict-policy violations retain their original summary counts, are listed in the final summary, and exit 1. Exit 2 means configuration, discovery, protocol, worker, or reporting failure; exit 130 means cancellation. Always retain daemon logs and the JSONL artifact for an infrastructure failure.

For changed-file jobs, pass project-relative paths to `--affected` as a comma-separated or repeated flag. The analysis is deliberately conservative: project configuration, autoloads, runtime/native inputs, unresolved dynamic loads, and unknown runtime resources can select all tests. A provably empty selection exits successfully.

The repository's own release gate is `scripts/check`, followed by:

```sh
scripts/package-addon <version>
scripts/test-package-addon <version>
```

The tag and manual release workflow makes the canonical `scripts/check` job a prerequisite of
packaging. Only the packaging job receives release-write permission, and it cannot upload or
publish artifacts after a failed source, race, real-engine, or gdkit gate.

The Linux CI jobs install the exact stock Godot 4.7.2 release asset through
`scripts/install-godot-linux-ci`. The script pins separate SHA-256 identities for x86-64 and
arm64 and rejects an unexpected engine version. Consequently, the canonical race suite executes
the real-engine integration, protocol, API-conformance, bootstrap, cancellation, and worktree
tests on Linux instead of accepting their explicit no-Godot skip path. The package job uses the
same engine to execute the installed Linux host slice and verify its runtime handshake.

Packaging and release jobs install gpm through `scripts/install-gpm-linux-ci`, which verifies the
architecture-specific v0.6.0 release archive and requires the binary to report that exact version.
Installing the module with `go install` is intentionally insufficient because gpm release versions
are injected while building release assets and a module-built executable reports `dev`.

The second command locally serves the staged package, exercises default `platforms = []`, host-only and all-platform installations, proves lock equality and repeat-install cleanliness, and runs a version-matched installed binary/runtime handshake when Godot is available.
