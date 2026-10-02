# ADR-0007: Public records are content-addressed

Status: Accepted
Date: 2026-10-02

## Context

Records must be citable, mirrorable and verifiable by anyone.

## Decision

`record_id = pumat:record:blake3:<blake3 of the signed publication manifest payload>`; the record bundle layout is §22.2. The requester's result directory already uses this layout.

## Alternatives considered

Sequential IDs from a central service.

## Consequences

Implemented in Phase 4.

## Security implications

Record integrity is independent of the host serving it.

## Revisit conditions

—
