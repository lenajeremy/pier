#!/usr/bin/env bash
# End-to-end check of the first slice: git push -> build -> live address.
#
# Starts Caddy and pierd on this machine with a throwaway data folder and SSH
# key, pushes a plain HTML project and a Vite project, and checks that each
# one answers at its address. Needs git, caddy and (for the Docker builder)
# a running Docker. Usage: scripts/e2e.sh [docker|local]
set -euo pipefail

BUILDER=${1:-docker}
ROOT=$(cd "$(dirname "$0")/.." && pwd)
WORK=$(mktemp -d "${TMPDIR:-/tmp}/pier-e2e.XXXXXX")
DATA=$WORK/data
DOMAIN=pier.localhost
HTTP_PORT=18088 SSH_PORT=12222 API_PORT=17777 ADMIN_PORT=12019
PIDS=()

cleanup() {
	for p in "${PIDS[@]:-}"; do kill "$p" 2>/dev/null || true; done
	wait 2>/dev/null || true
	if [[ ${KEEP:-} == 1 ]]; then echo "kept $WORK"; else rm -rf "$WORK"; fi
}
trap cleanup EXIT

step() { printf '\n\033[1m== %s\033[0m\n' "$*"; }
fail() { printf '\033[31mFAIL: %s\033[0m\n' "$*"; exit 1; }

step "build pierd and pier"
(cd "$ROOT" && go build -o "$WORK/bin/" ./cmd/...)
PIERD=$WORK/bin/pierd PIER=$WORK/bin/pier

step "start caddy (admin on :$ADMIN_PORT)"
printf '{"admin":{"listen":"127.0.0.1:%s"}}' "$ADMIN_PORT" > "$WORK/caddy.json"
caddy run --config "$WORK/caddy.json" > "$WORK/caddy.log" 2>&1 &
PIDS+=($!)
for _ in $(seq 50); do curl -fs "http://127.0.0.1:$ADMIN_PORT/config/" > /dev/null && break; sleep 0.1; done

step "start pierd (builder: $BUILDER)"
"$PIERD" serve -data "$DATA" -domain "$DOMAIN" -builder "$BUILDER" \
	-http ":$HTTP_PORT" -ssh "127.0.0.1:$SSH_PORT" -api "127.0.0.1:$API_PORT" \
	-caddy-admin "http://127.0.0.1:$ADMIN_PORT" > "$WORK/pierd.log" 2>&1 &
PIDS+=($!)
for _ in $(seq 50); do curl -fs "http://127.0.0.1:$API_PORT/healthz" > /dev/null && break; sleep 0.1; done

step "authorise a throwaway key"
ssh-keygen -q -t ed25519 -N "" -C "pier-e2e" -f "$WORK/key"
"$PIERD" keys add -data "$DATA" "$WORK/key.pub"
export GIT_SSH_COMMAND="ssh -i $WORK/key -o IdentitiesOnly=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR"
export GIT_AUTHOR_NAME=Ana GIT_AUTHOR_EMAIL=ana@example.com GIT_COMMITTER_NAME=Ana GIT_COMMITTER_EMAIL=ana@example.com

push() { # dir project branch
	git -C "$1" push "ssh://git@127.0.0.1:$SSH_PORT/$2" "HEAD:refs/heads/$3" 2>&1 | tee "$WORK/push-$2-$3.log"
}
fetch() { curl -fsS --max-time 5 --resolve "$1:$HTTP_PORT:127.0.0.1" "http://$1:$HTTP_PORT$2"; }

step "push a plain HTML site to production"
mkdir -p "$WORK/docs" && cd "$WORK/docs"
git init -q -b main
printf '<!doctype html><title>Docs</title><h1>Pier docs</h1>\n' > index.html
mkdir -p guide && printf '<h1>Install guide</h1>\n' > guide/index.html
git add . && git commit -q -m "First docs page"
push "$WORK/docs" docs main
grep -q "production ready in" "$WORK/push-docs-main.log" || fail "docs push didn't finish"
fetch docs.$DOMAIN / | grep -q "Pier docs" || fail "docs.$DOMAIN doesn't serve the page"
fetch docs.$DOMAIN /guide | grep -q "Install guide" || fail "clean URL /guide didn't resolve"
echo "docs.$DOMAIN serves the site"

step "push a Vite site on a branch"
mkdir -p "$WORK/blog/src" && cd "$WORK/blog"
git init -q -b main
cat > package.json <<'JSON'
{ "name": "blog", "private": true, "type": "module",
  "scripts": { "build": "vite build" },
  "devDependencies": { "vite": "^6.0.0" } }
JSON
printf '<!doctype html><title>Blog</title><div id="app"></div><script type="module" src="/src/main.js"></script>\n' > index.html
printf 'document.querySelector("#app").textContent = "Search results page"\n' > src/main.js
git add . && git commit -q -m "Search results page"
push "$WORK/blog" blog feat-search
grep -q "preview ready in" "$WORK/push-blog-feat-search.log" || fail "blog push didn't finish"
fetch feat-search--blog.$DOMAIN / | grep -q 'type="module"' || fail "the preview doesn't serve the built page"
echo "feat-search--blog.$DOMAIN serves the Vite build"

step "a push that can't build leaves the address serving"
cd "$WORK/docs"
git rm -q -r index.html guide && printf 'nothing to serve\n' > README.md
git add . && git commit -q -m "Readme only"
push "$WORK/docs" docs main || true
grep -q "still serves" "$WORK/push-docs-main.log" || fail "a failed build should say what still serves"
fetch docs.$DOMAIN / | grep -q "Pier docs" || fail "docs went down after a failed build"

step "pier board"
PIER_SERVER=http://127.0.0.1:$API_PORT PIER_TOKEN=$("$PIERD" token -data "$DATA") "$PIER" board

printf '\n\033[32mAll checks passed.\033[0m\n'
