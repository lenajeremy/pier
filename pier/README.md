# pier

The product: `pierd`, the daemon that runs on your server, and `pier`, the
CLI. Deploys are a plain `git push`.

## What works today

The first slice, end to end:

1. `git push pier <branch>` over SSH, with the developer's own key.
2. pierd looks at the push (package.json and its framework, or plain HTML),
   builds it in a throwaway Docker container, and keeps the output as a
   deployment that never changes.
3. It points the branch's address at the new deployment (production for
   `main`), and reloads Caddy through its admin API.
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

Every deployment gets three kinds of address:

```
blog.pier.example.com                 production
feat-search--blog.pier.example.com    the latest deployment of a branch
blog-c19e77.pier.example.com          one deployment, forever
```

A failed build changes nothing: the address keeps serving what it served.

## Layout

```
cmd/pierd          the daemon: SSH, API, dashboard, builds, routing; init
cmd/pier           the CLI
web                the dashboard (React and Vite), built into internal/web
internal/names     addresses and IDs
internal/store     SQLite: projects, deployments, aliases
internal/build     detecting how to build, and running the build
internal/deploy    a push becoming a live address
internal/router    Caddy's config, loaded through its admin API
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

You need Go, git, Caddy, and Docker (OrbStack on a Mac).

```sh
make pier-caddy                    # terminal 1: Caddy, admin on :2019
make pier-dev                      # terminal 2: pierd, data in pier/.data
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
outside the repository): the GitHub App, promote and rollback, domains with
Caddy embedded in pierd, variables, request logs, and server health.
