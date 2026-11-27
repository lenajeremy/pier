#!/usr/bin/env bash
# End-to-end check of the first slice: git push -> build -> live address.
#
# Starts pierd with embedded Caddy on this machine with a throwaway data folder and SSH
# key, pushes a plain HTML project and a Vite project, and checks that each
# one answers at its address. Needs git and (for the Docker builder)
# a running Docker with Buildx. Usage: scripts/e2e.sh [railpack-preview|docker|local]
set -euo pipefail

BUILDER=${1:-railpack-preview}
ROOT=$(cd "$(dirname "$0")/.." && pwd)
tempbase=${TMPDIR:-/tmp}
WORK=$(mktemp -d "${tempbase%/}/pier-e2e.XXXXXX")
DATA=$WORK/data
DOMAIN=pier.localhost
HTTP_PORT=18088 SSH_PORT=12222 API_PORT=17777
PIERD_PID=

cleanup() {
	if [[ -n $PIERD_PID ]]; then kill "$PIERD_PID" 2>/dev/null || true; fi
	wait 2>/dev/null || true
	if [[ ${BUILDER:-} == railpack-preview ]]; then
		owner=pier-$(printf '%s' "$DATA" | shasum -a 256 | cut -c1-16)
		for app in $(docker ps -a --filter "label=io.pier.instance=$owner" --format '{{.Names}}'); do
			docker rm -fv "$app" >/dev/null && docker image rm "$app:app" >/dev/null || true
		done
		docker network rm "$owner" >/dev/null 2>&1 || true
		for n in $(docker network ls -q --filter "label=io.pier.instance=$owner"); do docker network rm "$n" >/dev/null 2>&1 || true; done
		for v in $(docker volume ls -q --filter "label=io.pier.instance=$owner"); do docker volume rm "$v" >/dev/null 2>&1 || true; done
	fi
	if [[ ${KEEP:-} == 1 ]]; then echo "kept $WORK"; else rm -rf "$WORK"; fi
}
trap cleanup EXIT

step() { printf '\n\033[1m== %s\033[0m\n' "$*"; }
fail() { printf '\033[31mFAIL: %s\033[0m\n' "$*"; exit 1; }

step "build pierd and pier"
(cd "$ROOT" && go build -o "$WORK/bin/" ./cmd/...)
PIERD=$WORK/bin/pierd PIER=$WORK/bin/pier

start_pierd() {
	PIER_CADDY_ADMIN= "$PIERD" serve -data "$DATA" -domain "$DOMAIN" -builder "$BUILDER" \
		-http "127.0.0.1:$HTTP_PORT" -ssh "127.0.0.1:$SSH_PORT" -api "127.0.0.1:$API_PORT" >> "$WORK/pierd.log" 2>&1 &
	PIERD_PID=$!
	for _ in $(seq 100); do
		kill -0 "$PIERD_PID" 2>/dev/null || break
		if curl -fs -H "Host: $DOMAIN" "http://127.0.0.1:$HTTP_PORT/healthz" > /dev/null; then return; fi
		sleep 0.1
	done
	tail -20 "$WORK/pierd.log"
	fail "pierd did not bring up the dashboard through embedded Caddy"
}

step "start pierd with embedded Caddy (builder: $BUILDER)"
start_pierd

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

