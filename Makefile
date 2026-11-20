.PHONY: help test clean \
	site site-deps site-run site-api site-v2-dev site-v3-dev site-test \
	pier pier-web pier-web-deps pier-web-dev pier-test pier-e2e pier-caddy pier-dev

help:
	@echo "Pier monorepo"
	@echo ""
	@echo "  The product (pier/)"
	@echo "    make pier          build the dashboard, then pierd and pier into pier/bin"
	@echo "    make pier-web-deps install the dashboard's dependencies (first time)"
	@echo "    make pier-web-dev  the dashboard with live reload on :5175 (pierd on :7777)"
	@echo "    make pier-test     go vet and unit tests"
	@echo "    make pier-e2e      push real projects through Caddy, pierd and Docker"
	@echo "    make pier-release     package a Linux server installer (ARCH=amd64 or arm64)"
	@echo "    make pier-cli-release package macOS and Linux CLI tarballs"
	@echo "    make pier-vps         install/upgrade Pier on a VPS (HOST=user@ip)"
	@echo "    make pier-caddy    run a standalone Caddy for compatibility testing"
	@echo "    make pier-dev      run pierd with embedded Caddy for development"
	@echo ""
	@echo "  The website (site/)"
	@echo "    make site-deps     install both pages' dependencies (first time)"
	@echo "    make site          build both pages and the site server"
	@echo "    make site-run      serve the site on http://localhost:8080 (V3 at /v3/)"
	@echo "    make site-test     go vet, go test and TypeScript checks"
	@echo ""
	@echo "    make test          everything's tests"

test: pier-test site-test

clean:
	rm -rf pier/bin site/server/bin site/server/dist site/server/dist-v3

# ---- The product -------------------------------------------------------------

pier: pier-web
	cd pier && go build -trimpath -o bin/ ./cmd/...

# The dashboard is built into pier/internal/web/dist and embedded in pierd.
pier-web:
	cd pier/web && npm run build

pier-web-deps:
	cd pier/web && npm ci

pier-web-dev:
	cd pier/web && npm run dev

pier-test:
	cd pier && go vet ./... && go test ./...
	cd pier/web && npm run typecheck

pier-e2e:
	pier/scripts/e2e.sh railpack-preview

# Development: pierd runs its own Caddy. Sites are at
# http://<project>.pier.localhost:8088 and pushes go to ssh://git@localhost:2222.
pier-caddy:
	caddy run

pier-dev: pier
	./pier/bin/pierd serve -data pier/.data

# ---- The website -------------------------------------------------------------

site-deps:
	cd site/v2 && npm ci
	cd site/v3 && npm ci

site:
	cd site/v2 && npm run build
	cd site/v3 && npm run build
	cd site/server && go build -trimpath -o bin/piersite .

site-run: site
	cd site/server && ./bin/piersite

site-api:
	cd site/server && go run .

site-v2-dev:
	cd site/v2 && npm run dev

site-v3-dev:
	cd site/v3 && npm run dev

site-test:
	cd site/server && go vet ./... && go test ./...
	cd site/v2 && npm run typecheck
	cd site/v3 && npm run typecheck

.PHONY: pier-release pier-cli-release pier-vps
ARCH ?= amd64
pier-release: pier-web
	pier/scripts/release.sh $(ARCH)

pier-cli-release:
	pier/scripts/release-cli.sh

pier-vps:
	@test -n "$(HOST)" || { echo "Usage: make pier-vps HOST=user@your-vps-ip" >&2; exit 2; }
	pier/scripts/vps-install.sh --build "$(HOST)"
