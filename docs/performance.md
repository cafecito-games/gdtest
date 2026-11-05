# Performance measurement

This document covers gdtest's internal engineering corpora. The public `gdtest bench` command is
separate: it discovers user-authored `*.bench.gd` files, measures them inside persistent workers,
reports raw samples plus median/p95/min/max statistics, and can write or compare portable JSON
baselines. See `api-spec.md` section 14.8 and `docs/getting-started.md` for that user-facing API.
Neither path turns noisy wall-clock measurements into automatic pass/fail thresholds.

Run `scripts/benchmark` from a quiet machine. It prints UTC time, operating system, CPU architecture, Go version, scheduler width, and Godot version before five samples of every Go benchmark. It then measures 30 ready-daemon and warm trivial-file requests and executes the 10,000-assertion and 10,000-dynamic-case corpora through persistent workers. Each report is emitted as a `gdtest_performance` JSON record with sample count, minimum, median, p95, and maximum nanoseconds. Set `GDTEST_PERFORMANCE_SAMPLES` to change the large-corpus default of five or `GDTEST_LATENCY_SAMPLES` to change the latency default of 30.

For allocation or CPU investigation, run `scripts/profile [output-directory]`. It profiles the
1,000-file parser, 10,000-case serialization, 10,000 rich-failure fold, 64-session scheduler, and
deep-suite serialization corpora together. The ignored output directory receives the raw CPU and
heap profiles, the retained test executable required to symbolize them, environment metadata,
benchmark output, and deterministic top-30 text summaries. Set `GDTEST_PROFILE_BENCHTIME` to
change the default one-second profile sample.

The committed corpora cover 10,000 passing assertions, 10,000 dynamically registered and executed cases, 1,000 independently parsed test files, 32 nested dynamic suites, 10,000 failures with nested rich values, and 64 path-neutral project-session plans. Size-swept Go benchmarks expose scaling at 1,000, 5,000, and 10,000 cases/failures. The scheduler benchmark submits all 64 sessions through a four-slot global bound. Real multi-worktree worker-bound stress remains in the race-enabled integration suite.

The benchmark harness discards one warm-up operation per Godot corpus, so worker startup and first import are not included in its distributions. These large-corpus measurements characterize assertion, registration, event-streaming, and folding behavior; they are not the small-file latency objective.

The latency corpus starts a real local JSONL daemon transport, discards its cold operation, and measures each subsequent complete client request. Its direct trivial-file corpus also uses the real scheduler, persistent Godot worker, reset path, streamed events, and terminal response. A separate `warm_single_file_cli` corpus builds the real executable, warms its daemon, and measures independent complete Cobra process invocations, including process startup, configuration, worktree identity, static and dynamic discovery, reporting, and the daemon/worker request. Keeping both distributions prevents the direct daemon result from being mistaken for full command latency.

The initial objectives are below 5 ms for a ready-daemon client connection, below 50 ms median and below 100 ms p95 for a warm small-file run, linear event serialization, and no global worker-bound violations. Deliberate sleeps, frame waits, and user case computation are excluded. Measurements are reported, not enforced as flaky wall-clock unit-test thresholds, until a stable dedicated CI baseline exists.

## Local reference run

On 2026-11-05, `GDTEST_PERFORMANCE_SAMPLES=5 GDTEST_LATENCY_SAMPLES=30 scripts/benchmark` exercised the real daemon and Godot corpora on an Apple M3 Pro (`darwin/arm64`, Go 1.26.2, Godot 4.7.2 Cafecito). Latency rows use 30 samples and large-corpus rows use five:

| Corpus | Items | Minimum | Median | p95 |
| --- | ---: | ---: | ---: | ---: |
| Ready-daemon client request | 1 | 0.041 ms | 0.057 ms | 0.224 ms |
| Warm direct daemon/worker request | 1 | 11.2 ms | 13.8 ms | 23.6 ms |
| Warm complete Cobra CLI invocation | 1 | 25.9 ms | 27.9 ms | 34.5 ms |
| Dynamic discovery | 10,000 | 256.2 ms | 258.0 ms | 260.9 ms |
| Passing assertions | 10,000 | 17.2 ms | 20.8 ms | 22.1 ms |
| Dynamic case execution | 10,000 | 700.5 ms | 712.4 ms | 719.0 ms |

