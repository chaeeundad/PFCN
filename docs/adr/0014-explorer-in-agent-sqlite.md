# ADR-0014: Explorer as an agent mode with SQLite

Status: Accepted
Date: 2026-10-02

## Context

§7.9 recommends a Next.js frontend, a PostgreSQL index and a Go indexer. The
explorer must stay disposable and cheap to run (ADR-0006), including on the
same small VM as a bootstrap node.

## Decision

The explorer is a mode of the Go agent (`indexer.enabled: true`). It receives
announcements over GossipSub and direct push, fetches and verifies records
(signature, every file digest, receipt chain), keeps a mirror copy, indexes
public records into SQLite, and serves server-rendered pages and a REST API.
HTTPS is terminated by a reverse proxy (Caddy on pfcn.pumat.org).

## Alternatives considered

A separate Next.js app plus PostgreSQL (more services to operate for no
functional gain at the current scale).

## Consequences

One binary, one process; an explorer runs on a 1 GB VM next to a bootstrap
node. Rebuilding re-indexes mirrored records (tested). If search needs grow,
a separate indexer can consume the same announcements.

## Security implications

Pages are server-rendered with a strict CSP (no scripts); downloads are
limited to files listed in a verified manifest; unlisted records are never
indexed.

## Revisit conditions

Index size or query load beyond what SQLite on one node handles comfortably.
