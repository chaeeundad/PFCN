#!/usr/bin/env bash
# Local two-node end-to-end demo (spec §64 initial milestone) on one machine:
#   - builds the QE solver image and pushes it to a local registry
#   - signs a dev solver manifest with a throwaway dev signing key
#   - starts requester (A) and worker (B) agents
#   - submits the Silicon SCF example from A to B and verifies the receipt
#
# Requirements: Docker or Podman, Go, curl. Usage: make e2e  (or scripts/dev-e2e.sh)
# Env: E2E_DIR (default ./.e2e), ENGINE (docker|podman, default docker), KEEP=1 keeps agents running.
set -euo pipefail
cd "$(dirname "$0")/.."

ENGINE=${ENGINE:-docker}
E=${E2E_DIR:-$PWD/.e2e}
REG=localhost:5000
IMG=$REG/pumat-solvers/quantum-espresso
PUMAT=./bin/pumat
PORT_A=${PORT_A:-4202}
PORT_B=${PORT_B:-4201}

[ -x "$PUMAT" ] || make build
examples/qe-si-scf/fetch-pseudo.sh

echo "==> solver image"
"$ENGINE" build -t pumat-qe:dev -f solvers/quantum-espresso/Containerfile solvers/quantum-espresso
if ! "$ENGINE" ps --format '{{.Names}}' | grep -q '^pumat-registry$'; then
  "$ENGINE" rm -f pumat-registry >/dev/null 2>&1 || true
  "$ENGINE" run -d --name pumat-registry -p 5000:5000 registry:2 >/dev/null
  sleep 2
fi
"$ENGINE" tag pumat-qe:dev "$IMG:7.4.1"
"$ENGINE" push "$IMG:7.4.1" >/dev/null
ARCH=$("$ENGINE" info --format '{{.Architecture}}' 2>/dev/null || "$ENGINE" info --format '{{.Host.Arch}}')
case "$ARCH" in x86_64|amd64) ARCH=amd64 ;; aarch64|arm64) ARCH=arm64 ;; esac
# Resolve the platform-specific manifest digest from the registry (from the daemon's network).
INDEX=$("$ENGINE" run --rm --network host curlimages/curl -s \
  -H 'Accept: application/vnd.oci.image.index.v1+json' -H 'Accept: application/vnd.oci.image.manifest.v1+json' \
  "http://$REG/v2/pumat-solvers/quantum-espresso/manifests/7.4.1")
DIGEST=$(printf '%s' "$INDEX" | python3 -c '
import json,sys
arch=sys.argv[1]; m=json.load(sys.stdin)
if "manifests" not in m:
    print("INDEX_IS_MANIFEST"); sys.exit()
for d in m["manifests"]:
    p=d.get("platform",{})
    if p.get("os")=="linux" and p.get("architecture")==arch:
        print(d["digest"]); break
' "$ARCH")
if [ "$DIGEST" = "INDEX_IS_MANIFEST" ]; then
  DIGEST="sha256:$(printf '%s' "$INDEX" | (sha256sum 2>/dev/null || shasum -a 256) | cut -d' ' -f1)"
fi
echo "    linux/$ARCH manifest: $DIGEST"

echo "==> dev signing key + manifest"
mkdir -p "$E"
KEY="$E/dev-solver-signing.key"
[ -f "$KEY" ] || "$PUMAT" solver keygen --out "$KEY" >/dev/null
PARSER=$( (sha256sum internal/parser/artifacts/qe-parser.wasm 2>/dev/null || shasum -a 256 internal/parser/artifacts/qe-parser.wasm) | cut -d' ' -f1)
sed -e "s#@ARCH@#$ARCH#" -e "s#@ARTIFACT_DIGEST@#$DIGEST#" -e "s#@PARSER_DIGEST@#$PARSER#" \
    solvers/quantum-espresso/manifest.dev.yaml.in > "$E/manifest.dev.yaml"
SIGN_OUT=$("$PUMAT" solver sign "$E/manifest.dev.yaml" --key "$KEY" --out "$E/manifest.dev.signed.json")
MANIFEST=$(printf '%s\n' "$SIGN_OUT" | awk '/Manifest digest/{print $3}')
SIGNER=$(printf '%s\n' "$SIGN_OUT" | awk '/^Signer/{print $2}')
sed "s/sha256:REPLACE_WITH_MANIFEST_DIGEST/$MANIFEST/" examples/qe-si-scf/job.yaml > examples/qe-si-scf/job.dev.yaml

echo "==> nodes"
for n in A B; do
  [ -f "$E/$n/config.yaml" ] || "$PUMAT" --home "$E/$n" init >/dev/null
done
python3 - "$E" "$SIGNER" "$PORT_A" "$PORT_B" <<'PY'
import re, sys
E, signer, pa, pb = sys.argv[1:]
for n, port, cpu in (("A", pa, 1), ("B", pb, 2)):
    p = f"{E}/{n}/config.yaml"; s = open(p).read()
    s = re.sub(r"/udp/\d+/quic-v1", f"/udp/{port}/quic-v1", s)
    s = re.sub(r"/tcp/\d+", f"/tcp/{port}", s)
    s = re.sub(r"cpu: \d+", f"cpu: {cpu}", s, count=1)
    s = re.sub(r"memory: \S+", "memory: 4GiB", s, count=1)
    s = re.sub(r"solverSigners:\n(\s+- .*\n)+", f"solverSigners:\n        - {signer}\n", s)
    s = re.sub(r"resultsDir: .*", f"resultsDir: {E}/{n}/results", s)
    open(p, "w").write(s)
PY
for n in A B; do "$PUMAT" --home "$E/$n" solver add "$E/manifest.dev.signed.json" >/dev/null; done

pkill -f "pumat --home $E/" 2>/dev/null || true
sleep 1
"$PUMAT" --home "$E/B" agent >"$E/B.log" 2>&1 &
"$PUMAT" --home "$E/A" agent >"$E/A.log" 2>&1 &
trap '[ "${KEEP:-0}" = 1 ] || pkill -f "pumat --home $E/" 2>/dev/null || true' EXIT
sleep 3
"$PUMAT" --home "$E/B" on >/dev/null
WORKER=$("$PUMAT" --home "$E/B" id)

echo "==> submit"
"$PUMAT" --home "$E/A" submit examples/qe-si-scf/job.dev.yaml --peer "/ip4/127.0.0.1/udp/$PORT_B/quic-v1/p2p/$WORKER"
EXEC=$("$PUMAT" --home "$E/A" job list | awk 'NR==2{print $1}')
"$PUMAT" --home "$E/A" receipt verify "${EXEC#pumat:exec:blake3:}" >/dev/null && echo "==> receipt verified"
"$PUMAT" --home "$E/A" ledger verify
"$PUMAT" --home "$E/B" ledger verify
left=$(find "$E/B/work" -mindepth 1 | wc -l | tr -d ' ')
[ "$left" = 0 ] && echo "==> worker retained no job data" || { echo "worker left $left files"; exit 1; }
