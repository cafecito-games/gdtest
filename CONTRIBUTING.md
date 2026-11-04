# Contributing

Use Go 1.26 and keep changes compatible with Godot 4.7. Tests use Testify assertions. Before committing, run the canonical policy:

```sh
scripts/check
```

It runs gofmt cleanliness, module tidiness, golangci-lint v2, race tests, vet, build, and strict gdkit lint/format/architecture/UID checks over the shipped addon and valid examples. `prek run --all-files` and CI invoke the same script.

For performance work, run `scripts/benchmark`; report all five samples plus its environment header. Do not add flaky wall-clock thresholds. For release work, run `scripts/package-addon <version>` and `scripts/test-package-addon <version>`. Packaging always stages the addon, injects one exact version into Go and GDScript, cross-compiles six host slices, packages twice, and compares bytes. Never run a bare root-level `gpm package` or publish manually generated partial assets.

Keep the backend-neutral plan/event boundary free of Godot objects. Use content identity for reusable caches and canonical path identity for live sessions. Prefer false-positive affected selection, cache refusal, or worker replacement to a false result. Add focused race/lifecycle/protocol regression coverage for every bug.

Do not hand-edit `addons.lock` or `.gpm-state.toml`. Do not weaken gdkit by excluding `addons/gdtest`. Architectural decisions and justified specification deviations belong in `docs/adr/`; milestone evidence belongs in `docs/implementation-status.md`.
