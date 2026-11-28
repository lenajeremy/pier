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
- **Accounts:** one owner (the account `pierd init` made), and the members and viewers the owner invites by link. Roles hold across the whole server; per-project access comes later. A forgotten password is reset over SSH with `pierd reset-password` (the owner's, or `-email` for anyone's); Pier sends no password-reset email.
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
    - **GitHub:** `DELETE /api/github` disconnects the App, and `POST /api/github/jobs/{id}/dismiss` dismisses a waiting build (both in Integrations). A restart mid-build rebuilds the commit, at most three times: then the job waits in Integrations with a note, and Try again resets the count (`github_jobs.interrupted`). A delivery is streamed through its signature check into a temp file, so an unsigned one never sits in memory. A fork's pull requests wait for approval in GitHub's order (`pull_request.updated_at`, `github_jobs.pr_updated`), not arrival order, so a late delivery of an older commit is filed as superseded. pierd waits up to 30 s for the queue to unwind on shutdown. Events follow the repo's current installation. Temporary GitHub errors retry three times. Closing a pull request or deleting a branch retires its preview address. One preview comment per pull request, listing every project's preview. Links inside a repo are recreated, if they stay inside it.
    - **Logs:** chart days start at each real midnight in the viewer's zone (`zone`, an IANA name, on the chart request; `tz`, minutes east of UTC, is the fallback), so the day of a clock change is 23 or 25 hours. pierd embeds `time/tzdata`.
    - **Dashboard data:** a hidden tab doesn't poll. A project page polls every 3 s while something builds, otherwise every 15 s. A reload no longer stops a page's polling.
    - **Housekeeping:** activity older than a year is deleted; the build cache is measured in the background at most every 5 minutes; preview clean-up removes files even when Docker is down.
18. Monorepos and production addresses, on branch `monorepo`:
    - **One repo, several projects:** `github_repos` is keyed by project (migration 13), so importing a repo again makes another project, usually with another root directory. A push queues one job per project; each builds and reports its own commit status (`Pier / <project> / <branch>`). A fork's pull request waits once per commit: `GET /api/github` groups waiting jobs of one delivery (`projects: []`), and approving or dismissing acts on all of them. The catalog lists `projects: []` per repo, and the Integrations and New project lists say which projects a repo deploys, with Add project.
    - **Skipping what a push doesn't change** (`api/github_affected.go`, `internal/monorepo`): a project with a root directory compares the push with its last real build of that branch (or production's head for a new preview) using GitHub's compare API, and builds only if a changed file is in its root, in a workspace package it depends on (npm, yarn, bun and pnpm workspaces, followed transitively), or is a repo-wide file like a lockfile. Otherwise the job ends `skipped`, GitHub gets a success status saying nothing was built, and Activity records it. Any doubt (300+ files, a force push, an API error, a changed root) builds. `always_build` (migration 14, "Build on every push" in Build) turns skipping off.
    - **Folder picker** (`components/FolderPicker.tsx`): the Root directory field lists the repo's folders. `GET /api/github/repositories/{id}/folders` (import) and `GET /api/projects/{name}/folders` (settings: GitHub, the bare git repo, or the last upload) answer `{ref, branch, folders: [{path, kind, app}], truncated}`, cached 5 minutes. The list opens on focus at the top level; choosing a folder shows its folders; typing narrows them and finds matches further down. On import, a repo with two or more apps shows them as choices that fill the root and the name, and say which project already deploys each.
    - **Renaming production's address** (`PUT /api/projects/{name}/address {subdomain}`, migration 15, `store.Project.Label`): Domains, Change. Only production's address moves, in one transaction with its alias; previews and deployment addresses keep the project's name. A name another project goes by (name or address), a custom domain's, the dashboard's, or one shaped like another project's deployment address is refused, and new projects can't take a renamed address. A new base domain keeps the chosen name.
