# pier

The product: `pierd`, the daemon that runs on your server, and `pier`, the
CLI. Deploy from a connected GitHub repository or upload a folder with `pier deploy`.

## What works today

The first slice, end to end:

1. `git push pier <branch>` over SSH, with the developer's own key.
2. pierd looks at the push (package.json and its framework, or plain HTML),
   builds it in a throwaway Docker container, and keeps the output as a
   deployment that never changes.
3. It points the branch's address at the new deployment (production for
   `main`), and reloads the router inside pierd.
4. The build is narrated back to the push as `remote:` lines.
5. `pier board` lists every address and what it serves.

And the start of the second slice:

6. `pierd init` checks the machine, creates the owner's account, and sets
   Pier's address and base domain, checking the DNS records for them.
   `pierd reset-password` is the way back in; Pier never sends email.
7. pierd serves the dashboard: sign in, every project and what it serves,
   each project's deployments, and each deployment's build log, followed
   live while it builds.
8. Projects are created in the dashboard, with build settings that override
   what Pier detects (root directory, preset, commands, output folder).
9. `pier login ADDRESS` shows a code and opens the dashboard, where the owner
   approves the computer; it gets a token of its own, listed under Members.
10. `pier deploy` uploads the folder (what git would keep, in a checkout) and
    narrates its build: a preview at its own address, or production with
    `--prod`. The folder remembers its project in `.pier/project.json`.

## Deploy from GitHub

1. Sign in to the dashboard and open **Integrations**. Set the public HTTPS webhook URL ending in `/github/events`, then choose **Create GitHub App**.
2. Name the App on GitHub and install it on the repositories you want Pier to build. In **New project**, select **Import**, choose the production branch and any build overrides, then deploy.
3. Push to the production branch to update production. Other branches get previews. Pull requests also get a `pr-N--` address and a comment with the built deployment’s URL. Approve fork commits in **Integrations** after reviewing their code and the preview variables they will receive.

GitHub needs to reach the webhook. An HTTPS tunnel to that route works for local testing. The preview addresses also need public routing if collaborators on other machines will visit them. Pier retains the source archive for redeploy. Source archives containing symlinks are currently rejected.

Every deployment gets three kinds of address:

```
blog.pier.example.com                 production
feat-search--blog.pier.example.com    the latest deployment of a branch
blog-c19e77.pier.example.com          one deployment, forever
```

A failed build changes nothing: the address keeps serving what it served.

## Custom domains

Open a project’s **Domains** tab and enter the hostname. Copy the records shown into your DNS provider. For a registrable apex, Pier also checks the www CNAME and redirects that hostname to the apex. The page shows the current DNS answers and rechecks pending domains every ten seconds.

Once DNS matches, the hostname follows the project’s production deployment, including promotions and rollbacks. With HTTPS enabled, the dashboard checks the certificate served by the local site listener before showing **Live, with HTTPS**. The default development listener serves HTTP.

## Layout

```
cmd/pierd          the daemon: SSH, API, dashboard, builds, routing; init
cmd/pier           the CLI
web                the dashboard (React and Vite), built into internal/web
internal/names     addresses and IDs
internal/store     SQLite: projects, deployments, aliases
internal/build     detecting how to build, and running the build
internal/deploy    a push becoming a live address
internal/router    embedded Caddy, routes and deployment fallbacks
internal/gitssh    the SSH server that runs git
internal/hook      git's post-receive hook, which calls back into pierd
internal/board     the rows of the board
internal/api       the HTTP API, and the hook's local socket
internal/auth      passwords, sessions, and the sign-in lock
internal/bundle    packs a folder for pier deploy, and unpacks it safely
internal/dnscheck  checks the DNS records pierd init asks for
internal/web       serves the built dashboard, embedded in pierd
scripts/e2e.sh     the end-to-end check
```

## Developing

You need Go, git, and Docker (OrbStack on a Mac).

```sh
make pier-dev                      # pierd with embedded Caddy, data in pier/.data
./pier/bin/pierd init -data pier/.data -address pier.localhost -base-domain pier.localhost
./pier/bin/pierd keys add -data pier/.data ~/.ssh/id_ed25519.pub
./pier/bin/pier login --token "$(./pier/bin/pierd token -data pier/.data)"
```

Then, in any project:

```sh
git remote add pier ssh://git@localhost:2222/blog
git push pier main
open http://blog.pier.localhost:8088
```

Existing installations that run a separate Caddy can keep using it with
`pierd serve -caddy-admin http://127.0.0.1:2019` or `PIER_CADDY_ADMIN`.
For embedded mode, free the site listener first. If migrating an HTTPS installation,
copy the existing Caddy storage to `<pier data>/caddy/` before switching.

The dashboard is at http://pier.localhost:8088 (or http://127.0.0.1:7777).
`make pier-web-dev` serves it with live reload on :5175, talking to pierd.
`*.localhost` resolves to your machine in browsers and curl, so no DNS is
needed. `make pier-e2e` does all of this with throwaway data and ports.

## pierd's data folder

```
pier.db                  SQLite
repos/<project>.git      bare repositories
deployments/<id>/        built files, never changed once written
logs/<id>.log            each deployment's build log
authorized_keys          keys that can push (pierd keys add)
api.token                the CLI's token (pierd token)
ssh_host_ed25519_key     the SSH host key
pierd.sock               the git hook's socket
```

## Next

The order the dashboard needs them in (design/v3/dashboard-brief.md, kept
outside the repository): public wildcard certificate rollout, shared variables, the logs CLI, and server health.
