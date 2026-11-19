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

Every deployment gets three kinds of address:

```
blog.pier.example.com                 production
feat-search--blog.pier.example.com    the latest deployment of a branch
blog-c19e77.pier.example.com          one deployment, forever
```

A failed build changes nothing: the address keeps serving what it served.

## Layout

```
cmd/pierd          the daemon: SSH, API, builds, routing
cmd/pier           the CLI
internal/names     addresses and IDs
internal/store     SQLite: projects, deployments, aliases
internal/build     detecting how to build, and running the build
internal/deploy    a push becoming a live address
internal/router    Caddy's config, loaded through its admin API
internal/gitssh    the SSH server that runs git
internal/hook      git's post-receive hook, which calls back into pierd
internal/board     the rows of the board
internal/api       the HTTP API, and the hook's local socket
scripts/e2e.sh     the end-to-end check
```

## Developing

You need Go, git, Caddy, and Docker (OrbStack on a Mac).

```sh
make pier-caddy                    # terminal 1: Caddy, admin on :2019
make pier-dev                      # terminal 2: pierd, data in pier/.data
./pier/bin/pierd keys add -data pier/.data ~/.ssh/id_ed25519.pub
./pier/bin/pier login --token "$(./pier/bin/pierd token -data pier/.data)"
```

Then, in any project:

```sh
git remote add pier ssh://git@localhost:2222/blog
git push pier main
open http://blog.pier.localhost:8088
```

`*.localhost` resolves to your machine in browsers and curl, so no DNS is
needed. `make pier-e2e` does all of this with throwaway data and ports.

## pierd's data folder

```
pier.db                  SQLite
repos/<project>.git      bare repositories
deployments/<id>/        built files, never changed once written
authorized_keys          keys that can push (pierd keys add)
api.token                the CLI's token (pierd token)
ssh_host_ed25519_key     the SSH host key
pierd.sock               the git hook's socket
```

## Next

Promote and rollback, logs, then the dashboard, then variables and domains,
then services and functions.
