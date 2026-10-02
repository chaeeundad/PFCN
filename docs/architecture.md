# Pumat Federated Computing Network (PFCN)

> **Detailed Product, Protocol, Security, and Implementation Specification**  
> Status: Draft v0.2  
> Date: 2026-10-02  
> Intended audience: coding agents, system architects, open-source contributors, scientific-computing researchers

### Changes in v0.2

- Unified the Job, Solver Manifest, and Compute Receipt schemas; appendices now point to the normative examples in the main sections (§12.2, §13.1, §20.2).
- Defined one identifier format (§12.5) and removed the `exec_id` ↔ `lease_id` circular dependency (§12.4).
- Defined the signed-object encoding: sign exact canonical bytes (RFC 8785 JCS) with domain separation; hashes are computed over typed JSON objects, not string concatenation (§7.4, §7.5).
- Jobs reference a **solver manifest digest**; the executed platform-specific OCI digest is recorded in the receipt (§7.7, §12.2, §13.1).
- Parsing moved to the requester side (WASM parser artifact); workers only execute the solver (§23).
- Added sealed results and asynchronous delivery: detached submission, worker-held ciphertext, requester pull, optional result custodian (§16.6–§16.11, §15).
- Plaintext workspace is destroyed when the result is sealed, not when the requester acknowledges (§28).
- Clarified the capability advertisement transport, the job offer channel, the publication announcement channel, clock-skew tolerance, offline Sigstore verification, and revocation distribution.
- Added QE adapter rules (MPI launch, forbidden path parameters, BLAS policy) and rootless runtime requirements.
- Moved minimum security controls into Phase 1.
- Renamed the project from Pumasi to **Pumat** (품앗), covering identifiers, schemas, topics, domains, and the CLI. The PFCN acronym is unchanged.

---

## 0. Executive Summary

**Pumat Federated Computing Network (PFCN)** is an open, federated, peer-to-peer scientific computing network inspired by the Korean concept of **품앗이**: participants contribute idle computing resources to a shared scientific infrastructure and may use the network's available resources for scientific computation. The name **Pumat** romanizes 품앗, the stem of 품앗이.

The project is intentionally designed **not** as a conventional cloud service, centralized HPC scheduler, or cryptocurrency-backed compute marketplace. Its first objective is to create a **low-cost, globally distributed, open scientific computing commons** with the following properties:

1. Any compatible machine can join with minimal friction.
2. A contributor can toggle availability on/off at any time.
3. Jobs are discovered and matched through a federated/P2P network rather than a single central scheduler.
4. Arbitrary user code is **not allowed by default**.
5. Only approved, cryptographically verified scientific solver artifacts may execute.
6. Inputs and outputs are transferred peer-to-peer and are not retained on the compute host after completion.
7. Execution provenance is represented by signed **Compute Receipts**, not by a centralized authoritative job database.
8. Public jobs may publish input, raw output, parsed results, provenance, and verification metadata as permanent scientific records.
9. The public result layer should become an open scientific data corpus generated organically by network activity.
10. The system should minimize infrastructure operated by the founding organization and remain useful even if the founding organization stops operating public services.

The first implementation target is deliberately narrow:

- Linux worker nodes first.
- Quantum ESPRESSO as the first supported scientific solver.
- CPU execution first; GPU support added immediately after protocol stabilization.
- One P2P overlay based on libp2p.
- Signed solver artifacts distributed through existing OCI registries.
- Direct encrypted peer-to-peer transfer when possible, relay fallback when necessary.
- Workload isolation using containers initially, with a path toward stronger microVM isolation.
- Signed append-only local event chains and bilateral Compute Receipts.
- A lightweight public explorer/index that is **non-authoritative and reconstructible**.

The project should optimize for **scientific usefulness, security, reproducibility, openness, low operational cost, and graceful decentralization** rather than feature breadth.

---

# 1. Project Naming

## 1.1 Recommended name

### **Pumat Federated Computing Network**

Short forms:

- **Pumat** — project/CLI name
- **PFCN** — formal protocol/infrastructure acronym
- `pumat` — executable name
- `pumat://` — possible future URI scheme

Rationale:

- Preserves the philosophical origin of 품앗이: reciprocal contribution without requiring monetary settlement.
- Distinguishes the project from a specific existing application or commercial service.
- “Federated Computing Network” clearly describes the infrastructure category.
- "Pumat" romanizes 품앗, the stem of 품앗이. It is short, has one obvious pronunciation, makes a clean executable name (`pumat`), and is distinctive enough for open-source branding.

## 1.2 Alternative names

1. **Pumat Research Compute Network (PRCN)**  
   Better if the project wants to remain strictly academic.

2. **Pumat Open Compute Network (POCN)**  
   Broader and more ideological, but “open compute” is already used in other contexts.

3. **Pumat Scientific Compute Federation (PSCF)**  
   More institutional and formal.

4. **Pumat Compute Commons (PCC)**  
   Strong philosophical identity, but less explicit about federation/networking.

## 1.3 Naming recommendation

Use:

> **Pumat Federated Computing Network**  
> *An open scientific computing commons.*

Suggested external one-line message:

> **Share idle compute. Run science. Share the results.**

Do not lead with blockchain, tokens, crypto, AI, marketplace, or cloud terminology.

---

# 2. Project Constitution

This section is normative. Implementation choices should be judged against these principles.

## 2.1 Why Pumat exists

Scientific computing capacity is fragmented across universities, laboratories, institutions, companies, and individuals. Large amounts of CPU and GPU capacity remain idle while other researchers wait for access to compute resources.

Pumat exists to make idle scientific computing capacity discoverable and usable without requiring a centralized cloud provider or centralized ownership of hardware.

## 2.2 Core principles

### P1. Participation is voluntary

Resource owners can enable or disable participation at any time.

### P2. Local control is absolute

The machine owner determines:

- whether the node participates,
- when it participates,
- which resources are offered,
- which trust domains are accepted,
- which solvers may run,
- maximum wall time,
- CPU/GPU/memory/storage limits,
- whether public workloads are accepted.

### P3. No arbitrary remote code by default

The global public network must not behave as a generic remote code execution platform.

Only approved, signed solver artifacts and approved execution entrypoints may run on public worker nodes.

### P4. Data minimization

A worker should receive only the minimum data required for a job and retain no job data after successful completion and acknowledgment.

### P5. Cryptographic provenance

Execution claims should be independently verifiable through signed receipts and hashes rather than depending solely on a central database.

### P6. Reproducibility is a first-class feature

A public computation should be reproducible from its published scientific inputs, solver artifact identity, parser identity, and execution metadata.

### P7. Central infrastructure is optional, not authoritative

Bootstrap servers, public explorers, mirrors, and relay nodes may exist for usability, but the protocol must not rely on a single database as the source of truth.

### P8. Work-conserving fairness

Unused resources should not remain idle merely to preserve quotas. Fairness matters when resources are contested.

### P9. Scientific commons before marketplace

The initial network should not require cryptocurrency, cash settlement, or transferable compute tokens.

### P10. Open protocol, plural implementations

The protocol should be documented independently from the reference implementation.

---

# 3. Goals and Non-Goals

## 3.1 Primary goals

- Install a worker/client with one simple command.
- Support daemon/service mode on servers and tray/menu-bar mode on desktops.
- Discover peers through a decentralized overlay.
- Advertise ephemeral resource availability.
- Discover jobs compatible with local hardware and local policy.
- Execute only cryptographically approved solver artifacts.
- Transfer job data securely between requester and worker.
- Delete worker-side job state after successful result acknowledgment.
- Generate mutually signed Compute Receipts.
- Publish public job results as accessible, structured scientific records.
- Allow the public explorer/index to be rebuilt from public records.
- Scale from tens of nodes to thousands without architectural replacement.

## 3.2 Secondary goals

- Support institutional trust domains.
- Support private federations using the same agent.
- Support fair-share scheduling without monetary settlement.
- Support solver mirrors and eventual peer-assisted artifact distribution.
- Support verification/re-execution of published jobs on independent nodes.
- Support contribution and reliability reputation.

## 3.3 Explicit non-goals for v1

- Generic arbitrary Docker execution.
- Shell access to worker nodes.
- Interactive SSH jobs.
- Cryptocurrency/token issuance.
- Financial settlement between participants.
- Guaranteed SLA.
- Running confidential corporate workloads on unknown public nodes.
- Full Windows worker execution in the first milestone.
- Strong proof that a malicious worker actually executed every CPU/GPU operation claimed.
- Replacement of Slurm/PBS/Kubernetes inside institutional clusters.

---

# 4. Personas

## 4.1 Resource Contributor

Owns or administers a workstation/server with idle compute capacity.

Needs:

- one-command installation,
- simple on/off control,
- strict resource limits,
- clear security guarantees,
- visibility into what has run,
- ability to restrict trust domain and solver list,
- zero requirement for inbound firewall configuration where possible.

## 4.2 Researcher / Job Requester

Wants to run supported scientific workloads.

Needs:

- simple job specification,
- automatic node discovery,
- result retrieval,
- reproducibility metadata,
- reliable failure handling,
- public/private publication choice,
- no need to understand peer topology.

## 4.3 Solver Maintainer

Maintains an approved scientific solver package.

Needs:

- reproducible build process,
- artifact signing,
- versioned manifests,
- CI tests,
- security scanning,
- deprecation mechanism.

## 4.4 Federation Operator

Runs bootstrap/relay/index services or a private federation.

Needs:

- minimal infrastructure,
- observability,
- abuse controls,
- policy configuration,
- no possession of scientific job payloads unless explicitly configured.

## 4.5 Data Consumer

Does not necessarily execute jobs. Searches public scientific results.

Needs:

- stable URLs/IDs,
- structured APIs,
- downloadable raw input/output,
- provenance,
- parser version,
- citation metadata.

---

# 5. User Experience

## 5.1 Installation

Linux/macOS target UX:

```bash
curl -fsSL https://install.pumat.org | sh
```

Windows target UX:

```powershell
irm https://install.pumat.org/install.ps1 | iex
```

Security note: documentation must also provide a safer explicit download + checksum + signature verification installation path. `curl | sh` is convenience UX, not the only supported installation method.

## 5.2 First run

```bash
pumat init
```

Expected behavior:

1. Generate local Ed25519 identity keypair.
2. Store private key using OS-appropriate secure storage/permissions.
3. Generate Peer ID.
4. Detect hardware.
5. Detect available container/runtime capabilities.
6. Prompt for default contribution limits.
7. Join default public network namespace.
8. Persist config.

Example output:

```text
Pumat node initialized

Peer ID: 12D3KooW...
CPU: AMD Ryzen 9 9950X / 16 cores
GPU: NVIDIA RTX 5090 / 32 GB
RAM: 128 GB
Runtime: containerd available

Default contribution:
CPU: 8 cores
GPU: 0
RAM: 32 GB
Status: paused

Run `pumat on` to begin accepting work.
```

## 5.3 Server controls

```bash
pumat status
pumat on
pumat off
pumat pause --after-current-job
pumat resources set --cpu 16 --memory 64G --gpu 1
pumat trust show
pumat jobs recent
pumat receipts list
```

System service:

```bash
systemctl status pumat-agent
systemctl start pumat-agent
systemctl stop pumat-agent
```

`systemctl stop` must prevent new work immediately and gracefully handle the current job according to configured shutdown policy.

## 5.4 Desktop controls

Tray/menu-bar states:

- Gray: Paused
- Green: Available
- Blue: Running job
- Yellow: Draining
- Red: Error / policy violation / runtime unavailable

Menu:

```text
Pumat
● Available
○ Pause
○ Pause after current job
────────────
Current job: QE SCF / 17 min
CPU: 8 / 16 shared
GPU: none
────────────
Recent contributions
Settings
Open Explorer
Quit
```

## 5.5 Job submission

Example:

```bash
pumat submit si-scf.yaml
```

Output:

```text
Validating job specification... OK
Solver: quantum-espresso 7.4.1
Solver manifest: sha256:...
Visibility: public
Calculation: pumat:calc:blake3:...

Discovering compatible workers...
12 compatible advertisements found
4 currently eligible

Lease accepted by peer 12D3...A92F
Execution: pumat:exec:blake3:...
Uploading encrypted input from local disk... done
Running...
Result sealed by worker.
Downloading sealed result... done
Result decrypted and hash verified.
Parsed locally with pumat-qe-parser 0.1.0.
Receipt acknowledged.

Receipt: pumat:receipt:blake3:...
Public record: https://explorer.pumat.org/record/pumat:record:blake3:...
```

Detached submission (the laptop may close after the input upload finishes):

```bash
pumat submit si-scf.yaml --detach
```

```text
Lease accepted by peer 12D3...A92F
Execution: pumat:exec:blake3:...
Uploading encrypted input from local disk... done
Detached. The worker keeps the sealed result for up to 24h.
The local agent fetches it automatically when it is online,
or run: pumat job fetch pumat:exec:blake3:...
```

