#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
export VERSION="${VERSION:-$(cat RELEASE_VERSION)}"
export COMMIT="${COMMIT:-$(git rev-parse HEAD)}"
if [[ ! "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo 'VERSION must be a bare semantic version.' >&2
  exit 1
fi

go -C backend test ./...
go -C backend vet ./...
bash scripts/package.sh
(
  cd dist
  sha256sum -c "webfile-$VERSION-linux-amd64.tar.gz.sha256"
)
metadata="$("dist/webfile-$VERSION-linux-amd64/webfile" version)"
if [[ "$metadata" != "webfile $VERSION (commit $COMMIT, built "* ]]; then
  echo "Unexpected package metadata: $metadata" >&2
  exit 1
fi
printf '%s\n' "$metadata"
