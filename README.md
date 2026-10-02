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

- Specification: [docs/architecture.md](docs/architecture.md)
- Development log and decisions: [docs/DEVLOG.md](docs/DEVLOG.md), [docs/adr/](docs/adr/)

## Status

Pre-alpha. Phase 0 (protocol skeleton) and Phase 1 (two-peer execution) are
implemented. Two nodes run a Quantum ESPRESSO job end to end over libp2p:

- signed lease → encrypted input upload from the requester's disk
- sandboxed `pw.x` (rootless-capable container, no network, read-only root, CPU/memory/PID/walltime limits)
- result sealed to a per-execution key; worker plaintext destroyed at sealing
- requester pulls the sealed result (attached or detached), decrypts, parses locally (WASM parser), signs the receipt
- bilateral Compute Receipt and signed local event chains

DHT discovery (Phase 2), hardening (Phase 3) and public records (Phase 4) are next.

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

Machine B (worker, Linux with Docker or rootless Podman) and machine A
(requester, Linux or macOS).

1. On both machines: build and initialize.

   ```bash
   make build
   ./bin/pumat init
   ```

2. Make the solver image and signed manifest available on both machines.
   Until the project publishes them to GHCR (see docs/DEVLOG.md), build and sign
   your own:

   ```bash
   # once, on a trusted machine; keep the key offline
   ./bin/pumat solver keygen --out secrets/solver-signing.key
   # push solvers/quantum-espresso/Containerfile to a registry B can pull from,
   # put the platform manifest digest into a copy of manifest.dev.yaml.in, then:
   ./bin/pumat solver sign manifest.yaml --key secrets/solver-signing.key --out manifest.signed.json
   ```

   On both machines, add the printed signer ID to `trust.solverSigners` in
   `~/.pumat/config.yaml`, then install the manifest:

   ```bash
   ./bin/pumat solver add manifest.signed.json
   ```

3. On B, start the agent and accept work:

   ```bash
   ./bin/pumat agent &          # or a systemd unit
   ./bin/pumat on
   ./bin/pumat status           # copy one of the printed addresses
   ```

   B needs no inbound firewall changes on a LAN. Across networks it must be
   reachable on UDP/TCP 4001 until relay/hole punching lands in Phase 2.

4. On A, fetch the example pseudopotential, set `solver.manifestDigest` in
   `examples/qe-si-scf/job.yaml`, and submit:

   ```bash
   examples/qe-si-scf/fetch-pseudo.sh
   ./bin/pumat agent &
   ./bin/pumat submit examples/qe-si-scf/job.yaml --peer /ip4/<B-ip>/udp/4001/quic-v1/p2p/<B-peer-id>
   ```

   Use `--detach` to close the laptop after the upload; the agent fetches the
   sealed result when it comes back online (`pumat job list --pending`,
   `pumat job fetch <id>`).

5. Inspect provenance:

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
proto/                 protobuf wire framing
solvers/quantum-espresso  solver image and manifest templates
examples/              example jobs
docs/                  specification, ADRs, development log
```

## License

Not yet licensed. The spec recommends Apache-2.0 for code and CC BY 4.0 for
specifications (§54); a LICENSE file will be added after that decision.
