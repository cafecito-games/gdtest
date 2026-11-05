# ADR 0009: Ahead-of-time generated typed test doubles

- Status: accepted
- Date: 2026-11-05

## Context

Vest's `mock`, `when`, and `get_calls_of` synthesize a script at runtime. That approach weakens
static typing, reports mistakes late, complicates worker isolation, and cannot be checked
reproducibly in CI. `api-spec.md` deferred runtime mocks and `spec.md` excluded runtime source
generation, leaving users to hand-write fakes. Ahead-of-time generation in Go keeps the output
reviewable and deterministic while the runtime stays a small set of GDScript support types
(`GDTestDoubleContext`, `GDTestDoubleMethod`, and the generated controllers).

Call ordering must agree with signal probes (ADR 0008). A double therefore records its calls
through the same case-local sequence allocator that `watch_signal` uses.

## Decision

Doubles are declared in `gdtest.json` under `test_doubles`. `gdtest generate doubles [--check]
[--project DIR]` generates a subclass of each declared source script. The generator is
`internal/doubles`; the runtime is `addons/gdtest/gdtest_double_*.gd` plus `GDTest.double_context()`.
The contract is `api-spec.md` section 14.7, and the rules below are its normative basis.

- **D1, doubled methods.** Every non-static function whose name does not begin with `_`, declared
  at the top level of the source script or of a project-script ancestor. The nearest declaration
  wins, and methods sort by byte order of their names.
- **D2, constructor.** The nearest `_init` in the project-script chain supplies mirrored
  parameters. With none, the constructor takes only the context.
- **D3, behaviour queue.** Each call consumes the front queued behaviour. The last remaining
  behaviour is sticky and repeats for every later call (Mockito semantics). A call with no queued
  behaviour is unexpected.
- **D4, controller access.** `double.gdtest.<method>` returns the typed controller. `gdtest`,
  `GDTestControls`, `_GDTEST_DOUBLE_PATH`, and every generated `<Pascal>Method` name are reserved.
- **D5, configuration returns void.** Configuration methods do not chain, which avoids
  covariant-return questions in GDScript.
- **D6, void methods.** A void method gets `then_return() -> void` with no parameter. Calling it
  marks the call as expected.
- **D7, strictness.** An untyped, inferred (`:=`), or variadic parameter, a missing return type, or
  an unsupported return type is a diagnostic with exit `2`. An unprovable signature is refused.
- **D8, ownership.** A generated file is owned exactly when its first line is the marker
  `# gdtest:generated-double v1 source=res://<path>`. There is no state file; stale outputs are
  found by scanning the project for that marker.
- **D9, UID.** The `.gd.uid` value is derived from the output's `res://` path with a domain-separated
  SHA-256 digest and Godot's base-34 encoding. It is never random.
- **D10, typed-collection answers.** An answer for a typed-collection return must return an
  identically typed collection. `Array[int]().is_same_typed([7])` is `false` in Godot 4.7, so an
  untyped literal is rejected.

## Why the first-line marker instead of a state file

A state file records what the generator wrote, but it can disagree with the disk: a file can be
deleted, copied, or edited while the state still claims it. The marker is in the file, so ownership
is evidence the file itself carries. The scanner validates the marker with an exact pattern, so a
hand-written file that merely mentions gdtest is never treated as owned. A configured output that
exists without the marker is refused, so generation never overwrites a hand-written script.

## Why path-derived UIDs

Godot uses the `.gd.uid` to identify a script across renames, so a random UID would change on every
regeneration and produce churn in version control. Deriving the UID from the output path keeps the
output byte-identical across machines and worktrees. A renamed output receives a new UID, which is
the correct outcome because it is a different resource.

## Why the formatter fixed point

Generated text is formatted with gdparser's `format.GodotStyle()` before it is written, and the
renderer test asserts that formatting its output changes nothing. Without that property, a
developer's formatter run would produce a diff that `--check` then reports as stale. The
acceptance rule is therefore that a write followed by `--check` exits `0`, and the golden output is
pinned byte-for-byte.

## Failure model

Every diagnostic (invalid configuration, unparseable or missing ancestor, inheritance cycle, D7
refusal, collision, unmarked output, symbolic link, or filesystem escape) exits `2` before any file
changes. A runtime misuse is a case failure at the caller's line, not in the generated file. An
unconfigured call, a wrong-typed answer, and a wrong-arity answer each fail the executing case and
return the neutral default. A closed context records nothing into any case.

## Consequences

Users never write Vest-style runtime synthesis, and their doubles are ordinary, reviewable GDScript
that gdkit checks like any other script. The cost is a generation step, and the generated files
must be committed and kept current, which `scripts/check` and CI enforce with `--check`.

Matchers, callback probes, cross-case captures, native-class and static method interception, and
monkey-patching of existing instances are out of scope. Doubles are injected by construction only,
so code under test must accept the double through its constructor, a parameter, or a property
typed as the source class.