The CLI is a thin client. Submission state, transfers, and result fetching are owned by the local `pumat` agent daemon (§32.0), so the requester's machine needs the agent running, in requester-only mode if it does not contribute resources. See §16.7–§16.11 for asynchronous delivery.

---

# 6. High-Level Architecture

```text
                                +---------------------+
                                | Optional Public     |
                                | Explorer / Index    |
                                +----------+----------+
                                           ^
                                           | public metadata only
                                           |
+-------------+       P2P overlay       +--+----------+
| Requester A |<----------------------->| Worker B    |
|             |                         |             |
| Job submit  |                         | Executor    |
| Input owner |                         | Sandbox     |
+------+------+                         +------+------+ 
       |                                       |
       |       +-----------------------+       |
       +------>| DHT / PubSub / Relay |<------+
               +-----------------------+
                  ^        ^       ^
                  |        |       |
             Worker C  Worker D  Requester E

External non-authoritative infrastructure:
- OCI registries for solver artifacts
- public bootstrap peers
- optional circuit relays
- optional explorer/indexers
- optional public data mirrors
```

Architectural rule:

> No single service should simultaneously be required for identity, scheduling, execution, data transfer, receipts, and public result persistence.

---

# 7. Recommended Reference Implementation Stack

## 7.1 Agent language: Go

**Recommendation: Go for the Pumat agent.**

Reasons:

- Excellent static binary distribution.
- Strong cross-platform support.
- Mature go-libp2p ecosystem.
- Straightforward system daemon implementation.
- Low runtime footprint.
- Easy concurrency model for peer networking.
- Easier `curl | sh` style installation than Python runtime dependency management.

Rust is a valid alternative but increases initial implementation complexity. Python is not recommended for the core agent because packaging and daemon/runtime distribution become harder across heterogeneous nodes.

## 7.2 P2P networking

Use **libp2p**.

Required capabilities:

- Peer identity.
- Kademlia DHT.
- Identify protocol.
- AutoNAT.
- Circuit Relay v2.
- DCUtR/hole punching.
- GossipSub only where appropriate.
- QUIC preferred transport where available.
- TLS/Noise secure channels provided by libp2p.

Current libp2p documentation describes Kad-DHT as the distributed routing layer for peer and content discovery and provides Circuit Relay and NAT hole punching for non-public nodes.

## 7.3 Local storage

Use SQLite for durable local metadata:

- local identity metadata,
- job state,
- receipt state,
- peer reputation cache,
- local event chain,
- artifact cache metadata,
- publication queue.

Actual blobs should live in a content-addressed filesystem store rather than SQLite.

Use a pure-Go SQLite driver (e.g. `modernc.org/sqlite`) so the agent stays a static, cgo-free binary.

Suggested layout:

```text
/var/lib/pumat/
├── identity/
├── db/pumat.sqlite
├── blobs/
│   └── blake3/...
├── solver-cache/
├── receipts/
├── work/
└── logs/
```

## 7.4 Serialization

Three encodings, each with one job:

| Encoding | Used for |
|---|---|
| Protocol Buffers | wire framing of network messages |
| Canonical JSON (RFC 8785 JCS) | every **signed** or **hashed** object: capability, lease, receipt statements, solver manifest, revocation list, publication manifest, canonical job |
| YAML | human authoring only (job files, solver manifest sources, local config) |

Protobuf serialization is not guaranteed to be byte-identical across languages or library versions, so it must never be the input to a signature or content hash. P10 (plural implementations) depends on this.

### 7.4.1 Signed envelope

A signed object travels as exact bytes, never re-serialized:

```protobuf
message SignedEnvelope {
  string schema        = 1;  // e.g. "pumat.lease.v1"
  bytes  payload       = 2;  // RFC 8785 canonical JSON bytes
  repeated Signature signatures = 3;
}

message Signature {
  string signer_peer_id = 1;
  string alg            = 2;  // "ed25519"
  bytes  sig            = 3;
}
```

Signature input (domain separated):

```text
"pumat-sig-v1" || 0x00 || schema || 0x00 || payload
```

Rules:

- The verifier checks that `payload` is already canonical (re-canonicalize and compare bytes); non-canonical payloads are rejected.
- Signed payloads must not contain floating-point numbers. Use integers in base units (bytes, seconds, millicores) or decimal strings.
- Timestamps are RFC 3339 UTC strings with `Z` and second precision.
- The `schema` inside the payload must equal the envelope `schema`.

### 7.4.2 Job YAML canonicalization

1. Parse YAML strictly: reject anchors, aliases, merge keys, custom tags, duplicate keys, and unknown fields.
2. Decode into the typed job struct for the declared `kind`.
3. Apply defaults and normalize units (`32GiB` → `34359738368` bytes, `2h` → `7200` seconds).
4. Replace local file paths with `{filename, digest, size}` entries for the input bundle.
5. Emit RFC 8785 canonical JSON. This is the **canonical job**, stored as `input/canonical-job.json` in the input bundle.

## 7.5 Hashes

Recommended:

- SHA-256 where OCI compatibility requires SHA-256.
- BLAKE3 for internal high-throughput content IDs where protocol design permits.

Do not mix hashes implicitly. Every identifier must include algorithm namespace.

Examples:

```text
sha256:abcd...
blake3:abcd...
```

Digests are lowercase hex. BLAKE3 output is 256 bits.

Derived identifiers are never built by concatenating strings (`hash(a + b + c)` is ambiguous). They are computed over a typed canonical JSON object:

```text
derive(type, fields) = blake3( JCS({"type": type, ...fields}) )
```

Example: `calc_id = derive("pumat.calc.v1", {...})`. See §12.4.

## 7.6 Signatures

- Node identity: Ed25519.
- Solver artifacts: Sigstore/Cosign compatible signing.
- Compute Receipts: Ed25519 node signatures (§7.4.1 envelope).
- Result sealing: HPKE (RFC 9180), X25519 + HKDF-SHA256 + ChaCha20-Poly1305 (§16.6).

Sigstore verification must work offline:

- Solver releases ship a Sigstore **bundle** (signature, certificate, Rekor inclusion proof) next to the artifact.
- The agent verifies the bundle against a **pinned trusted root** shipped in the agent release and refreshed via TUF when online.
- Verification must not require live access to Fulcio or Rekor (P7).
- The allowed signer identities (OIDC issuer + subject, e.g. the solver CI workflow) are part of the namespace solver trust policy.

## 7.7 Solver distribution

Use OCI artifacts/images distributed from existing public OCI registries.

Initial registry:

- GitHub Container Registry (GHCR) is acceptable for MVP.

Protocol must reference artifacts by immutable digest, not mutable tag.

Example:

```text
ghcr.io/pumat-solvers/quantum-espresso@sha256:...
```

ORAS/OCI supports arbitrary content-addressed artifacts identified by digest. This allows future mirrors without changing solver identity.

Digest levels:

| Digest | Meaning | Where it appears |
|---|---|---|
| solver manifest digest | `sha256` of the signed Pumat solver manifest (JCS bytes) | job spec, `calc_id`, receipt |
| platform artifact digest | OCI image manifest digest for one platform (e.g. `linux/amd64`) | solver manifest `artifacts[]`, lease, receipt |
| OCI index digest | multi-platform index, optional convenience | solver manifest `index` |

Requesters do not know a worker's platform in advance, so jobs pin the **solver manifest digest**. The worker resolves its platform artifact digest from that manifest, and the receipt records the digest that actually ran.

## 7.8 Execution runtime

Phase 1:

- rootless Podman or containerd where available,
- strict seccomp/AppArmor/SELinux profile,
- read-only solver filesystem,
- ephemeral writable workspace,
- no network by default.

Phase 2:

- Firecracker on supported Linux/KVM hosts,
- or Kata Containers where GPU/container integration is easier.

Firecracker uses hardware virtualization plus the `jailer` process as an additional isolation layer and is appropriate for stronger isolation on Linux hosts.

## 7.9 Public explorer

Recommended stack:

- Next.js or equivalent static-capable frontend.
- PostgreSQL for disposable/rebuildable indexing.
- Object storage only as optional mirror/cache.
- Indexer written in Go.

Important:

> The explorer database is an index, not the authoritative ledger.

---

# 8. Identity Model

## 8.1 Node identity

On first initialization:

```text
Ed25519 keypair
      |
      +--> Peer ID
      +--> receipt signing
      +--> capability advertisement signing
```

The private key never leaves the node.

## 8.2 Human identity

Human identity is optional in the public network.

Possible layers:

```text
Level 0: pseudonymous node
Level 1: email verified
Level 2: GitHub verified
Level 3: ORCID verified
Level 4: institution verified
Level 5: federation-specific trusted identity
```

These are attestations, not replacements for node cryptographic identity.

## 8.3 Institution identity

Future design:

- institutional signing authority,
- domain-based verification,
- SAML/OIDC federation,
- signed statement binding a node public key to an institution.

Example attestation:

```json
{
  "type": "InstitutionNodeAttestation",
  "institution": "example.edu",
  "peer_id": "12D3KooW...",
  "valid_from": "...",
  "valid_until": "...",
  "signature": "..."
}
```

---

# 9. Network Topology

## 9.1 Bootstrap

The reference project operates several geographically distributed bootstrap peers.

Bootstrap peers:

- do not schedule jobs,
- do not see job inputs,
- do not authorize execution,
- only help new nodes enter the overlay.

Bootstrap peer addresses are included in the client release but configurable.

Community-operated bootstrap peers must be supported.

## 9.2 DHT

Use Kademlia DHT for:

- peer routing,
- provider records,
- capability index hints,
- public object provider discovery.

Do **not** store large job specifications or scientific datasets directly in the DHT.

DHT records must be:

- small,
- signed,
- TTL-limited,
- versioned,
- resistant to replay.

## 9.3 NAT traversal

Order:

1. direct QUIC/TCP connection,
2. hole punching/DCUtR,
3. Circuit Relay v2 fallback.

The agent should avoid requiring inbound firewall rules for normal participation.

## 9.4 Relay economics/limits

Relay operators should be able to configure:

- max reservations,
- bytes per reservation,
- max duration,
- max concurrent streams,
- allow/deny network namespaces.

Large scientific input/output transfer should prefer direct connections. Relays should not become permanent bulk-data transit when direct connectivity is possible.

---

# 10. Network Namespaces / Federations

The same agent may join multiple logical networks.

Examples:

```text
/pumat/public/v1
/pumat/academic/v1
/pumat/example-university/v1
/pumat/private/abc123/v1
```

Each namespace may define:

- trusted solver roots,
- identity requirements,
- acceptable-use policy,
- receipt publication policy,
- node eligibility rules,
- resource scheduling policy.

A node may contribute different resources to different namespaces.

Example:

```yaml
networks:
  public:
    enabled: true
    cpu: 8
    gpu: 0
  university:
    enabled: true
    cpu: 24
    gpu: 2
```

---

# 11. Node Capability Advertisement

## 11.1 Objective

A node periodically publishes a signed, expiring description of its currently available resources.

## 11.2 Example

```json
{
  "schema": "pumat.capability.v1",
  "peer_id": "12D3KooW...",
  "sequence": 381,
  "issued_at": "2026-10-02T01:00:00Z",
  "expires_at": "2026-10-02T01:05:00Z",
  "architecture": "linux/amd64",
  "cpu": {
    "vendor": "AMD",
    "model": "EPYC 9654",
    "available_cores": 32
  },
  "memory_bytes": 137438953472,
  "gpus": [
    {
      "vendor": "nvidia",
      "model": "RTX 5090",
      "vram_bytes": 34359738368,
      "count": 1,
      "capabilities": ["cuda"]
    }
  ],
  "runtime": {
    "container": true,
    "microvm": false,
    "workspace_encryption": true
  },
  "solvers": [
    {"manifest_digest": "sha256:...", "artifact_cached": true}
  ],
  "policy": {
    "max_walltime_seconds": 21600,
    "max_result_retention_seconds": 259200,
    "network": "deny",
    "visibility": ["public"]
  },
  "status": "available",
  "signature": "..."
}
```

## 11.3 Transport and expiration

Availability changes every few seconds, so it does not belong in the DHT. Kademlia provider records are designed for slow-changing data (hours-scale republish) and cannot be withdrawn. Three layers:

| Layer | Content | Lifetime | Purpose |
|---|---|---|---|
| DHT provider records (§14.2) | "this peer may serve solver X / capability bucket Y in namespace N" | standard provider republish (~22h), expiry ~48h | coarse candidate discovery |
| Namespace GossipSub presence topic `/pumat/<ns>/presence/v1` (optional) | small signed availability beacon: `peer_id`, `sequence`, `status`, `expires_at` | 60s heartbeat, TTL 180s | freshness hint, lower lookup latency |
| Direct capability query `/pumat/capability/1.0.0` | full signed capability document (§11.2) | TTL 180s | **authoritative** for matching |

