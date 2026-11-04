# ADR 0001: Version every process boundary from the first implementation

Status: accepted

The client/daemon and daemon/worker protocols use independent integer versions. The product
version is checked separately during the worker handshake. Test plans and events live in a
backend-neutral Go package and carry project-relative identifiers.

This keeps protocol compatibility, release compatibility, and future executor compatibility as
three explicit decisions rather than conflating them in one version string.
