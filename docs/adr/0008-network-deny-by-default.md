# ADR-0008: Solver network access is denied

Status: Accepted
Date: 2026-10-02

## Context

Network access enables mining pools, C2, scanning and exfiltration.

## Decision

Every public solver runs with `--network none`; the solver manifest validator rejects any entrypoint or security block that does not deny network. `pumat doctor --image` checks isolation on the host.

## Alternatives considered

Egress allowlists (complex, leaky).

## Consequences

Solvers needing data must receive it in the input bundle.

## Security implications

Primary containment control together with ADR-0003.

## Revisit conditions

Governance approves a specific exception for a private federation.
