# ADR-0001: Use Go for the agent

Status: Accepted
Date: 2026-10-02

## Context

The agent must ship as a single static binary for heterogeneous Linux/macOS hosts, run as a daemon, and speak libp2p.

## Decision

Write the reference agent in Go. Keep it cgo-free (pure-Go SQLite via modernc.org/sqlite, embedded wazero for WASM).

## Alternatives considered

Rust (smaller attack surface, slower to build the MVP), Python (packaging and daemon distribution are hard).

## Consequences

`CGO_ENABLED=0` cross-compiles for linux/{amd64,arm64} and darwin; CI checks it.

## Security implications

Go's memory safety covers the protocol decoders; fuzzing is still required (Phase 3).

## Revisit conditions

A second implementation needs capabilities Go cannot provide well.
