# Handoff: building Pier's dashboard and daemon

Read this first, then `pier/README.md`. It's written for the next agent picking
up the implementation. Everything here comes from the owner (Jeremiah) or was
decided with him. Follow it closely: he reviews every detail and has strong,
specific preferences.

## 1. What Pier is

An open-source Vercel that runs on your own VPS. Take inspiration from Vercel
and Railway, but never Railway's canvas.

- **Two programs.**
  - `pierd` is the daemon on the VPS: builds, deployments, routing and the dashboard. It also has the owner-only commands, run over SSH: `pierd init`, `pierd reset-password`, `pierd domain set` (not built), `pierd status` (not built).
  - `pier` is the CLI on a laptop: `pier login`, `pier deploy`, `pier logs`, `pier rollback`.
- **Two ways to deploy.**
  - **GitHub.** Each owner creates their own GitHub App from the dashboard, through GitHub's app-manifest flow, in one click. Every push deploys: the production branch goes to production, and other branches and pull requests get previews (`pr-42--agentman.lenajeremy.xyz`). Pier posts the preview link on the pull request. Pull requests from forks wait for the owner's approval.
  - **The CLI (built).** `pier deploy` makes a preview, and `pier deploy --prod` deploys to production, as on Vercel.
- **Projects are created first, then deployed.**
- **Build presets:** static HTML and Vite now; Next.js and Node servers later. Pier detects the preset. The owner can override the root directory, install command, build command and output folder (and, later, start command and port).
- **Previews:** frontends get a preview URL per deployment. Server apps don't, until cold starts exist.
- **Setup happens in the terminal.** `pierd init` runs over SSH right after the install script. It checks the machine, creates the owner's account, and asks for Pier's address and the base domain. Nobody else can claim a fresh server, and no password crosses plain HTTP.
- **Pier's own address** is a domain like `pier.lenajeremy.xyz`, or the bare IP with a Let's Encrypt IP certificate (short-lived, renewed automatically). sslip.io was dropped: it isn't on the Public Suffix List, so all its users share one Let's Encrypt allowance of 50 certificates a week.
- **Addresses.** Test URLs live under the base domain:
  - `agentman.lenajeremy.xyz` is production,
  - `feat-x--agentman.lenajeremy.xyz` is a branch's latest deployment,
  - `agentman-c19e77.lenajeremy.xyz` is one deployment, forever.

  Each project can also have custom domains (like `agentman.xyz`). Pier shows the DNS records to add and checks them.
- **Certificates.**
  - **Wildcard:** one certificate for `*.<base>`. pierd answers the ACME DNS challenge itself, after the owner adds an NS record handing `_acme-challenge.<base>` to the Pier server. This works with any DNS provider, including Namecheap, the owner's provider, whose API is gated.
  - **On demand:** the fallback.
  - **Caddy:** embedded in pierd as a Go library. An explicit `-caddy-admin` still connects to an external instance.
  - The DNS responder's requirements are in the dashboard brief, under "Certificate check".
- **Docker:** the install script offers to install it.
- **Accounts:** one owner account for now; teams and permissions come later. A forgotten password is reset over SSH with `pierd reset-password`; Pier never sends email.
- **Environment variables** have Production and Preview scopes. In Vite, only `VITE_` names reach the browser, and changing a variable needs a redeploy.
- **Logs are required.** Analytics and observability come later.
- **The Board** (a split-flap view of every address) is not in v1. The home page is the list of projects with their status.

The full product brief, with the moments, screens, decisions and certificate
research, is `design/v3/dashboard-brief.md`. It's on disk but not in git.

## 2. Where the design lives

The design is the source of truth. It's on the Screenshotter board **"Pier website Claude"** (id `menmd0tudnok`):

- **Dashboard v1:** frames for every flow, in rows A to K:
  - A: install and `pierd init`
  - B: sign in, and the empty Projects page
  - C: Integrations and GitHub
  - D: new project and first deploy
  - E: project home, previews, a failed build, a rollback
  - F: domains
  - G: variables
  - H: logs, including H2, the custom date range
  - I: server
  - K: Activity, Shared variables, Members

  Row J (the CLI) was never drawn.
