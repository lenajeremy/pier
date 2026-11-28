# Sourced in the e2e suite after the server apps. Uses only its temporary Pier instance.
step "give a project Postgres, and production and previews a database each"
curl -fsS -b "$JAR" -H 'X-Pier: 1' -d '{"name":"store"}' "$API/api/projects" >/dev/null
curl -fsS -b "$JAR" -H 'X-Pier: 1' -d '{"memory_mb":128}' "$API/api/projects/store/database" | grep -q '"state":"running"' || fail "the database didn't start"
mkdir -p "$WORK/store" && cd "$WORK/store"
cat >package.json <<'JSON'
{"name":"store","private":true,"scripts":{"start":"node server.js"},"dependencies":{"pg":"8.13.1"}}
JSON
cat >server.js <<'JS'
const { Pool } = require('pg')
const pool = new Pool({ connectionString: process.env.DATABASE_URL })
require('http').createServer(async (_, res) => {
  try {
    await pool.query('CREATE TABLE IF NOT EXISTS visits (id serial PRIMARY KEY)')
    await pool.query('INSERT INTO visits DEFAULT VALUES')
    const r = await pool.query('SELECT count(*)::int AS n, current_database() AS db FROM visits')
    res.end(`${r.rows[0].n} in ${r.rows[0].db}`)
  } catch (e) { res.statusCode = 500; res.end(e.message) }
}).listen(Number(process.env.PORT), '0.0.0.0')
JS
"$PIER" deploy --project store --prod > "$WORK/store-prod.log" || { cat "$WORK/store-prod.log"; fail "the database app didn't deploy"; }
grep -q "with the project's Postgres as DATABASE_URL" "$WORK/store-prod.log" || fail "the build log doesn't say it has the database"
fetch store.$DOMAIN / | grep -Eq '^[0-9]+ in production$' || fail "production didn't reach its database"
"$PIER" deploy > "$WORK/store-preview.log" || { cat "$WORK/store-preview.log"; fail "the preview didn't deploy"; }
SP=$(sed -n 's|.*alias   http://\(store-[0-9a-f]*\.[a-z.]*\):.*|\1|p' "$WORK/store-preview.log" | tail -1)
fetch "$SP" / | grep -Eq '^[0-9]+ in preview$' || fail "the preview didn't get the previews' database"

step "link previews to production's database, and give production a second one"
curl -fsS -b "$JAR" -H 'X-Pier: 1' -X PUT -d '{"environments":["production","preview"]}' "$API/api/projects/store/database/databases/production" >/dev/null 2>&1 && fail "two databases gave previews DATABASE_URL"
curl -fsS -b "$JAR" -H 'X-Pier: 1' -X PUT -d '{"environments":[]}' "$API/api/projects/store/database/databases/preview" >/dev/null || fail "couldn't unlink the preview database"
curl -fsS -b "$JAR" -H 'X-Pier: 1' -X PUT -d '{"environments":["production","preview"]}' "$API/api/projects/store/database/databases/production" >/dev/null || fail "couldn't link previews to production's database"
curl -fsS -b "$JAR" -H 'X-Pier: 1' -d '{"name":"analytics","environments":["production"],"variable":"ANALYTICS_URL"}' "$API/api/projects/store/database/databases" | grep -q '"name":"analytics"' || fail "couldn't add a second database"
"$PIER" deploy > "$WORK/store-preview2.log" || { cat "$WORK/store-preview2.log"; fail "the second preview didn't deploy"; }
SP=$(sed -n 's|.*alias   http://\(store-[0-9a-f]*\.[a-z.]*\):.*|\1|p' "$WORK/store-preview2.log" | tail -1)
fetch "$SP" / | grep -Eq '^[0-9]+ in production$' || fail "the preview didn't share production's database"
"$PIER" db url store --db analytics | grep -q '@postgres:5432/analytics' || fail "pier db url --db"

step "browse the database, run SQL, back it up, and reach it from this computer"
curl -fsS -b "$JAR" "$API/api/projects/store/database/tables" | grep -q '"name":"visits"' || fail "the tables list is missing visits"
curl -fsS -b "$JAR" "$API/api/projects/store/database/tables/public/visits?where=id%3D1" | grep -q '"count":1' || fail "a filtered page of rows"
curl -fsS -b "$JAR" "$API/api/projects/store/database/tables/public/visits?where=true%3B%20DROP%20TABLE%20visits" >/dev/null 2>&1 && fail "a filter ran a second statement"
curl -fsS -b "$JAR" -H 'X-Pier: 1' -d '{"sql":"SELECT count(*) AS n FROM visits"}' "$API/api/projects/store/database/query" | grep -q '"tag":"SELECT 1"' || fail "a query didn't run"
curl -fsS -b "$JAR" -H 'X-Pier: 1' -X POST "$API/api/projects/store/database/backups" | grep -o '"state":"done"' | wc -l | grep -q 3 || fail "the backups failed"
"$PIER" db url store | grep -q '@postgres:5432/production' || fail "pier db url"
if command -v psql >/dev/null; then
	"$PIER" db tunnel store --port 15499 > "$WORK/tunnel.log" 2>&1 &
	TUNNEL=$!
	for _ in $(seq 50); do grep -q DATABASE_URL "$WORK/tunnel.log" && break; sleep 0.1; done
	psql "$(sed -n 's/^DATABASE_URL=//p' "$WORK/tunnel.log")" -Atc 'SELECT count(*) > 0 FROM visits' | grep -q t || fail "psql through pier db tunnel"
	kill $TUNNEL; wait $TUNNEL 2>/dev/null || true
fi
cd "$WORK"
curl -fsS -b "$JAR" -H 'X-Pier: 1' -X DELETE "$API/api/projects/store" >/dev/null || fail "couldn't delete the project"
owner=pier-$(printf '%s' "$DATA" | shasum -a 256 | cut -c1-16)
docker ps -a --format '{{.Names}}' | grep -q "^$owner-db-store$" && fail "the deleted project's database is still running"
ls "$DATA/backups/deleted"/store-*/ | grep -q '^last-production.dump$' || fail "the deleted project's last backup wasn't kept"
echo "the databases served production and previews, and went with their project"
