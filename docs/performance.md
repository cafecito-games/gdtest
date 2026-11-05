# Performance measurement

Run `scripts/benchmark` from a quiet machine. It prints UTC time, operating system, CPU architecture, Go version, scheduler width, and Godot version before five samples of every Go benchmark. It then measures 30 ready-daemon and warm trivial-file requests and executes the 10,000-assertion and 10,000-dynamic-case corpora through persistent workers. Each report is emitted as a `gdtest_performance` JSON record with sample count, minimum, median, p95, and maximum nanoseconds. Set `GDTEST_PERFORMANCE_SAMPLES` to change the large-corpus default of five or `GDTEST_LATENCY_SAMPLES` to change the latency default of 30.

The committed corpora cover 10,000 passing assertions, 10,000 dynamically registered and executed cases, 1,000 independently parsed test files, 32 nested dynamic suites, 10,000 failures with nested rich values, and 64 path-neutral project-session plans. Size-swept Go benchmarks expose scaling at 1,000, 5,000, and 10,000 cases/failures. The scheduler benchmark submits all 64 sessions through a four-slot global bound. Real multi-worktree worker-bound stress remains in the race-enabled integration suite.

The benchmark harness discards one warm-up operation per Godot corpus, so worker startup and first import are not included in its distributions. These large-corpus measurements characterize assertion, registration, event-streaming, and folding behavior; they are not the small-file latency objective.

The latency corpus starts a real local JSONL daemon transport, discards its cold operation, and measures each subsequent complete client request. Its trivial-file corpus also uses the real scheduler, persistent Godot worker, reset path, streamed events, and terminal response. The initial objectives are below 5 ms for a ready-daemon client connection, below 50 ms median and below 100 ms p95 for a warm small-file run, linear event serialization, and no global worker-bound violations. Deliberate sleeps, frame waits, and user case computation are excluded. Measurements are reported, not enforced as flaky wall-clock unit-test thresholds, until a stable dedicated CI baseline exists.

## Local reference run

On 2026-11-04, `GDTEST_PERFORMANCE_SAMPLES=5 GDTEST_LATENCY_SAMPLES=30 scripts/benchmark` exercised the real daemon and Godot corpora on an Apple M3 Pro (`darwin/arm64`, Go 1.26.2, Godot 4.7.2 Cafecito). Latency rows use 30 samples and large-corpus rows use five:

| Corpus | Items | Minimum | Median | p95 |
| --- | ---: | ---: | ---: | ---: |
| Ready-daemon client request | 1 | 0.054 ms | 0.096 ms | 0.340 ms |
| Warm trivial-file run | 1 | 26.9 ms | 34.4 ms | 35.1 ms |
| Dynamic discovery | 10,000 | 353.4 ms | 354.3 ms | 364.4 ms |
| Passing assertions | 10,000 | 19.6 ms | 20.7 ms | 20.8 ms |
| Dynamic case execution | 10,000 | 924.6 ms | 959.2 ms | 969.0 ms |

These are reference measurements, not portable thresholds. The corpus run also verifies every assertion count, every dynamic case start/finish pair, and every terminal status. It exposed and removed a quadratic dynamic-ID ordinal scan; ordinals are now tracked per source line in constant time.

The same run's size sweeps showed proportional allocation growth for rich-event folding: 3,036 allocations at 1,000 events, 15,066 at 5,000, and 30,102 at 10,000. Serialized manifest bytes likewise grew proportionally, and a deterministic unit test guards the 1,000-to-10,000 encoded-size ratio. Timing samples remain observational because scheduler noise is unsuitable for a unit-test threshold.
