#!/usr/bin/env bash
set -euo pipefail

command -v goreleaser >/dev/null 2>&1 || {
  echo "goreleaser is required" >&2
  exit 1
}

goreleaser check
rm -rf dist
goreleaser release --snapshot --clean

test -s dist/checksums.txt || {
  echo "missing dist/checksums.txt" >&2
  exit 1
}

archives=()
while IFS= read -r archive; do
  [[ -n "$archive" ]] && archives+=("$archive")
done < <(find dist -maxdepth 1 -type f \( -name 'forgeai_*_linux_amd64.tar.gz' -o -name 'forgeai_*_linux_arm64.tar.gz' -o -name 'forgeai_*_darwin_arm64.tar.gz' -o -name 'forgeai_*_windows_amd64.zip' \) | sort)

if [[ "${#archives[@]}" -ne 4 ]]; then
  echo "expected exactly four supported archives, found ${#archives[@]}" >&2
  printf '%s\n' "${archives[@]}" >&2
  exit 1
fi

if find dist -maxdepth 1 -type f \( -name 'forgeai_*_darwin_amd64.*' -o -name 'forgeai_*_windows_arm64.*' \) | grep -q .; then
  echo "unexpected unsupported archive target found" >&2
  exit 1
fi

echo "release archives:"
printf '  %s\n' "${archives[@]}"

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$arch" in
  x86_64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
esac

case "$os/$arch" in
  linux/amd64|linux/arm64|darwin/arm64)
    archive="$(find dist -maxdepth 1 -type f -name "forgeai_*_${os}_${arch}.tar.gz" | head -n1)"
    tmp="$(mktemp -d)"
    trap 'rm -rf "$tmp"' EXIT
    tar -xzf "$archive" -C "$tmp"
    binary="$(find "$tmp" -type f -name forgeai | head -n1)"
    test -n "$binary"
    "$binary" version
    ;;
  *)
    echo "host $os/$arch is not a v0.1 native smoke target; archive validation completed"
    ;;
esac

echo "release smoke passed"
