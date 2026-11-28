# Sourced in the e2e suite after the database. Uses only its temporary Pier
# instance, the stand-in for Cloudflare and the S3 server e2e.sh started.
step "connect Cloudflare, give a project buckets, and its apps their variables"
curl -fsS -b "$JAR" -H 'X-Pier: 1' -X PUT -d '{"account":"0123456789abcdef0123456789abcdef","token":"wrong"}' "$API/api/cloudflare" >/dev/null 2>&1 && fail "a wrong token connected"
curl -fsS -b "$JAR" -H 'X-Pier: 1' -X PUT -d '{"account":"0123456789abcdef0123456789abcdef","token":"admin-token"}' "$API/api/cloudflare" | grep -q '"connected":true' || fail "Cloudflare didn't connect"
curl -fsS -b "$JAR" -H 'X-Pier: 1' -d '{"name":"media"}' "$API/api/projects" >/dev/null
curl -fsS -b "$JAR" -H 'X-Pier: 1' -d '{"name":"files","environments":["production","preview"]}' "$API/api/projects/media/storage" | grep -q '"prefix":"STORAGE"' || fail "the first bucket isn't STORAGE_"
mkdir -p "$WORK/media" && cd "$WORK/media"
cat >package.json <<'JSON'
{"name":"media","private":true,"scripts":{"start":"node server.js"}}
JSON
cat >server.js <<'JS'
const e = process.env
require('http').createServer((_, res) => res.end(`${e.STORAGE_BUCKET} ${e.AWS_ACCESS_KEY_ID} ${(e.STORAGE_SECRET_ACCESS_KEY || '').length} ${e.AWS_REGION}`)).listen(Number(e.PORT), '0.0.0.0')
JS
"$PIER" deploy --project media --prod > "$WORK/media-prod.log" || { cat "$WORK/media-prod.log"; fail "the storage app didn't deploy"; }
grep -q 'with 1 bucket: pier-[a-z0-9]*-media-files' "$WORK/media-prod.log" || fail "the build log doesn't name the bucket"
fetch media.$DOMAIN / | grep -Eq '^pier-[a-z0-9]+-media-files key[0-9]+ 64 auto$' || fail "production didn't get its bucket's variables"
"$PIER" deploy > "$WORK/media-preview.log" || { cat "$WORK/media-preview.log"; fail "the preview didn't deploy"; }
MP=$(sed -n 's|.*alias   http://\(media-[0-9a-f]*\.[a-z.]*\):.*|\1|p' "$WORK/media-preview.log" | tail -1)
fetch "$MP" / | grep -Eq '^pier-[a-z0-9]+-media-files ' || fail "the preview didn't share production's bucket"

step "move previews to a bucket of their own"
curl -fsS -b "$JAR" -H 'X-Pier: 1' -d '{"name":"test"}' "$API/api/projects/media/storage" >/dev/null || fail "couldn't add a second bucket"
curl -fsS -b "$JAR" -H 'X-Pier: 1' -X PUT -d '{"environments":["preview"],"prefix":"STORAGE"}' "$API/api/projects/media/storage/test" >/dev/null 2>&1 && fail "two STORAGE_ buckets served previews"
curl -fsS -b "$JAR" -H 'X-Pier: 1' -X PUT -d '{"environments":["production"]}' "$API/api/projects/media/storage/files" >/dev/null || fail "couldn't unlink previews"
curl -fsS -b "$JAR" -H 'X-Pier: 1' -X PUT -d '{"environments":["preview"],"prefix":"STORAGE"}' "$API/api/projects/media/storage/test" >/dev/null || fail "couldn't link the test bucket"
"$PIER" deploy > "$WORK/media-preview2.log" || { cat "$WORK/media-preview2.log"; fail "the second preview didn't deploy"; }
MP=$(sed -n 's|.*alias   http://\(media-[0-9a-f]*\.[a-z.]*\):.*|\1|p' "$WORK/media-preview2.log" | tail -1)
fetch "$MP" / | grep -Eq '^pier-[a-z0-9]+-media-test ' || fail "the preview didn't get the test bucket"
fetch media.$DOMAIN / | grep -Eq '^pier-[a-z0-9]+-media-files ' || fail "production lost its bucket"

step "upload, list, read and delete files from the dashboard"
printf 'hello from pier\n' > "$WORK/hello.txt"
curl -fsS -b "$JAR" -H 'X-Pier: 1' -H 'Content-Type: text/plain' -X PUT --data-binary @"$WORK/hello.txt" "$API/api/projects/media/storage/files/file?key=notes/hello%20world.txt" >/dev/null || fail "an upload"
curl -fsS -b "$JAR" "$API/api/projects/media/storage/files/files" | grep -q '"notes/"' || fail "the folder isn't listed"
view=$(curl -fsS -b "$JAR" "$API/api/projects/media/storage/files/file?key=notes/hello%20world.txt" | python3 -c 'import json,sys; print(json.load(sys.stdin)["view"])')
curl -fsS "$view" | grep -q 'hello from pier' || fail "the file's link didn't read it"
curl -fsS -b "$JAR" "$API/api/projects/media/storage/files/variables" | grep -q '"STORAGE_SECRET_ACCESS_KEY"' || fail "a member can't see the bucket's variables"
curl -fsS -b "$JAR" -H 'X-Pier: 1' -X DELETE "$API/api/projects/media/storage/files/files?prefix=notes/" | grep -q '"deleted":1' || fail "deleting a folder"

cd "$WORK"
curl -fsS -b "$JAR" -H 'X-Pier: 1' -X DELETE "$API/api/projects/media" >/dev/null || fail "couldn't delete the project"
ls "$WORK/r2" | grep -q media && fail "the deleted project's buckets are still there"
curl -fsS -b "$JAR" -H 'X-Pier: 1' -X DELETE "$API/api/cloudflare" | grep -q '"connected":false' || fail "Cloudflare didn't disconnect"
echo "the buckets served production and previews, and went with their project"