19. Versions, CLI polish and loading states, on branch `versioning`:
    - **Versions:** `internal/version` holds the release, set at build time with `-ldflags "-X pier/internal/version.Version=v0.6.0"` (the release scripts and `make pier` use `git describe --tags --always --dirty`; `VERSION` overrides). Without it, Go's build info gives `dev-<commit>`, with `-dirty` for uncommitted changes. `pierd version`/`--version`, the startup log and banner; `GET /api/version` (signed in) and `server.version` in `GET /api/session`; `pier version` (both, plus the update command for whichever is behind; an old pierd without the endpoint reads as older than v0.6.0) and `pier --version` (the CLI only, offline). The menu shows the release on the tagline row. The server installer prints the version it installed.
    - **CLI:** `pier` on a terminal opens the cockpit (design: board page CLI, artboard "CLI C2: The cockpit, with the board's tiles"; mock generator kept outside the repo). Projects pane on the left, the project's addresses (status tiles) and deployments on the right, single keys along the bottom (d, D, p, r, l, b, /, ?, q), refreshing every 3 s while something builds and 15 s otherwise. `pier deploy` on a terminal plays the four steps (Push, Build, Deploy, Go live) with tile numbers above the log; `pier board` is the server board with tiles. Below 90 columns the projects pane hides behind Tab; NO_COLOR turns tiles into words. Every action is also a row: the detail screen, home, the deploy screen, dialogs and sign-in list what can be done, with the key on the right; arrows and Enter run them, and the mouse works (a click selects, a second click or a double click runs, the wheel scrolls; off on the log screen so log lines stay selectable). Code: `cmd/pier/cockpit*.go`, action rows and clicks in `cockpit_actions.go`, drawing helpers in `frame.go`. Piped and `--plain` output is unchanged. Waits on pierd outside the full-screen views show a spinner on stderr after 250 ms (`spin`, `callWith` in `cmd/pier/spinner.go`), only on a terminal.
    - **404:** an address nothing is deployed at gets `internal/router/pages/notfound.html` in a browser (Accept contains text/html): 404 built from the website's figure boxes with the 0's top corner box missing, and what to do next. Its fonts are inlined at startup from `pages/*.woff2` (OFL), so it loads nothing from elsewhere; it reads the address with JavaScript, never from the request. Everything else still gets the one plain line.
20. Teams:
    - **Roles** (migration 16, `users.role`): the owner does everything; a member deploys, promotes, rolls back, restarts and changes every project (variables, domains, settings, creating and deleting projects, shared variables, approving fork builds); a viewer looks, and sees variables by name only. The owner alone manages people, the server's addresses, cleanup and the GitHub App. The owner can't be removed or demoted, and there's only one.
    - **One route table** (`api/routes.go`): every endpoint is listed with who may use it (`public`, `viewer`, `member`, `owner`) and what it does, and `guard` checks the role before the handler runs. A route without a decision is refused to everyone. `TestEveryRouteHasARoleDecision` walks every route as nobody, a viewer, a member and the owner against its own table of decisions, so a new route fails the test until someone decides. A refusal is one sentence ("Viewers can't deploy. Ask the owner to make you a member.") with `role` and `need`.
    - **Fresh on every request:** the session's user, and so its role, is read from the database each time, so a role change or a removal applies to the next request. Removing someone deletes their sessions and CLI tokens. A computer approved with `pier login` acts as whoever approved it.
    - **Values:** viewers get 403 on reveal. Build logs, runtime logs and job runs (output and failure detail) shown to a viewer have the project's values replaced with dots (`api/hide.go`, from `deploy.SecretValues`): its variables in both scopes, shared ones included, and its CRON_SECRET once made. Whatever its length: a value of 6 characters or more is hidden wherever it appears; a shorter one only as a whole word (no letter, digit or underscore right before or after it, even when the value starts or ends with punctuation), so "abc" hides in `key=abc` but not in `abcdef`. Never hidden, since they can't be secret and would blank out every log: true, false, yes, no, on, off, 0, 1, empty values, and production, preview, development, staging. The stand-in is always six dots, so it never tells a value's length. One compiled matcher per request, longest value first, so overlapping values hide whole; a short value that isn't whole at a place falls back to a shorter one there, then the search moves on one character, so a longer value starting inside it is still found.
    - **Invites** (`invites` table, `api/members.go`): the owner makes a link for a role; only its token's hash is kept, it works once and for 7 days. The link is `/invite#token`: the token is in the fragment, so it never reaches a log. The invite page asks for name, email and password and signs the person in. With email set up in Alerts, the dialog has an optional "Email the link to", sent through the alert mailer (`alerts.Notifier.SendEmail`) from the configured sender; the link is shown to copy either way, and a failed send says why.
    - **Who did it:** activity rows have an `actor` (the name at the time), and deployments a `sent_by` for uploads and redeploys. The dashboard shows "uploaded by Ada", the Activity page "by Ada", the cockpit "sent by Ada with pier deploy".
    - **Dashboard:** Members (K3) lists people with role and last activity, invites waiting, and your own sessions; the owner invites (a dialog: role, then the link to copy), changes roles and removes people from a row's menu. Controls a role can't use stay in place switched off, with the reason on hover (`Gate` in `lib/role.tsx`); row menus show only what the role can do. A refusal from pierd makes the dashboard ask for the session again, so a role change shows at once.
    - **CLI:** `pier deploy`, `promote`, `rollback` and `restart` ask pierd for the role first and give a viewer the one sentence before packing anything. The cockpit hides a viewer's change rows and keys, and a key pressed anyway says the sentence.
    - Not on the board yet: the Members page with roles, the invite dialog and the invite page.
