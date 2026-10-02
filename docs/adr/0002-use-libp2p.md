# ADR-0002: Use libp2p

Status: Accepted
Date: 2026-10-02

## Context

Peers need authenticated identity, NAT traversal, and later a DHT, without a central server.

## Decision

Use go-libp2p. Peer IDs are derived from the node's Ed25519 key. Phase 1 uses default transports (QUIC, TCP) and security (Noise/TLS) with explicit `--peer` multiaddrs; Kad-DHT, AutoNAT, DCUtR and Circuit Relay v2 come in Phase 2.

## Alternatives considered

Custom QUIC + TLS (reimplements discovery and NAT traversal), central broker (violates P7).

## Consequences

Protocol handlers are libp2p stream protocols: /pumat/{capability,lease,input,result}/1.0.0.

## Security implications

Stream authorization uses the libp2p-authenticated remote peer ID; only the lease requester may upload input or fetch results.

## Revisit conditions

libp2p cannot reach typical university/home networks reliably (§62.1).
