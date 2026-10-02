# ADR-0009: Phase 1 solver trust uses Pumat-signed manifests

Status: Accepted
Date: 2026-10-02

## Context

§7.6 calls for Sigstore/Cosign. Offline Sigstore bundle verification needs a pinned TUF trusted root and CI OIDC identities that do not exist yet.

## Decision

The solver manifest is a §7.4.1 envelope signed by an Ed25519 **solver signing key**; nodes trust signer IDs listed in `trust.solverSigners` (default: the project key `12D3KooWNmqy7RJXuA8VKQDgfcWhhAtiVPpwXGRQA3K2VvaDRKeP`). Image integrity follows from the digest pinned inside the signed manifest. An empty trust list trusts nothing.

## Alternatives considered

Wait for Sigstore before shipping Phase 1.

## Consequences

The project signing key (secrets/solver-signing.key, never committed) is a root of trust and must be backed up offline.

## Security implications

Compromise of the signing key lets an attacker approve arbitrary images; revocation lists (Phase 3) and multi-signer thresholds (§13.2 future) mitigate.

## Revisit conditions

Phase 3: add Sigstore bundle verification as an additional required check for the public namespace.
