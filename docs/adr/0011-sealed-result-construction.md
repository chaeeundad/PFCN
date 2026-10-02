# ADR-0011: Result sealing construction

Status: Accepted
Date: 2026-10-02

## Context

Results must be retained for offline requesters without the worker being able to read them, and fetched chunk-by-chunk in any order.

## Decision

Per execution, the requester generates an X25519 key and puts its public half in the lease. The worker runs HPKE (RFC 9180) base mode with DHKEM(X25519)/HKDF-SHA256 in **export-only** mode, `info = "pumat-result-v1\0" || exec_id`, exports a 32-byte key, and seals each 1 MiB chunk with ChaCha20-Poly1305 using nonce = chunk index and AAD = info || index. The encapsulated key is served with the status. Plaintext workspace, input and output bundles are deleted as soon as the result is sealed.

## Alternatives considered

HPKE's stateful Seal (forces in-order decryption), age/NaCl box per chunk (no standard KDF binding to the execution).

## Consequences

Uses Go 1.26+ crypto/hpke and x/crypto/chacha20poly1305.

## Security implications

A compromised worker can still read data during execution (§37.3). Swapped or reordered chunks fail authentication.

## Revisit conditions

Post-quantum KEM (crypto/hpke supports ML-KEM hybrids) once peers can negotiate it.