These are reference measurements, not portable thresholds. The corpus run also verifies every assertion count, every dynamic case start/finish pair, and every terminal status. It exposed and removed a quadratic dynamic-ID ordinal scan; ordinals are now tracked per source line in constant time.

A prior complete verification run on the same machine passed every corpus but became heavily
contended while unrelated Swift builds were active. It observed a full-CLI median/p95 of
110.5/168.0 ms and a direct daemon/worker median/p95 of 55.5/132.0 ms; even the 10,000-case
Godot corpora were roughly ten times slower than the quiet reference. These numbers prove the new
path executes and disclose the effect of host contention; they are not substituted for the
quiet-host baseline above, which meets the latency objectives for both direct and full-CLI runs.

The same run's size sweeps showed proportional allocation growth for rich-event folding: 3,036 allocations at 1,000 events, 15,066 at 5,000, and 30,102 at 10,000. Serialized manifest bytes likewise grew proportionally, and a deterministic unit test guards the 1,000-to-10,000 encoded-size ratio. Timing samples remain observational because scheduler noise is unsuitable for a unit-test threshold.

## Uzir customer corpus

The isolated Uzir dogfood worktree is a separate customer-scale functional corpus rather than a
portable benchmark. On 2026-11-05 a fresh daemon executed 682 files and 8,238 cases through four
bounded worker lanes in 96.13 seconds with every case passing. A selected same-configuration
follow-up reused the retained primary worker and completed in 3.48 seconds. The earlier
process-per-file implementation took 629.73 seconds before terminating with only 1,408 passes, one
failure, and one error; this comparison motivated the bounded lane design in ADR 0006.

The customer run also verifies behavior that a microbenchmark cannot: initial project bootstrap
finishes before concurrent lanes start, every lane has isolated `user://` storage, reset restores
the startup audio-bus layout, and all worker data leaves are removed when the daemon stops. These
wall-clock figures are local evidence, not release thresholds.

## Scoped autofree and leak-detection overhead

On 2026-11-05, `scripts/benchmark` ran back to back before and after adding scoped `autofree`, the
per-scope one-frame `queue_free()` drain, and post-scope orphan/`SceneTree` leak detection (ADR
0010) on an AMD Ryzen 7 PRO 8845HS (`linux/amd64`, Go 1.26.9, Godot 4.7.2 Cafecito):

| Corpus | Items | Before median (p95) | After median (p95) |
| --- | ---: | ---: | ---: |
| Ready-daemon client request | 1 | 0.110 ms (0.470 ms) | 0.119 ms (0.365 ms) |
| Warm direct daemon/worker request | 1 | 55.17 ms (55.94 ms) | 55.19 ms (55.49 ms) |
| Warm complete Cobra CLI invocation | 1 | 75.87 ms (76.64 ms) | 75.85 ms (76.76 ms) |
| Dynamic discovery | 10,000 | 315.45 ms (317.00 ms) | 315.76 ms (318.58 ms) |
| Passing assertions (single case) | 10,000 | 54.94 ms (55.90 ms) | 54.77 ms (55.73 ms) |
| Dynamic case execution | 10,000 | 759.33 ms (796.04 ms) | 69,258.50 ms (69,299.06 ms) |

Parser, case/deep-suite serialization, rich-failure folding, scheduler, discovery, assertion, and
single-file warm-daemon latencies are unchanged within host noise. The 10,000-case execution corpus
reflects one `await SceneTree.process_frame` yield per completed case (~6.92 ms per case under
Godot's default headless frame cadence) required to drain deferred `queue_free()` deletions before
the post-scope leak check runs.
