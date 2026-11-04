# Worktrees and concurrent agents

gdtest's daemon is user-scoped, while every session is bound to one canonical Git worktree, configuration identity, Godot executable, source generation, and gdtest build. A worker never changes projects or crosses worktree boundaries.

This makes concurrent dirty worktrees safe:

```sh
git worktree add ../feature-a -b feature-a
git worktree add ../feature-b -b feature-b

(cd ../feature-a && ./addons/gdtest/gdtest run tests/feature.test.gd) &
(cd ../feature-b && ./addons/gdtest/gdtest run tests/feature.test.gd) &
wait
```

The user daemon applies one global fair worker bound across every project and agent. Interactive work is not indefinitely starved by warmup/background requests. Idle or deleted-worktree sessions are reclaimed without terminating workers owned by another worktree.

Static analysis caches are shared only by path-neutral content identity. Dynamic manifests include tool, runtime, Godot, configuration, and content identities; absolute worktree paths never enter stable test IDs or reusable keys. A source change advances the session generation. Results that complete against an obsolete generation are marked stale and should not be treated as current.

Use `gdtest daemon sessions` to inspect worker ownership and generations. `gdtest daemon stop` stops only the compatible build namespace. Different development builds intentionally use different daemon namespaces.

For a disposable worktree, install only core plus the current host slice without changing the cross-platform lock:

```sh
gpm install --host-only
./addons/gdtest/gdtest run --affected path/to/changed.gd --verbose
```

If strict process isolation matters more than worker reuse, use `run --no-daemon`. This keeps orchestration in the foreground but still launches an authenticated project-local Godot worker.
