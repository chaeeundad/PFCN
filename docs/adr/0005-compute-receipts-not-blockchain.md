# ADR-0005: Compute Receipts, not blockchain

Status: Accepted
Date: 2026-10-02

## Context

The network needs tamper evidence and bilateral attestation, not global financial consensus.

## Decision

A receipt is two signed statements: worker completion (at sealing) and requester acceptance (after decrypt/verify/parse), bound by `receipt_id = blake3(completion payload)`. Each node keeps a signed, hash-linked local event chain in SQLite.

## Alternatives considered

Blockchain anchoring (cost, dependency, no concrete consensus problem yet).

## Consequences

`pumat receipt verify` checks lease ↔ completion ↔ acceptance offline; `pumat ledger verify` checks the chain.

## Security implications

A receipt proves agreement, not honest computation (§20.1).

## Revisit conditions

A concrete consensus problem appears (e.g. global contribution accounting).
