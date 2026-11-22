# pier

The product: `pierd`, the daemon that runs on your server, and `pier`, the
CLI. Deploy from a connected GitHub repository or upload a folder with `pier deploy`.

## What works today

The first slice, end to end:

1. `git push pier <branch>` over SSH, with the developer's own key.
2. pierd looks at the push (package.json and its framework, or plain HTML),
   builds the site and keeps its static files as a
   deployment that never changes.
3. It points the branch's address at the new deployment (production for
   `main`), and reloads the router inside pierd.
4. The build is narrated back to the push as `remote:` lines.
5. `pier board` lists every address and what it serves.

And the start of the second slice:

6. `pierd init` checks the machine, creates the owner's account, and sets
   Pier's address and base domain, checking the DNS records for them.
   `pierd reset-password` is the way back in; Pier sends no password-reset email.
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

## Interactive CLI

Run `pier` in a terminal to open the cockpit: every project on the left, and the chosen one on the right with its addresses and deployments. It opens on the project the folder is linked to, and refreshes on its own, every few seconds while something builds. Tab moves between the panes, ↑ and ↓ move within one, and Enter opens a deployment. What can be done is listed as rows to run with Enter or a click: deploying the folder and rolling production back on the project, promoting, rolling back, opening, copying the address, logs, restarting and building again on a deployment. Each row shows its single key too: `d` deploys the folder as a preview and `D` to production, `p` promotes the selected deployment, `r` rolls production back, `l` opens logs, `b` shows the board, `J` shows the project's scheduled jobs, `/` finds a project or deployment, and `?` lists every key. The jobs screen lists each job's schedule in words, how its last run went and when it runs next; a job shows what it runs and its runs, newest first, with Run now (`n`) for members and the owner. A run's output opens like a log, followed live while it runs and ending with how it went. Jobs are added and changed in the dashboard's Jobs tab, which the screen offers to open. A click selects a row, and a second click or a double click runs it; the wheel scrolls lists. The mouse is off on the logs so their lines can be selected and copied. `pier deploy`, `pier board` and `pier deployments` open the same screens. Below 90 columns the projects pane hides and Tab picks a project instead. Piped output and `--plain` stay plain text for scripts, and `NO_COLOR=1` shows the screens without colour.

## Versions

`pier version` shows the CLI's release and, when you're signed in, the release of the `pierd` it talks to, and says when a newer release is out. When one is behind, it prints the command that updates it. `pier --version` prints only the CLI's, without asking the server. On the server, `pierd version` prints its release, and the dashboard shows it in the account menu and on the Server page. A build from source reports the commit it came from, like `dev-3f9a2c1`.

### Upgrading

- `pier upgrade` replaces the CLI with the latest release: it downloads the bundle for your computer from GitHub, checks it against the SHA256SUMS inside, makes sure it runs, and puts it in place of the running `pier`. If pier is in a folder you can't write to, it says so and prints the `sudo` command.
- `pier upgrade --server` asks `pierd` to update itself to the latest release, and follows it until the new `pierd` answers. Only the owner can. The owner can also press "Update to v…" on the dashboard's Server page, which shows when a new release is out (`pierd` asks GitHub every few hours).
- `pierd` runs as the `pier` user and can't replace its own binary, so the update itself is done by a small root helper the installer sets up (`pierd-upgrade.path` and `pierd-upgrade.service`). On a server installed before this release, run the installer once more to turn it on; until then, the Server page and `pier upgrade --server` say so, with the command.

## Preview builds

The default `pierd serve -builder railpack-preview` uses Railpack v0.43.0 and Docker Buildx for new previews from Git, GitHub, and the CLI. Docker pulls the pinned Railpack frontend on first use. HTML previews export their static files directly through BuildKit. Node builds use Node 22, and projects with explicit build commands, package-manager hints, or a custom Pier image use those settings in the Railpack plan.

Production static builds still use Pier’s existing Docker builder. Both builders publish static files to shared Caddy. Promoting a preview or rolling back switches the files served without rebuilding. Use `-builder docker` or `PIER_BUILDER=docker` to return new previews to the previous builder. BuildKit manages its own cache separately from the package cache shown on Pier’s Server screen.

## HTTP applications

Deploy an Express app with a `package.json` start script and no static entry point. The process should listen on `0.0.0.0` and `process.env.PORT`. For an app with both build and start scripts, such as server-rendered Next.js, select **Railpack web server** in the project’s build settings. Railpack supplies the image and its start command. Use `railpack.json` for provider-specific settings.

Pier detects a `Dockerfile` in the selected project root and builds it directly. Specify the HTTP port with the project’s `PORT` variable, the image’s `PORT`, or a single TCP `EXPOSE`. The default is 8080. A Compose project can deploy an individual HTTP service by selecting a root with its own Dockerfile. Compose stacks and prebuilt registry image imports are outside this release.

Use `pier deploy` for a preview or `pier deploy --prod` for production. Pier publishes one loopback port per container and forwards requests through Caddy. The startup probe accepts an HTTP response below 500 at `/` so an API can return 404 at its root. Set `PIER_HEALTH_PATH` to a readiness endpoint such as `/health` to require a 2xx or 3xx response. Pier also waits for any image-defined Docker health check. A failed startup leaves the previous deployment serving.