step "paths that aren't files: the app's index.html, the site's 404.html, or a 404"
status() { curl -s -o /dev/null -w '%{http_code}' --max-time 5 --resolve "$1:$HTTP_PORT:127.0.0.1" "http://$1:$HTTP_PORT$2"; }
fetch feat-search--blog.$DOMAIN /settings/profile | grep -q 'type="module"' || fail "a Vite app's own route didn't get index.html"
[[ $(status feat-search--blog.$DOMAIN /missing.png) == 404 ]] || fail "a missing file in a Vite app should 404"
[[ $(status docs.$DOMAIN /nowhere) == 404 ]] || fail "a static site should 404 on a path it doesn't have"
cd "$WORK/docs"
printf '<!doctype html><title>Lost</title><h1>This page wandered off</h1>\n' > 404.html
git add . && git commit -q -m "A page for wrong addresses"
push "$WORK/docs" docs main > /dev/null
[[ $(status docs.$DOMAIN /nowhere) == 404 ]] || fail "a missing page should still answer 404"
fetch_any() { curl -s --max-time 5 --resolve "$1:$HTTP_PORT:127.0.0.1" "http://$1:$HTTP_PORT$2"; }
fetch_any docs.$DOMAIN /nowhere | grep -q "wandered off" || fail "the site's own 404.html wasn't served"
fetch docs.$DOMAIN /guide | grep -q "Install guide" || fail "a 404.html broke clean URLs"
echo "app routes, 404 pages and plain 404s all answer as they should"

if [[ $BUILDER == railpack-preview ]]; then
	step "compare Railpack previews with production builds of the same commits"
	push "$WORK/docs" docs candidate
	grep -q "using Railpack" "$WORK/push-docs-candidate.log" || fail "HTML preview missed Railpack"
	[[ $(fetch candidate--docs.$DOMAIN /guide) == "$(fetch docs.$DOMAIN /guide)" ]] || fail "HTML differs between builders"
	[[ $(status candidate--docs.$DOMAIN /nowhere) == 404 ]] || fail "HTML preview lost its 404 status"
	[[ $(fetch_any candidate--docs.$DOMAIN /nowhere) == "$(fetch_any docs.$DOMAIN /nowhere)" ]] || fail "custom 404 differs"
	push "$WORK/blog" blog main
	grep -q "using Railpack" "$WORK/push-blog-feat-search.log" || fail "Vite preview missed Railpack"
	if grep -q "using Railpack" "$WORK/push-blog-main.log"; then fail "production used the preview builder"; fi
	[[ $(fetch feat-search--blog.$DOMAIN /settings/profile) == "$(fetch blog.$DOMAIN /settings/profile)" ]] || fail "Vite route differs"
	asset=$(fetch blog.$DOMAIN / | sed -n 's|.*src="\(/assets/[^"]*\)".*|\1|p')
	[[ -n $asset ]] || fail "the Vite build has no JS asset"
	fetch blog.$DOMAIN "$asset" > "$WORK/production.js"
	fetch feat-search--blog.$DOMAIN "$asset" > "$WORK/preview.js"
	cmp "$WORK/production.js" "$WORK/preview.js" || fail "Vite asset differs"
fi

step "a push that can't build leaves the address serving"
cd "$WORK/docs"
git rm -q -r index.html guide && printf 'nothing to serve\n' > README.md
git add . && git commit -q -m "Readme only"
push "$WORK/docs" docs main || true
grep -q "still serves" "$WORK/push-docs-main.log" || fail "a failed build should say what still serves"
fetch docs.$DOMAIN / | grep -q "Pier docs" || fail "docs went down after a failed build"

step "pier board"
PIER_SERVER=http://127.0.0.1:$API_PORT PIER_TOKEN=$("$PIERD" token -data "$DATA") "$PIER" board

