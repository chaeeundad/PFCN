#!/bin/sh
# Pumat installer (spec §42.1).
#
#   curl -fsSL https://raw.githubusercontent.com/chaeeundad/PFCN/main/scripts/install.sh | sh
#
# Safer: download this script, read it, then run it. It
#   1. detects OS/architecture
#   2. downloads the release archive and SHA256SUMS
#   3. verifies the Sigstore signature of SHA256SUMS when cosign is installed
#      (pinned to this repository's release workflow), and the archive checksum
#   4. installs pumat to $PREFIX/bin (default /usr/local)
#   5. on Linux as root: creates the unprivileged `pumat` user and the systemd unit
#   6. never enables resource sharing: run `pumat on` yourself
#
# Env: PUMAT_VERSION (default: latest), PREFIX, REQUIRE_SIGNATURE=1 (fail without cosign)
set -eu

REPO=chaeeundad/PFCN
PREFIX=${PREFIX:-/usr/local}
say() { printf '%s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || die "$1 is required"; }

need curl; need tar; need uname
os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in linux|darwin) ;; *) die "unsupported OS: $os (Windows workers are not supported yet)" ;; esac
arch=$(uname -m)
case "$arch" in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; *) die "unsupported architecture: $arch" ;; esac

version=${PUMAT_VERSION:-}
if [ -z "$version" ]; then
  version=$(curl -fsSL "https://api.github.com/repos/$REPO/releases?per_page=1" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1)
  [ -n "$version" ] || die "could not determine the latest release"
fi
name="pumat_${version}_${os}_${arch}"
base="https://github.com/$REPO/releases/download/$version"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

say "Downloading pumat $version for $os/$arch"
curl -fsSL -o "$tmp/$name.tar.gz" "$base/$name.tar.gz"
curl -fsSL -o "$tmp/SHA256SUMS" "$base/SHA256SUMS"
curl -fsSL -o "$tmp/SHA256SUMS.sigstore.json" "$base/SHA256SUMS.sigstore.json"

if command -v cosign >/dev/null 2>&1; then
  cosign verify-blob --bundle "$tmp/SHA256SUMS.sigstore.json" \
    --certificate-identity-regexp "^https://github.com/$REPO/.github/workflows/release.yml@refs/tags/v" \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    "$tmp/SHA256SUMS" >/dev/null 2>&1 || die "SHA256SUMS signature verification FAILED"
  say "Signature verified (Sigstore, release workflow of $REPO)"
elif [ "${REQUIRE_SIGNATURE:-0}" = 1 ]; then
  die "cosign not found and REQUIRE_SIGNATURE=1"
else
  say "warning: cosign not installed; verifying checksum only (install cosign for signature verification)"
fi

want=$(grep " $name.tar.gz\$" "$tmp/SHA256SUMS" | cut -d' ' -f1)
[ -n "$want" ] || die "no checksum for $name.tar.gz"
if command -v sha256sum >/dev/null 2>&1; then got=$(sha256sum "$tmp/$name.tar.gz" | cut -d' ' -f1)
else got=$(shasum -a 256 "$tmp/$name.tar.gz" | cut -d' ' -f1); fi
[ "$want" = "$got" ] || die "checksum mismatch for $name.tar.gz"
say "Checksum verified"

tar -xzf "$tmp/$name.tar.gz" -C "$tmp"
sudo=""
[ -w "$PREFIX/bin" ] || sudo="sudo"
$sudo install -m 0755 "$tmp/$name/pumat" "$PREFIX/bin/pumat"
say "Installed $PREFIX/bin/pumat"

if [ "$os" = linux ] && [ "$(id -u)" = 0 ] && command -v systemctl >/dev/null 2>&1; then
  if ! id pumat >/dev/null 2>&1; then
    useradd --system --create-home --home-dir /var/lib/pumat --shell /usr/sbin/nologin pumat
  fi
  # Rootless Podman needs subordinate IDs for the service user.
  grep -q '^pumat:' /etc/subuid 2>/dev/null || echo "pumat:200000:65536" >> /etc/subuid
  grep -q '^pumat:' /etc/subgid 2>/dev/null || echo "pumat:200000:65536" >> /etc/subgid
  command -v loginctl >/dev/null 2>&1 && loginctl enable-linger pumat || true
  curl -fsSL -o /etc/systemd/system/pumat-agent.service \
    "https://raw.githubusercontent.com/$REPO/$version/deploy/systemd/pumat-agent.service"
  su -s /bin/sh pumat -c "PUMAT_HOME=/var/lib/pumat $PREFIX/bin/pumat init" >/dev/null
  systemctl daemon-reload
  say "Created user 'pumat' and pumat-agent.service (not started)."
  say "Next: systemctl enable --now pumat-agent && sudo -u pumat PUMAT_HOME=/var/lib/pumat pumat on"
else
  say "Next: pumat init && pumat agent   (then \`pumat on\` to contribute)"
fi
say "Resource sharing is OFF until you run \`pumat on\`."
