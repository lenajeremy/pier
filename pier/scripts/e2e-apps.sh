# Sourced in the e2e suite after CLI login. Uses only its temporary Pier instance.
step "run an Express application built by Railpack"
curl -fsS -b "$JAR" -H 'X-Pier: 1' -d '{"name":"clock"}' "$API/api/projects" >/dev/null
mkdir -p "$WORK/clock"
cd "$WORK/clock"
cat >package.json <<'JSON'
{"name":"clock","private":true,"scripts":{"start":"node server.js"},"dependencies":{"express":"5.1.0"}}
JSON
cat >server.js <<'JS'
const app = require('express')()
app.get('/health', (_, response) => response.sendStatus(204))
app.get('/time', (_, response) => response.send(process.env.SCOPE + ' ' + Date.now()))
app.listen(Number(process.env.PORT), '0.0.0.0')
JS
for scope in production preview; do
 curl -fsS -b "$JAR" -H 'X-Pier: 1' -X PUT -d "{\"name\":\"SCOPE\",\"value\":\"$scope\",\"scopes\":[\"$scope\"]}" "$API/api/projects/clock/variables" > /dev/null
done
curl -fsS -b "$JAR" -H 'X-Pier: 1' -X PUT -d '{"name":"PIER_HEALTH_PATH","value":"/health","scopes":["production","preview"]}' "$API/api/projects/clock/variables" >/dev/null
"$PIER" deploy --project clock --prod > "$WORK/clock-prod.log" || { cat "$WORK/clock-prod.log"; fail "Express deployment failed"; }
first=$(fetch clock.$DOMAIN /time)
second=$(fetch clock.$DOMAIN /time)
[[ $first == "production "* && $second != "$first" ]] || fail "Railpack process did not serve a dynamic response with its production variables"
prod_id=$(sed -n 's/.*application \([0-9a-f]*\) passed.*/\1/p' "$WORK/clock-prod.log")
[[ -n $prod_id ]] || fail "missing server deployment ID"

step "deploy a Dockerfile preview and promote it"
cat >Dockerfile <<'DOCKER'
FROM node:22-alpine
WORKDIR /app
COPY docker-server.js .
EXPOSE 3210
CMD ["node","docker-server.js"]
DOCKER
cat >docker-server.js <<'JS'
require('node:http').createServer((request,response) => {
 response.end(request.url === '/health' ? 'healthy' : 'Dockerfile ' + process.env.SCOPE + ' ' + process.env.PORT)
}).listen(Number(process.env.PORT),'0.0.0.0')
JS
"$PIER" deploy > "$WORK/clock-preview.log" || { cat "$WORK/clock-preview.log"; fail "Dockerfile preview failed"; }
id=$(sed -n 's/.*application \([0-9a-f]*\) passed.*/\1/p' "$WORK/clock-preview.log")
preview=clock-$id.$DOMAIN
[[ $(fetch "$preview" /time) == "Dockerfile preview 3210" ]] || fail "Dockerfile EXPOSE or preview variables missing"
[[ $(fetch clock.$DOMAIN /time) == "production "* ]] || fail "preview moved production"
"$PIER" promote "$id" >/dev/null
[[ $(fetch clock.$DOMAIN /time) == "Dockerfile preview 3210" ]] || fail "promote did not route to the running container"
header=$(curl -fsS -D - -o /dev/null -H "Host: clock.$DOMAIN" "http://127.0.0.1:$HTTP_PORT/time" | tr -d '\r')
echo "$header" | grep -iq "^x-pier-deployment: $id$" || fail "proxy lost the deployment header"

step "reject a crashing process and roll back across image builders"
printf '\nCMD ["node","-e","process.exit(23)"]\n' >> Dockerfile
if "$PIER" deploy --prod > "$WORK/clock-crash.log" 2>&1; then fail "a crashing process became ready"; fi
grep -q 'starting application' "$WORK/clock-crash.log" || { cat "$WORK/clock-crash.log"; fail "bad image did not reach the startup check"; }
[[ $(fetch clock.$DOMAIN /time) == "Dockerfile preview 3210" ]] || fail "crashing process replaced the live version"
"$PIER" rollback "$prod_id" >/dev/null
[[ $(fetch clock.$DOMAIN /time) == "production "* ]] || fail "rollback did not restore the Railpack app"

step "honor the Docker image health check before replacing production"
sed '$d' Dockerfile > healthy.Dockerfile
mv healthy.Dockerfile Dockerfile
printf '\nHEALTHCHECK --interval=1s --timeout=1s --retries=1 CMD ["node","-e","process.exit(1)"]\n' >> Dockerfile
if "$PIER" deploy --prod > "$WORK/clock-health.log" 2>&1; then fail "an unhealthy image became ready"; fi
grep -q "image HEALTHCHECK failed" "$WORK/clock-health.log" || { cat "$WORK/clock-health.log"; fail "Docker health check was ignored"; }
[[ $(fetch clock.$DOMAIN /time) == "production "* ]] || fail "unhealthy deployment took production"

step "preserve a server's binding across a container restart"
container=$(curl -fsS -b "$JAR" "$API/api/projects/clock/deployments/$id" | sed -n 's/.*"runtime":"\(pier-app-[a-z0-9]*\)".*/\1/p')
[[ -n $container ]] || fail "deployment omitted the container identity"
binding=$(docker port "$container" 3210/tcp)
[[ $binding == 127.0.0.1:* ]] || fail "the app has a non-loopback published port"
docker restart -t 1 "$container" >/dev/null
[[ $(docker port "$container" 3210/tcp) == "$binding" ]] || fail "container restart changed the binding"
for _ in $(seq 40); do fetch "$preview" /time 2>/dev/null | grep -q "Dockerfile preview 3210" && break; sleep 0.25; done
[[ $(fetch "$preview" /time) == "Dockerfile preview 3210" ]] || fail "container restart lost the preview"

step "recover server addresses after a daemon restart"
kill "$PIERD_PID"
wait "$PIERD_PID"
PIERD_PID=
start_pierd
[[ $(fetch clock.$DOMAIN /time) == "production "* ]] || fail "server production did not recover"
[[ $(fetch "$preview" /time) == "Dockerfile preview 3210" ]] || fail "immutable process preview did not recover"
cd "$WORK"