- **V3 Foundations 1–6:** brand, colour, type, spacing, tiles, motion.
- **V3 Components 1–6:** every component. Use these.
- **V3 Patterns:** Patterns 2 (the dashboard) still shows an old top bar and needs refreshing to the sidebar layout.

**Build from the board, not from memory.** Get exact values with `board_read` (`what: "html"`) on a frame or component. You can also read the board's data file at `~/Pictures/Screenshotter/.studio/boards/menmd0tudnok.json`: `nodes` keyed by id, each with `style`, `text`, `children`, and `master` for component copies; SVG paths are `type: "vector"` nodes.

## 3. Design rules (he enforces all of these)

- **Follow the design system.** Copy its components; never invent a size or a colour. If something is missing, add it to the system on the board first, then use it.
- **Type: only the Foundations 3 scale.** Its "In the dashboard" mapping:

  | Style | Size and weight | Used for |
  | --- | --- | --- |
  | Title | 20/28 500 | Page titles |
  | Small | 15/24 | Panel headings, row names, values |
  | Caption | 14/20 | Sidebar, tabs, table cells, form labels |
  | Note | 13/19 | Meta lines, table headers, detail labels |
  | Mono ID | 14/20 | IDs, addresses, commits |
  | Mono output | 13/21 | Logs |
  | Mono command | 15/22 | Commands |

  Buttons, badges, fields and the segmented control keep their own component sizes. Some component masters still show 12.5px and 13.5px; follow the scale, not those.
- **Colours** (`pier/web/src/styles/tokens.css` has them all):
  - Status: pink `#F49CCB` for production and live; violet `#BCAEF8` for preview and building; teal `#62D8C6` for done and healthy; red `#FF8080` for failed (`#FFB4B4` as text); amber `#F0B85C` for warnings; grey `#8E96AA` for nothing deployed.
  - Charts: blue `#3987E5`.
  - Text: `#ECEEF4`, `#A3ABBE`, `#8A92A8`, `#5E667B`.
  - Page background `#07090F`. Panels `rgba(14,18,28,0.92)` with a `rgba(255,255,255,0.09)` border.
  - Fonts: Schibsted Grotesk and Red Hat Mono.
- **When Vercel has the same screen, start from Vercel's layout.**
- **Place each element next to what it relates to.**
  - No dead side columns.
  - No duplicated facts.
  - No two buttons doing the same thing: one Visit per page.
  - A chart goes beside the number it explains.
  - Merge panels that say the same thing.
- **Panels are full width.** Limit the width of long text instead of narrowing panels.
- **Show only useful, real data.**
  - A section that isn't built says plainly what it will hold. Never show invented data.
  - Leave out buttons that don't work yet.
- **Banned:** coloured accent bars and coloured left borders, glow blobs, pill eyebrows, mono labels as decoration, and anything that looks generated.
- **Layout:** projects are a list, not cards and not a canvas. Clean and compact.
- **Corners are concentric:** a nested shape follows its container. No rounded rectangle inside a pill.
- **Motion acts out the product.** Diagrams demonstrate the mechanism, text entrances stay subtle, and nothing bounces.
- **Split-flap tiles** (the flipping text) appear in exactly three places, by his choice:
  - production's deployment ID on a project's home, in 22×32 tiles;
  - a deployment's status on its page;
  - each project's status in the projects list.

  A row shows its value at once when a page opens and flips only when the real thing changes. Status words have eight tiles, so BUILDING fits, but empty tiles are hidden. The engine is copied from the website, `site/v3/src/flap`.
- **Logs (H):**
  - Requests and errors are two line charts on one time axis, each with its own scale, with the total beside each chart.
  - The range control is 15m, 1h, 24h, 7d and Custom. Custom opens the date-range picker, up to 6 months back.
  - Log lines are kept 14 days and hourly counts 6 months, and the picker says so.
- **Sidebar:**
  - Global sections first: Projects, Activity, Integrations, Shared variables, Members, Server, Settings.
  - Then every project, with its status swatch and a "+" for a new project.
  - At the top, the switcher: its name, with the server address as a quiet note, and no box.
  - Inside a project, tabs: Deployments, Domains, Variables, Logs, Settings.
