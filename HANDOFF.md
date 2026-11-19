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
  - `pier` is the CLI on a laptop: `pier login`, `pier deploy`, `pier logs` (not built), `pier rollback`.
- **Two ways to deploy.**
  - **GitHub (not built yet).** Each owner creates their own GitHub App from the dashboard, through GitHub's app-manifest flow, in one click. Every push deploys: the production branch goes to production, and other branches and pull requests get previews (`pr-42--agentman.lenajeremy.xyz`). Pier posts the preview link on the pull request. Pull requests from forks wait for the owner's approval.
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
  - **Caddy:** embedded in pierd as a Go library (not built yet; slice 1 still drives an external Caddy).
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
   - The remaining Logs work is the `pier logs` CLI command.

**Placeholders for now:**
- **Pages:** Activity, Integrations, Shared variables, Server and Settings, plus the project’s Domains tab. They say what they'll hold.

## 6. Running and testing

```sh
make pier          # builds pier/web (React and Vite) into pier/internal/web/dist, then pierd and pier
make pier-test     # go vet, go test, and the dashboard's type-check
make pier-e2e      # end to end: pushes, init, sign-in, pier login, pier deploy, settings, reset-password
make pier-web-dev  # the dashboard with live reload on :5175, proxying /api to pierd on :7777
```

**Ports.** The end-to-end script uses ports 12019, 12222, 17777 and 18088, so don't run another instance on them. A manual test instance can use 22019 for Caddy's admin, 27777 for the dashboard and API, 28088 for sites, and 22222 for SSH.

**macOS limits unix socket paths to 104 characters,** so put the data folder somewhere short, like `mktemp -d "$TMPDIR/pd.XXXX"`.

**A manual instance:**

```sh
printf '{"admin":{"listen":"127.0.0.1:22019"}}' > caddy.json && caddy run --config caddy.json &
pier/bin/pierd serve -data $D -domain pier.localhost -http :28088 -ssh 127.0.0.1:22222 \
  -api 127.0.0.1:27777 -caddy-admin http://127.0.0.1:22019 &
echo "a long password" | pier/bin/pierd init -data $D -email you@example.com -password-stdin \
  -address pier.localhost -base-domain pier.localhost -ip 127.0.0.1
```

**To sign a browser in without typing a password:** run `curl -c jar -H 'X-Pier: 1' -d '{"email":…,"password":…}' http://127.0.0.1:27777/api/session`, then set `pier_session=<token from the jar>` with `document.cookie` in the page.

Everything so far was tested on macOS: pierd runs natively, and builds run in Docker through OrbStack. These are untested and need Linux, either an OrbStack Ubuntu machine (`orb create ubuntu`) or the real VPS (80.20.201.29):
- the swap and systemd steps of `pierd init`,
- binding ports 80, 443 and 53 as root,
- real DNS and Let's Encrypt.

## 7. Code map

- **`pier/cmd/pierd`:** `main.go` (serve), `init.go` (init and reset-password), `term.go` (terminal output in the design's colours).
- **`pier/cmd/pier`:** `main.go` (login, board, deployments), `deploy.go` (link, pack, upload, narration).
- **`pier/internal`:**
  - `api`: routes in `api.go`; session, projects, create (projects, settings, upload), cli (login approval), sessions.
  - `auth`: passwords and the lock.
  - `store`: SQLite, with migrations in `store.go`.
  - `deploy`: the build pipeline and its sources, plus logs.
  - `build`: detection, overrides and runners.
  - `bundle`, `dnscheck`, `router` (the external Caddy for now), `web` (the embedded dashboard).
- **`pier/web/src`:** `App.tsx` (routes), `components/` (Shell, ui, Icons, ProjectMark), `pages/`, `flap/`, and `styles/` (tokens, base, components; the comments name the board component each style copies).

## 8. What to build next

The brief's backend order:

1. **The GitHub App** (skipped so far because its webhooks need a public HTTPS address; build it once Pier runs on the VPS or behind a tunnel). The manifest flow from Integrations (C1, C2); webhooks for pushes and pull requests; commit statuses with the preview link; `pr-N--` previews; approval for pull requests from forks. Then D1's repo list and D2's Configure screen, for imports. The SSH git push from slice 1 is to be dropped once this works.
2. **Promote and rollback:** done (see §5).
3. **Caddy embedded in pierd,** with the `_acme-challenge` DNS responder. Then wildcard and on-demand certificates, and custom domains (F1, F2: records, live checks, and what DNS returns now), with the Certificates step of A4. Then `pierd domain set`.
4. **Variables:** done (see §5). Still to do: Shared variables (K2), linked into several projects; a project's own variable wins.
5. **Request logs:** the dashboard and project summary are built (see §5). The CLI still needs `pier logs`.
6. **Server health (I1):** disk breakdown and clean-up, CPU, memory, and services.
7. **Activity (K1), Settings,** the install script (A1), and `pier logs`.

**Known gaps:**
- The board doesn't show the tiles or the single Visit yet. Update the frames when convenient.
- Row J (the CLI screens) isn't drawn.
- A failed build's preview box shows a generic error; showing the first error line from the log would be better.
- Static uploads serve dotfiles such as `.gitignore`.
- Kept upload sources (`data/sources/`) are never cleaned up yet; the Server page's clean-up should cover them.
- Values with newlines work in builds, but the dashboard's single-line field can't enter them; an "Import .env" (G1 shows the button) would help.
- Sessions made by non-browser clients are named "A browser".
