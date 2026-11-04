# gdtest

gdtest is a typed GDScript test framework for Godot 4.7 with a Go client, a user-level daemon, and persistent isolated headless workers. It supports conventional `test_*` methods, parameter providers, dynamic suites, async hooks and cases, exact stable-ID selection, affected-test analysis, JSONL reporting, and mechanical Vest migration.

## Install with gpm

Install a versioned release as a host development tool. The atomic host-tool flag writes `platforms = []`, so mobile and web export targets do not request meaningless gdtest binaries while gpm still installs the current host executable:

```sh
gpm init # only when addons.toml does not exist
gpm add --name gdtest \
  --source github-release \
  --repo cafecito-games/gdtest \
  --version <tag> \
  --host-tool
```

Commit `addons.toml` and `addons.lock`; do not commit `.gpm-state.toml`. Run the project-local executable without a global gdtest installation:

```sh
./addons/gdtest/gdtest run
```

CI and disposable worktrees can use `gpm install --host-only`; ordinary CI installation always uses `gpm install`, never `gpm update`. Windows release slices are intentionally deferred until the user-level daemon has a validated Windows transport and Windows CI coverage; the current release set is macOS and Linux arm64/x86_64.

The `--host-tool` capability was implemented for gpm on branch `feat/host-tool-platforms`, commit `80a6244`; use a gpm release containing that change. With an older gpm, add the equivalent manifest entry and run `gpm install`:

```toml
[addons.gdtest]
source = "github-release"
repo = "cafecito-games/gdtest"
version = "<tag>"
platforms = []
```

## Write and run a test

```gdscript
extends GDTest


## Verifies the inventory total.
func test_total() -> void:
	expect_equal(2 + 2, 4)
```

Save it as a `.test.gd` file and run:

```sh
./addons/gdtest/gdtest run
./addons/gdtest/gdtest run tests/inventory.test.gd
./addons/gdtest/gdtest run 'tests/inventory.test.gd::test_total'
./addons/gdtest/gdtest run --name total
```

See [Getting started](docs/getting-started.md), the [executable example](examples/basic/math.test.gd), and the authoritative [GDScript API specification](api-spec.md).

## Worktrees, affected tests, and CI

The daemon keys sessions by canonical repository and worktree identity, so simultaneous dirty Git worktrees never share a Godot worker. Run changed dependencies conservatively with:

```sh
./addons/gdtest/gdtest run --affected src/inventory.gd,resources/items.tres --verbose
```

Unknown dynamic loads and project-global inputs widen selection rather than risk a false negative. An empty affected selection succeeds and prints `no tests were affected`.

For hermetic debugging use `gdtest run --no-daemon`. For CI use `--json` and preserve its JSON Lines output. See [worktree usage](docs/worktrees.md) and [CI setup](docs/ci.md).

## Migrate from Vest

```sh
./addons/gdtest/gdtest migrate vest --check
./addons/gdtest/gdtest migrate vest --write
```

Migration uses gdparser syntax spans, preserves unsupported source, and reports precise manual findings. See the [Vest migration guide](docs/vest-migration.md).

## Development

Go 1.26, Godot 4.7, gdkit 0.10.0 or newer, golangci-lint v2, and gpm 0.5.1 or newer are required. Run `scripts/check` before committing. See [CONTRIBUTING.md](CONTRIBUTING.md) for packaging, benchmarks, and the complete local workflow.
