.PHONY: test test-production audit audit-backend audit-frontend frontend backend build build-dev dev package

NPM ?= npm
GO ?= go
GOVULNCHECK ?= $(GO) run golang.org/x/vuln/cmd/govulncheck@v1.8.0
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

frontend:
	cd web && $(NPM) ci --no-audit --no-fund && $(NPM) run build

test:
	cd backend && $(GO) test ./... && $(GO) vet ./...

test-production:
	cd backend && CGO_ENABLED=1 $(GO) test -tags "production pam" ./... && CGO_ENABLED=1 $(GO) vet -tags "production pam" ./...

audit: audit-backend audit-frontend

audit-backend:
	cd backend && $(GOVULNCHECK) ./...

audit-frontend:
	cd web && $(NPM) audit --audit-level=high

backend:
	mkdir -p dist
	cd backend && CGO_ENABLED=1 $(GO) build -tags "production pam" \
		-ldflags "-s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildDate=$(BUILD_DATE)" \
		-o ../dist/webfile ./cmd/webfile

build: frontend test-production backend

build-dev: frontend test
	mkdir -p dist
	cd backend && CGO_ENABLED=1 $(GO) build \
		-ldflags "-X main.version=$(VERSION)-dev -X main.commit=$(COMMIT) -X main.buildDate=$(BUILD_DATE)" \
		-o ../dist/webfile-dev ./cmd/webfile

package:
	VERSION=$(VERSION) COMMIT=$(COMMIT) BUILD_DATE=$(BUILD_DATE) ./scripts/package.sh

dev:
	@echo "Terminal 1: cd backend && go run ./cmd/webfile serve -config ../webfile.dev.json"
	@echo "Terminal 2: cd web && npm ci && npm run dev"