Each retained server deployment runs its own container, including older versions with immutable URLs. Promotion and rollback point production at that deployment with the environment it received when it was built and started. Deploy with `--prod` to use production variables. Docker restarts the process on exit. Pier rediscovers current port bindings after a restart. Preview cleanup removes eligible containers, anonymous volumes and image tags. Treat container files as ephemeral across deployments. View process `stdout`/`stderr` in the deployment's **Runtime logs** panel or with `pier logs <project> --runtime` (`--once` for a snapshot).

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

A server app's container keeps running while production, a custom domain or a rollback needs it. A preview's sleeps after 30 minutes without a visit, and an older deployment's sleeps once nothing serves it. The next visit to any of their addresses starts it again: that visit waits as long as the app takes to boot, up to a minute. Change the idle time with `pierd serve -sleep-after 2h`, or keep previews awake with `-sleep-after 0`.

## Monorepos

One GitHub repository can deploy several projects: import it once per app, and choose each one's root directory (Pier lists the repo's folders and suggests the apps it finds). Every project has its own address, variables, domains and logs. A push builds only the projects it changes: a project builds when the push touches its root directory, a workspace package it depends on, or a repo-wide file such as a lockfile. Turn on **Build on every push** in a project's Build settings to build it regardless.

## Production's address

A project's production address under the base domain is its name, `blog.pier.example.com`. To use another name, open **Domains** and choose **Change**. The old address stops working at once; previews and each deployment's own address keep the project's name.

## Custom domains

Open a project’s **Domains** tab and enter the hostname. Copy the records shown into your DNS provider. For a registrable apex, Pier also checks the www CNAME and redirects that hostname to the apex. The page shows the current DNS answers and rechecks pending domains every ten seconds.

Once DNS matches, the hostname follows the project’s production deployment, including promotions and rollbacks. With HTTPS enabled, the dashboard checks the certificate served by the local site listener before showing **Live, with HTTPS**. The default development listener serves HTTP.

## Operating Pier

- In **Shared variables**, add an encrypted value, choose Production or Preview, and link the projects that need it. A project’s own variable overrides the shared one in that scope. Redeploy to apply a change to an existing site.
- **Activity** shows server events and build history. Filter by project to find a deployment, promotion or rollback.
- **Server** samples CPU, memory and disk every minute, retains seven days of measurements, and shows service status and disk usage. Cleanup can clear the Pier package cache or remove inactive previews older than 30 days, including their build logs and saved uploads. Branch heads, production history and deployments from before cleanup tracking are protected.
- **Settings** saves the dashboard hostname and project base domain, checks DNS, and changes your password. Restart pierd to apply saved addresses. Explicit `-address` and `-domain` flags take precedence. Changing the password signs you out on every browser and linked computer.
- **Members** lists everyone who can sign in. The owner invites people with a link that works once, for 7 days (emailed, when email is set up in Alerts), as a member (deploys and changes every project) or a viewer (looks, and sees variables by name only), and can change a role or remove someone at any time; it applies at once, in every browser and on every computer. `pierd reset-password -email ada@example.com` resets someone else's password on the server and signs them out.

In a terminal, `pier logs` opens a live tail for the linked project. Request logs start with recent traffic from the last 24 hours. Runtime logs show the production container (or the latest ready server). Build logs open the latest deployment and end after its last line. Choose **Deployment history** in the menu to inspect an earlier deployment.

Use **Space** to pause and resume. While paused, new lines keep arriving and the counter shows how many you missed. Use **↑/↓** or **Page Up/Page Down** to read wrapped lines, **End** to jump to the live tail, and **Esc** to close. The viewer keeps the latest 1,000 lines. Set `NO_COLOR=1` for monochrome.

Pipes receive one plain snapshot by default. Use `--follow` to stream into another command or `--once` for a terminal snapshot. Upgrade `pierd` for the push-based log feeds. The CLI also follows older servers using their paginated endpoints.

```sh
pier logs docs --since 1h --status 404 --path /guide
pier logs docs --runtime
pier logs docs --build --deployment abc123
pier logs docs --follow | tee requests.log
```

## Linux installer

To install or upgrade a VPS directly from this repository in one command (auto-detecting `amd64` vs `arm64`, uploading the bundle, installing `pierd.service`, and running `pierd init` on a fresh server):

```sh
./pier/scripts/vps-install.sh root@YOUR_VPS_IP
# or: make pier-vps HOST=ubuntu@YOUR_VPS_IP
```

Once release tarballs are hosted on a URL or GitHub Releases, [install/get.sh](install/get.sh) supports `curl -fsSL <url> | sudo sh` (configured via `PIER_BASE_URL` or `PIER_REPO`).

For manual installation from the repository root, run `make pier-web-deps` once, then `make pier-release ARCH=amd64` or `make pier-release ARCH=arm64`. Copy the matching archive from `pier/bin/releases` to the Linux machine. For example, on an ARM64 server with systemd, git, Docker Engine and the Buildx plugin installed:

```sh
tar -xzf pier-linux-arm64.tar.gz
cd pier-linux-arm64
./install.sh --check
sudo ./install.sh
sudo /usr/local/bin/pierd init
```

The installer verifies the bundle, creates the `pier` service account and gives it access to the local Docker engine. On apt-based systems it can install Docker with your confirmation. Running the installer again upgrades the binaries and restarts an active service. Pier stores its database, uploads, deployment files and keys under `/var/lib/pier`.

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

You need Go, git, and Docker with Buildx (OrbStack on a Mac).

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

## Public rollout

The public VPS test is deferred. It covers a fresh systemd installation, delegated DNS and publicly trusted certificates for the dashboard and project addresses.
