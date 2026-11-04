# Continuous integration

Commit `addons.toml` and `addons.lock`, ignore `.gpm-state.toml`, and install pinned addon bytes with `gpm install`. Never use `gpm update` in CI.

Minimal Unix job:

```sh
gpm install --host-only
./addons/gdtest/gdtest run --json | tee gdtest-results.jsonl
```

Minimal Windows job:

```powershell
gpm install --host-only
addons\gdtest\gdtest.cmd run --json | Tee-Object gdtest-results.jsonl
```

`--host-only` materializes mandatory core and the implicit current-host executable. It does not alter the byte-identical lockfile. A normal install is also valid because the gdtest addon entry carries `platforms = []`.

Use `--only forbid` to reject focused tests in CI. Exit 1 means user test failures; exit 2 means configuration, discovery, protocol, worker, or reporting failure; exit 130 means cancellation. Always retain daemon logs and the JSONL artifact for an infrastructure failure.

For changed-file jobs, pass project-relative paths to `--affected` as a comma-separated or repeated flag. The analysis is deliberately conservative: project configuration, autoloads, runtime/native inputs, unresolved dynamic loads, and unknown runtime resources can select all tests. A provably empty selection exits successfully.

The repository's own release gate is `scripts/check`, followed by:

```sh
scripts/package-addon <version>
scripts/test-package-addon <version>
```

The second command locally serves the staged package, exercises default `platforms = []`, host-only and all-platform installations, proves lock equality and repeat-install cleanliness, and runs a version-matched installed binary/runtime handshake when Godot is available.
