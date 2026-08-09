#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

allowed='^(server/internal/config/legacy_compat\.go|server/internal/config/config_test\.go|server/internal/server/legacy_compat\.go|server/internal/server/session_middleware_test\.go|web/src/stores/legacyStorageCompat\.ts|web/src/stores/appStore\.test\.ts|web/src/app/documentTitle\.test\.ts|web/buildEnvCompat\.ts|web/buildEnvCompat\.test\.ts|scripts/migrate-kubejojo-to-aurora-aiops\.sh|scripts/test-brand-migration\.sh|docs/migrations/kubejojo-to-aurora-aiops\.md|scripts/verify-brand-rename\.sh):|^(README\.md|scripts/build-release\.sh):[0-9]+:.*(migrate-kubejojo-to-aurora-aiops|kubejojo-to-aurora-aiops)'

violations="$(git grep -In -i 'kubejojo' -- . | grep -Ev "$allowed" || true)"
if [[ -n "$violations" ]]; then
  echo "Unexpected legacy brand references:" >&2
  echo "$violations" >&2
  exit 1
fi

echo "Aurora AIOps brand guard passed"
