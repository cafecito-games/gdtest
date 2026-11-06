# Continuous integration

Commit `addons.toml` and `addons.lock`, ignore `.gpm-state.toml`, and install pinned addon bytes with `gpm install`. Never use `gpm update` in CI.

Minimal Unix job:

```sh
gpm install --host-only
./addons/gdtest/gdtest run --only forbid \
  --report-format junit --report-file gdtest-junit.xml
./addons/gdtest/gdtest bench --json --write-baseline gdtest-benchmarks.json \
  | tee gdtest-benchmark-events.jsonl
```

`--report-format` accepts `terminal`, `jsonl`, or `junit` (`--json` is shorthand for `--report-format jsonl`). Passing `--report-file` publishes the formatted report atomically while keeping normal human-readable terminal output on stdout; omitting `--report-file` writes the selected format directly to stdout.

Windows release/CI support is deferred until gdtest has a validated native
user-level daemon transport and a Windows CI runner. The package index does not
advertise Windows executable slices in the meantime.

`--host-only` materializes mandatory core and the implicit current-host executable. It does not alter the byte-identical lockfile. A normal install is also valid because the gdtest addon entry carries `platforms = []`.

Benchmark deltas are informational. Preserve the baseline and JSONL artifacts for review, but do
not treat a positive delta as a failing exit status. Configuration, malformed baseline, worker,
protocol, cancellation, and incomplete-sample outcomes still use the normal infrastructure exit
policy.

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

The manual Release workflow accepts only a `patch`, `minor`, or `major` choice. It derives strict
semantic versioning from the latest tag, refuses non-semver history or a moved `main`, performs the
same package/install verification, and creates the tag and release from the exact verified commit.

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
