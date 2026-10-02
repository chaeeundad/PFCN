# ADR-0004: Solver artifacts are OCI images pinned by digest

Status: Accepted
Date: 2026-10-02

## Context

Solver binaries need cheap, mirrorable, content-addressed distribution.

## Decision

Solvers are OCI images in existing registries. Jobs pin the signed **solver manifest digest**; the manifest lists one platform-specific image manifest digest per platform; the worker pulls `reference@digest` (the engine verifies content) and the receipt records the digest that ran. Images run with `--pull never` after the explicit digest pull.

## Alternatives considered

Tags (mutable), a project-run binary server (P7), IPFS for binaries (operational cost).

## Consequences

Registry outages are mitigated by mirrors serving the same digest.

## Security implications

Integrity comes from digest pinning inside a signed manifest; registry compromise cannot change what runs.

## Revisit conditions

Peer-assisted artifact distribution is needed at scale.