Rules:

- A candidate found via DHT or presence is never leased without a fresh direct capability query.
- When toggled off, the node publishes an `unavailable` presence beacon (if presence is enabled) and answers capability queries with `status: paused`.
- Stale advertisements (past `expires_at` plus skew allowance, §15.5) are treated as unavailable.
- `sequence` increases monotonically per peer. A lower `sequence` than one already seen is rejected as replay.

## 11.4 Privacy

Do not advertise:

- hostname,
- private IP,
- user name,
- institution unless explicitly configured,
- exact physical location,
- serial numbers,
- unnecessary hardware identifiers.

---

# 12. Job Model

## 12.1 Principle

A job is a declarative scientific computation, not an arbitrary shell command.

## 12.2 Job schema (normative example)

This is the single normative job example. Appendix A refers to it.

```yaml
apiVersion: pumat.org/v1alpha1
kind: QuantumEspressoJob

metadata:                       # not part of calc_id
  name: silicon-scf
  visibility: public            # public | unlisted | private
  labels:
    project: tutorial

solver:
  name: quantum-espresso
  version: "7.4.1"
  manifestDigest: "sha256:..."  # signed solver manifest (§13.1), not an OCI digest
  entrypoint: pw.x

resources:                      # not part of calc_id
  cpu:
    cores: 16
  memory: 32GiB
  walltime: 2h
  gpu:
    count: 0

system:
  structure:
    file: structure.cif         # path relative to this YAML, read from the requester's local disk
  pseudopotentials:
    Si:
      filename: Si.pbe-n-kjpaw_psl.1.0.0.UPF
      digest: "sha256:..."
      source: "https://..."     # where an agent may fetch it if not bundled (§40)

calculation:
  type: scf                     # scf | relax | vc-relax | nscf | bands (QE adapter allowlist)
  parameters:                   # QE adapter allowlist only (§39.1); QE native units
    ecutwfc: 60                 # Ry
    kpoints: [8, 8, 8]

publication:                    # not part of calc_id
  input: true
  rawOutput: true
  parsedOutput: true
  provenance: true
  license: CC-BY-4.0            # required when visibility is public or unlisted

delivery:                       # not part of calc_id (§16.7)
  mode: attached                # attached | detached
  resultRetention: 24h          # requested; capped by worker policy
  custodian: null               # optional custodian peer ID (§16.9)
```

Field ownership:

| Block | In `calc_id` | Notes |
|---|---|---|
| `solver` | yes | `manifestDigest` + `entrypoint` |
| `system`, `calculation` | yes | file contents enter via digest |
| `metadata`, `resources`, `publication`, `delivery` | no | runtime and policy only |

`resources` is excluded on purpose. Changing core count can change MPI decomposition and the last digits of floating-point results. That is handled by tolerance comparison (§26.2), not by treating it as a different calculation.

Input files are always read from the requester's local disk. Together with the canonical job they form the **input bundle**, which is transferred directly to the worker (§16).

## 12.3 Forbidden fields in public v1

Public jobs must not support:

```text
command
shell
script
dockerImage
entrypointOverride
hostMount
privileged
hostNetwork
capAdd
arbitraryEnvironment
postRunCommand
preRunCommand
```

## 12.4 Job ID

Canonicalize the declarative scientific input separately from runtime allocation.

Two identifiers are recommended:

### Scientific Calculation ID

Represents the logical calculation. Computed by the requester from the canonical job (§7.4.2), using `derive` (§7.5):

```text
calc_id = derive("pumat.calc.v1", {
  "kind":                    "QuantumEspressoJob",
  "solver_manifest_digest":  "sha256:...",
  "entrypoint":              "pw.x",
  "system":                  { ...canonical, files as {filename, digest, size} },
  "calculation":             { ...canonical },
  "dependencies":            [ ...digests, sorted ]
})
```

### Execution ID

Represents one actual execution attempt. It must not depend on the lease, because the lease contains it.

```text
exec_id = derive("pumat.exec.v1", {
  "calc_id":            "pumat:calc:blake3:...",
  "requester_peer_id":  "12D3...",
  "worker_peer_id":     "12D3...",
  "requester_nonce":    "<128-bit random, hex>"
})
```

### Lease ID

```text
lease_id = blake3(lease payload bytes)   // the JCS payload of the signed lease envelope
```

Dependency order: `calc_id` → `exec_id` → lease (contains `exec_id`) → `lease_id`. There is no cycle.

This allows multiple workers to independently reproduce the same calculation while keeping separate provenance.

## 12.5 Identifier format

Top-level protocol objects use a typed identifier:

```text
pumat:<type>:<alg>:<hex>
```

| Type | Example | Derivation |
|---|---|---|
| `calc` | `pumat:calc:blake3:…` | §12.4 |
| `exec` | `pumat:exec:blake3:…` | §12.4 |
| `lease` | `pumat:lease:blake3:…` | §12.4 |
| `receipt` | `pumat:receipt:blake3:…` | blake3 of the worker completion payload (§20.2) |
| `record` | `pumat:record:blake3:…` | blake3 of the publication manifest payload (§22.4) |

Raw content digests (bundles, chunks, files, OCI artifacts) stay in the short form `sha256:<hex>` / `blake3:<hex>`.

Every field holding a typed ID carries the full `pumat:...` form. CLIs accept unambiguous prefixes for convenience, but the protocol never does.

---

# 13. Solver Trust Model

## 13.1 Solver Manifest

A solver is represented by a signed manifest. This is the single normative example; Appendix B refers to it.

The YAML below is the authoring source. The signed form is its RFC 8785 canonical JSON, and `sha256` of those bytes is the **solver manifest digest** that jobs pin (§7.7).

```yaml
schema: pumat.solver.v1
name: quantum-espresso
version: "7.4.1"

source:
  repository: https://github.com/QEF/q-e
  commit: "..."
  license: GPL-2.0-or-later

artifacts:                       # one platform-specific OCI image manifest per platform
  - platform: linux/amd64
    reference: ghcr.io/pumat-solvers/quantum-espresso
    mediaType: application/vnd.oci.image.manifest.v1+json
    digest: sha256:...
  - platform: linux/arm64
    reference: ghcr.io/pumat-solvers/quantum-espresso
    mediaType: application/vnd.oci.image.manifest.v1+json
    digest: sha256:...
index:                           # optional
  digest: sha256:...

entrypoints:                     # map keyed by entrypoint name
  pw.x:
    launcher: mpi                # mpi | openmp | serial (§39.1)
    network: deny
    maxProcesses: 512
    maxWalltime: 24h

security:                        # applies to every entrypoint; an entrypoint may only tighten it
  rootFilesystem: readOnly
  network: deny
  privileged: false
  hostMounts: false

numerics:                        # §39.1, §62.4
  blas: openblas
  blasVersion: "0.3.28"
  cpuDispatch: generic           # generic | dynamic-arch

parser:                          # runs on the requester side (§23)
  name: pumat-qe-parser
  version: "0.1.0"
  format: wasm                   # wasip1 module
  reference: ghcr.io/pumat-parsers/quantum-espresso
  digest: sha256:...

attestations:
  sbom: sha256:...
  buildProvenance: sha256:...    # SLSA provenance
```

## 13.2 Root of trust

MVP:

- Pumat project maintains approved solver signing policy.
- Solver releases are signed via Sigstore/Cosign.
- Agent verifies signature and digest before execution.

Future:

- multi-maintainer threshold approval,
- institutional solver trust roots,
- community governance,
- federation-specific allowlists.

## 13.3 Artifact immutability

Never execute based only on:

```text
quantum-espresso:latest
quantum-espresso:7.4
```

Execution must resolve to and record an immutable OCI digest.

Tags are discovery conveniences only.

## 13.4 Solver build pipeline

Required CI stages:

```text
source checkout
    |
reproducible build
    |
unit/integration tests
    |
scientific reference tests
    |
SBOM generation
    |
vulnerability scan
    |
OCI publish
    |
digest capture
    |
signature / provenance attestation
    |
manifest publication
```

## 13.5 Solver revocation

The network must support signed revocation statements.

Reasons:

- critical vulnerability,
- malicious artifact,
- broken scientific output,
- compromised signing identity.

Workers must refresh revocation data periodically and refuse revoked artifacts.

Revocation list format and distribution:

```json
{
  "schema": "pumat.revocation.v1",
  "namespace": "/pumat/public/v1",
  "sequence": 42,
  "issued_at": "2026-10-02T00:00:00Z",
  "next_update_before": "2026-10-03T00:00:00Z",
  "revoked": [
    {"kind": "solver_manifest", "digest": "sha256:...", "reason": "critical_vulnerability"},
    {"kind": "artifact",        "digest": "sha256:...", "reason": "malicious_artifact"},
    {"kind": "signer",          "identity": "https://github.com/...", "reason": "compromised_signing_identity"}
  ]
}
```

- The list is signed under the namespace trust root (Sigstore bundle) and published as an OCI artifact. Mirrors serve it by tag; agents verify signature and `sequence`.
- The latest known list is also gossiped on `/pumat/<ns>/revocation/v1` so it propagates without the registry.
- A lower `sequence` than the cached one is ignored (rollback protection).
- Staleness: if the cached list is older than `next_update_before` + 48h, the worker stops accepting **new** public-namespace leases until it refreshes (fail closed). Private namespaces may configure their own window.

---

# 14. Job Discovery and Matching

## 14.1 Design requirement

Avoid all-to-all status polling.

## 14.2 Recommended MVP approach

Use capability buckets in the DHT plus short-lived job advertisements.

Example capability keys:

```text
/pumat/v1/cap/cpu/linux-amd64
/pumat/v1/cap/gpu/nvidia/cuda
/pumat/v1/solver/quantum-espresso/7.4.1
```

Exact design should avoid combinatorial key explosion.

## 14.3 Matching flow

```text
Requester
  |
  | construct requirements
  v
DHT/provider lookup
  |
  v
Candidate peers
  |
  | direct capability query
  v
Policy/resource filtering
  |
  v
Lease offers
  |
  v
Worker selection
```

## 14.4 Pull-biased alternative

For security and contributor autonomy, the worker may pull job offers rather than accept unsolicited push execution.

Preferred semantic model:

> Workers advertise availability; requesters advertise signed job offers; workers explicitly accept compatible work.

This preserves the mental model that a contributor's machine chooses to accept work.

How this works in each phase:

- **MVP (requester-initiated, worker-decides).** The requester sends a signed `LeaseRequest` directly to a candidate on `/pumat/lease/1.0.0`. The request carries the job summary: solver manifest digest, entrypoint, resources, visibility, input bundle size, retention. The worker evaluates it against local policy and replies `LeaseOffer` or `Reject`. Nothing runs until the worker countersigns the lease. The worker still chooses; only the first contact is requester-initiated.
- **Later (pull job board).** Requesters publish signed job offers (summary only, never input data) on `/pumat/<ns>/offers/<capability-bucket>/v1` GossipSub topics. Idle workers subscribe to the buckets they match and initiate the lease. This helps when requesters greatly outnumber reachable workers, or when workers sit behind strict NAT and can only dial out.

---

# 15. Job Lease Protocol

## 15.1 Purpose

Prevent duplicate accidental execution and define temporary ownership of a worker resource allocation.

## 15.2 State machine

```text
REQUESTED            requester sent LeaseRequest
    |
OFFERED              worker replied LeaseOffer (worker-signed draft)
    |
LEASED               requester countersigned; both hold the signed lease
    |
INPUT_TRANSFER       requester -> worker, from requester's local disk
    |
RUNNING              requester may go offline from here (detached mode)
    |
SEALED               output bundle encrypted to result_recipient_key;
    |                plaintext workspace destroyed; worker completion signed
    |
    +--> DELIVERING ------------------------------+   (requester or custodian online)
    |                                             |
    +--> HELD (ciphertext only, until retention   |
    |        deadline) --> DELIVERING ------------+
    |                                             v
    |                                        DELIVERED   requester (or custodian) has
    |                                             |      the full ciphertext, hash verified
    |                                             v
    |                                        ACKNOWLEDGED  requester acceptance signed
    |                                             |
    |                                             v
    |                                        COMPLETED   worker deleted ciphertext
    |
    +--> RESULT_EXPIRED   retention deadline passed with no delivery;
                          ciphertext deleted; worker-only receipt kept
```

`CUSTODY` is a substate of `DELIVERED`. If a custodian (§16.9) received the ciphertext, the worker deletes its copy on the custodian's signed custody acknowledgment. Requester acceptance follows later, once the requester fetches from the custodian.

Failure paths:

