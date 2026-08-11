#!/usr/bin/env bash

set -euo pipefail

die() {
  echo "verify-release-assets: $*" >&2
  exit 1
}

if [[ $# -ne 2 ]]; then
  die "usage: $0 <release-directory> <version>"
fi

release_dir="$1"
version="$2"
[[ -d "$release_dir" ]] || die "release directory does not exist: $release_dir"
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+([-.+][0-9A-Za-z.-]+)?$ ]] || die "invalid version: $version"

if command -v sha256sum >/dev/null 2>&1; then
  checksum_cmd=(sha256sum)
elif command -v shasum >/dev/null 2>&1; then
  checksum_cmd=(shasum -a 256)
else
  die "neither sha256sum nor shasum is available"
fi

expected_names=(
  "aurora-aiops_${version}_linux_amd64.tar.gz"
  "aurora-aiops_${version}_linux_arm64.tar.gz"
  "aurora-aiops_${version}_darwin_arm64.tar.gz"
)

[[ ! -L "$release_dir/latest" ]] || die "latest symlink is not a release asset"
[[ -f "$release_dir/checksums.txt" ]] || die "missing checksums.txt"

shopt -s nullglob
archive_files=("$release_dir"/*.tar.gz)
shopt -u nullglob
(( ${#archive_files[@]} == ${#expected_names[@]} )) || die "expected ${#expected_names[@]} archives, found ${#archive_files[@]}"
for archive_path in "${archive_files[@]}"; do
  archive_name="$(basename "$archive_path")"
  case "$archive_name" in
    "aurora-aiops_${version}_linux_amd64.tar.gz"|"aurora-aiops_${version}_linux_arm64.tar.gz"|"aurora-aiops_${version}_darwin_arm64.tar.gz")
      ;;
    *)
      die "unexpected archive name: $archive_name"
      ;;
  esac
done
for archive_name in "${expected_names[@]}"; do
  [[ -f "$release_dir/$archive_name" ]] || die "missing archive: $archive_name"
done

checksum_names=()
checksum_values=()
checksum_lines=0
while IFS= read -r checksum_line || [[ -n "$checksum_line" ]]; do
  [[ -n "$checksum_line" ]] || die "blank line in checksums.txt"
  if [[ "$checksum_line" =~ ^([[:xdigit:]]{64})[[:space:]]+\*?([^[:space:]]+)$ ]]; then
    digest="${BASH_REMATCH[1]}"
    checksum_name="${BASH_REMATCH[2]}"
  else
    die "malformed checksum entry: $checksum_line"
  fi
  for known_name in "${checksum_names[@]-}"; do
    [[ "$known_name" != "$checksum_name" ]] || die "duplicate checksum entry: $checksum_name"
  done
  checksum_names+=("$checksum_name")
  checksum_values+=("$(printf '%s' "$digest" | tr '[:upper:]' '[:lower:]')")
  checksum_lines=$((checksum_lines + 1))
done < "$release_dir/checksums.txt"
(( checksum_lines == ${#expected_names[@]} )) || die "checksums.txt must contain exactly ${#expected_names[@]} entries"

for checksum_name in "${expected_names[@]}"; do
  checksum_index=-1
  index=0
  while (( index < ${#checksum_names[@]} )); do
    if [[ "${checksum_names[$index]}" == "$checksum_name" ]]; then
      checksum_index="$index"
      break
    fi
    index=$((index + 1))
  done
  (( checksum_index >= 0 )) || die "missing checksum entry: $checksum_name"
  archive_path="$release_dir/$checksum_name"
  actual_digest="$("${checksum_cmd[@]}" "$archive_path" | awk '{print tolower($1)}')"
  [[ "$actual_digest" == "${checksum_values[$checksum_index]}" ]] || die "checksum mismatch: $checksum_name"

  member_listing="$(tar -tzf "$archive_path")" || die "cannot read archive: $checksum_name"
  [[ -n "$member_listing" ]] || die "empty archive: $checksum_name"
  stem="${checksum_name%.tar.gz}"
  seen_members=()
  member_count=0
  has_binary=0
  has_service=0
  has_migration=0
  while IFS= read -r member || [[ -n "$member" ]]; do
    member_count=$((member_count + 1))
    [[ "$member" != /* ]] || die "absolute archive member in $checksum_name: $member"
    [[ "$member" != *..* ]] || die "path traversal archive member in $checksum_name: $member"
    member_name="${member%/}"
    [[ -n "$member_name" ]] || die "empty archive member in $checksum_name"
    for known_member in "${seen_members[@]-}"; do
      [[ "$known_member" != "$member_name" ]] || die "duplicate archive member in $checksum_name: $member_name"
    done
    seen_members+=("$member_name")
    top_level="${member_name%%/*}"
    [[ "$top_level" == "$stem" ]] || die "archive member is outside versioned root in $checksum_name: $member"
    case "$member_name" in
      "$stem/aurora-aiops") has_binary=1 ;;
      "$stem/aurora-aiops.service") has_service=1 ;;
      "$stem/migrate-kubejojo-to-aurora-aiops.sh") has_migration=1 ;;
    esac
  done <<< "$member_listing"
  (( member_count > 0 )) || die "empty archive: $checksum_name"

  # A release archive must not carry symlinks, especially a mutable `latest` pointer.
  if tar -tvzf "$archive_path" | awk '$1 ~ /^l/ { exit 1 }'; then
    :
  else
    die "symlink archive member in $checksum_name"
  fi

  if [[ "$checksum_name" == *"_linux_"* ]]; then
    (( has_binary == 1 )) || die "missing executable aurora-aiops in $checksum_name"
    (( has_service == 1 )) || die "missing aurora-aiops.service in $checksum_name"
    (( has_migration == 1 )) || die "missing migration script in $checksum_name"
    mode_line="$(tar -tvzf "$archive_path" "$stem/aurora-aiops" | head -n 1)"
    mode="${mode_line%% *}"
    [[ "$mode" =~ ^-[rwx-]{9}$ ]] || die "aurora-aiops is not a regular file in $checksum_name"
    [[ "$mode" == *x* ]] || die "aurora-aiops is not executable in $checksum_name"
  fi
done

echo "Release assets verified: $version"
