# Pier website

The landing page and waitlist for Pier, built from the approved design on the
"Pier website" Screenshotter board (desktop, mobile, design system and
component library pages).

```
v2/       React 19 + Vite + TypeScript. The V2 page, built into server/dist, served at /.
v3/       The V3 design (split-flap board), built into server/dist-v3, served at /v3/.
server/   Go. Serves both pages (embedded at build time) and the waitlist API.
```

Design sources and board exports live in ../design.

## Commands

Run these from the repository root:

```sh
make site-deps   # first time: install both pages' dependencies
make site        # build both pages, then the Go binary (site/server/bin/piersite)
make site-run    # build and serve on http://localhost:8080 (V3 at /v3/)
make site-test   # go vet + go test, and TypeScript checks
```

For development, run `make site` once, then `make site-api` and `make site-v2-dev`
(or `make site-v3-dev`) in two terminals. Vite hot-reloads the page and proxies
`/api` to the Go server.

## The server

One binary, no external services. Waitlist signups go to a SQLite file.

| Setting | Flag | Env | Default |
|---|---|---|---|
| Listen address | `-addr` | `ADDR` | `:8080` |
| Database file | `-db` | `DB_PATH` | `data/waitlist.db` |
| Trust `X-Forwarded-For` | `-trust-proxy` | `TRUST_PROXY=1` | off |

Set `TRUST_PROXY=1` when it runs behind Caddy or another reverse proxy, so the
rate limit (5 attempts per minute per client) sees real client addresses.

- `POST /api/waitlist` with `{"email": "..."}` returns `201 {"status":"joined"}`,
  `200 {"status":"already"}`, or `400 {"error": "..."}` with a message written
  for the visitor. A hidden honeypot field drops bot signups silently.
- `GET /healthz` returns `ok` when the database is reachable.
- `piersite export` prints the waitlist as CSV (`email,created_at`).

Only the address and the time it joined are stored. Addresses are lowercased,
so nobody can join twice by changing case.

The page sends a strict Content-Security-Policy (self only); fonts are
self-hosted, so the page makes no third-party requests.

## Deploying

`docker build -t pier-site site/` (from the repository root) produces a distroless image that serves on 8080
and keeps the database in the `/data` volume. Back up `/data/waitlist.db`.

## Before launch

- `v2/src/site.ts`: GitHub, changelog, docs, contact and privacy links are
  placeholders that point back into the page.
- The comparison table (`v2/src/components/Compare.tsx`): check the Vercel,
  Coolify and Dokku cells against their current docs.
- The FAQ answers and the CLI commands shown on the page were drafted from
  the product description; check them against the real Pier.
- Also drafted: the `git push` output in How it works (`PushLog.tsx`), the
  remote URL `pier.example.com:blog`, the "5 idle minutes" sleep default, and
  the facts in the architecture panel header (`Architecture.tsx`).
- The comparison's newer rows (web dashboard, custom domains, logs) and the
  tool cards need the same check as the rest of the table.
