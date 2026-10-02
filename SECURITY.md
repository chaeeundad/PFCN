# Security policy

Pumat runs scientific workloads on machines owned by volunteers. Security
reports are the highest priority (spec §63).

## Reporting a vulnerability

Please do **not** open a public issue. Use GitHub's private vulnerability
reporting for this repository (Security → Report a vulnerability). Include
affected version/commit, impact, and reproduction steps. We aim to
acknowledge within 3 days and to agree on a disclosure date with you.

## Scope

In scope: sandbox escapes, ways to express a command or host access through a
job, solver/parser trust bypasses, signature or receipt forgery, lease
replay, protocol decoder crashes, data retained on workers after sealing,
installer/update verification bypasses.

## Current limitations (by design, spec §37.3)

- A compromised worker can read job data while it executes.
- A malicious worker can fabricate plausible results; receipts prove
  agreement, not honest computation. Reproduction (§26) is the mitigation.
- No anonymity against global network observers.
- Workspace encryption at rest is not implemented yet; nodes advertise
  `workspace_encryption: false`.
- GPU jobs are not supported.

## Accepted dependency risks

CI runs govulncheck (`scripts/vulncheck.sh`) and fails on reachable
vulnerabilities not listed with a justification in `.govulncheck-accepted`.
Currently accepted: GO-2024-3218 (Kademlia DHT provider censorship), which
affects only discovery availability; capability checks, mDNS, `--peer` and
bootstrap peers remain.

## Trust roots

The project solver signing key (signer ID
`12D3KooWNmqy7RJXuA8VKQDgfcWhhAtiVPpwXGRQA3K2VvaDRKeP`) approves solver
manifests for the public namespace (ADR-0009). Release archives are signed
keylessly by the `release.yml` workflow of this repository via Sigstore.
