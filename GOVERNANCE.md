# Governance

## Now (spec §53.1)

The founding maintainers decide protocol releases, reference implementation
releases, the default public-namespace solver allowlist, default trust roots,
and security response.

## As adoption grows (§53.2)

- maintainer roles published in this file
- public proposal process (`docs/adr/` and PFCN proposals)
- multiple approvals for trust-root changes
- a solver review group and a security response group

## Avoiding founder lock-in (§53.3)

Bootstrap peers, solver trust roots, relays, explorer URLs and revocation
sources are configuration, not constants. A community fork can continue the
network with its own trust roots.
