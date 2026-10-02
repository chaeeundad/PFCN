#!/bin/sh
# Pumat QE entrypoint wrapper (spec §39.1).
# Mounts provided by the worker:
#   /work/in      read-only   pw.in
#   /work/pseudo  read-only   verified pseudopotentials
#   /work/scratch read-write  QE outdir (not returned)
#   /work/out     read-write  returned output bundle
set -eu

ep="${1:-}"
case "$ep" in
  pw.x) ;;
  *) echo "pumat-entry: unknown entrypoint '$ep'" >&2; exit 64 ;;
esac

np="${PUMAT_NPROCS:-1}"
nk="${PUMAT_POOLS:-1}"
case "$np$nk" in
  *[!0-9]*|"") echo "pumat-entry: invalid PUMAT_NPROCS/PUMAT_POOLS" >&2; exit 64 ;;
esac

export HOME=/tmp
export OMP_NUM_THREADS=1
export OPENBLAS_NUM_THREADS=1
# Pin BLAS dispatch to a baseline core type for cross-hardware reproducibility.
case "$(uname -m)" in
  x86_64)  export OPENBLAS_CORETYPE=NEHALEM ;;
  aarch64) export OPENBLAS_CORETYPE=ARMV8 ;;
esac
# CMA single-copy needs ptrace, which the sandbox drops.
export OMPI_MCA_btl_vader_single_copy_mechanism=none

cd /work/scratch
status=0
if [ "$np" -eq 1 ]; then
  /opt/qe/bin/pw.x -nk "$nk" -in /work/in/pw.in >/work/out/stdout.txt 2>/work/out/stderr.txt || status=$?
else
  mpirun --np "$np" --bind-to none \
    /opt/qe/bin/pw.x -nk "$nk" -in /work/in/pw.in >/work/out/stdout.txt 2>/work/out/stderr.txt || status=$?
fi

if [ -f /work/scratch/pumat.save/data-file-schema.xml ]; then
  cp /work/scratch/pumat.save/data-file-schema.xml /work/out/data-file-schema.xml
fi
exit "$status"
