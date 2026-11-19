.PHONY: help test clean \
	site site-deps site-run site-api site-v2-dev site-v3-dev site-test \
	pier pier-test pier-e2e pier-caddy pier-dev

help:
	@echo "Pier monorepo"
	@echo ""
	@echo "  The product (pier/)"
	@echo "    make pier          build pierd and pier into pier/bin"
	@echo "    make pier-test     go vet and unit tests"
	@echo "    make pier-e2e      push real projects through Caddy, pierd and Docker"
	@echo "    make pier-caddy    run Caddy for development (one terminal)"
	@echo "    make pier-dev      run pierd for development (another terminal)"
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

pier:
	cd pier && go build -trimpath -o bin/ ./cmd/...

pier-test:
	cd pier && go vet ./... && go test ./...

pier-e2e:
	pier/scripts/e2e.sh docker

# Development: Caddy with an empty config, then pierd driving it. Sites are at
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
