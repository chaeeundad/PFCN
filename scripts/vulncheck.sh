#!/usr/bin/env bash
# Runs govulncheck and fails on any called vulnerability that is not listed
# (with a justification) in .govulncheck-accepted.
set -euo pipefail
cd "$(dirname "$0")/.."
accepted=$(grep -v '^#' .govulncheck-accepted | awk '{print $1}' | grep . || true)
out=$(go run golang.org/x/vuln/cmd/govulncheck@latest -format json ./...)
called=$(printf '%s' "$out" | python3 -c '
import json, sys
dec = json.JSONDecoder(); s = sys.stdin.read(); i = 0; ids = set()
while i < len(s):
    while i < len(s) and s[i].isspace(): i += 1
    if i >= len(s): break
    obj, i = dec.raw_decode(s, i)
    f = obj.get("finding")
    if f and f.get("trace") and f["trace"][0].get("function"):
        ids.add(f["osv"])
print("\n".join(sorted(ids)))
')
fail=0
for id in $called; do
  if printf '%s\n' "$accepted" | grep -qx "$id"; then
    echo "accepted: $id (see .govulncheck-accepted)"
  else
    echo "VULNERABLE: $id is reachable from Pumat code"; fail=1
  fi
done
[ -n "$called" ] || echo "no reachable vulnerabilities"
exit $fail
