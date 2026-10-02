# ADR-0010: Parsers run on the requester as WASI modules

Status: Accepted
Date: 2026-10-02

## Context

The parsed result must be reproducible from raw output by anyone, and workers should run as little code as possible.

## Decision

Parsers are WASI (`wasip1`) modules built reproducibly (`-trimpath -buildid=`) and run with wazero: output dir mounted read-only at /in, no network, fake clock, 256 MiB memory, 2 min timeout. The solver manifest pins the parser digest. Phase 1 embeds the QE parser in the agent and refuses unknown parser digests.

## Alternatives considered

Worker-side parsing (more trusted code on workers), native plugins (not sandboxed, not portable).

## Consequences

CI rebuilds the parser and fails if its digest changes. Changing parser code requires a new solver manifest.

## Security implications

A malicious parser can only produce wrong JSON; raw output stays the evidence.

## Revisit conditions

Registry-fetched parser artifacts (Phase 4).
