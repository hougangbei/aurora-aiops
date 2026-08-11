#!/usr/bin/env bash

set -euo pipefail
umask 022

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WEB_DIR="$ROOT_DIR/web"
SERVER_DIR="$ROOT_DIR/server"
SERVICE_FILE="$ROOT_DIR/deploy/aurora-aiops.service"

VERSION_FILE="$SERVER_DIR/cmd/aurora-aiops/VERSION"
VERSION="${VERSION:-$(tr -d '[:space:]' < "$VERSION_FILE")}"
COMMIT="${COMMIT:-$(git -C "$ROOT_DIR" rev-parse --short=12 HEAD)}"
DATE="${DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
GOOS="${GOOS:-$(go env GOOS)}"
GOARCH="${GOARCH:-$(go env GOARCH)}"

ASSET_OUT_DIR="$SERVER_DIR/internal/web/dist/app"
RELEASE_DIR="$SERVER_DIR/dist/release"
PACKAGE_STEM="aurora-aiops_${VERSION}_${GOOS}_${GOARCH}"
PACKAGE_DIR="$RELEASE_DIR/$PACKAGE_STEM"
ARCHIVE_PATH="$RELEASE_DIR/${PACKAGE_STEM}.tar.gz"
CHECKSUM_PATH="$RELEASE_DIR/checksums.txt"

if [[ -z "$VERSION" ]]; then
  echo "VERSION is empty" >&2
  exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
  CHECKSUM_CMD=(sha256sum)
elif command -v shasum >/dev/null 2>&1; then
  CHECKSUM_CMD=(shasum -a 256)
else
  echo "Neither sha256sum nor shasum is available" >&2
  exit 1
fi

echo "==> Release build"
echo "Version:   $VERSION"
echo "Commit:    $COMMIT"
echo "Date:      $DATE"
echo "Platform:  $GOOS/$GOARCH"

if [[ "${SKIP_NPM_INSTALL:-0}" != "1" ]]; then
  echo "==> Installing frontend dependencies"
  NPM_INSTALL_MODE="${NPM_INSTALL_MODE:-install}"
  (
    cd "$WEB_DIR"
    npm "$NPM_INSTALL_MODE"
  )
fi

# 门禁：前端测试、后端 vet + 测试任一失败立即退出，不产出 release。
echo "==> Verifying frontend tests"
(
  cd "$WEB_DIR"
  npm test
)
echo "==> Verifying backend vet + tests"
(
  cd "$SERVER_DIR"
  go vet ./...
  go test ./...
)

echo "==> Verifying Aurora naming and migration"
"$ROOT_DIR/scripts/verify-brand-rename.sh"
"$ROOT_DIR/scripts/test-brand-migration.sh"
"$ROOT_DIR/scripts/verify-systemd-service.sh"

echo "==> Building frontend"
rm -rf "$ASSET_OUT_DIR"
mkdir -p "$ASSET_OUT_DIR"
(
  cd "$WEB_DIR"
  AURORA_AIOPS_WEB_OUT_DIR=../server/internal/web/dist/app npm run build
)

echo "==> Building backend"
rm -rf "$PACKAGE_DIR"
mkdir -p "$PACKAGE_DIR"
(
  cd "$SERVER_DIR"
  GOOS="$GOOS" GOARCH="$GOARCH" go build \
    -trimpath \
    -ldflags="-s -w -X 'main.Version=$VERSION' -X 'main.Commit=$COMMIT' -X 'main.Date=$DATE' -X 'main.BuildType=release'" \
    -o "$PACKAGE_DIR/aurora-aiops" \
    ./cmd/aurora-aiops
)

cp "$SERVICE_FILE" "$PACKAGE_DIR/aurora-aiops.service"
cp "$ROOT_DIR/scripts/migrate-kubejojo-to-aurora-aiops.sh" "$PACKAGE_DIR/"
chmod 0755 "$PACKAGE_DIR/aurora-aiops" "$PACKAGE_DIR/migrate-kubejojo-to-aurora-aiops.sh"
chmod 0644 "$PACKAGE_DIR/aurora-aiops.service"

echo "==> Packaging release archive"
mkdir -p "$RELEASE_DIR"
rm -f "$ARCHIVE_PATH"
if [[ -L "$RELEASE_DIR/latest" ]]; then
  rm -f "$RELEASE_DIR/latest"
fi
if tar --version 2>/dev/null | grep -q 'GNU tar'; then
  tar -C "$RELEASE_DIR" \
    --sort=name \
    --mtime='UTC 1970-01-01' \
    --owner=0 \
    --group=0 \
    --numeric-owner \
    -czf "$ARCHIVE_PATH" "$PACKAGE_STEM"
elif command -v python3 >/dev/null 2>&1; then
  # BSD tar (the default on macOS) lacks the reproducibility flags above.
  # Keep the same archive contract through a small deterministic tar writer.
  python3 - "$RELEASE_DIR" "$PACKAGE_STEM" "$ARCHIVE_PATH" <<'PY'
import gzip
import pathlib
import sys
import tarfile

release_dir = pathlib.Path(sys.argv[1])
package_stem = sys.argv[2]
archive_path = pathlib.Path(sys.argv[3])
package_dir = release_dir / package_stem
entries = [package_dir, *sorted(package_dir.rglob("*"), key=lambda path: path.relative_to(release_dir).as_posix())]

with archive_path.open("wb") as raw:
    with gzip.GzipFile(fileobj=raw, mode="wb", filename="", mtime=0) as compressed:
        with tarfile.open(fileobj=compressed, mode="w:", format=tarfile.GNU_FORMAT) as archive:
            for entry in entries:
                relative_name = entry.relative_to(release_dir).as_posix()
                info = archive.gettarinfo(str(entry), arcname=relative_name)
                info.mtime = 0
                info.uid = 0
                info.gid = 0
                info.uname = ""
                info.gname = ""
                if info.isdir():
                    info.mode = 0o755
                    archive.addfile(info)
                else:
                    with entry.open("rb") as source:
                        archive.addfile(info, source)
PY
else
  echo "GNU tar or python3 is required for deterministic release archives" >&2
  exit 1
fi

echo "==> Writing checksum"
(
  cd "$RELEASE_DIR"
  "${CHECKSUM_CMD[@]}" "$(basename "$ARCHIVE_PATH")" > "$(basename "$CHECKSUM_PATH")"
)

echo "Release archive: $ARCHIVE_PATH"
echo "Checksums file:  $CHECKSUM_PATH"