```text
REJECTED          worker declined LeaseRequest
EXPIRED           start or input deadline passed before RUNNING
CANCELLED         requester-signed cancel accepted before SEALED
FAILED            solver failure; a failure bundle (exit code, stderr tail) is still sealed and delivered
WORKER_LOST       requester-side view: worker unreachable past grace period
RESULT_EXPIRED    see above
```

Every transition is persisted in SQLite before the corresponding network message is sent, and appended to the local event chain (§20.4).

## 15.3 Lease

The lease is a §7.4.1 signed envelope with schema `pumat.lease.v1`, signed by both peers.

```json
{
  "schema": "pumat.lease.v1",
  "namespace": "/pumat/public/v1",
  "exec_id": "pumat:exec:blake3:...",
  "calc_id": "pumat:calc:blake3:...",
  "requester_peer_id": "12D3KooWRequester...",
  "worker_peer_id": "12D3KooWWorker...",
  "requester_nonce": "…",
  "worker_nonce": "…",
  "solver_manifest_digest": "sha256:...",
  "artifact_digest": "sha256:...",
  "platform": "linux/amd64",
  "entrypoint": "pw.x",
  "visibility": "public",
  "resources": {"cpu_millicores": 16000, "memory_bytes": 34359738368, "gpu_count": 0, "disk_bytes": 21474836480},
  "input_bundle_digest": "blake3:...",
  "input_bundle_size": 1048576,
  "max_output_bytes": 10737418240,
  "issued_at": "2026-10-02T01:00:00Z",
  "input_deadline": "2026-10-02T01:10:00Z",
  "max_walltime_seconds": 7200,
  "result_retention_seconds": 86400,
  "result_recipient_key": "x25519:…",
  "custodian_peer_id": null
}
```

- `result_recipient_key` is a fresh X25519 public key generated by the requester **per execution**. The private key stays in the requester agent's keystore until the result is decrypted, then is destroyed. Losing it makes the result unrecoverable, by design.
- `result_retention_seconds` is the requested value, capped by worker policy. The countersigned lease carries the effective value.
- `artifact_digest` and `platform` are filled in by the worker in its offer.
- `lease_id` is defined in §12.4.

## 15.4 Expiration

- Before `RUNNING`: if the input bundle is not fully received by `input_deadline`, the lease moves to `EXPIRED` and the worker frees the allocation.
- During `RUNNING`: the requester being offline is normal in detached mode and is **not** a failure. The worker runs to completion or wall-time limit, then seals.
- After `SEALED`: the worker holds only ciphertext until the retention deadline (`sealed_at + result_retention_seconds`), then deletes it (`RESULT_EXPIRED`).
- Defaults for the public namespace: requested retention 24h, worker cap 72h. A worker may refuse leases asking for more than its cap.

## 15.5 Clock skew

