# Pumat

Pumat is an open federated computing network for science.

Researchers and institutions can contribute idle CPU/GPU resources, while
scientific jobs are discovered and executed across the network using verified
solver artifacts. Public computations can be preserved with complete inputs,
raw outputs, parsed results, and cryptographic provenance.

No central scheduler is required. No cryptocurrency is required. Public worker
nodes do not execute arbitrary user code.

**Share idle compute. Run science. Share the results.**

> Pumat (품앗) comes from 품앗이, the Korean tradition of reciprocal work.
> The protocol is the Pumat Federated Computing Network (PFCN).

- Public explorer: [pfcn.pumat.org](https://pfcn.pumat.org)
- Quick start (Korean): [docs/QUICKSTART.md](docs/QUICKSTART.md)
- Introduction slides (Korean): [chaeeundad.github.io/PFCN/slides](https://chaeeundad.github.io/PFCN/slides/) ([source](docs/slides/index.html))
- Specification: [docs/architecture.md](docs/architecture.md)
- Development log and decisions: [docs/DEVLOG.md](docs/DEVLOG.md), [docs/adr/](docs/adr/)

## Status

Pre-alpha, latest release [`v0.1.0-alpha.3`](https://github.com/chaeeundad/PFCN/releases).
Verified across the internet: a requester on AWS Singapore found a worker
behind a home NAT in Korea through the public bootstrap, connected directly
by hole punching, and completed a Quantum ESPRESSO job in 12.8 s.

What works today:

- **Execution**: signed leases, encrypted input upload from the requester's
  disk, sandboxed `pw.x` (no network, read-only root, CPU/memory/PID/disk/walltime
  limits, never as root), results sealed to a per-execution key with worker
  plaintext destroyed at sealing, attached or detached delivery, cancellation
- **Provenance**: bilateral Compute Receipts, signed local event chains,
  requester-side WASM parser with a reproducible digest
- **Discovery**: no central scheduler: DHT provider records, public
  bootstrap/relay at `pfcn.pumat.org` (set via DNS `/dnsaddr`), mDNS on the
  LAN, hole punching with relay fallback for control messages
- **Trust and safety**: signed solver manifests and revocation lists, local
  reputation, caps for unknown requesters, fuzzed decoders, govulncheck in CI
- **Open science**: public records with offline verification, mirroring,
  independent reproduction with signed comparisons, and the public explorer
  at [pfcn.pumat.org](https://pfcn.pumat.org)

Not yet: workspace encryption at rest, Sigstore verification of solver
manifests, a second solver, a native Windows build, institutional
federations. Windows users run the Linux build in WSL2 (documented, not yet
verified on Windows hardware). GPU support is out of scope for now. See [docs/DEVLOG.md](docs/DEVLOG.md).

## Getting started

Full guide (Korean): [docs/QUICKSTART.md](docs/QUICKSTART.md).

Install a signed release (Linux or macOS, amd64/arm64; on Windows, inside
WSL2 Ubuntu as described in the quick start). The installer
verifies the checksum and, when `cosign` is installed, the Sigstore signature.
It never turns resource sharing on.

```bash
curl -fsSL https://raw.githubusercontent.com/chaeeundad/PFCN/main/scripts/install.sh | sh
```

Every node:

```bash
R=https://raw.githubusercontent.com/chaeeundad/PFCN/main
curl -fsSLO $R/solvers/quantum-espresso/manifest.signed.json
pumat init
pumat solver add manifest.signed.json
pumat agent &                # or the systemd unit in deploy/systemd
```

Contribute compute (Linux, or macOS with Docker Desktop/OrbStack; Docker or rootless Podman):

```bash
pumat doctor --image ghcr.io/chaeeundad/pumat-quantum-espresso@sha256:7ea3d7fb93f904b435071ceb6e3797a4cfc57ec0e848cf124ad43e6c28d18d54
pumat on
```

Run a calculation (no container runtime needed):

```bash
for f in job.yaml silicon.cif fetch-pseudo.sh; do curl -fsSLO $R/examples/qe-si-scf/$f; done
chmod +x fetch-pseudo.sh && ./fetch-pseudo.sh
pumat submit job.yaml        # add --detach to close the laptop after the upload
pumat receipt verify <exec-id>
pumat job publish <exec-id>  # public jobs only; appears on pfcn.pumat.org
```

Workers are found through the public bootstrap anywhere on the internet, or
by mDNS on the same LAN; NAT is handled by hole punching. To target a
specific worker, pass `--peer <multiaddr>/p2p/<peer-id>` from its `pumat status`.

## Development

Requires Go (version in `go.mod`).

```bash
make build        # bin/pumat
make test         # unit + integration tests (no container needed)
make race         # with the race detector
make e2e          # two local nodes, real QE container, local registry (Docker/Podman)
```

See [CONTRIBUTING.md](CONTRIBUTING.md). Run your own bootstrap/relay node
with [deploy/bootstrap](deploy/bootstrap/README.md).

## Repository layout

```text
cmd/pumat              CLI and agent
cmd/pumat-qe-parser    QE parser, compiled to WASI and embedded in the agent
internal/agent         node daemon: discovery, lease, transfer, execution, fetch, records, local API
pkg/{canonical,contentid}              RFC 8785 JSON and content identifiers
internal/{envelope,identity}           signed envelopes and node identity
internal/{bundle,seal}                 chunked bundles and result sealing
internal/{job,solver,solver/qe}        job schema, solver trust, QE adapter
internal/{sandbox,store,protocol,pb}   container runtime, SQLite state, wire protocol
internal/{record,indexer}              public records, reproduction, explorer
internal/{parser,qeparse}              WASI parser host and the QE output parser
internal/{config,capability}           node configuration, hardware detection
internal/integration                   multi-node scenario tests
proto/                 protobuf wire framing
scripts/               installer, local e2e, vulnerability check
deploy/                systemd unit, bootstrap/relay node setup
solvers/quantum-espresso  solver image and manifest templates
examples/              example jobs
docs/                  specification, ADRs, development log
```

## License

- Code: [Apache License 2.0](LICENSE)
- Specification and documentation (`docs/`): [CC BY 4.0](docs/LICENSE.md)
- Solver images contain third-party software under their own licenses (see [NOTICE](NOTICE)).
- Public scientific records carry the license chosen by their publisher.
