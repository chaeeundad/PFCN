# ADR-0013: DNS-based bootstrap set and relay policy

Status: Accepted
Date: 2026-10-02

## Context

New nodes need at least one reachable peer to join the DHT, and nodes behind
NAT need a relay to be dialed at all. Hardcoding a server IP in the release
breaks every installed node when the server moves, and adding servers would
require a release. Circuit Relay v2 connections are limited in duration and
bytes, and go-libp2p refuses streams over them by default.

## Decision

- Default bootstrap list: `/dnsaddr/pfcn.pumat.org` (expanded at startup from
  `_dnsaddr.pfcn.pumat.org` TXT records), then `/dns4/pfcn.pumat.org/...`,
  then the IPv4 address as a last resort. The same list is the default static
  relay set. Failed DNS lookups are skipped.
- Cloud bootstrap/relay nodes set `network.announce` (public addresses behind
  1:1 NAT) and `network.reachability: public` (the relay service otherwise
  waits for AutoNAT confirmation, which rarely arrives on a small network).
- Only control messages (capability, lease, announcements) may travel over a
  relayed connection. Data streams wait up to 20 s for hole punching (DCUtR)
  to yield a direct connection and otherwise fail with an explicit error.

## Alternatives considered

A signed bootstrap list served over HTTPS (more moving parts; bootstrap peers
are trusted for nothing but connectivity, so unsigned DNS is acceptable).
Bulk transfer over relays (unbounded cost for relay operators, contradicts §9.4).

## Consequences

Servers are added or replaced by editing DNS. A node whose DNS is broken still
reaches the last-resort IPv4 address. Two peers that are both behind NATs that
defeat hole punching cannot exchange data; one of them must open a port.

## Security implications

DNS can only affect availability (eclipse at join time), not integrity:
capabilities, leases, receipts and records are all signed.

## Revisit conditions

Many volunteer bootstrap operators (consider a signed list), or measured
hole-punching failure rates high enough to justify bounded relayed transfers.
