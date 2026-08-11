#!/usr/bin/env bash
set -euo pipefail

mode="dry-run"
root="/"

usage() {
  echo "Usage: $0 [--dry-run|--apply|--rollback] [--root PATH]"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run) mode="dry-run" ;;
    --apply) mode="apply" ;;
    --rollback) mode="rollback" ;;
    --root)
      shift
      [[ $# -gt 0 ]] || { usage >&2; exit 2; }
      root="${1%/}"
      [[ -n "$root" ]] || root="/"
      ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
  shift
done

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/.." && pwd)"
old_dir="$root/opt/kubejojo"
new_dir="$root/opt/aurora-aiops"
old_unit="$root/etc/systemd/system/kubejojo.service"
new_unit="$root/etc/systemd/system/aurora-aiops.service"
state_file="$new_dir/.brand-migration-state"

binary_source="${AURORA_AIOPS_BINARY_SOURCE:-}"
unit_source="${AURORA_AIOPS_UNIT_SOURCE:-}"

resolve_sources() {
  if [[ -z "$binary_source" ]]; then
    if [[ -f "$script_dir/aurora-aiops" ]]; then
      binary_source="$script_dir/aurora-aiops"
    else
      binary_source="$(find "$repo_root/server/dist/release" -mindepth 2 -maxdepth 2 -type f -name aurora-aiops -print 2>/dev/null | sort | tail -n 1)"
    fi
  fi
  if [[ -z "$unit_source" ]]; then
    if [[ -f "$script_dir/aurora-aiops.service" ]]; then
      unit_source="$script_dir/aurora-aiops.service"
    else
      unit_source="$repo_root/deploy/aurora-aiops.service"
    fi
  fi
  [[ -f "$binary_source" ]] || { echo "Aurora binary not found: $binary_source" >&2; exit 1; }
  [[ -f "$unit_source" ]] || { echo "Aurora unit not found: $unit_source" >&2; exit 1; }
}

require_old_installation() {
  [[ -d "$old_dir" ]] || { echo "Legacy installation not found: $old_dir" >&2; exit 1; }
}

if [[ "$mode" == "dry-run" ]]; then
  require_old_installation
  resolve_sources
  echo "Dry run only; no files or services will be changed."
  echo "Copy: $old_dir -> $new_dir"
  echo "Install binary: $binary_source -> $new_dir/aurora-aiops"
  echo "Rename database to aurora-aiops.db"
  echo "Install unit: $new_unit"
  echo "Keep the legacy installation intact for rollback"
  exit 0
fi

if [[ "$root" == "/" && ${EUID:-$(id -u)} -ne 0 ]]; then
  echo "--apply and --rollback require root for a live system" >&2
  exit 1
fi

if [[ "$mode" == "apply" ]]; then
  require_old_installation
  resolve_sources
  [[ ! -e "$new_dir" ]] || { echo "Target already exists: $new_dir" >&2; exit 1; }

  old_service_was_active="false"
  if [[ "$root" == "/" ]] && command -v systemctl >/dev/null 2>&1; then
    if systemctl is-active --quiet kubejojo.service; then
      old_service_was_active="true"
      systemctl stop kubejojo.service
    fi
  fi

  mkdir -p "$new_dir"
  cp -a "$old_dir/." "$new_dir/"
  if [[ -f "$new_dir/kubejojo" ]]; then
    mv "$new_dir/kubejojo" "$new_dir/.legacy-binary"
  fi
  install -m 0755 "$binary_source" "$new_dir/aurora-aiops"
  if [[ -f "$new_dir/data/kubejojo.db" && ! -e "$new_dir/data/aurora-aiops.db" ]]; then
    mv "$new_dir/data/kubejojo.db" "$new_dir/data/aurora-aiops.db"
  fi
  printf 'legacy_service_was_active=%s\n' "$old_service_was_active" > "$state_file"
  chmod 0600 "$state_file"
  mkdir -p "$(dirname "$new_unit")"
  install -m 0644 "$unit_source" "$new_unit"

  if [[ "$root" == "/" ]]; then
    if ! getent group aurora-aiops >/dev/null; then
      groupadd --system aurora-aiops
    fi
    if ! id aurora-aiops >/dev/null 2>&1; then
      useradd --system --gid aurora-aiops --home-dir /opt/aurora-aiops --shell /usr/sbin/nologin aurora-aiops
    fi
    chown -R aurora-aiops:aurora-aiops "$new_dir"
    systemctl daemon-reload
    systemctl enable --now aurora-aiops.service
    systemctl is-active --quiet aurora-aiops.service || {
      echo "Aurora AIOps did not become active; run $0 --rollback" >&2
      exit 1
    }
  fi

  echo "Migration applied. Legacy data remains at $old_dir for rollback."
  exit 0
fi

[[ -d "$new_dir" ]] || { echo "Aurora installation not found: $new_dir" >&2; exit 1; }
[[ -f "$state_file" ]] || { echo "Migration state not found: $state_file" >&2; exit 1; }

old_service_was_active="$(sed -n 's/^legacy_service_was_active=//p' "$state_file")"
rollback_suffix="$(date -u +%Y%m%dT%H%M%SZ)"
rollback_dir="$root/opt/aurora-aiops.rollback-$rollback_suffix"

if [[ "$root" == "/" ]] && command -v systemctl >/dev/null 2>&1; then
  systemctl disable --now aurora-aiops.service >/dev/null 2>&1 || true
fi

mv "$new_dir" "$rollback_dir"
if [[ -f "$new_unit" ]]; then
  mv "$new_unit" "$new_unit.rollback-$rollback_suffix"
fi

if [[ "$root" == "/" ]] && command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload
  if [[ "$old_service_was_active" == "true" && -f "$old_unit" ]]; then
    systemctl start kubejojo.service
  fi
fi

echo "Rollback complete. Aurora files were preserved at $rollback_dir."
