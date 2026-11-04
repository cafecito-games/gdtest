# Performance measurement

Run `scripts/benchmark` from a quiet machine. It prints UTC time, operating system, CPU architecture, Go version, scheduler width, and Godot version before five samples of every synthetic benchmark. Results are tracked rather than enforced as wall-clock test thresholds until CI has a stable dedicated baseline.

The committed corpora cover 10,000 passing assertions, 10,000 dynamically registered cases, 1,000 independently parsed test files, 32 nested dynamic suites, 10,000 failures with nested rich values, and 64 path-neutral project-session plans. Real multi-worktree worker-bound stress remains in the race-enabled integration suite.

For warm-path measurements, start the daemon with `gdtest warm`, then run a small selected file repeatedly while recording at least 30 samples. Report median and p95 separately from startup/import time. The initial objectives are below 5 ms for a ready-daemon client connection, below 50 ms median and below 100 ms p95 for a warm small-file run, linear event serialization, and no global worker-bound violations. Deliberate sleeps, frame waits, and user case computation are excluded.
