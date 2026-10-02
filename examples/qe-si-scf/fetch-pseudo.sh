#!/bin/sh
# Download the pinned pseudopotential for this example and verify its digest.
# Pseudopotentials are not committed (§40: redistribute only with license review).
set -eu
cd "$(dirname "$0")"
mkdir -p pseudo
f=Si.pbe-n-rrkjus_psl.1.0.0.UPF
want=669fb75395a9d26973b0ea1ce8223bbcb30d3396c5d48bf5e794d1243c52375a
[ -f "pseudo/$f" ] || curl -fsSL -o "pseudo/$f" "https://pseudopotentials.quantum-espresso.org/upf_files/$f"
got=$( (sha256sum "pseudo/$f" 2>/dev/null || shasum -a 256 "pseudo/$f") | cut -d' ' -f1)
[ "$got" = "$want" ] || { echo "digest mismatch for $f: $got" >&2; exit 1; }
echo "ok: pseudo/$f"
