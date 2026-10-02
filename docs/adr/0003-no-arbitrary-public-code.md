# ADR-0003: No arbitrary code on public workers

Status: Accepted
Date: 2026-10-02

## Context

Generic remote code execution would turn contributor machines into a botnet substrate.

## Decision

Jobs are declarative. The YAML parser rejects command-like fields (§12.3), anchors, custom tags and unknown fields. Solver parameters pass through an allowlist; paths and restart settings are fixed by the adapter (§39.1). The launch command lives inside the signed image (`entry.sh`), never in the job.

## Alternatives considered

Signed user containers (still arbitrary code), WASM user code (scientific solvers need native MPI/BLAS).

## Consequences

Adding a solver feature means extending the adapter allowlist and its tests.

## Security implications

Phase 3 exit criterion: no job spec can express a shell command; covered by tests in internal/solver/qe.

## Revisit conditions

Never for the public network; private federations may define their own policy.
