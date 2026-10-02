# ADR-0012: Wire framing and local API

Status: Accepted
Date: 2026-10-02

## Context

Signed objects must be language-independent; the CLI must control a long-running daemon.

## Decision

Wire: length-delimited protobuf `Frame` messages (one oneof) per libp2p stream; signed objects travel as `SignedEnvelope{schema, payload(JCS bytes), signatures}` and are never re-serialized. Local API: HTTP/JSON over a Unix socket (mode 0660) in the Pumat home; submission streams NDJSON progress.

## Alternatives considered

gRPC (heavier, no benefit on libp2p streams), signing protobuf bytes (non-deterministic across languages).

## Consequences

`make proto` regenerates internal/pb from proto/pumat.proto.

## Security implications

Only users who can open the socket can control the node.

## Revisit conditions

—
