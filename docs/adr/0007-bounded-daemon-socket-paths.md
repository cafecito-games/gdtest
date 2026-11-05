# ADR 0007: Bound Unix daemon socket paths independently of cache paths

- Status: accepted
- Date: 2026-11-05

## Context

Daemon metadata, logs, and locks belong below the user cache directory. Placing the Unix-domain
socket there as well fails when a managed home or cache path approaches the platform socket-path
limit. macOS reported `connect: invalid argument` from an otherwise valid packaged `doctor` run
under an isolated temporary home.

## Decision

The namespace keeps metadata, logs, and locks below
`<user-cache>/gdtest/daemons/<protocol>/<build-id>`. A socket whose direct path is at most 90 bytes
stays beside that state. A longer path uses
`<temporary-directory>/gdt-<namespace-hash>/d.sock`, falling back to `/tmp` only when the platform
temporary path is itself too long.

The hash covers the canonical namespace root, including protocol and build identity. Preparation
rejects non-directory or symbolic-link leaves and enforces mode 0700 on both the namespace and
short socket directory. The socket remains mode 0600, metadata accepts only the deterministically
derived path, and shutdown removes the socket and its empty fallback directory.

## Consequences

Clients and their exact daemon build derive the same bounded rendezvous path without adding a new
protocol field or trusting an arbitrary metadata path. Permission-protected persistent state
remains in the user cache as specified; only the ephemeral socket inode may live in the temporary
directory. Focused race tests create an over-limit cache path, serve a real status request, verify
private permissions, and prove cleanup. Packaged `doctor` verification exercises the behavior from
the installed host executable on macOS and Linux arm64.
