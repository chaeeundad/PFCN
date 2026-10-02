# ADR-0006: The explorer is non-authoritative

Status: Accepted
Date: 2026-10-02

## Context

A central database must not become the source of truth (P7).

## Decision

The explorer/indexer only indexes signed, content-addressed public records; deleting it loses nothing that cannot be rebuilt from records and announcements.

## Alternatives considered

Explorer as registry of record.

## Consequences

Implemented in Phase 4.

## Security implications

Indexers verify every signature and hash before indexing.

## Revisit conditions

—
