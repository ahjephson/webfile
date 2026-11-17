#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="${VERSION:-dev}"
COMMIT="${COMMIT:-$(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || echo unknown)}"
BUILD_DATE="${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
OUT="$ROOT/dist"
STAGE="$OUT/webfile-$VERSION-linux-amd64"

rm -rf "$STAGE"
mkdir -p "$STAGE/deploy"

cd "$ROOT/web"
npm ci --no-audit --no-fund
npm audit --audit-level=high
python3 "$ROOT/scripts/generate-notices.py" --check
npm run build

cd "$ROOT/backend"
CGO_ENABLED=1 go test -tags "production pam" ./...
CGO_ENABLED=1 go vet -tags "production pam" ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
CGO_ENABLED=1 go build -tags "production pam" \
  -ldflags "-s -w -X main.version=$VERSION -X main.commit=$COMMIT -X main.buildDate=$BUILD_DATE" \
  -o "$STAGE/webfile" ./cmd/webfile
go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 -mode=binary "$STAGE/webfile"

cp "$ROOT/README.md" "$STAGE/"
cp "$ROOT/LICENSE" "$STAGE/"
cp "$ROOT/AGENTS.md" "$STAGE/"
cp -R "$ROOT/docs" "$STAGE/docs"
cp -R "$ROOT/releases" "$STAGE/releases"
cp "$ROOT/webfile.example.json" "$STAGE/"
cp "$ROOT/deploy/webfile.service" "$STAGE/deploy/"
cp "$ROOT/deploy/webfile-launcher.service" "$STAGE/deploy/"
cp "$ROOT/deploy/webfile.tmpfiles" "$STAGE/deploy/"
cp "$ROOT/deploy/webfile.pam" "$STAGE/deploy/"
cp "$ROOT/deploy/install.sh" "$STAGE/deploy/"
cp "$ROOT/deploy/webfile-update" "$STAGE/deploy/"
chmod +x "$STAGE/deploy/install.sh" "$STAGE/deploy/webfile-update"

cd "$OUT"
tar -czf "webfile-$VERSION-linux-amd64.tar.gz" "$(basename "$STAGE")"
sha256sum "webfile-$VERSION-linux-amd64.tar.gz" > "webfile-$VERSION-linux-amd64.tar.gz.sha256"
echo "$OUT/webfile-$VERSION-linux-amd64.tar.gz"
