#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
unit="$repo_root/deploy/aurora-aiops.service"

[[ -f "$unit" ]] || { echo "missing $unit" >&2; exit 1; }
grep -q '^Description=Aurora AIOps ' "$unit"
grep -q '^User=aurora-aiops$' "$unit"
grep -q '^Group=aurora-aiops$' "$unit"
grep -q '^WorkingDirectory=/opt/aurora-aiops$' "$unit"
grep -q '^ExecStart=/opt/aurora-aiops/aurora-aiops$' "$unit"
grep -q '^Environment=AURORA_AIOPS_AIOPS_DB=/opt/aurora-aiops/data/aurora-aiops.db$' "$unit"

if command -v systemd-analyze >/dev/null 2>&1; then
  systemd-analyze verify "$unit"
else
  echo "systemd-analyze unavailable; static unit checks passed"
fi
