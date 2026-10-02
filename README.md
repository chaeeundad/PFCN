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

- Introduction slides (Korean): [chaeeundad.github.io/PFCN/slides](https://chaeeundad.github.io/PFCN/slides/) ([source](docs/slides/index.html))
- Specification: [docs/architecture.md](docs/architecture.md)
- Development log and decisions: [docs/DEVLOG.md](docs/DEVLOG.md), [docs/adr/](docs/adr/)

## Status

Pre-alpha. Phases 0-4 are implemented; Phase 5 (public alpha operations)
is in progress. Nodes run a Quantum ESPRESSO job end to end over libp2p:

- signed lease → encrypted input upload from the requester's disk
- sandboxed `pw.x` (rootless-capable container, no network, read-only root, CPU/memory/PID/walltime limits)
- result sealed to a per-execution key; worker plaintext destroyed at sealing
- requester pulls the sealed result (attached or detached), decrypts, parses locally (WASM parser), signs the receipt
- bilateral Compute Receipt and signed local event chains

- discovery without a central scheduler: DHT provider records, bootstrap peers, mDNS, hole punching/relay
- signed solver revocation lists; fuzzed protocol and input decoders

- public scientific records: `pumat job publish`, P2P record mirroring, GossipSub announcements
- independent reproduction on another worker with signed exact/tolerance comparison (`pumat reproduce`)
- a disposable explorer/indexer (`indexer.enabled: true`) with search, record pages and REST API

Not yet: workspace encryption at rest, Sigstore verification of solver
manifests, GPU, institutional federations (see docs/DEVLOG.md).

## Install

Once a release is tagged (see docs/DEVLOG.md):

```bash
curl -fsSL https://raw.githubusercontent.com/chaeeundad/PFCN/main/scripts/install.sh | sh
```

The installer verifies the release checksum and, if `cosign` is installed,
its Sigstore signature. It never turns resource sharing on.

## Build

Requires Go (version in `go.mod`).

```bash
make build        # bin/pumat
make test         # unit + integration tests (no container needed)
```

## Quick start: two nodes on one machine

Requires Docker or Podman in addition to Go. This builds the QE solver image,
pushes it to a local registry, signs a dev solver manifest, starts two agents
and runs the Silicon SCF example:

```bash
make e2e
```

## Two machines

The QE solver image is published to GHCR (`ghcr.io/chaeeundad/pumat-quantum-espresso`,
linux/amd64 and linux/arm64) and pinned by the signed manifest
`solvers/quantum-espresso/manifest.signed.json`. Nodes trust the project
solver signer by default.

Worker (Linux with Docker or rootless Podman):

```bash
make build
./bin/pumat init
./bin/pumat solver add solvers/quantum-espresso/manifest.signed.json
./bin/pumat agent &          # or a systemd unit
./bin/pumat on
./bin/pumat status           # shows addresses to give requesters
```

Requester (Linux or macOS; no container runtime needed):

```bash
make build
./bin/pumat init
./bin/pumat solver add solvers/quantum-espresso/manifest.signed.json
examples/qe-si-scf/fetch-pseudo.sh
./bin/pumat agent &
./bin/pumat submit examples/qe-si-scf/job.yaml
```

On the same LAN the worker is found automatically (mDNS). Across networks,
either add a bootstrap peer to `network.bootstrap` in `~/.pumat/config.yaml`
(DHT discovery), or point at the worker directly:

```bash
./bin/pumat submit examples/qe-si-scf/job.yaml --peer /ip4/<B-ip>/udp/4001/quic-v1/p2p/<B-peer-id>
```

Use `--detach` to close the laptop after the upload; the agent fetches the
sealed result when it is back online (`pumat job list --pending`,
`pumat job fetch <id>`).

Inspect provenance:

```bash
./bin/pumat receipt verify <receipt-or-exec-id>
./bin/pumat ledger verify
ls ~/pumat/results/<exec-id>/{input,output,parsed,provenance}
```

## Repository layout

```text
cmd/pumat              CLI and agent
cmd/pumat-qe-parser    QE parser, compiled to WASI and embedded in the agent
internal/agent         node daemon: lease, transfer, execution, fetch, local API
internal/{canonical,contentid,envelope,identity}  signing and identifiers
internal/{bundle,seal}                 chunked bundles and result sealing
internal/{job,solver,solver/qe}        job schema, solver trust, QE adapter
internal/{sandbox,store,protocol,pb}   container runtime, SQLite state, wire protocol
internal/{record,indexer}              public records, reproduction, explorer
proto/                 protobuf wire framing
deploy/                systemd unit, bootstrap/relay node setup
solvers/quantum-espresso  solver image and manifest templates
examples/              example jobs
docs/                  specification, ADRs, development log
```

## License

Not yet licensed. The spec recommends Apache-2.0 for code and CC BY 4.0 for
specifications (§54); a LICENSE file will be added after that decision.