step "pier and pierd say which release they are, and pier asks pierd for its own"
"$PIERD" version | grep -q '^pierd ' || fail "pierd version printed nothing"
"$PIER" --version | grep -q '^pier ' || fail "pier --version printed nothing"
versions=$(PIER_SERVER=http://127.0.0.1:$API_PORT PIER_TOKEN=$("$PIERD" token -data "$DATA") "$PIER" version)
echo "$versions"
echo "$versions" | grep -q "pierd  *$("$PIERD" version | cut -d' ' -f2)" || fail "pier version didn't get pierd's version from the server"

step "pierd init creates the owner, and the dashboard signs them in"
echo "correct horse battery" | "$PIERD" init -data "$DATA" -email owner@example.com -password-stdin \
	-address pier.localhost -base-domain pier.localhost -ip 127.0.0.1 > "$WORK/init.log" 2>&1 || { cat "$WORK/init.log"; fail "pierd init failed"; }
grep -q "Account created" "$WORK/init.log" || fail "init didn't create the account"
"$PIERD" init -data "$DATA" -yes > /dev/null 2>&1 && fail "a second pierd init should refuse"
API=http://127.0.0.1:$API_PORT
JAR=$WORK/cookies
code=$(curl -s -o /dev/null -w '%{http_code}' -H 'X-Pier: 1' -d '{"email":"owner@example.com","password":"wrong one"}' "$API/api/session")
[[ $code == 401 ]] || fail "a wrong password answered $code"
curl -fsS -c "$JAR" -H 'X-Pier: 1' -d '{"email":"owner@example.com","password":"correct horse battery"}' "$API/api/session" | grep -q '"email":"owner@example.com"' || fail "sign in failed"
curl -fsS -b "$JAR" "$API/api/projects" | grep -q '"name":"docs"' || fail "/api/projects doesn't list docs"
curl -fsS -b "$JAR" "$API/api/projects/docs" | grep -q '"production":{' || fail "docs has no production address"
DEP=$(curl -fsS -b "$JAR" "$API/api/projects/blog" | sed -n 's/.*"deployments":\[{"id":"\([0-9a-f]*\)".*/\1/p')
[[ -n $DEP ]] || fail "blog has no deployments"
curl -fsS -b "$JAR" "$API/api/projects/blog/deployments/$DEP/log" | grep -q '"tag":"build"' || fail "blog's build log wasn't kept"
curl -fsS "$API/" | grep -q '<div id="root">' || fail "pierd doesn't serve the dashboard"
curl -fsS "$API/projects/blog" | grep -q '<div id="root">' || fail "the dashboard's own routes don't load"
echo "signed in, and the dashboard's API answers"

step "create a project, approve pier login, and pier deploy to it"
curl -fsS -b "$JAR" -H 'X-Pier: 1' -d '{"name":"site"}' "$API/api/projects" | grep -q '"name":"site"' || fail "couldn't create the project"
code=$(curl -s -o /dev/null -w '%{http_code}' -b "$JAR" -H 'X-Pier: 1' -d '{"name":"site"}' "$API/api/projects")
[[ $code == 409 ]] || fail "creating site twice answered $code"
export XDG_CONFIG_HOME=$WORK/cli
"$PIER" login "127.0.0.1:$API_PORT" > "$WORK/login.log" 2>&1 &
LOGIN=$!
for _ in $(seq 50); do grep -q "shows the code" "$WORK/login.log" 2>/dev/null && break; sleep 0.1; done
CODE=$(sed -n 's/.*shows the code \([A-Z0-9-]*\)\..*/\1/p' "$WORK/login.log")
[[ -n $CODE ]] || { cat "$WORK/login.log"; fail "pier login printed no code"; }
curl -fsS -b "$JAR" -H 'X-Pier: 1' -X POST "$API/api/cli/login/$CODE/approve" > /dev/null || fail "approving the computer failed"
wait $LOGIN || { cat "$WORK/login.log"; fail "pier login didn't finish after approval"; }
grep -q "Signed in to" "$WORK/login.log" || fail "pier login didn't say it signed in"

mkdir -p "$WORK/site" && cd "$WORK/site"
printf '<!doctype html><title>Site</title><h1>Version one</h1>\n' > index.html
"$PIER" deploy --project site --prod | tee "$WORK/deploy-prod.log"
grep -q "production ready in" "$WORK/deploy-prod.log" || fail "pier deploy --prod didn't finish"
fetch site.$DOMAIN / | grep -q "Version one" || fail "site.$DOMAIN doesn't serve the upload"
[[ -f .pier/project.json ]] || fail "pier deploy didn't remember the project"

printf '<!doctype html><title>Site</title><h1>Version two</h1>\n' > index.html
"$PIER" deploy | tee "$WORK/deploy-preview.log"
grep -q "preview ready in" "$WORK/deploy-preview.log" || fail "the preview didn't finish"
PREVIEW=$(sed -n 's|.*alias   http://\(site-[0-9a-f]*\.[a-z.]*\):.*|\1|p' "$WORK/deploy-preview.log" | tail -1)
fetch "$PREVIEW" / | grep -q "Version two" || fail "the preview's own address doesn't serve it"
fetch site.$DOMAIN / | grep -q "Version one" || fail "a preview changed production"

step "restart pierd and recover production, branch and immutable addresses"
kill "$PIERD_PID"
wait "$PIERD_PID" || fail "pierd failed to shut down"
PIERD_PID=
start_pierd
fetch site.$DOMAIN / | grep -q "Version one" || fail "production changed across restart"
fetch "$PREVIEW" / | grep -q "Version two" || fail "the immutable preview was lost on restart"
fetch feat-search--blog.$DOMAIN / | grep -q 'type="module"' || fail "the branch preview was lost on restart"

step "a project's build settings override what Pier detects"
curl -fsS -b "$JAR" -H 'X-Pier: 1' -X PUT -d '{"production_branch":"","build":{"root":"web","preset":"","install":"","command":"","output":""}}' \
	"$API/api/projects/site/settings" | grep -q '"root":"web"' || fail "saving settings failed"
mkdir -p web && printf '<!doctype html><title>Site</title><h1>From the web folder</h1>\n' > web/index.html
"$PIER" deploy --prod > "$WORK/deploy-root.log"
grep -q "found in web/" "$WORK/deploy-root.log" || { cat "$WORK/deploy-root.log"; fail "the root directory wasn't used"; }
fetch site.$DOMAIN / | grep -q "From the web folder" || fail "production doesn't serve the root directory"
echo "pier login and pier deploy work, and settings apply"

step "roll back, hold production builds, undo, and promote"
"$PIER" rollback > "$WORK/rollback.log" || { cat "$WORK/rollback.log"; fail "pier rollback failed"; }
fetch site.$DOMAIN / | grep -q "Version one" || fail "rollback didn't bring back the previous production deployment"
printf '<!doctype html><title>Site</title><h1>Built while rolled back</h1>\n' > web/index.html
"$PIER" deploy --prod > "$WORK/deploy-held.log"
grep -q "rolled back, so this build didn't replace it" "$WORK/deploy-held.log" || { cat "$WORK/deploy-held.log"; fail "a production build went live during a rollback"; }
fetch site.$DOMAIN / | grep -q "Version one" || fail "production moved during a rollback"
"$PIER" rollback --undo > /dev/null || fail "undoing the rollback failed"
fetch site.$DOMAIN / | grep -q "From the web folder" || fail "undo didn't restore production"
PREVIEW_ID=$(echo "$PREVIEW" | sed 's/^site-\([0-9a-f]*\)\..*/\1/')
"$PIER" promote "$PREVIEW_ID" > /dev/null || fail "pier promote failed"
fetch site.$DOMAIN / | grep -q "Version two" || fail "promote didn't move production to the preview"
if [[ $BUILDER == railpack-preview ]]; then
	"$PIER" rollback > /dev/null || fail "rollback after promoting a Railpack preview failed"
	fetch site.$DOMAIN / | grep -q "Version one" || fail "rollback across builders served the wrong version"
	"$PIER" rollback --undo > /dev/null || fail "undo after cross-builder rollback failed"
	fetch site.$DOMAIN / | grep -q "Version two" || fail "undo lost the promoted Railpack preview"
	step "preview honors a project root, explicit commands and a nested output folder"
	curl -fsS -b "$JAR" -H 'X-Pier: 1' -X PUT --data-binary @- "$API/api/projects/site/settings" > /dev/null <<'JSON'
{"build":{"root":"web","install":"mkdir -p .deps && printf '%s' installed > .deps/proof","command":"mkdir -p publish/site && cp index.html publish/site/index.html && cp .deps/proof publish/site/proof.txt","output":"publish/site"}}
JSON
	"$PIER" deploy > "$WORK/custom-preview.log" || { cat "$WORK/custom-preview.log"; fail "custom Railpack preview failed"; }
	custom_host=$(sed -n 's|.*alias   http://\(site-[0-9a-f]*\.[a-z.]*\):.*|\1|p' "$WORK/custom-preview.log" | tail -1)
	[[ $(fetch "$custom_host" /proof.txt) == installed ]] || fail "custom install or output folder was ignored"
	[[ $(status "$custom_host" /.deps/proof) == 404 ]] || fail "files outside the publish directory were served"

fi
cd "$WORK"
echo "rollback, undo and promote move production without rebuilding"

step "variables reach the build, per scope, and redeploy picks up a change"
curl -fsS -b "$JAR" -H 'X-Pier: 1' -d '{"name":"greet"}' "$API/api/projects" > /dev/null || fail "couldn't create greet"
setvar() { curl -fsS -b "$JAR" -H 'X-Pier: 1' -X PUT -d "{\"name\":\"GREETING\",\"value\":\"$1\",\"scopes\":[\"$2\"]}" "$API/api/projects/greet/variables" > /dev/null || fail "couldn't set GREETING for $2"; }
setvar "hello from production" production
curl -fsS -b "$JAR" -H 'X-Pier: 1' -d '{"name":"GREETING","value":"hello from a preview","scopes":["production","preview"],"projects":["greet","blog"]}' "$API/api/shared-variables" > /dev/null
shared=$(curl -fsS -b "$JAR" "$API/api/shared-variables")
[[ $shared == *'"projects":["blog","greet"]'* && $shared != *'hello from a preview'* ]] || fail "shared variable links or masking"
mkdir -p "$WORK/greet" && cd "$WORK/greet"
printf '{"name":"greet","private":true,"scripts":{"build":"mkdir -p dist && printf \\"<h1>%%s</h1>\\" \\"$GREETING\\" > dist/index.html"}}\n' > package.json
"$PIER" deploy --project greet --prod > "$WORK/greet-prod.log" || { cat "$WORK/greet-prod.log"; fail "greet didn't deploy"; }
fetch greet.$DOMAIN / | grep -q "hello from production" || fail "production didn't get its variable"
grep -q "with 1 variable for production: GREETING" "$WORK/greet-prod.log" || fail "the log doesn't say which variables the build had"
grep -q "hello from production" "$WORK/greet-prod.log" && fail "a variable's value showed up in the build log"
"$PIER" deploy > "$WORK/greet-preview.log"
GP=$(sed -n 's|.*alias   http://\(greet-[0-9a-f]*\.[a-z.]*\):.*|\1|p' "$WORK/greet-preview.log" | tail -1)
fetch "$GP" / | grep -q "hello from a preview" || fail "the preview didn't get the preview's variable"
setvar "hello again" production
curl -fsS -b "$JAR" "$API/api/projects/greet/variables" | grep -q '"production":true' || fail "a changed variable didn't mark production as out of date"
curl -fsS -b "$JAR" -H 'X-Pier: 1' -d '{"name":"GREETING","scopes":["production"]}' "$API/api/projects/greet/variables/reveal" | grep -q '"value":"hello again"' || fail "reveal didn't return the value"
curl -fsS -b "$JAR" "$API/api/projects/greet/variables" | grep -q 'hello again' && fail "the list of variables leaked a value"
curl -fsS -b "$JAR" -H 'X-Pier: 1' -d '{}' "$API/api/projects/greet/redeploy" | grep -q '"id"' || fail "redeploy didn't start"
for _ in $(seq 60); do fetch greet.$DOMAIN / 2>/dev/null | grep -q "hello again" && break; sleep 0.5; done
fetch greet.$DOMAIN / | grep -q "hello again" || fail "the redeploy didn't pick up the new value"
curl -fsS -b "$JAR" "$API/api/projects/greet/variables" | grep -q '"production":false' || fail "production still looks out of date after the redeploy"
cd "$WORK"
echo "variables are scoped, kept out of the log, and a redeploy picks up a change"

if [[ $BUILDER == railpack-preview ]]; then source "$ROOT/scripts/e2e-apps.sh"; source "$ROOT/scripts/e2e-db.sh"; fi

step "every request a site answers is in its Logs"
for p in / /missing-page; do curl -s -o /dev/null --resolve "greet.$DOMAIN:$HTTP_PORT:127.0.0.1" "http://greet.$DOMAIN:$HTTP_PORT$p"; done
sleep 2.5 # pierd writes requests down once a second
curl -fsS -b "$JAR" "$API/api/projects/greet/requests" | grep -q '"path":"/missing-page"' || fail "a request didn't reach the Logs"
curl -fsS -b "$JAR" "$API/api/projects/greet/requests?status=errors" | grep -q '"status":404' || fail "the errors filter lost a 404"
curl -fsS -b "$JAR" "$API/api/projects/greet/requests/chart" | grep -Eq '"errors":[1-9]' || fail "the chart didn't count the error"
curl -fsS -b "$JAR" "$API/api/projects/docs/requests" | grep -q '/missing-page' && fail "one project's requests showed in another's Logs"
echo "requests are kept per project, filtered and charted"

step "request logs CLI, activity, server maintenance and owner settings"
"$PIER" logs greet --since 1h --path /missing-page --status 404 | grep -q '404  GET "/missing-page"' || fail "CLI log filters"
"$PIER" logs greet --path /missing-page --status 200 | grep -q . && fail "CLI status filter"
curl -fsS -b "$JAR" "$API/api/activity?project=site" | grep -q 'Promoted to production' || fail "production activity"
curl -fsS -b "$JAR" "$API/api/server" | grep -q '"disk_total":' || fail "server disk metrics"
curl -fsS -b "$JAR" -H 'X-Pier: 1' -X POST "$API/api/server/cleanup/previews" | grep -q '"done":true' || fail "preview cleanup"
fetch site.$DOMAIN / | grep -q "Version two" || fail "cleanup changed production"
curl -fsS -b "$JAR" -H 'X-Pier: 1' -X PUT -d '{"address":"owner.pier.localhost","base":"pier.localhost"}' "$API/api/settings" | grep -q '"pending":true' || fail "pending server addresses"
curl -fsS -b "$JAR" -H 'X-Pier: 1' -X POST "$API/api/settings/dns" | grep -q 'Local development address' || fail "DNS check"
curl -fsS -b "$JAR" -H 'X-Pier: 1' -d '{"current":"correct horse battery","new":"my replacement password"}' "$API/api/settings/password" | grep -q '"changed":true' || fail "change password"
code=$(curl -s -o /dev/null -w '%{http_code}' -b "$JAR" "$API/api/projects")
[[ $code == 401 ]] || fail "old browser session survived password change"
"$PIER" logs greet >/dev/null 2>&1 && fail "old CLI token survived password change"
curl -fsS -c "$JAR" -H 'X-Pier: 1' -d '{"email":"owner@example.com","password":"my replacement password"}' "$API/api/session" >/dev/null

step "pierd reset-password signs everyone out"
echo "another long password" | "$PIERD" reset-password -data "$DATA" -password-stdin > /dev/null
code=$(curl -s -o /dev/null -w '%{http_code}' -b "$JAR" "$API/api/projects")
[[ $code == 401 ]] || fail "the old session still works after a reset ($code)"

printf '\n\033[32mAll checks passed.\033[0m\n'
