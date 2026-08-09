#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture_root="$(mktemp -d)"
trap 'rm -rf "$fixture_root"' EXIT

mkdir -p "$fixture_root/opt/kubejojo/data"
mkdir -p "$fixture_root/staged"
printf 'legacy-binary' > "$fixture_root/opt/kubejojo/kubejojo"
printf 'database' > "$fixture_root/opt/kubejojo/data/kubejojo.db"
printf 'new-aurora-binary' > "$fixture_root/staged/aurora-aiops"
printf '[Unit]\nDescription=Aurora AIOps\n' > "$fixture_root/staged/aurora-aiops.service"

export AURORA_AIOPS_BINARY_SOURCE="$fixture_root/staged/aurora-aiops"
export AURORA_AIOPS_UNIT_SOURCE="$fixture_root/staged/aurora-aiops.service"

"$repo_root/scripts/migrate-kubejojo-to-aurora-aiops.sh" --dry-run --root "$fixture_root"
test ! -e "$fixture_root/opt/aurora-aiops"

"$repo_root/scripts/migrate-kubejojo-to-aurora-aiops.sh" --apply --root "$fixture_root"
test -f "$fixture_root/opt/aurora-aiops/aurora-aiops"
test "$(cat "$fixture_root/opt/aurora-aiops/aurora-aiops")" = "new-aurora-binary"
test -f "$fixture_root/opt/aurora-aiops/data/aurora-aiops.db"
test -f "$fixture_root/opt/aurora-aiops/.brand-migration-state"
test -f "$fixture_root/etc/systemd/system/aurora-aiops.service"
test -f "$fixture_root/opt/kubejojo/data/kubejojo.db"

"$repo_root/scripts/migrate-kubejojo-to-aurora-aiops.sh" --rollback --root "$fixture_root"
test ! -e "$fixture_root/opt/aurora-aiops"
find "$fixture_root/opt" -maxdepth 1 -type d -name 'aurora-aiops.rollback-*' | grep -q .
test -f "$fixture_root/opt/kubejojo/data/kubejojo.db"

echo "Brand migration test passed"
