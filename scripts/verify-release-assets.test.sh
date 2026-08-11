#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture_root="$(mktemp -d)"
trap 'rm -rf "$fixture_root"' EXIT

make_fixture() {
  local version="$1"
  local root="$fixture_root/input"
  rm -rf "$root"
  mkdir -p "$root/bin" "$root/assets"
  printf '#!/bin/sh\necho aurora\n' > "$root/bin/aurora-aiops"
  chmod 0755 "$root/bin/aurora-aiops"
  printf '[Unit]\nDescription=Aurora AIOps\n' > "$root/assets/aurora-aiops.service"
  printf '#!/bin/sh\n' > "$root/assets/migrate-kubejojo-to-aurora-aiops.sh"
  chmod 0755 "$root/assets/migrate-kubejojo-to-aurora-aiops.sh"

  rm -rf "$fixture_root/release"
  mkdir -p "$fixture_root/release"
  for target in linux_amd64 linux_arm64 darwin_arm64; do
    local stem="aurora-aiops_${version}_${target}"
    local package="$fixture_root/package"
    rm -rf "$package"
    mkdir -p "$package/$stem"
    cp "$root/bin/aurora-aiops" "$package/$stem/aurora-aiops"
    cp "$root/assets/aurora-aiops.service" "$package/$stem/aurora-aiops.service"
    cp "$root/assets/migrate-kubejojo-to-aurora-aiops.sh" "$package/$stem/migrate-kubejojo-to-aurora-aiops.sh"
    tar -C "$package" -czf "$fixture_root/release/$stem.tar.gz" "$stem"
  done
  (cd "$fixture_root/release" && sha256sum *.tar.gz > checksums.txt)
}

version="0.1.2"
make_fixture "$version"
"$repo_root/scripts/verify-release-assets.sh" "$fixture_root/release" "$version"

# Duplicate archive members are invalid even when the checksum is correct.
duplicate="$fixture_root/release/aurora-aiops_${version}_linux_amd64.tar.gz"
mkdir -p "$fixture_root/duplicate/aurora-aiops_${version}_linux_amd64"
cp "$fixture_root/input/bin/aurora-aiops" "$fixture_root/duplicate/aurora-aiops_${version}_linux_amd64/aurora-aiops"
tar -C "$fixture_root/duplicate" -cf "$fixture_root/duplicate.tar" "aurora-aiops_${version}_linux_amd64"
tar -C "$fixture_root/duplicate" --append -f "$fixture_root/duplicate.tar" "aurora-aiops_${version}_linux_amd64/aurora-aiops"
gzip -c "$fixture_root/duplicate.tar" > "$duplicate"
(cd "$fixture_root/release" && sha256sum *.tar.gz > checksums.txt)
if "$repo_root/scripts/verify-release-assets.sh" "$fixture_root/release" "$version"; then
  echo "duplicate archive/checksum fixture unexpectedly passed" >&2
  exit 1
fi

make_fixture "$version"
mv "$fixture_root/release/aurora-aiops_${version}_linux_arm64.tar.gz" "$fixture_root/release/missing.tar.gz"
if "$repo_root/scripts/verify-release-assets.sh" "$fixture_root/release" "$version"; then
  echo "missing linux architecture fixture unexpectedly passed" >&2
  exit 1
fi

make_fixture "$version"
printf '0000000000000000000000000000000000000000000000000000000000000000  aurora-aiops_%s_linux_amd64.tar.gz\n' "$version" > "$fixture_root/release/checksums.txt"
if "$repo_root/scripts/verify-release-assets.sh" "$fixture_root/release" "$version"; then
  echo "mismatched digest fixture unexpectedly passed" >&2
  exit 1
fi

make_fixture "$version"
traversal="$fixture_root/release/aurora-aiops_${version}_linux_amd64.tar.gz"
mkdir -p "$fixture_root/traversal/root"
printf 'bad' > "$fixture_root/traversal/root/..-escape"
python3 - "$traversal" <<'PY'
import io
import sys
import tarfile

with tarfile.open(sys.argv[1], "w:gz") as archive:
    payload = b"bad"
    member = tarfile.TarInfo("../escape")
    member.size = len(payload)
    archive.addfile(member, io.BytesIO(payload))
PY
if "$repo_root/scripts/verify-release-assets.sh" "$fixture_root/release" "$version"; then
  echo "path traversal fixture unexpectedly passed" >&2
  exit 1
fi

echo "Release asset contract tests passed"