- **Building a dashboard screen:** every page is made from the same pieces, in `pier/web/src/components`. Use them rather than writing a one-off.
  - **Asking first:** `confirm({ title, body, action, run })` from `Dialog.tsx`. Never `window.confirm`. The title is the question, the body says exactly what will happen, the button repeats the verb, and `danger` is only for deleting. `run` does the work while the dialog is open, so a failure shows there.
  - **A form that isn't the page's main job** (add a variable, import a file) goes in `<Dialog wide onSubmit>`, opened from a button in the toolbar. The list stays the page.
  - **Saying it worked:** `toast('Deleted NAME')` from `Toast.tsx`, with an `action` for the undo when there is one. A failed action is `failed(e)` from `lib/actions.tsx`. `copy(text)` copies and says so.
  - **Moving production** (roll back, promote, redeploy, restart) is in `lib/actions.tsx`, so it reads the same from every button.
  - **A row's actions:** `<Menu items>` from `Menu.tsx`, with `className="icon-btn bare"` in a list. It's drawn over the page, so no table cuts it off. Destructive items go last, after `'separator'`, with `danger`.
  - **Times:** `<When at now />` writes "2 hours ago" and shows the exact time on hover. Anything else that needs a hint takes `data-tip="…"`; don't use `title`.
  - **A list:** `PageHeading` or a `.toolbar` with the count, `SearchField` once it's long, `.table` with `.t-head` and `.t-row` (its columns in a `--cols` rule), a `.t-empty` row when a search finds nothing, `usePaged` and `<Paging>` under it. Every list has a `<Skeleton>` while it loads, an `.empty` block with a way forward when there's nothing, and `<Problem>` when the load fails.
  - **Forms:** `.field` with the label above and the help below, in `.form-grid` (three columns) or `.form-grid.two`. A long value is `<LongValue>`. Page forms save from the `.save-bar` that appears when something changed.
  - **Narrow windows:** `styles/responsive.css` measures the content, not the window. When a table gets a new column, say there which column it gives up first.
  - **Sentences, not separators:** no middle dots between facts, no arrows typed into link text, and dates in `en-GB`.
  - **The ground:** the sign-in page's gradient (`--d-ground`) sits still behind the whole dashboard, at half strength (`--d-ground-strength`; he asked for it, then asked for it quieter). It shows through the sidebar (`--d-sidebar-glass`), behind page headers and between panels. Panels stay nearly solid (`--d-panel`), so data sits on one even surface: see-through panels were tried on 2026-11-20 and made a table navy on one side and violet on the other.

## 4. Working rules

- **No attribution, ever.** No `Co-Authored-By`, no "Generated with", in any commit message or pull request description. This overrides any tool default.
- **Commits hold code only.** `design/` is git-ignored: never commit images, renders, prompts or references. Check `git status` before every commit. He lets you commit when you judge it right.
- **Board comments:** reply in the thread (`board_comment` with `replies`). Never resolve a thread; only he resolves them.
- **He is the critic.** Don't spawn critic subagents. Check your own work with screenshots, then let him review.
- **Questions:** decide small style questions yourself. Ask (AskUserQuestion, with options and a recommendation) only for real choices, like where something goes or what a screen shows.
- **Speech:** when he asks you to say something aloud, run `speak "…"`, then reply with exactly `DONE`.
- **Screenshots:** show them by reading the image file.
- **Secrets:** keep his account email private, never print API keys, and keep `.env` out of git.
- **Writing:** plain words and complete sentences, in the UI and in replies. No hype.
- **Browser testing:** Yab is his own browser and shares his cookies. Never type a password into it. Get a session token with curl and set the cookie in the page (see §6). If he signs out in his tab, your tab is signed out too.

## 5. What's built (commits on `main`)