21. Alerts (migration 17):
    - **Where:** `internal/alerts` (the `Notifier`: queue, limits, channels, email over SMTP or Resend, message text), `api/alerts.go` (the Alerts page's API), `deploy/alerts.go` (a failed build raises one; the next good build of that address resolves it), `pages/Alerts.tsx`.
    - **Channels:** email (SMTP or Resend), Slack or Discord (told apart by the webhook URL, each sent in its own format), and a webhook that gets JSON signed with HMAC-SHA256 in `X-Pier-Signature`; its secret is made by Pier and shown once. Passwords, keys, URLs and the signing secret are sealed like variables and never go back to the browser; leaving a secret field empty keeps the saved one.
    - **Not obvious:** one alert per incident (`alert_incidents`), and one more when it's fixed; an incident quiet for 24 hours counts as new next time. At most 20 alerts in 10 minutes, the rest counted into the next. Delivery is in the background with retries (10 s, 1 m, 5 m); a final refusal (4xx other than 408 and 429, SMTP 5xx, a removed channel) isn't retried. Failed previews can be turned off. Send a test alert answers with exactly what went wrong.
    - **Deliveries survive a restart** (migration 20, `alert_deliveries`, `store/deliveries.go`): once an alert passes the incident and limit checks, `send` keeps one row per channel, and per person for email, with its attempts, next try and last error, and each try is recorded. When pierd stops, a delivery waiting for its next try stays pending; `Run` starts by picking those up on the same schedule. One queued more than 24 hours before is dropped instead of sent late, and the drop goes in the row and in Activity ("Dropped old alerts"). Done rows (delivered, failed, dropped) are cleared after 3 days. pierd waits for deliveries to note where they got to before closing the database. An event raised in the last instant before a stop, not yet past the incident check, is still in memory only.
    - **Who gets email:** the owner and members, until each turns it off in the account menu (`alert_email_off`). Viewers never do and have no switch, because an alert quotes a build's or a job's last lines as they were. The Alerts page is the owner's; a member sees one sentence there pointing to their switch.
22. Health checks (migration 18):
    - **Where:** `deploy/health.go` (`WatchHealth`, run from `pierd serve`), `apphost/check.go` (one check), `store/health.go`, `components/Health.tsx`.
    - **What:** every 30 s, each server production serves: its process runs, the image's HEALTHCHECK, and the health check path, asked on the container's own port, never through the router. Three failures in a row mark it unhealthy, restart it and alert, with its last output; restarts that don't help wait longer each time (1 m doubling to 30 m). The first pass marks it healthy and resolves the alert.
    - **Not obvious:** sleeping containers, previews and static sites are never checked, so nothing is woken. Settings' Health check path wins over `PIER_HEALTH_PATH`; `health_off` turns the request off and watches only the process. The state is kept in SQLite, so a restart of pierd doesn't forget a failure. Restart from the health line is a member's action, like any restart.
23. Scheduled jobs (migration 19):
    - **Where:** `internal/jobs` (cron parsing and its plain-words description, the scheduler, run output files), `api/jobs.go`, `deploy/cron.go` (CRON_SECRET), `apphost/job.go` (a command's container), `cmd/pier/jobs.go` (`pier jobs`, `pier jobs run NAME`), `pages/Jobs.tsx` and `JobPage.tsx`.
    - **What:** a job runs a command in a fresh container from production's image, with production's variables, the app's memory limit and a timeout (15 minutes unless changed, at most 60); or requests a path on production through the router with `Authorization: Bearer` and the project's CRON_SECRET. Any 2xx counts. Schedules are five-part cron in UTC or a chosen zone.
    - **Cockpit:** `J` on home, or the project pane's "Jobs (N)" row, opens the jobs screen (`cmd/pier/cockpit_jobs*.go`): name, schedule in words, last run and next run. A job's screen has what it runs, its schedule, Run now (`n`, hidden from viewers, who get the role sentence) and its runs, a page of 25 at a time as the cursor reaches the last. A run's output opens in the log view, coloured as the job page colours it, and Run now follows the new run live, ending with its result. The row shows only when the project has jobs or a production to run them against. Adding and changing jobs stays in the dashboard.
    - **Who ran it** (migration 21, `job_runs.run_by`): Run now keeps the name of who pressed it; the job page and the cockpit say "run by Ada". Scheduled runs and the server's own token leave it empty.
    - **Not obvious:** a run that comes due while the last is still going is skipped and recorded; minutes missed while pierd is down aren't made up. CRON_SECRET is made once per project, sealed, and added to production's variables unless the project sets its own, which wins; a production built before it existed shows a banner to redeploy. It's a value: viewers never see it (see Teams). Runs and their output are kept 30 days. A failed run alerts once; the next success resolves it. Viewers read the Jobs tab and run history; members add, change, run and delete jobs.
24. Upgrading without the installers:
    - **Where:** `internal/release` (versions, the latest-release check, download and unpack, SHA256SUMS, atomic replace; shared by both sides), `internal/upgrade` (pierd's request and status, and `Apply`, the root helper), `cmd/pierd/upgrade.go` (`pierd upgrade-apply`), `api/upgrade.go`, `cmd/pier/upgrade.go` (`pier upgrade [--server]`), `components/Update.tsx` (the Server page), `install/pierd-upgrade.path` and `.service`.
    - **How the server updates:** pierd runs as pier and can't replace `/usr/local/bin/pierd` (root's) or restart itself. The owner's request (`POST /api/server/upgrade {version}`, owner only) makes pierd write just that version into `/var/lib/pier/upgrade/request`, whole (temp file, rename). `pierd-upgrade.path` (`PathExists=`) starts `pierd-upgrade.service`, a root oneshot running `pierd upgrade-apply`. It removes the request, takes only a version from it (strictly `vX.Y.Z`, never echoed back otherwise), refuses one not newer than its own (no downgrades, no source builds), downloads `pier-linux-<arch>.tar.gz` for that tag from the fixed GitHub release address over HTTPS (redirects may not drop to HTTP), unpacks only plain files from the one expected folder, checks SHA256SUMS, ARCH and VERSION, runs the new `pierd version`, then copies pier and pierd beside the old ones and renames them over, refreshes changed unit files, daemon-reloads and restarts pierd. Any failure before the renames leaves everything as it was. Progress goes to `/var/lib/pier/upgrade/status` (started with a step, failed with the reason, done with the version), which `GET /api/server/upgrade` returns.
    - **Not obvious:** pier owns the upgrade folder, so the helper refuses a folder that's a link, works only through `os.Root` there, removes a request that's a link without reading it, and writes the status to a new file renamed into place, never through a link. The download address can only change with `upgrade-apply -release-base`, which only root's unit file could set; `pierd serve -release-api` and `-upgrade-unit` exist for trying the flow and never affect where a release is downloaded from. `GET /api/version` now has `latest` (from `release.Checker`: GitHub's latest release, asked 10 s after start and every 6 hours, failures ignored) and `upgrade: true`, which tells `pier version` to suggest `pier upgrade --server`. Without the helper's unit file, the route and the Server page say "Run the installer once more to turn on updates from here", with the command; `install.sh` installs both units and enables the path unit, and `release.sh` puts them and `install.sh` in the bundle and its SHA256SUMS.
    - **CLI:** `pier upgrade` checks the latest release, downloads `pier-cli-<os>-<arch>.tar.gz`, checks it and that it runs, and replaces `os.Executable()` (symlinks resolved) with a temp file in the same folder renamed over it. A folder it can't write to is found before downloading, and it prints `sudo <path> upgrade` and the `cli.sh` line. A build from source is left alone. `pier upgrade --server` (owner only) asks, then follows the status and `/api/version` until the new pierd answers. The cockpit's header says "update available" when pier, or for the owner pierd, is behind the latest release.
    - **Tested:** unit tests against httptest release servers (versions, downgrades, bad requests, damaged or mislabelled bundles, links, the replace, an unwritable folder); live on this Mac with the real `upgrade-apply` installing Mac binaries packed as a Linux bundle, a shell loop standing in for systemd, and the dashboard and `pier upgrade --server` following it through the restart; `pier upgrade` on a copy against the real v0.7.0 release. Then on Linux (arm64 containers): `upgrade-apply` against real v0.7.1 and v0.7.2 bundles served locally passed 13 checks (junk, same-version, downgrade, tampered and missing releases refused with pierd untouched; a link as the request, as the status file and as the folder refused; the real update; units left alone; no temp files left); and under real systemd in a Debian container, `pier upgrade --server` took pierd from v0.7.1 to v0.7.2 through the path unit and restarted it, with `systemd-analyze verify` clean. Still to do: a first real update on a VPS.

25. Postgres databases (migrations 22 and 24):
    - **Where:** `internal/database` (the manager: create, named databases, resize, restart, delete, backups, restore, external URLs, the nightly and health loop; `browse.go` for tables, rows and queries over pgx; `external.go`), `internal/pgproxy` (the public TLS port), `apphost/postgres.go` (container, volume, network, loopback port, exec, sizing), `deploy/database.go` (each linked database's variable and the network for builds and jobs), `store/databases.go`, `api/database.go`, `cmd/pier/db.go`, `pages/Database.tsx`, `DatabaseData.tsx`, `DatabaseQuery.tsx`, `lib/sql.tsx`.
    - **What:** one Postgres 18 Alpine server per project, `<owner>-db-<project>`, data on a named volume mounted at `/var/lib/postgresql`, on `<owner>-net-<project>` (ICC on) with the alias `postgres`. App and job containers of that project join that network as well as the shared one (ICC off). The server holds named databases (`project_databases`): each has a role of the same name, the environments it's linked to, and the variable it's given as (`DATABASE_URL` by default; two databases can't give one environment the same variable). A new server has `production` and `preview`. Servers from v0.8 keep `app` for production; their per-branch `preview_*` databases are kept, linked to nothing, and `upgrade` in the loop makes previews a shared `preview` (or `previews`) database once, marked by the `database_preview_pending:<project>` setting. Every database is `REVOKE ALL ... FROM PUBLIC`, and `postgres` too, so one database's role can't reach another's. Passwords are sealed like variables and hidden from viewers' logs (`Secrets`).
    - **External URLs:** at most one per database (`database_externals`, unique on project and database), made on demand. It's a login role `ext_<random>` `IN ROLE <database>` with `ALTER ROLE ... SET role = <database>`, so what it makes belongs to the database. pierd listens on `-postgres` (`:5432`; on by default only with HTTPS on a real base domain) and `pgproxy` answers the SSLRequest (or direct TLS with ALPN `postgresql`), terminates TLS with Caddy's stored certificate for `db.<domain>` or the base domain's wildcard (`router.Caddy.Certificate`, reread every 10 minutes; `router.Caddy.Certify` has Caddy keep the `db.<domain>` one, since no route asks for it, until the wildcard is ready), reads the startup message, routes by the last user name (`Manager.Route`, as Postgres takes the last) to the project's loopback port, and splices; Postgres does the SCRAM itself. Plaintext is refused. Cancel requests can't be routed and `channel_binding=require` can't work through it. The VPS firewall must allow 5432.
    - **How pierd talks to it:** `psql`, `pg_dump` and `pg_restore` run inside the container as the superuser over the local socket (`Host.Exec`). Data, Query and the tunnel use a port published on 127.0.0.1 only (`PostgresAddress`), connecting as the database's own role. Browsing runs `BEGIN READ ONLY`, then the `SELECT` alone in the extended protocol, so a filter can't add a statement; Query runs in the simple protocol as one implicit transaction, 30 s per statement, and records what it did (command tags, never the SQL) in Activity. `GET .../database/tunnel` upgrades to `pier-postgres` and splices the connection to that port; `pier db tunnel` listens locally and opens one per client.
    - **Backups:** one `pg_dump --format=custom --compress=zstd:3` per database (`database_backups.database`), nightly once past 03:00 server time (a failure retries after an hour and alerts; the next success resolves it), by hand (every database), before a restore and before deleting a database or the server. Kept per database: 7 nightly, 10 by hand, 3 of each other kind; failed rows 30 days. `DeleteBackup` removes one (its file, its offsite copy, its row; not one still running). Restore makes a safety backup, loads into `<db>_restoring`, then with new connections refused swaps the names; a failure at any point leaves the database as it was. `Offsite` (an S3-style store) is an interface with no implementation yet; R2 storage could fill it.
    - **Not obvious:** the image's first start runs a socket-only server to initialise, so readiness is checked over TCP. Health: the loop starts a stopped server, recreates a missing container from the store (the volume keeps the data), and alerts after three missed checks (`database.down`/`database.recovered`). Changing memory recreates the container with new settings. Production started before the database existed shows a redeploy banner. Members and the owner use Data, Query, the URLs, backups and the tunnel; viewers see the Overview only.
    - **Tested:** `go test ./internal/database` with `PIER_TEST_DOCKER=1` runs the whole life cycle on real Postgres (isolation between networks and roles, browse, filters that try to write, queries, a second database and variable clashes, an external URL through the real proxy, backups of every database, exact restore, resize, dropping a database, the upgrade step, delete); `internal/pgproxy` tests the protocol paths and pgx through it; `store` tests migration 24 with v0.8 rows; `scripts/e2e-db.sh` deploys a `pg` app to production and previews, relinks previews, adds a second database, browses, queries, backs up, tunnels with psql and deletes the project.
26. File storage on Cloudflare R2 (migration 23):
    - **Where:** `internal/storage` (`s3.go`: a small SigV4 client for list, head, get, put, delete, batch delete and presigned links; `cloudflare.go`: buckets, account tokens, custom and r2.dev domains; `storage.go`: the manager), `deploy/storage.go`, `store/storage.go`, `api/storage.go`, `pages/Storage.tsx`, `pages/Cloudflare.tsx`, `components/Sheet.tsx` (the panel the row, file and database panels share), `components/EnvPicker.tsx`.
    - **What:** the owner connects one Cloudflare account in Integrations (account ID and an account token, sealed; its ID is the S3 key ID and SHA-256 of its value the secret). A project has named buckets (`storage_buckets`), private or public, each linked to environments with a variable prefix (`STORAGE` first; it also sets the AWS variables). Each bucket gets an account token scoped to it alone (`Workers R2 Storage Bucket Item Write`). R2 names are `<server prefix>-<project>-<name>`, plus a 4-letter suffix when that's taken (project `a`'s `b-c` and `a-b`'s `c`, or a bucket left on the account, which R2 answers with 409). A public bucket gets `files-<project>-<name>.<domain>` (the same suffix rule; the project part is cut to fit 63 characters) as an R2 custom domain when the base domain is a zone in the account, else r2.dev. Deleting a bucket empties it (the owner's token, 1,000 at a time), detaches its domain, deletes it and its key; deleting a project reads its buckets first and purges them after.
    - **Viewer:** files listed by folder, a page of 200 at a time; thumbnails and previews come from 15-minute presigned links, which the dashboard's CSP allows (`img-src`/`media-src` `https://*.r2.cloudflarestorage.com`); uploads stream through pierd (up to 5 GB). Viewers see the bucket list; members browse files and copy keys.
    - **Tested:** `go test ./internal/storage` with `PIER_TEST_DOCKER=1` runs the client against `rclone serve s3`, which checks every signature, and the manager's whole flow against a fake Cloudflare API (`fakeCloudflare`); `scripts/e2e-storage.sh` with `scripts/e2e-cloudflare.py` and rclone connects, adds and relinks buckets, deploys production and previews, uploads, reads a presigned link and deletes. The test pierd takes `PIER_TEST_CLOUDFLARE_API` and `PIER_TEST_R2_ENDPOINT`. `TestRealCloudflare` runs the whole flow against a real account (`PIER_TEST_CLOUDFLARE_ACCOUNT`, `PIER_TEST_CLOUDFLARE_TOKEN_FILE`, `PIER_TEST_CLOUDFLARE_DOMAIN`, or `none` for r2.dev) and sweeps up after itself; it passed on Jeremiah's account with `thesunset.live` and with r2.dev. Real Cloudflare facts it confirmed: account tokens start `cfat_`, the S3 key ID is the token's ID and the secret the SHA-256 of its value; a new custom domain's CNAME appears a moment after the call returns (a lookup before then can cache the base domain's wildcard for 5 minutes), so the Storage tab shows "Setting up its address" until `DomainReady`.
27. Integrations is a list (`/integrations`); each integration has its own page (`/integrations/github`, `/integrations/cloudflare`). The GitHub App's setup URL is the GitHub page; the list forwards an install return from Apps made before.
28. The CLI for agents: `pier storage` (list, add, link, env, ls, put, get, rm), `pier env` (a project's own variables: list, set, rm), `pier db link` and `pier db rm`, `pier deploy --project NAME --create`, and `pier docs`, which prints `cmd/pier/guide.md` (embedded). Deletes ask for the name in a terminal and need `--yes` outside one.
29. Analytics (replaces the Logs tab; `/logs` links still open it), with Overview, Logs, Traces and Events. Phase 3, the telemetry stack, is built (below), and Traces from phase 4. Metrics, the service map, dashboards and the panel editor are designed on the board, row Q, and not built yet.
    - **Request log fields:** `internal/requests` stores host, env (the deployment's target), visitor (a sha256 of salt, client IP and user agent; the salt is per UTC day, in `salts`, deleted after two days), page (GET 2xx with Accept text/html), bot (user agent, or a scanner path such as `/.env`), browser, device, referrer host (none for the same site) and country (Cloudflare's CF-IPCountry only). Columns are added to older databases on open.
    - **Overview:** `GET /api/projects/{p}/analytics?from&to&env` (`requests.Overview`): headline numbers and the span before, about 60 trend points (p95 per point only for a day or less), breakdowns (top 5 with the rest), the slowest routes (p95 among the 40 busiest with five or more requests). Bots are left out of everything but their own list.
    - **Logs:** `f=field:value` and `nf=field:value` filter `/requests` and `/requests/chart` (`requests.Fields` lists the fields, including a derived `level`); `/requests/values?field=` counts a field's values under the other filters. The chart returns `e4xx` and `e5xx`.
    - **Events:** table `events` in the requests database, kept 90 days. Servers POST to `/api/events` (public) with `PIER_EVENTS_TOKEN`, `pier_ev_<project>.<env>.<hmac>` signed with the `events_secret` setting; every deploy gets it and `PIER_EVENTS_URL` (the dashboard's address, so none without `-address`). Browsers POST to `/_pier/event` on the app's own address: `router.Caddy.Events` adds a subroute to every deployment's host that proxies that path to pierd's loopback events listener with `X-Pier-Event` set to the deployment. 6,000 events a minute per project. The funnel's steps are the `events_funnel:<project>` setting; `$visit` is people who opened a page.
    - **Telemetry stack** (`internal/telemetry`, `apphost/telemetry.go`): the owner turns it on in Server (`PUT /api/server/telemetry`, setting `telemetry_on`). pierd runs `grafana/tempo:2.8.2`, `grafana/loki:3.5.5` and `prom/prometheus:v3.5.0` as `<owner>-telemetry-<name>`, each with a volume of the same name, settings files in `<data>/telemetry` (a hash of image and file in the `io.pier.config` label replaces the container when either changes) and loopback ports only. Tempo and Loki are multi-tenant, one tenant per project (`X-Scope-OrgID`); Prometheus takes OTLP and labels series `pier_project`. Retention 14 days. Off stops the containers and keeps the volumes.
    - **Receiving:** `POST /otlp/v1/{traces,metrics,logs}` (public) takes OTLP protobuf or JSON (gzip too) with the events token; `telemetry.Forward` stamps `pier.project` and `deployment.environment` on every resource (an app can't claim another project), names the service after the project if the app didn't, and posts protobuf to the right server. Deploys get the `OTEL_*` variables only while it's on, and only with `-address`.
    - **Request traces:** with it on, `router.Caddy.Traces` adds Caddy's `tracing` handler before every server app's handler (not static sites). pierd sets `OTEL_TRACES_EXPORTER=none` for the embedded Caddy, so Caddy only makes the IDs, passes `traceparent` on and logs `traceID`/`spanID`; `requests.Log.Traced` hands those lines to `telemetry.SendRequests`, which writes the root span (service `pier`) to Tempo. The request log keeps the trace ID (column `trace`). Turning it on or off re-syncs Caddy.
    - **Traces:** `GET /api/projects/{p}/traces?q=&from=&to=&env=` runs TraceQL on Tempo as the project (an empty query is `{}`; `env` adds a resource filter) and joins each trace to the request that started it (`requests.Log.ByTrace`) for its status. Tempo drops leading zeros from IDs (`padID` puts them back) and names a root still on its way `<root span not yet received>` (`partial`). `GET /traces/{id}` reads the trace as protobuf (Tempo's Trace has the export request's shape), the request, and up to 500 log lines with that `trace_id` from Loki. The dashboard plots traces by start and duration (log scale, drag to select), and a trace opens as a waterfall that starts on the deepest failed span, or the one with the most time of its own; Logs links a traced request to its trace (`?view=traces&trace=<id>`). Health checks send an unsampled `traceparent` so SDKs skip them.
    - **App output:** `telemetry.FollowLogs` follows every running app container (from `ReadyDeployments` with a running `Runtime`) and posts its lines to Loki over OTLP, service the project, with `log.iostream`, a trace ID and level from JSON lines. Containers already running when it starts send from then on; later ones from their first line; a follower that ends picks up after its last line.

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
  - `api`: every route and who may use it in `routes.go`; session, projects, create (projects, settings, upload), cli (login approval), sessions, members (people and invites), hide (values in a viewer's logs).
  - `auth`: passwords and the lock.
  - `store`: SQLite, with migrations in `store.go`.
  - `deploy`: the build pipeline and its sources, plus logs.
  - `build`: detection, overrides and runners.
  - `monorepo`: a repo's folders and what each looks like, workspaces and what a package uses, and whether a push affects a root directory.
  - `alerts`: the notifier, its channels and its email; `jobs`: cron schedules, the job scheduler and each run's output.
  - `database`: each project's Postgres server, its previews' databases, backups, restores, and browsing and queries.
  - `bundle`, `dnscheck`, `router` (embedded Caddy and external admin compatibility), `web` (the embedded dashboard).
- **`pier/web/src`:**
  - `App.tsx` (routes), `pages/`, `flap/`, `lib/` (`api`, `data`, `router`, `format`, and `actions` for what moves production).
  - `components/`: Shell (the sidebar), CommandMenu (⌘K), ui (headings, tables' paging and search, loading and error states), Dialog, Toast, Tooltip, Menu, BuildFields, FolderPicker (the root directory field), the charts, Icons, ProjectMark.
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
