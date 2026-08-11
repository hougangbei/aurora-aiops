#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

allowed='^(server/internal/config/legacy_compat\.go|server/internal/config/config_test\.go|server/internal/server/legacy_compat\.go|server/internal/server/session_middleware_test\.go|web/src/stores/legacyStorageCompat\.ts|web/src/stores/appStore\.test\.ts|web/src/app/documentTitle\.test\.ts|web/buildEnvCompat\.ts|web/buildEnvCompat\.test\.ts|scripts/migrate-kubejojo-to-aurora-aiops\.sh|scripts/test-brand-migration\.sh|docs/migrations/kubejojo-to-aurora-aiops\.md|scripts/verify-brand-rename\.sh):|^docs/architecture/asset-inventory-development\.md:[0-9]+:.*KUBEJOJO_ASSET_(ENCRYPTION_KEY|COLLECT_INTERVAL)|^(README\.md|scripts/build-release\.sh):[0-9]+:.*(migrate-kubejojo-to-aurora-aiops|kubejojo-to-aurora-aiops)'

violations="$(git grep -In -i 'kubejojo' -- . | grep -Ev "$allowed" || true)"
if [[ -n "$violations" ]]; then
  echo "Unexpected legacy brand references:" >&2
  echo "$violations" >&2
  exit 1
fi

echo "Aurora AIOps brand guard passed"

legacy_owner_allowed='^docs/superpowers/plans/2026-08-09-aurora-project-installation\.md:124:.*without.*heihuzicity-tech|^docs/superpowers/plans/2026-08-09-aurora-project-installation\.md:269:.*never.*heihuzicity-tech|^docs/superpowers/plans/2026-08-09-aurora-project-installation\.md:282:git grep -n '\''heihuzicity-tech'\'' -- .*'
legacy_owner_references="$(git grep -In 'heihuzicity-tech' -- . ':(exclude)scripts/verify-brand-rename.sh' | grep -Ev "$legacy_owner_allowed" || true)"
if [[ -n "$legacy_owner_references" ]]; then
  echo "Unexpected legacy repository owner references:" >&2
  echo "$legacy_owner_references" >&2
  exit 1
fi

echo "Repository ownership guard passed"