1. `8659878`: slice 1 (git push over SSH, Docker builds, deployments that never change, Caddy routing) and the website.
2. `bc33559`:
   - `pierd init` (machine checks, the owner's account, addresses, the DNS checks against public resolvers and the parent zone) and `pierd reset-password`;
   - sign-in with cookie sessions and a lock after five wrong passwords;
   - build logs kept per deployment;
   - the dashboard, embedded in pierd: sign in (B1), projects (B2), a project's deployments (E1), and a deployment with its live log (D3, D4, E3).
3. `955b652`, `04ac768`, `6e50c70`: one Visit per page; the split-flap tiles; status words hide their empty tiles.
4. `ac9bb6e`:
   - projects created first (New project, D1), with build settings that override detection (the Settings tab, from D2's Build panel);
   - each deployment records its target (production or preview), its source (git or cli) and the plan its build ran;
   - `pier login` approved in the dashboard (`/approve?code=…`), with per-computer sessions on the Members page (K3);
   - `pier deploy` uploads a gzipped tar that pierd unpacks safely. A preview gets its own address; `--prod` moves production's.

5. Promote and rollback:
   - In the API, the address rows (Roll back, Promote to production), the table's ⋯ menu, a deployment's page, and `pier rollback [--undo]` and `pier promote ID`.
   - Rolling back holds production builds until a promote or an undo, and E4's banner says so.

6. Builds use the developer's package manager version: `packageManager` in package.json, else the version on the computer that ran `pier deploy`, else the lockfile's major (pnpm 10, Yarn 1 or 4). The log says which.
7. Paths that aren't files get a fallback: index.html for Vite and Create React App builds (paths with an extension still 404), the site's own 404.html with a 404 status, or a plain 404.

8. Variables (G1):
   - Values are sealed with AES-GCM using `data/secret.key`, per scope (production, preview), and passed to builds through the environment. The build log lists names only.
   - The tab has an add form, a list with reveal, change and delete, and a banner when production's build is older than a variable change, with Redeploy.
   - Redeploy (also on a deployment's page) rebuilds a deployment's source with today's settings and variables. Uploads are kept in `data/sources/` so they can be rebuilt.

9. Request logs (H1):
   - Caddy streams its JSON access log to `data/access.sock`. pierd keeps each request (time, method, path, status, duration, size, deployment; never the visitor's IP) in `data/requests.db`: lines for 14 days, hourly counts for 6 months.
   - The Logs tab has filters (path search, status, the 15m, 1h, 24h and 7d range), the requests and errors line charts with hover, and the live list.
   - H2’s Custom picker accepts local calendar days up to six months back. Ranges ending today follow live. Filtered charts use the 14 days of request lines.
   - The project home shows yesterday’s requests and errors when either of the previous two days had traffic. Each number opens the matching range in Logs.
   - `pier logs [PROJECT]` prints requests in chronological order. It supports `--follow`, `--path`, `--status`, `--since` and `--limit`.

10. GitHub deployments:
    - Integrations registers a private GitHub App through the manifest flow. Install it on selected repositories. New project imports a repository, confirms the production branch and build overrides, and deploys its current head.
    - Signed push and pull-request deliveries are queued in SQLite. Redelivery of the same delivery ID is idempotent. Fork builds wait in Integrations for approval of a particular commit. Changes to the head require fresh approval.
    - The build uses an installation token scoped to the imported repository and stores the immutable source archive for redeploy. Production and branch aliases move on success. GitHub receives a commit status, and a successful PR build gets a preview comment.
    - The real registration, installation, push, PR, failed production build and recovery were exercised with one private repository on a Mac through a webhook-only HTTPS tunnel. The fork approval path was exercised against an isolated GitHub API fixture.
    - For a VPS, use a stable public HTTPS URL for `/github/events` and routable project addresses. A local test tunnel exposes the webhook alone. The server key and database together are needed to recover App credentials.

11. Project domains (F1–F3):
    - The owner can attach and remove a domain, inspect the current A/CNAME answers and check again. A registrable apex includes a permanent www redirect. Verified names follow the current production deployment.
    - The Domains page distinguishes pending DNS, pending HTTPS, HTTP development mode and verified HTTPS. F3 on the board covers the certificate wait.
    - Embedded Caddy restores production, branch and immutable routes from SQLite on restart. Local Pebble tests issue and serve custom and wildcard certificates. The delegated DNS responder has TCP and UDP tests. Activating that responder on public interfaces and verifying a publicly trusted certificate on a VPS are outstanding.

12. Shared variables (K2) are encrypted, scoped and linked to selected projects. Project variables override shared values in the same scope. The project Variables tab lists inherited values by name.
13. Activity (K1) records builds, production changes, sign-ins, approvals and owner actions. Older projects and completed builds are included when upgrading.
14. Server (I1) shows current resource usage, 24-hour and 7-day charts, service status, disk usage by category, and controls for clearing the package cache and inactive previews.
15. Settings (K4) manages dashboard and base addresses, checks DNS, links to the GitHub App and sessions, and lets the owner change their password. Address changes take effect on restart. Password changes revoke the owner’s browser and CLI sessions.
16. `make pier-release ARCH=amd64` and `ARCH=arm64` produce Linux bundles with checksums and a systemd installer. See `pier/README.md` for setup.
17. Fixes from the 2026-11-20 review, on branch `dashboard-ui`. Behaviour worth knowing:
    - **Server apps:** production (and its custom domains), the rollback target, the deployment a rollback came from and anything building always run. A preview's container (a branch's address, or `pier deploy`'s newest of a branch) sleeps after `-sleep-after` (default 30 m, `PIER_SLEEP_AFTER`; 0 keeps previews awake) without a visit; everything else sleeps as soon as nothing serves it. Images stay. A sleeping app's addresses route to pierd's loopback waker (`deploy.WakeHandler`, `X-Pier-Wake` header set by Caddy), which starts the container, answers that request through it (the visitor waits for the boot, up to 60 s) and moves the routes back. A failed start answers 503 for a minute before trying again. Waking or a manual Start/Restart keeps it up until it goes `-sleep-after` without a visit (30 m for superseded ones when that is 0). Visits come from the request log (`requests.Log.LastSeen`), in memory, so after a pierd restart previews get a fresh idle period. The sweep runs every quarter of the idle time (5 s to 1 m). Promoting or rolling back to a sleeping one starts it and waits until it answers before moving production, and fails with a clear error if it won't start. Containers use `unless-stopped`.
    - **Routes:** each host gets one route (dashboard, then aliases, then deployment addresses, then custom domains), so a collision can't stop Caddy from loading. A failed Docker probe keeps the last known app endpoints. Proxies keep WebSockets open for 5 minutes across reloads.
    - **Wildcard certificates:** dropped only after 5 failed checks in a row, and never while an unexpired wildcard certificate is in storage. On Ubuntu, the DNS responder binds every non-loopback interface address.
    - **GitHub:** `DELETE /api/github` disconnects the App, and `POST /api/github/jobs/{id}/dismiss` dismisses a waiting build (both in Integrations). A restart mid-build rebuilds the commit, at most three times: then the job waits in Integrations with a note, and Try again resets the count (`github_jobs.interrupted`). A delivery is streamed through its signature check into a temp file, so an unsigned one never sits in memory. A fork's pull requests wait for approval in GitHub's order (`pull_request.updated_at`, `github_jobs.pr_updated`), not arrival order, so a late delivery of an older commit is filed as superseded. pierd waits up to 30 s for the queue to unwind on shutdown. Events follow the repo's current installation. Temporary GitHub errors retry three times. Closing a pull request or deleting a branch retires its preview address. One preview comment per pull request. Links inside a repo are recreated, if they stay inside it.
    - **Logs:** chart days start at each real midnight in the viewer's zone (`zone`, an IANA name, on the chart request; `tz`, minutes east of UTC, is the fallback), so the day of a clock change is 23 or 25 hours. pierd embeds `time/tzdata`.
    - **Dashboard data:** a hidden tab doesn't poll. A project page polls every 3 s while something builds, otherwise every 15 s. A reload no longer stops a page's polling.
    - **Housekeeping:** activity older than a year is deleted; the build cache is measured in the background at most every 5 minutes; preview clean-up removes files even when Docker is down.

## 6. Running and testing

```sh
make pier          # builds pier/web (React and Vite) into pier/internal/web/dist, then pierd and pier
make pier-test     # go vet, go test, and the dashboard's type-check
make pier-e2e      # end to end: pushes, init, sign-in, pier login, pier deploy, settings, reset-password
make pier-web-dev  # the dashboard with live reload on :5175, proxying /api to pierd on :7777
```

**Ports.** The end-to-end script uses ports 12222, 17777 and 18088, so don't run another instance on them. A manual test instance can use 27777 for the dashboard and API, 28088 for sites, and 22222 for SSH.

**macOS limits unix socket paths to 104 characters,** so put the data folder somewhere short, like `mktemp -d "$TMPDIR/pd.XXXX"`.

**A manual instance:**

```sh
pier/bin/pierd serve -data $D -domain pier.localhost -http :28088 -ssh 127.0.0.1:22222 \
  -api 127.0.0.1:27777 &
echo "a long password" | pier/bin/pierd init -data $D -email you@example.com -password-stdin \
  -address pier.localhost -base-domain pier.localhost -ip 127.0.0.1
```

**To sign a browser in without typing a password:** run `curl -c jar -H 'X-Pier: 1' -d '{"email":…,"password":…}' http://127.0.0.1:27777/api/session`, then set `pier_session=<token from the jar>` with `document.cookie` in the page.

Local checks use macOS, OrbStack Docker builds and Linux containers. Public DNS delegation, public certificate issuance and a fresh VPS installation remain deferred until a VPS and domain are available.

## 7. Code map

- **`pier/cmd/pierd`:** `main.go` (serve), `init.go` (init and reset-password), `term.go` (terminal output in the design's colours).
- **`pier/cmd/pier`:** `main.go` (login, board, deployments), `deploy.go` (link, pack, upload, narration).
- **`pier/internal`:**
  - `api`: routes in `api.go`; session, projects, create (projects, settings, upload), cli (login approval), sessions.
  - `auth`: passwords and the lock.
  - `store`: SQLite, with migrations in `store.go`.
  - `deploy`: the build pipeline and its sources, plus logs.
  - `build`: detection, overrides and runners.
  - `bundle`, `dnscheck`, `router` (embedded Caddy and external admin compatibility), `web` (the embedded dashboard).
- **`pier/web/src`:**
  - `App.tsx` (routes), `pages/`, `flap/`, `lib/` (`api`, `data`, `router`, `format`, and `actions` for what moves production).
  - `components/`: Shell (the sidebar), CommandMenu (⌘K), ui (headings, tables' paging and search, loading and error states), Dialog, Toast, Tooltip, Menu, BuildFields, the charts, Icons, ProjectMark.
  - `styles/`: tokens, base, components (the comments name the board component each style copies), overlays (dialog, toast, tooltip, menus) and responsive (narrow windows, loaded last).
  - `PIER_API=http://127.0.0.1:PORT npx vite` serves the dashboard against a pierd on another port.

## 8. What to build next

The brief's backend order:

1. **The GitHub App:** built and tested (see section 5). The older SSH push path is still available. Retiring it needs a migration for existing callers and test coverage.
2. **Promote and rollback:** done (see §5).
3. **Domains and embedded Caddy are built.** The challenge responder and wildcard issuance have been tested with a private CA. Connect the responder to production daemon startup, verify delegated DNS and public HTTPS with an authorized VPS and domain, then finish the Certificates step of A4 and `pierd domain set`.
4. The local operator features are implemented, including shared variables, CLI logs, health monitoring and cleanup, activity, settings and the Linux installer. See `5 and `pier/README.md`.

**Known gaps:**
- The board doesn't show the tiles or the single Visit yet. Update the frames when convenient.
- Row J (the CLI screens) isn't drawn.
- A failed build's preview box shows a generic error; showing the first error line from the log would be better.
- Static uploads serve dotfiles such as `.gitignore`.
- Values with newlines work in builds, but the dashboard's single-line field can't enter them; an "Import .env" (G1 shows the button) would help.
- Sessions made by non-browser clients are named "A browser".