- Peers reject signed objects whose `issued_at` is more than 120s in the future.
- Deadlines and expirations are evaluated with a 120s grace period.
- `pumat doctor` warns when local clock offset (vs. connected peers' Identify timestamps or NTP) exceeds 30s.

---

# 16. Secure Data Transfer

## 16.1 Principle

Input and output data should not be globally published merely because job discovery is public.

Data path:

```text
requester local disk ──input bundle──> worker          (direct P2P, relay fallback)
worker ──sealed output bundle──> requester              (requester pulls; worker push is opportunistic)
worker ──sealed output bundle──> custodian ──> requester (optional, §16.9)
```

No bootstrap node, explorer, or project-operated server sits in the data path, except a circuit relay when direct connectivity fails. A relay only sees encrypted bytes.

## 16.2 Transport

Use authenticated encrypted libp2p streams.

For defense in depth, scientific payload bundles should additionally use application-layer encryption tied to requester/worker ephemeral session keys.

## 16.3 Session

Recommended:

1. Requester and worker establish authenticated P2P connection.
2. Derive ephemeral job session keys.
3. Transfer content-addressed chunks.
4. Verify each chunk hash.
5. Verify full bundle hash.

## 16.4 Chunking

Large input/output files require:

- resumable chunks,
- Merkle/chunk hashes,
- bounded memory use,
- retry support,
- direct-stream preference.

## 16.5 No plaintext persistence

Worker temporary disk should preferably use an ephemeral encrypted workspace.

Pattern:

```text
random workspace encryption key
        |
encrypted temporary volume
        |
job runs
        |
result acknowledged
        |
destroy encryption key
        |
delete workspace metadata
```

Do not promise physical secure erase of SSD blocks.

Documentation must say:

> Pumat destroys ephemeral encryption keys and removes the logical workspace; it does not claim guaranteed physical erasure of underlying flash cells.

## 16.6 Result sealing

When the solver exits (success, failure, or wall-time kill):

1. The worker builds the **output bundle**: `output/` files, stdout/stderr, exit status, and `execution-environment.json` (artifact digest, platform, CPU model, core count, wall/CPU seconds).
2. It chunks the bundle and computes the chunk Merkle root, which becomes `output_bundle_digest` (plaintext digest).
3. It encrypts the bundle with HPKE (RFC 9180, base mode) to the lease's `result_recipient_key`. Each chunk is sealed with the HPKE context and a chunk-index nonce. The ciphertext gets its own chunk Merkle root, `sealed_bundle_digest`.
4. It signs the **worker completion statement** (§20.2) covering both digests.
5. It destroys the workspace encryption key and deletes the plaintext workspace (§28.2). State becomes `SEALED`.

After step 5 the worker holds only ciphertext it cannot decrypt. This narrows P4: retaining a result for an offline requester never means retaining plaintext.

Sealing makes stored data unreadable after sealing. It does not protect data from a compromised worker during execution (§37.3).

## 16.7 Delivery modes

| Mode | Requester online during | Typical use |
|---|---|---|
| `attached` (default) | whole job | short jobs, interactive CLI. `pumat submit` waits and streams progress |
| `detached` | lease + input upload only | long jobs from a laptop. `pumat submit --detach` returns after the input upload |
| `custodian` | lease + input upload only | jobs longer than retention, or requesters rarely online (§16.9) |

In every mode the requester agent must stay online until `RUNNING`, because the input lives on the requester's disk. If the agent stops mid-upload, the chunked transfer resumes on reconnect as long as `input_deadline` has not passed.

## 16.8 Fetching a held result (requester pull)

The requester agent remembers every non-terminal execution in SQLite: `exec_id`, worker peer ID, lease, recipient private key. Its fetch loop:

```text
on agent start, on network change, and every 5 min while executions are pending:
  for exec in pending executions (state RUNNING / SEALED / HELD from requester's view):
      connect(worker_peer_id)                     # DHT peer routing, hole punch, relay
      status = query /pumat/result/1.0.0 Status(exec_id)
      if status.state == RUNNING:   record progress; continue
      if status.state in (SEALED, HELD):
          verify worker completion statement signature
          download sealed chunks (resumable, any order, verify each chunk against sealed root)
          verify sealed_bundle_digest
          decrypt with recipient private key
          verify output_bundle_digest
          run parser locally (§23) -> parsed_result_digest
          sign requester acceptance (§20.2) and send Ack(exec_id, acceptance)
          destroy recipient private key
      if status.state == RESULT_EXPIRED or unknown exec:
          mark RESULT_EXPIRED locally; offer resubmission (same calc_id, new exec_id)
```

Pull is the primary path because the requester usually sits behind NAT and reconnects at unpredictable times, so the reconnecting side dialing is the most reliable. As an optimization, the worker also tries to dial the requester once on `SEALED` and then with exponential backoff (1m → 1h cap) to send a `ResultReady` notification. The requester never has to accept inbound connections for delivery to work.

User-facing commands:

```bash
pumat submit job.yaml --detach
pumat job status <exec-id>      # last known state + when it was last confirmed
pumat job wait <exec-id>        # block until DELIVERED / FAILED / RESULT_EXPIRED
pumat job fetch <exec-id>       # force an immediate fetch attempt
pumat job list --pending
```

Progress while detached: when connected, the worker sends signed heartbeats (`state`, `elapsed_seconds`, optional coarse progress such as SCF iteration count, never output content). When not connected, `job status` shows `RUNNING (last confirmed 3h ago)`. The requester never assumes a state it has not seen signed.

## 16.9 Result custodian (optional)

A custodian is any Pumat peer the requester designates to receive ciphertext on its behalf:

- the requester's own always-on workstation or lab server,
- an institutional node in a private federation,
- a community volunteer mailbox node (opt-in, quota-limited).

Flow:

1. The lease carries `custodian_peer_id`, so the worker knows in advance where to push.
2. On `SEALED`, the worker pushes the sealed bundle to the custodian on `/pumat/custody/1.0.0`.
3. The custodian verifies `sealed_bundle_digest` and the worker completion signature, then returns a signed **custody acknowledgment** (`exec_id`, `sealed_bundle_digest`, `held_until`).
4. On custody ack the worker deletes its ciphertext. Its receipt state becomes `delivered_to_custodian`.
5. The requester later fetches from the custodian with the same §16.8 pull loop, decrypts, and sends the acceptance to the worker directly or through the custodian, which relays it on its next contact.

Properties:

- The custodian cannot decrypt (HPKE to the requester's per-execution key).
- The custodian learns metadata: execution size, timing, and peer IDs of worker and requester.
- Custody is not publication. Public results are still published by the requester after decryption and parsing (§45).
- Custodian retention and quota are custodian policy. Default for volunteer mailboxes: 7 days, 10 GiB per requester.

## 16.10 Large inputs

Input must originate from the requester, but it does not have to be uploaded at the moment of submission:

- **MVP:** the requester uploads directly to the worker during `INPUT_TRANSFER`.
- **Later:** `input.source: custodian`. The requester pre-stages the encrypted input bundle on its custodian, and the worker pulls it from there after the lease. The input is HPKE-sealed to a worker key that appears only in the countersigned lease, so the custodian cannot read it. This lets a laptop submit a large-input job and disconnect immediately.

## 16.11 Retention edge cases

| Situation | Behavior |
|---|---|
| Requester offline past retention | Worker deletes ciphertext. Worker-only receipt state `result_expired`. Requester may resubmit the same `calc_id`. |
| Requester reinstalls or loses agent state | Recipient private key is gone, so the result is unrecoverable. Agent state backup is the user's responsibility; the CLI warns at `--detach`. |
| Worker goes offline while holding ciphertext | Requester keeps retrying until the retention deadline as seen by the requester, then marks `WORKER_LOST` and may resubmit. A later delivery of the old execution is still accepted (§44.3). |
| Worker disk pressure | Held ciphertext counts against the worker's disk quota. If space runs out, new leases are refused; held results are never evicted before their deadline. |
| Partial download | Resumes by chunk. The sealed Merkle root lets either side verify partial progress. |

---

# 17. Execution Sandbox

## 17.1 Threat model

Treat all remote job inputs as hostile.

Treat approved solver binaries as lower-risk but not perfectly trusted.

## 17.2 Container MVP controls

Mandatory:

- non-root execution,
- rootless runtime where practical,
- no privileged mode,
- no host PID namespace,
- no host IPC namespace,
- no host network,
- no arbitrary device access,
- allow only assigned GPU devices,
- read-only root filesystem,
- ephemeral workspace,
- CPU quota/cpuset,
- memory limit,
- process/PID limit,
- disk quota,
- wall-time limit,
- seccomp profile,
- capability drop-all,
- no SSH daemon,
- no inbound ports,
- outbound network denied by default.

Host requirements for rootless execution (checked by `pumat doctor --runtime`; the worker refuses `pumat on` for public namespaces if any check fails):

| Requirement | Why |
|---|---|
| cgroup v2 with `cpu`, `memory`, `pids`, `io` delegated to the `pumat` user (systemd `Delegate=yes`) | rootless CPU/memory/PID limits do not work otherwise |
| subordinate UID/GID ranges for `pumat` (`/etc/subuid`, `/etc/subgid`) | user namespaces for rootless containers |
| seccomp support in the runtime | syscall filtering |
| workspace on a filesystem where size can be bounded | disk quota |

Disk quota and workspace encryption are hard to do fully rootless. Recommended approach:

- The installer, which runs once as root, creates a fixed-size workspace pool (e.g. loop-backed file or dedicated LV) owned by `pumat`.
- Per-job workspaces are size-bounded directories or images inside that pool.
- Encryption uses a per-job key held only in agent memory, either via a narrowly scoped root helper (dm-crypt on a per-job loop image) or a userspace encrypted filesystem.
- If neither is available, the node may run but must advertise `workspace_encryption: false`, and requesters may filter on it.

## 17.3 Network policy

Scientific solvers normally should have:

```text
network = DENY
```

Exceptions require explicit solver manifest policy and must not be allowed in the public network without governance review.

Blocking network access is a major defense against:

- mining pool access,
- botnet command/control,
- scanners,
- exfiltration,
- arbitrary remote downloads.

## 17.4 GPU access

GPU passthrough materially changes isolation risk.

MVP GPU workers should initially be opt-in experimental nodes.

Before calling GPU isolation production-ready, threat-model:

- driver attack surface,
- device node permissions,
- shared GPU memory behavior,
- container runtime hooks,
- vendor runtime vulnerabilities.

## 17.5 MicroVM phase

For stronger isolation, support Firecracker/Kata where practical.

Do not block MVP on microVM GPU support.

---

# 18. Abuse Prevention

## 18.1 Threats

- cryptocurrency mining,
- botnet workloads,
- arbitrary malware,
- resource exhaustion,
- malformed solver input exploiting parser/solver vulnerabilities,
- DDoS via job payloads,
- Sybil nodes,
- fake completion receipts,
- reputation gaming,
- storage abuse,
- public-result spam,
- illegal/prohibited content hidden in job artifacts.

## 18.2 Primary defense: closed execution vocabulary

Public network accepts only:

```text
approved solver
+ approved version/digest
+ approved entrypoint
+ validated input schema
```

No user-controlled shell.

## 18.3 Secondary defenses

- no solver network access,
- runtime quotas,
- per-requester fair share,
- job size limits,
- parser/input limits,
- reputation,
- node-local allow/deny rules,
- optional identity level requirements,
- solver-specific semantic validation,
- rate limiting,
- duplicate-job detection,
- signed revocation lists.

## 18.4 Important limitation

A user may submit scientifically valid but useless calculations solely to consume resources.

The protocol cannot determine scientific intent perfectly.

Therefore anti-abuse must combine:

- executable restrictions,
- resource economics/fairness,
- identity/reputation,
- local contributor policy.

---

# 19. Fairness and Resource Allocation

## 19.1 Philosophy

Do not create transferable monetary credits in v1.

Use fair-share and contribution reputation.

## 19.2 Work-conserving rule

If no competing demand exists, allow a heavy user to consume otherwise idle resources.

When contention appears, reduce that user's priority relative to under-served users.

## 19.3 Inputs to scheduling score

Potential factors:

- recent consumption,
- recent contribution,
- requester reliability,
- worker reliability,
- job age,
- resource scarcity,
- scientific verification/reproduction jobs,
- federation policy,
- new-user minimum access guarantee.

## 19.4 Decay

Consumption/contribution history should decay over time.

Example:

```text
weighted_usage = sum(resource_seconds * exp(-age / tau))
```

Do not expose this as a redeemable balance.

## 19.5 Multi-resource fairness

Future scheduler may adapt Dominant Resource Fairness concepts so CPU-heavy, RAM-heavy, and GPU-heavy jobs can be compared without pretending all resources are equivalent.

---

# 20. Compute Receipt

## 20.1 Purpose

A Compute Receipt is the cryptographic record that two peers agreed a specific execution occurred.

It is **not** necessarily proof that every claimed floating-point operation happened honestly.

## 20.2 Receipt structure

A receipt is two separately signed statements, each a §7.4.1 envelope. This is the single normative example; Appendix C refers to it.

**Worker completion** (`pumat.receipt.completion.v1`, signed by the worker at `SEALED`):

```json
{
  "schema": "pumat.receipt.completion.v1",
  "exec_id": "pumat:exec:blake3:...",
  "calc_id": "pumat:calc:blake3:...",
  "lease_id": "pumat:lease:blake3:...",
  "requester_peer_id": "12D3KooWRequester...",
  "worker_peer_id": "12D3KooWWorker...",
  "solver_manifest_digest": "sha256:...",
  "artifact_digest": "sha256:...",
  "platform": "linux/amd64",
  "input_bundle_digest": "blake3:...",
  "output_bundle_digest": "blake3:...",
  "sealed_bundle_digest": "blake3:...",
  "outcome": "succeeded",
  "exit_code": 0,
  "resource_claim": {
    "cpu_millicores": 16000,
    "gpu_count": 0,
    "wall_seconds": 1872,
    "cpu_seconds": 29811,
    "max_rss_bytes": 21474836480
  },
  "started_at": "2026-10-02T01:20:00Z",
  "finished_at": "2026-10-02T01:51:12Z"
}
```

`outcome` is one of `succeeded | solver_failed | walltime_exceeded | resource_exceeded`.

**Requester acceptance** (`pumat.receipt.acceptance.v1`, signed by the requester after decrypting, verifying, and parsing):

```json
{
  "schema": "pumat.receipt.acceptance.v1",
  "receipt_id": "pumat:receipt:blake3:...",
  "exec_id": "pumat:exec:blake3:...",
  "output_bundle_digest": "blake3:...",
  "parser_digest": "sha256:...",
  "parsed_result_digest": "blake3:...",
  "accepted_at": "2026-10-02T09:03:40Z",
  "verdict": "accepted"
}
```

- `receipt_id = blake3(worker completion payload bytes)`. The acceptance references it, so it binds to the exact completion statement.
- `verdict` is one of `accepted | output_mismatch | malformed_output`. A requester that receives bytes not matching `output_bundle_digest` signs `output_mismatch` rather than staying silent.
- `parsed_result_digest` lives only in the acceptance, because parsing happens on the requester side (§23). Anyone can recompute it from the raw output and the parser digest.
- With a custodian, a third statement `pumat.custody.ack.v1` (custodian-signed) may sit between them (§16.9).

## 20.3 Two-stage signing

1. Worker signs completion at `SEALED`.
2. Requester decrypts, verifies `output_bundle_digest`, parses, and signs acceptance.

Receipt states held by each side:

```text
completed               both statements present, verdict accepted
disputed                acceptance verdict != accepted
delivered_to_custodian  completion + custody ack, awaiting acceptance
unacknowledged          completion only, result delivered but no acceptance yet
result_expired          completion only, never delivered before retention deadline
```

## 20.4 Local event chain

Each agent maintains a hash-linked append-only event chain:

```text
event_001 -> event_002 -> event_003 -> ...
```

Each event includes previous event hash.

Periodic roots can optionally be:

- gossiped,
- published to public transparency services,
- witnessed by independent peers,
- anchored elsewhere.

## 20.5 Why not blockchain first

The network initially needs:

- tamper evidence,
- bilateral attestation,
- transparency,
- reproducibility,

not global financial consensus.

A blockchain may be added later only if a concrete consensus problem requires it.

---

# 21. Transparency and Witnessing

## 21.1 Solver artifacts

Use Sigstore/Cosign-style software supply-chain verification.

Sigstore's Rekor provides an append-only, tamper-resistant transparency log for signed software metadata and allows inclusion/integrity verification.

## 21.2 Compute receipts

Do not send all receipts to Rekor by default; this may be semantically inappropriate and creates dependency/cost issues.

Instead define a protocol-neutral witness interface:

```text
submit_receipt_root(root_hash)
verify_receipt_root(root_hash, proof)
```

Possible implementations:

- community transparency log,
- federation log,
- Git-based signed snapshots,
- third-party witness,
- future blockchain anchoring.

## 21.3 Requirement

Core execution must continue if no witness service is reachable.

Receipts queue locally for later witnessing.

---

# 22. Public Scientific Record

## 22.1 Principle

A public job should produce a citable, machine-readable scientific object.

## 22.2 Record structure

```text
Public Scientific Record
├── manifest.json
├── input/
│   ├── canonical-job.json        # §7.4.2; the input bundle as sent to the worker
│   ├── job.yaml                  # original authoring file, informational only
│   ├── structure.cif
│   └── dependencies.json
├── output/
│   ├── stdout.txt
│   └── solver-specific files
├── parsed/
│   └── result.json
├── provenance/
│   ├── receipt-completion.json   # signed envelope
│   ├── receipt-acceptance.json   # signed envelope
│   ├── lease.json                # signed envelope
│   ├── solver-manifest.json      # signed solver manifest
│   └── execution-environment.json
└── checksums.txt
```

`manifest.json` is the **publication manifest**: a requester-signed envelope (`pumat.record.v1`) listing every file with its digest and size, the `calc_id`, `exec_id`, `receipt_id`, visibility, and license.

## 22.3 Visibility modes

```text
public    - discoverable and publicly downloadable
unlisted  - accessible by object ID but not indexed
private   - encrypted and not published to public data layer
```

Public federation may encourage `public`, but it must never silently override explicit privacy policy.

## 22.4 Public record ID

```text
record_id = pumat:record:blake3:<blake3 of the publication manifest payload bytes>
```

One `calc_id` may have many records, one per published execution. The explorer groups records by `calc_id` to show reproduction (§26).

## 22.5 Web page

Example fields:

```text
Calculation
-----------
Material: Silicon
Solver: Quantum ESPRESSO 7.4.1
Calculation: SCF
Status: converged

Results
-------
Total energy: ...
Fermi energy: ...
Final structure: ...

Reproducibility
---------------
Solver artifact: sha256:...
Parser artifact: sha256:...
Input bundle: blake3:...
Output bundle: blake3:...
Executions: 3 independent runs
Agreement: verified

Downloads
---------
Input bundle
Raw output
Parsed JSON
Compute receipts
```

---

# 23. Parsing Architecture

## 23.1 Parser as verified artifact

Parser must be versioned and hashed independently from solver.

```text
quantum-espresso parser v0.1.0
sha256:...
```

Where the parser runs: **on the requester side**, after decryption. It can also run on anyone's machine when reproducing or re-parsing a public record. Workers never run the parser.

Why requester-side:

- The worker's job ends at the raw output, which keeps the worker's attack surface and trusted code minimal.
- The parsed result is a pure function of raw output and parser digest, so it should be computed by whoever needs it rather than attested by a worker.
- A parser bug fix can re-parse every historical record without re-running any solver.

Parser artifact format: a WASI (`wasip1`) WebAssembly module distributed as an OCI artifact and executed by an embedded pure-Go runtime (e.g. wazero). This makes the parser:

- sandboxed: no filesystem beyond the mounted output bundle, no network, no clock,
- deterministic: same bytes in, same bytes out,
- cross-platform: runs on macOS and Windows requester machines without a container runtime.

## 23.2 Canonical result schema

Define a solver-independent scientific result schema incrementally.

MVP fields:

```json
{
  "schema": "pumat.result.materials.v1alpha1",
  "converged": true,
  "energy": {
    "value": -123.456,
    "unit": "eV"
  },
  "fermi_energy": {
    "value": 5.21,
    "unit": "eV"
  },
  "forces": [],
  "stress": null,
  "final_structure": {}
}
```

## 23.3 Never discard raw output

Parsed values are derivative.

For public records:

```text
raw output = evidence
parsed result = convenience/structured interpretation
```

Both must carry hashes.

## 23.4 Parser reproducibility

Anyone should be able to re-run the parser on the same raw output and obtain the same canonical parsed JSON.

---

# 24. Public Data Distribution

## 24.1 Requirement

Avoid making one founding-company storage account the permanent single point of failure.

## 24.2 MVP

Use inexpensive object storage for public result mirror + HTTP access.

Also allow clients to serve public records directly through P2P content provider records.

## 24.3 Future

Content-addressed replication:

```text
record digest
   |
provider discovery
   +--> requester node
   +--> worker cache
   +--> university mirror
   +--> public archive mirror
```

## 24.4 Persistence policy

P2P alone does not guarantee long-term persistence.

Define explicit pinning roles:

- requester pin,
- volunteer archive node,
- institution mirror,
- official best-effort mirror.

The explorer must display replication health.

Example:

```text
Replication: 4 known providers
Last seen: 2 min ago
Archive status: mirrored
```

---

# 25. Explorer / Indexer Architecture

## 25.1 Important rule

Explorer is disposable.

If deleted, it should be possible to rebuild from published records and public network announcements.

## 25.2 Components

```text
pumat-indexer
  |
  +--> listens for public record announcements
  +--> validates signatures/hashes
  +--> fetches manifests
  +--> extracts searchable metadata
  +--> writes PostgreSQL/Meilisearch/OpenSearch index

pumat-web
  |
  +--> search
  +--> record page
  +--> dataset export
  +--> solver page
  +--> node/federation public statistics
```

## 25.3 Search examples

```text
solver = quantum-espresso
material contains Ni
calculation = relax
converged = true
functional = PBE
```

Future domain-specific query:

```text
Ni-containing oxides
PBE
energy cutoff >= 500 eV equivalent
converged only
public license compatible
```

---

# 26. Scientific Reproducibility Verification

## 26.1 Re-execution

A public record can be voluntarily re-executed on another node.

```text
Record R
   |
reproduce request
   |
Worker X -> output hash/value
Worker Y -> output hash/value
```

## 26.2 Exact vs tolerant comparison

Floating-point/HPC results may not be byte-identical across hardware and libraries.

Therefore define:

- exact file hash verification where deterministic,
- scientific tolerance verification for numeric outputs.

Example:

```yaml
verification:
  total_energy:
    absTolerance: 1e-6
    unit: eV
  forces:
    maxAbsTolerance: 1e-5
    unit: eV/angstrom
```

## 26.3 Reproducibility status

Possible states:

```text
UNVERIFIED
REPRODUCED_EXACT
REPRODUCED_WITHIN_TOLERANCE
DIVERGENT
INSUFFICIENT_METADATA
```

Never label results “scientifically correct” merely because two workers agree.

---

# 27. Local Resource Policy

Example config:

```yaml
node:
  mode: available

resources:
  cpu:
    maxCores: 16
    maxUtilizationPercent: 80
  memory:
    max: 64GiB
  gpu:
    devices:
      - 1

schedule:
  allowedHours:
    - "20:00-08:00"
  timezone: Asia/Seoul

jobs:
  maxWalltime: 6h
  maxConcurrent: 1

trust:
  networks:
    - public
  minimumIdentityLevel: 0

solvers:
  allow:
    - quantum-espresso

publication:
  exposeNodeIdentity: false

shutdown:
  onDisable: drain
```

---

# 28. Lifecycle and Cleanup

## 28.1 Worker job directory

```text
work/<exec_id>/
├── lease.json                 # signed lease envelope
├── state.json
├── workspace/                 # encrypted per-job volume; exists only until SEALED
└── sealed/                    # HPKE ciphertext chunks; exists SEALED .. COMPLETED / RESULT_EXPIRED
    ├── completion.json        # signed worker completion
    └── chunks/
```

## 28.2 Cleanup stages

Cleanup happens in two stages, so plaintext never waits on the requester.

**At `SEALED`** (immediately after the solver exits and the bundle is sealed, §16.6):

1. sign worker completion and persist it,
2. destroy the workspace encryption key,
3. delete `workspace/` (input files, solver scratch, plaintext output),
4. keep the solver artifact cache.

**At `COMPLETED`** (requester acceptance received, or custody ack received):

5. delete `sealed/chunks/`,
6. persist the receipt statements and event chain entries only.

**At `RESULT_EXPIRED`** (retention deadline passed without delivery):

5. delete `sealed/chunks/`,
6. persist worker-only receipt with state `result_expired`.

A node that explicitly acts as a public mirror may additionally pin *published* records it fetches from the public layer (§24). This is separate from, and never derived from, its worker workspace.

## 28.3 Retention defaults

| Data | Retention |
|---|---|
| plaintext workspace (any outcome) | until `SEALED`; on agent crash, deleted at next agent start |
| sealed ciphertext | until delivery/ack, at most `result_retention_seconds` (public default 24h, worker cap 72h) |
| input bundle not yet running | until `input_deadline` |
| receipts / event chain | persistent |
| operational logs | 7 days default, configurable |

A failed job is still sealed and delivered, including exit status and a bounded stderr tail, so the requester can diagnose it. The worker keeps no diagnostic copy of input or output.

Logs must not contain scientific input/output content.

---

# 29. Logging and Privacy

## 29.1 Local logs

Allowed:

- state transitions,
- peer IDs,
- transfer byte counts,
- solver digest,
- error codes,
- timing,
- resource usage.

Avoid:

- full input contents,
- raw output contents,
- secret tokens,
- unnecessary IP retention.

## 29.2 Public metadata

A public record may show worker pseudonymous Peer ID only if policy permits.

A contributor must be able to choose:

```text
public worker attribution
pseudonymous attribution
no explorer attribution (receipt still exists)
```

---

# 30. Protocol Modules

Reference repository should separate protocol from implementation.

```text
pumat/
├── specs/
│   ├── PFCN-0001-overview.md
│   ├── PFCN-0002-identity.md
│   ├── PFCN-0003-capability.md
│   ├── PFCN-0004-job.md
│   ├── PFCN-0005-lease.md
│   ├── PFCN-0006-transfer.md
│   ├── PFCN-0007-receipt.md
│   ├── PFCN-0008-solver.md
│   └── PFCN-0009-public-record.md
├── proto/
├── agent/
├── solver-sdk/
├── parser-sdk/
├── indexer/
├── web/
├── deploy/
└── docs/
```

---

# 31. Suggested Monorepo Structure

```text
pumat/
├── README.md
├── LICENSE
├── GOVERNANCE.md
├── SECURITY.md
├── CODE_OF_CONDUCT.md
├── CONTRIBUTING.md
│
├── cmd/
│   ├── pumat/
│   └── pumat-indexer/
│
├── internal/
│   ├── identity/
│   ├── node/
│   ├── discovery/
│   ├── protocol/
│   ├── scheduler/
│   ├── lease/
│   ├── transfer/
│   ├── executor/
│   ├── sandbox/
│   ├── solver/
│   ├── receipt/
│   ├── ledger/
│   ├── publication/
│   ├── policy/
│   └── storage/
│
├── pkg/
│   ├── canonical/
│   ├── contentid/
│   └── schemas/
│
├── proto/
│   ├── capability.proto
│   ├── job.proto
│   ├── lease.proto
│   ├── transfer.proto
│   └── receipt.proto
│
├── solvers/
│   └── quantum-espresso/
│       ├── manifest.yaml
│       ├── Containerfile
│       ├── tests/
│       └── parser/
│
├── explorer/
│   ├── indexer/
│   └── web/
│
├── tests/
│   ├── integration/
│   ├── interoperability/
│   ├── security/
│   └── network-sim/
│
└── scripts/
    ├── install.sh
    └── install.ps1
```

---

# 32. CLI Specification

## 32.0 CLI and agent

`pumat` is one binary with two roles:

- `pumat agent` (systemd service or user-level launch agent) owns identity, libp2p host, SQLite state, transfers, execution, and the result fetch loop.
- Every other subcommand is a thin client that talks to the local agent over a Unix domain socket (`/run/pumat/agent.sock`, or `$XDG_RUNTIME_DIR/pumat/agent.sock` for user installs), mode `0660`, group `pumat`.

A requester that never contributes resources still runs the agent, with contribution disabled (`mode: requester-only`). Detached jobs and result fetching depend on it (§16.8).

## 32.1 Core

```bash
pumat init
pumat status
pumat on
pumat off
pumat pause
pumat resume
```

## 32.2 Resources

```bash
pumat resources show
pumat resources set --cpu 8 --memory 32G
pumat resources set --gpu 0
```

## 32.3 Network

```bash
pumat network list
pumat network join public
pumat network leave public
pumat peers
pumat network diagnose
```

## 32.4 Jobs

```bash
pumat submit job.yaml [--detach] [--peer <peer-id>] [--custodian <peer-id>] [--retention 24h]
pumat job list [--pending]
pumat job status <exec-id>
pumat job wait <exec-id>
pumat job fetch <exec-id>
pumat job cancel <exec-id>
pumat job logs <exec-id>      # local agent's state transitions, never output content
pumat job publish <exec-id>
pumat reproduce <record-id>
```

Results are written to `~/pumat/results/<exec-id>/` by default (configurable), with the same `input/ output/ parsed/ provenance/` layout as a public record (§22.2).

## 32.4.1 Custodian

```bash
pumat custodian enable --quota 50G --retention 7d [--allow <peer-id>...]
pumat custodian status
pumat custodian disable        # stops accepting; held items kept until their deadline
```

## 32.5 Solver

```bash
pumat solver list
pumat solver inspect quantum-espresso@7.4.1
pumat solver verify quantum-espresso@7.4.1
pumat solver cache
pumat solver prune
```

## 32.6 Receipts

```bash
pumat receipts list
pumat receipt show <id>
pumat receipt verify <id>
pumat ledger verify
```

## 32.7 Diagnostics

```bash
pumat doctor
pumat doctor --network
pumat doctor --runtime
pumat doctor --gpu
```

---

# 33. API/Protocol Versioning

All public objects require explicit schema/version.

Examples:

```text
pumat.capability.v1
pumat.job.v1alpha1
pumat.lease.v1
pumat.receipt.v1
pumat.solver.v1
pumat.record.v1
```

Rules:

- unknown required fields/features -> reject,
- unknown optional fields -> ignore/preserve as specified,
- protocol negotiation before job lease,
- never silently reinterpret semantics across versions.

---

# 34. Scheduler Design for MVP

Do not build a global omniscient scheduler.

Requester-side selection is sufficient initially.

Pseudo-flow:

```text
requirements = parse(job)
candidates = discover(requirements.coarseCapabilities)

for peer in candidates:
    capability = query(peer)
    if not verify_signature(capability): continue
    if capability.expired: continue
    if not solver_compatible(capability, job): continue
    if not resource_compatible(capability, job): continue
    if not local_trust_policy(peer): continue
    ask_for_lease_offer(peer)

rank offers using:
    expected_start
    reliability
    fair-share
    direct-connectivity
    artifact-cache-hint
    resource fit

select one
```

Do not rank by precise geographical location in MVP.

---

# 35. Reputation

## 35.1 Avoid global magical score

Do not begin with one opaque number such as `reputation=0.982`.

Store dimensions separately:

```text
jobs accepted
jobs completed
jobs failed
result transfer success
median start delay
receipt acknowledgments
reproduction agreements
recent availability
```

## 35.2 Local trust calculation

Each client may derive its own trust score.

This reduces dependence on a central reputation authority.

## 35.3 Sybil resistance

Public pseudonymous participation makes perfect Sybil resistance impossible without identity/cost assumptions.

Mitigations:

- reputation takes time,
- institution attestations,
- ORCID/GitHub attestations,
- per-identity fair share,
- job-size caps for low-history requesters,
- optional federation membership requirements.

---

# 36. Private and Institutional Federations

Same binary, different policy.

Example private federation config:

```yaml
network:
  name: university-a
  bootstrap:
    - /dns4/bootstrap.example.edu/...
  solverTrustRoots:
    - did:key:...
  membership:
    required: true
  publication:
    default: private
```

This enables future institutional deployment without forking the agent.

---

# 37. Security Model

## 37.1 Assets to protect

Contributor:

- host filesystem,
- host credentials,
- network,
- GPU/CPU availability,
- other local workloads.

Requester:

- input data,
- unpublished scientific results,
- result integrity,
- identity privacy.

Network:

- solver trust roots,
- reputation integrity,
- DHT health,
- public record integrity.

## 37.2 Untrusted actors

- malicious requester,
- malicious worker,
- malicious relay,
- malicious bootstrap node,
- compromised solver publisher,
- malicious explorer/indexer,
- network observer.

## 37.3 Trust assumptions

MVP does **not** guarantee:

- confidentiality against a fully compromised worker host while plaintext computation is executing,
- correctness against a malicious worker fabricating plausible results,
- anonymity against global network observers.

These limitations must be explicit.

## 37.4 Confidential workloads

Unknown public workers must not be marketed for confidential computation.

Potential future technologies:

- confidential VMs,
- hardware TEEs,
- attestation,
- encrypted computation where feasible.

Not MVP.

---

# 38. Malicious Worker Detection

Possible strategies:

1. duplicate execution sampling,
2. random reproducibility audits,
3. known-answer canary jobs,
4. scientific consistency checks,
5. receipt history,
6. worker software attestation where available.

For selected jobs:

```text
Requester sends same calc to workers A and B
            |
compare canonical parsed results
            |
agreement -> confidence signal
```

This is not absolute proof of correctness.

---

# 39. Solver Input Validation

Each solver plugin must implement:

```text
Validate(job)              requester + worker   (worker re-validates; never trusts requester)
Normalize(job)             requester            -> canonical job (§7.4.2)
EstimateResources(job)     requester
BuildInput(job)            worker, inside sandbox prep -> native solver input files
ParseOutput(output)        requester / anyone   (WASM parser artifact, §23)
ScientificCompare(a, b)    requester / anyone   (§26.2)
```

Input validation should impose:

- maximum atom count initially,
- maximum generated file size,
- maximum iterations where expressible,
- allowed pseudopotential sources,
- allowed solver features,
- no external hooks/plugins.

## 39.1 Quantum ESPRESSO adapter rules

**Parameter allowlist.** `calculation.parameters` accepts only keys the adapter explicitly maps to QE namelists/cards (e.g. `ecutwfc`, `ecutrho`, `kpoints`, `occupations`, `smearing`, `degauss`, `conv_thr`, `mixing_beta`, `nspin`, `starting_magnetization`, `electron_maxstep`, `ion_dynamics`, `cell_dynamics`, `press`). Unknown keys are rejected, never passed through.

**Adapter-controlled parameters.** Users can never set these; `BuildInput` fixes them:

| Parameter | Fixed value | Reason |
|---|---|---|
| `outdir` | `/work/out` | path injection |
| `pseudo_dir` | `/work/pseudo` | path injection; pseudos come from the verified bundle |
| `wfcdir` | unset (defaults to `outdir`) | path injection |
| `prefix` | `pumat` | predictable output names for the parser |
| `disk_io` | `low` | bound scratch disk use |
| `max_seconds` | `walltime - 60s` | lets QE stop cleanly before the hard kill |

**Pseudopotentials.** Every pseudopotential file in the bundle must match its pinned digest before `BuildInput` runs.

**Launch.** For `launcher: mpi`, the solver image contains an entrypoint wrapper that runs:

```text
mpirun -np <cores> --bind-to core pw.x -nk <pools> -in /work/in/pw.in
```

- `<cores>` comes from the lease.
- `<pools>` comes from `EstimateResources` (k-point parallelization).
- `OMP_NUM_THREADS=1`.
- The wrapper is part of the signed image, never part of the job.

**Numerics.** Solver images build BLAS/LAPACK with a pinned version and a fixed CPU dispatch target (`numerics.cpuDispatch: generic` for public images) to improve cross-hardware reproducibility (§62.4). Faster per-microarchitecture images may be published as separate solver manifests, so they get a different `solver_manifest_digest`.

---

# 40. Pseudopotentials and Scientific Dependencies

Quantum ESPRESSO calculations depend on pseudopotentials, which must be treated as immutable scientific dependencies.

Do not reference only filename.

Record:

```json
{
  "element": "Si",
  "filename": "Si.pbe-n-kjpaw_psl.1.0.0.UPF",
  "source": "...",
  "digest": "sha256:...",
  "license": "..."
}
```

Only redistribute artifacts whose licenses allow redistribution.

Otherwise require user/network to obtain dependency through permitted source while still pinning expected digest.

---

# 41. Public Licensing

Public scientific records require explicit licensing metadata.

Suggested defaults to evaluate legally:

- metadata: CC0 or CC BY,
- user-created input/output bundles: selectable license,
- software artifacts: original software licenses,
- Pumat code: Apache-2.0 recommended.

Do not assume solver output licensing automatically.

Add mandatory job field once legal design is finalized:

```yaml
publication:
  license: CC-BY-4.0
```

---

# 42. Installation and Update Security

## 42.1 Installer

`install.sh` must:

1. detect OS/architecture,
2. download release binary,
3. download signature/checksum bundle,
4. verify release,
5. install executable,
6. create service account where appropriate,
7. install systemd unit,
8. not enable resource sharing until explicit user action.

## 42.2 Updates

Never auto-run an unsigned downloaded binary.

Support:

```bash
pumat update check
pumat update
```

Auto-update should be opt-in for worker nodes.

## 42.3 Service privilege

Prefer dedicated user:

```text
pumat
```

Do not run the network daemon as root unless a narrowly scoped helper is unavoidable.

---

# 43. Observability

Local endpoint only by default:

```text
127.0.0.1:<port>/metrics
```

Metrics:

```text
pumat_node_available
pumat_jobs_running
pumat_jobs_completed_total
pumat_jobs_failed_total
pumat_cpu_shared_cores
pumat_gpu_shared_devices
pumat_bytes_sent_total
pumat_bytes_received_total
pumat_peer_connections
pumat_relay_bytes_total
```

Prometheus format is acceptable.

No central telemetry by default.

Optional anonymous project telemetry must be opt-in or clearly disclosed.

---

# 44. Failure Handling

## 44.1 Worker disappears

Requester:

- detect stream/heartbeat failure,
- wait grace period,
- mark lease uncertain,
- resubmit with new execution ID,
- retain prior execution ID for later duplicate-result handling.

## 44.2 Requester disappears

Worker:

- requester absence during `RUNNING` is normal (detached mode); keep running,
- seal the result to the requester's per-execution key and destroy plaintext (§16.6),
- hold ciphertext only, push to custodian if one is set (§16.9),
- delete ciphertext at the retention deadline (`RESULT_EXPIRED`).

Absence *before* `RUNNING` (input not received by `input_deadline`) expires the lease (§15.4).

## 44.3 Duplicate completion

Possible after network partition/resubmission.

Accept multiple executions under same calculation ID.

Do not overwrite history.

## 44.4 Solver artifact unavailable

Worker may retrieve same digest from alternate mirrors.

If digest unavailable, reject lease cleanly.

---

# 45. Public Record Announcement

After successful public execution:

1. requester (after decrypting and parsing, §16.8) creates immutable record bundle (§22.2),
2. requester signs publication manifest, which determines `record_id` (§22.4),
3. record is made available from at least one provider: the requester agent itself, plus the HTTP mirror upload in MVP (§24.2),
4. requester announces a DHT provider record for `record_id`,
5. requester publishes a small signed announcement on GossipSub topic `/pumat/<ns>/records/v1`, and also POSTs it to configured indexers (optional, best-effort),
6. indexers verify the announcement, fetch the manifest, verify all hashes and signatures, and index.

Announcement (§7.4.1 envelope, schema `pumat.publication.v1`, signed by the publisher):

```json
{
  "schema": "pumat.publication.v1",
  "record_id": "pumat:record:blake3:...",
  "calc_id": "pumat:calc:blake3:...",
  "exec_id": "pumat:exec:blake3:...",
  "receipt_id": "pumat:receipt:blake3:...",
  "solver_manifest_digest": "sha256:...",
  "solver": "quantum-espresso",
  "visibility": "public",
  "license": "CC-BY-4.0",
  "created_at": "2026-10-02T09:10:00Z",
  "publisher_peer_id": "12D3..."
}
```

`unlisted` records skip step 5. Indexers must ignore announcements whose visibility is not `public`.

---

# 46. Minimal Public Infrastructure

Founding project can initially operate:

```text
1. install.pumat.org
   static install script/releases link

2. bootstrap1.pumat.org
3. bootstrap2.pumat.org
   tiny libp2p bootstrap nodes

4. relay1.pumat.org
   bounded relay service

5. explorer.pumat.org
   optional public index/UI
```

Not required:

- central job database,
- central job queue,
- central solver binary server,
- central user account system,
- central storage of private job data.

Goal: network execution remains functional if explorer is down.

---

# 47. MVP Scope

## MVP definition

MVP is successful when **three independently administered Linux machines can execute a Quantum ESPRESSO job end-to-end through the Pumat protocol without a central scheduler**.

Required:

- Go agent.
- Ed25519 identity.
- libp2p connection.
- bootstrap discovery.
- DHT peer discovery.
- availability advertisement.
- node resource policy.
- QE solver artifact by digest.
- signature verification.
- sandbox execution.
- encrypted P2P input transfer.
- result transfer.
- cleanup.
- bilateral Compute Receipt.
- one public result bundle.
- simple static/HTML explorer for that result.

Not required:

- GUI tray.
- Windows worker.
- GPU.
- fairness algorithm.
- institution identity.
- fully decentralized public storage.
- blockchain.
- sophisticated reputation.

---

# 48. Implementation Phases

## Phase 0 — Protocol skeleton

Deliverables:

- repository,
- project constitution,
- protocol object schemas,
- threat model,
- CLI skeleton,
- identity generation,
- canonical serialization tests.

Exit condition:

```text
pumat init
pumat status
```

work reliably.

## Phase 1 — Two-peer execution

Deliverables:

- direct libp2p connection,
- fixed peer address execution,
- resource advertisement,
- QE artifact verification,
- local container execution,
- file transfer,
- sealed result return (attached mode; detached fetch loop if time permits),
- receipt signing.

Minimum security controls, required in Phase 1 rather than deferred to Phase 3:

- solver manifest signature and artifact digest verification,
- strict job schema (no command-like fields, unknown fields rejected),
- QE adapter-controlled parameters (§39.1),
- rootless, network-denied container with CPU/memory/PID/wall-time limits,
- plaintext workspace deletion at `SEALED`.

No DHT required yet.

Exit condition:

Machine A submits QE job to known Machine B and obtains result.

## Phase 2 — Federated discovery

Deliverables:

- bootstrap,
- DHT,
- NAT detection,
- relay support,
- short-lived availability advertisements,
- candidate discovery,
- lease protocol.

Exit condition:

Requester does not know worker address beforehand.

## Phase 3 — Security hardening

Deliverables:

- strict container profile,
- network denial,
- resource quotas,
- solver allowlist,
- signed revocations,
- encrypted ephemeral workspace,
- fuzzing of network/input parsers.

Exit condition:

Security test suite passes and arbitrary shell command cannot be expressed through job spec.

## Phase 4 — Public scientific records

Deliverables:

- QE parser,
- canonical result schema,
- immutable public record bundle,
- publication announcement,
- basic indexer,
- web explorer.

Exit condition:

A public QE result is browsable and downloadable with provenance.

## Phase 5 — Multi-node public alpha

Deliverables:

- installer,
- systemd integration,
- resource controls,
- failure recovery,
- relay quota,
- contributor docs,
- security disclosure process.

Target:

10-30 volunteer nodes.

## Phase 6 — GPU + second solver

Add:

- NVIDIA GPU resource advertisement,
- experimental GPU sandbox,
- LAMMPS or CP2K as second solver,
- generic solver SDK validation.

## Phase 7 — Fair share and reputation

Add:

- decayed usage history,
- contribution history,
- local reputation dimensions,
- contention-aware selection.

## Phase 8 — Institutional federation

Add:

- institution attestations,
- private namespaces,
- federation trust roots,
- policy bundles,
- organization dashboards as optional separate software.

---

# 49. Coding Agent Work Breakdown

## Epic A — Repository bootstrap

Tasks:

- initialize Go module,
- establish lint/test tooling,
- add GitHub Actions,
- define release process,
- create CLI with Cobra or equivalent,
- implement config directory conventions,
- implement structured logging.

Acceptance:

```bash
go test ./...
pumat version
pumat init
pumat status
```

## Epic B — Identity

Tasks:

- generate Ed25519 key,
- persist securely,
- derive Peer ID,
- sign/verify generic canonical message,
- key backup/export intentionally NOT implemented initially.

Acceptance:

- identity stable across restart,
- file permissions checked,
- corrupted identity fails closed.

## Epic C — P2P host

Tasks:

- initialize libp2p host,
- QUIC/TCP transports,
- secure channels,
- bootstrap peer configuration,
- identify protocol,
- connectivity diagnostics.

Acceptance:

Two agents can establish authenticated stream and display peer IDs.

## Epic D — Capability

Tasks:

- hardware detection,
- resource sharing config,
- protobuf schema,
- capability signing,
- TTL validation,
- request/response protocol.

Acceptance:

Requester can query remote capabilities and reject invalid signature/expired record.

## Epic E — Solver artifact

Tasks:

- define solver manifest schema,
- build QE OCI artifact,
- publish by digest,
- implement artifact pull,
- implement checksum/digest validation,
- implement signature validation,
- local CAS cache.

Acceptance:

Agent refuses altered or unsigned artifact.

## Epic F — Job schema

Tasks:

- YAML parser,
- strict schema validation,
- reject unknown dangerous fields,
- canonicalization,
- calculation ID derivation,
- QE job adapter.

Acceptance:

Equivalent YAML formatting produces same calculation ID.

## Epic G — Execution sandbox

Tasks:

- rootless container runner,
- CPU/memory/PID limits,
- network disabled,
- read-only solver layer,
- ephemeral workspace,
- timeout,
- exit code mapping.

Acceptance:

Solver cannot reach public network during security test.

## Epic H — Transfer

Tasks:

- session establishment,
- encrypted stream,
- chunk protocol,
- resumable transfer,
- bundle hashes,
- transfer limits.

Acceptance:

100MB test payload survives intentional stream interruption and resumes correctly.

## Epic I — Lease

Tasks:

- lease protobuf,
- bilateral signatures,
- state machine,
- expiration,
- cancellation,
- replay protection.

Acceptance:

Expired lease cannot launch execution.

## Epic J — Receipt

Tasks:

- receipt schema,
- worker completion signature,
- requester acknowledgment,
- verification CLI,
- local event chain.

Acceptance:

Changing any receipt field causes verification failure.

## Epic K — Discovery

Tasks:

- DHT setup,
- capability/provider announcement,
- candidate lookup,
- NAT traversal,
- relay fallback.

Acceptance:

Three machines on separate networks discover and execute without manual peer address configuration.

## Epic L — QE parser/publication

Tasks:

- parser artifact,
- canonical result schema,
- raw output bundling,
- publication manifest,
- content ID,
- public provider announcement.

Acceptance:

Record can be independently downloaded and all hashes verified.

## Epic M — Explorer

Tasks:

- public announcement listener,
- verification pipeline,
- PostgreSQL index,
- REST API,
- record page,
- search.

Acceptance:

Deleting/rebuilding explorer DB from test announcements reproduces same searchable records.

---

# 50. Required Test Strategy

## 50.1 Unit tests

- canonicalization,
- signatures,
- hash IDs,
- policy parsing,
- solver manifests,
- receipt verification.

## 50.2 Integration tests

Use Docker/network namespaces to simulate:

- requester,
- worker,
- bootstrap,
- relay,
- malicious peer.

## 50.3 Network fault tests

Inject:

- packet loss,
- delayed packets,
- disconnect during upload,
- disconnect during result download,
- duplicate messages,
- replayed lease,
- stale advertisement.

## 50.4 Security tests

Must include attempts to:

- execute arbitrary shell,
- access host filesystem,
- access metadata endpoints,
- reach internet from solver,
- exhaust PIDs,
- exceed RAM,
- exceed disk,
- use unapproved GPU,
- alter solver artifact,
- forge capability,
- forge receipt,
- replay old lease.

## 50.5 Scientific tests

QE known-reference jobs:

- SCF silicon,
- relaxation simple crystal,
- intentional non-convergence,
- malformed pseudopotential,
- invalid parameters.

Store expected parsed properties with tolerances.

---

# 51. Performance Targets for Alpha

These are engineering targets, not promises.

- Idle agent memory: < 150 MB.
- Idle CPU: near 0%, excluding discovery refresh.
- Availability propagation: < 3 minutes.
- Direct connection setup median: < 5 s on typical reachable peers.
- Small-job control-plane overhead: < 10 s excluding solver artifact download.
- Artifact cache hit should avoid re-download.
- No central service should process solver stdout or private job files.

---

# 52. Protocol Security Checklist Before Public Alpha

- [ ] Threat model published.
- [ ] No arbitrary command field exists.
- [ ] Solver digest mandatory.
- [ ] Solver signature verified.
- [ ] Network disabled inside public solver sandbox.
- [ ] Rootless execution.
- [ ] CPU/memory/PID/disk/walltime limits.
- [ ] Input bundle size limit.
- [ ] Output bundle size limit.
- [ ] Lease replay protection.
- [ ] Signed capability advertisement.
- [ ] Receipt signature verification.
- [ ] Ephemeral workspace cleanup.
- [ ] Installer signature/checksum verification.
- [ ] Security reporting email/process.
- [ ] Dependency vulnerability scanning.
- [ ] Fuzz tests for external protocol decoders.
- [ ] Relay rate limits.

---

# 53. Governance

## 53.1 Initial governance

Initially the founding maintainers control:

- protocol releases,
- reference implementation releases,
- default public-network solver allowlist,
- default trust roots,
- security response.

## 53.2 Governance evolution

Once external adoption exists:

- publish maintainer roles,
- use public RFC/PFCN proposal process,
- require multiple approvals for trust-root changes,
- establish solver review group,
- establish security response group.

## 53.3 Avoid founder lock-in

Critical network constants should be configurable:

- bootstrap peers,
- solver trust roots,
- relay list,
- explorer URL,
- witness providers.

A community fork should be able to continue the network.

---

# 54. Open-Source Licensing

Recommended default:

- Agent/protocol reference implementation: **Apache-2.0**.
- Specifications: CC BY 4.0 or similarly permissive documentation license.

Why Apache-2.0:

- permissive academic/commercial use,
- explicit patent grant,
- institution-friendly.

Legal review required before final release.

---

# 55. Branding Strategy

Project should look independent and scientific.

Recommended footer:

> Pumat is an open scientific infrastructure project initiated by Virtual Lab.

Avoid:

- prominent product cross-selling,
- account walls,
- proprietary protocol extensions in the public network,
- forced Virtual Lab cloud dependency.

Brand value should arise from credible stewardship.

---

# 56. Scientific Citation

Each public record should eventually expose citation metadata.

Example:

```text
Pumat Record: pumat:record:blake3:...
Solver: Quantum ESPRESSO 7.4.1
Created: 2026-10-02
```

Future:

- DOI minting through an archival integration,
- DataCite metadata,
- ORCID author binding.

Do not require DOI infrastructure for MVP.

---

# 57. Suggested README Opening

```markdown
# Pumat

Pumat is an open federated computing network for science.

Researchers and institutions can contribute idle CPU/GPU resources, while
scientific jobs are discovered and executed across the network using verified
solver artifacts. Public computations can be preserved with complete inputs,
raw outputs, parsed results, and cryptographic provenance.

No central scheduler is required. No cryptocurrency is required. Public worker
nodes do not execute arbitrary user code.

Share idle compute. Run science. Share the results.
```

---

# 58. First Demo Scenario

The first public demo should be deliberately simple and visually understandable.

## Setup

Three machines:

```text
Node A: laptop/workstation, requester
Node B: Linux server, contributor
Node C: Linux server on another network, contributor
```

## Demo

1. Install Pumat on B and C.
2. Run `pumat on`.
3. Submit Silicon SCF QE calculation from A.
4. A discovers B/C without knowing IP addresses.
5. B accepts lease.
6. B obtains verified QE artifact.
7. Input transfers encrypted.
8. QE runs without external network access.
9. Result returns to A.
10. B destroys workspace.
11. Both peers sign Compute Receipt.
12. A publishes input/output/parsed result.
13. Explorer shows scientific record.
14. C voluntarily reproduces the public record.
15. Explorer shows `REPRODUCED_WITHIN_TOLERANCE`.

This single demo communicates nearly the entire project thesis.

---

# 59. Key Design Decisions to Freeze Early

Before large implementation begins, explicitly freeze these decisions:

1. Go as reference-agent language.
2. libp2p as networking substrate.
3. Ed25519 node identity.
4. No arbitrary code on public workers.
5. Solver artifacts pinned by OCI digest.
6. Signed solver trust policy.
7. Public solver network access denied.
8. Compute Receipt instead of blockchain consensus.
9. Public explorer is non-authoritative.
10. Raw outputs are retained for public records.
11. Private jobs are never automatically published.
12. Work-conserving fairness without transferable credits.
13. Central infrastructure may improve UX but may not be required for job scheduling/execution.

Changes to these should require an architecture decision record (ADR).

---

# 60. Architecture Decision Records

Create `docs/adr/` immediately.

Initial ADRs:

```text
0001-use-go-for-agent.md
0002-use-libp2p.md
0003-no-arbitrary-public-code.md
0004-oci-solver-artifacts.md
0005-compute-receipts-not-blockchain.md
0006-explorer-is-non-authoritative.md
0007-public-record-content-addressing.md
0008-network-deny-by-default.md
```

Each ADR:

```text
Context
Decision
Alternatives considered
Consequences
Security implications
Revisit conditions
```

---

# 61. Questions Deliberately Deferred

Do not block MVP on these:

- Is a blockchain ever necessary?
- Should contribution influence priority globally or locally?
- What exact institution verification system should be used?
- Which permanent scientific archive should host records?
- Should public records use IPFS, BitTorrent-like distribution, or custom CAS?
- How should GPU-hour equivalence be defined?
- Should commercial workloads ever join the public federation?
- How should confidential computing be supported?
- What eventual governance foundation should own trademarks/trust roots?

Build enough real usage data before answering them.

---

# 62. Critical Unknowns to Prototype First

The highest-risk technical questions are:

## 62.1 NAT connectivity

Can ordinary university/home/company networks establish sufficiently reliable peer connectivity using libp2p direct + hole punching + relay fallback?

Prototype before building sophisticated scheduling.

## 62.2 Sandbox safety

Can a practical contributor installation offer strong enough isolation without requiring an expert administrator?

## 62.3 GPU containment

Can GPUs be shared securely enough for public untrusted jobs under the chosen runtime?

## 62.4 Scientific artifact portability

Can the same QE artifact produce scientifically consistent results across heterogeneous CPU hardware?

## 62.5 Data persistence

Can public records remain sufficiently replicated without the project quietly becoming a centralized storage provider?

---

# 63. Development Priority Rule

When choosing between features, use this order:

```text
Security
> scientific reproducibility
> protocol correctness
> contributor autonomy
> decentralization
> operational simplicity
> UX convenience
> performance optimization
> feature breadth
```

If a UX feature weakens public-node security, reject it.

---

# 64. Coding Agent Initial Assignment

The coding agent should begin with **Phase 0 and Phase 1 only**.

Do not implement blockchain, explorer, fairness, ORCID, institution federation, or GPU execution in the first iteration.

## Initial milestone

Create a repository in which two Linux machines can perform:

```text
Machine A
pumat submit examples/qe-si-scf.yaml --peer <Machine-B-PeerID>

Machine B
pumat on
```

Expected lifecycle:

```text
A validates and canonicalizes job, computes calc_id
A connects to B
A queries B capability
A sends LeaseRequest
B verifies approved QE solver manifest + artifact digest, offers lease
A countersigns lease (includes per-execution result_recipient_key)
A transfers input bundle from local disk
B re-validates, builds QE input, runs solver in restricted container
B seals output bundle to result_recipient_key, signs completion
B destroys plaintext workspace
A downloads sealed result, verifies sealed + plaintext digests, decrypts
A runs parser locally, signs acceptance, sends it to B
B deletes ciphertext
A stores output, parsed result, and receipt statements
```

## Required code quality

- no placeholder security checks,
- errors must fail closed,
- state transitions persisted,
- every network message versioned,
- cryptographic verification unit-tested,
- README contains exact local two-node reproduction steps,
- integration test runs in CI where feasible.

---

# 65. Definition of Done for v0.1

Pumat v0.1 is done when:

1. A fresh Linux server installs the agent from a signed release.
2. `pumat on` makes it an available worker.
3. Another peer discovers it through the network.
4. A validated Quantum ESPRESSO job can be leased.
5. Worker executes an approved immutable solver artifact.
6. Solver has no external network access.
7. CPU/memory/time limits are enforced.
8. Inputs/results transfer securely.
9. Worker does not retain plaintext job data after successful completion.
10. Both parties can verify a Compute Receipt.
11. Public-mode execution can produce a portable scientific record bundle.
12. A third party can independently verify all bundle hashes and provenance.
13. At least one result is independently re-executed on another worker and compared within declared scientific tolerance.

---

# 66. Reference Technologies and Rationale

These are references, not hard dependencies unless marked above.

## libp2p

Relevant concepts:

- Kademlia DHT for decentralized peer/content routing.
- Circuit Relay v2 for peers that cannot connect directly.
- AutoNAT and hole punching/DCUtR for NAT traversal.

Official documentation:

- https://libp2p.io/docs/kademlia-dht/
- https://docs.libp2p.io/concepts/hole-punching
- https://libp2p.io/docs/circuit-relay/

## OCI / ORAS

OCI descriptors and artifacts are content-addressed by digest and can represent arbitrary artifacts, not only conventional container images. ORAS provides tooling for pushing/pulling OCI artifacts and caching by content identity.

Official documentation:

- https://specs.opencontainers.org/image-spec/descriptor/
- https://oras.land/docs/concepts/artifact/

## Sigstore / Cosign / Rekor

Useful for signing solver artifacts, build provenance, and supply-chain transparency. Rekor is an append-only transparency log for signed metadata; Cosign supports signing and verification of containers/blobs.

Official documentation:

- https://docs.sigstore.dev/cosign/signing/overview/
- https://docs.sigstore.dev/cosign/verifying/verify/
- https://docs.sigstore.dev/logging/overview/

## Firecracker

Potential stronger Linux execution isolation using microVMs. Firecracker uses KVM-based virtualization and a jailer process for additional isolation.

Official documentation:

- https://firecracker-microvm.github.io/

---

# 67. Final Product Thesis

Pumat should not be built as:

> “a cheap decentralized cloud.”

It should be built as:

> **an open protocol and network through which scientific computing resources, reproducible execution, and public scientific data reinforce one another.**

The desired network effect is:

```text
more contributors
      ↓
more available compute
      ↓
more scientific calculations
      ↓
more reproducible public records
      ↓
more useful scientific data
      ↓
more researchers
      ↓
more contributors
```

If this loop emerges, Pumat becomes more than distributed execution software. It becomes an open scientific computing commons whose compute capacity and scientific dataset grow together.

---

# Appendix A. Example Quantum ESPRESSO Job

Normative example: §12.2. It is not duplicated here, so the two cannot drift.

---

# Appendix B. Example Solver Manifest

Normative example: §13.1.

---

# Appendix C. Example Compute Receipt

Normative example: §20.2 (worker completion + requester acceptance statements).

---

# Appendix D. Suggested First 12 GitHub Issues

1. `core: initialize Go monorepo and pumat CLI`
2. `identity: implement Ed25519 node identity persistence`
3. `protocol: define deterministic signed message envelope`
4. `network: establish authenticated libp2p stream between two peers`
5. `capability: detect CPU/RAM and expose signed capability response`
6. `solver: define solver manifest schema and QE test artifact`
7. `solver: verify OCI digest and signature before execution`
8. `job: define strict QuantumEspressoJob YAML schema`
9. `sandbox: execute QE rootless with network disabled and limits`
10. `transfer: implement chunked input bundle stream and HPKE-sealed result fetch`
11. `lease: implement signed lease and persistent state machine`
12. `receipt: implement bilateral Compute Receipt and verification CLI`

Only after these are closed should DHT/global discovery become the next major milestone.

