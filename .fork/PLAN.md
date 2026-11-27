# salthepal/homebox — fork plan (overview)

> **Agents: read this file, then `.fork/TASKS.md`. Do the next unchecked task only.**
> Repo/GitHub settings that a human must click through are in `.fork/REPO-SETUP.md`.

## 0. What this fork is

- `sysadminsmedia/homebox` (**upstream**) → `salthepal/homebox:main` (**mirror**) → `salthepal/homebox:custom` (**our features**).
- Personal features only: **Projects** (multi-step upgrades) and **Google Calendar sync** for maintenance.
- Deployed on Unraid from `ghcr.io/salthepal/homebox:custom`, with Postgres. Reachable publicly through a Cloudflare tunnel.

## 1. Branch model and flow

```
upstream/main ──sync (x-sync-main.yml, daily)──▶ main  (pure mirror, never commit here)
                                                  │
                                    rebase (.fork/rebase.sh or x-rebase.yml)
                                                  ▼
x/<feature> ──draft PR, squash-merge──▶ custom  (default branch, deployed)
                                                  │
                                          x-docker.yml ──▶ ghcr.io/salthepal/homebox:custom
```

| Branch | Purpose | Who writes |
|--------|---------|-----------|
| `main` | Exact mirror of upstream `main`. Fast-forward only. | Only the sync workflow (or the GitHub "Sync fork" button). |
| `custom` | `main` + our commits on top. **Default branch.** Deploy source. | Squash merges from `x/*` PRs, plus force-push after a rebase. |
| `x/<feature>` | One task from TASKS.md = one branch = one PR into `custom`. | Agents/you. Deleted on merge. |

- **Rebasing custom onto main:** run periodically (after a sync brings new upstream commits, at least monthly). A weekly scheduled run of `x-rebase` does a trial run (no push) and opens a `rebase-break` issue if it breaks. Use `git rebase main`, never `git merge main`. That keeps our delta as a clean stack of feature commits on top of upstream.
- After a rebase, `custom` is force-pushed (`--force-with-lease`). Any open `x/*` branch must then be re-based: `git rebase --onto origin/custom <old-custom-sha> x/<feature>`.
- Squash-merge every PR with a conventional prefix (`x(projects): …`, `x(gcal): …`, `x(fork): …`). One commit per feature step makes rebase conflicts small and attributable.

## 2. The rebase contract (rules every change must follow)

The rule behind all of these: **add new files, don't edit upstream files.** Edits to upstream files conflict on rebase. New files almost never do.

| # | Rule | Why (what in this repo forces it) |
|---|------|-----------------------------------|
| R1 | All custom code lives in **custom-owned paths** (§3). Upstream files are edited only at the **seams** in `.fork/SEAMS.txt`, one line each. | Keeps rebase conflicts to a known handful of lines. `x-seam-guard` enforces it. |
| R2 | **Never add or change ent schemas in `backend/internal/data/ent/schema`**, and never regenerate upstream ent. Custom tables use their **own ent client** in `backend/internal/custom/ent`. | Regenerating upstream ent rewrites ~128 generated files, and they'd conflict on every rebase. |
| R3 | **Custom migrations use their own goose version table `x_goose_db_version`** and their own embedded FS. Never add files to `backend/internal/data/migrations/*`. SQL migrations only (no Go migrations: goose's Go registry is global). | goose refuses to start on out-of-order ("missing") versions. An upstream migration dated before ours would brick startup. |
| R4 | **No `ALTER` of upstream tables, and no DB foreign keys pointing at upstream tables.** Store upstream IDs (`group_id`, `entity_id`, `maintenance_entry_id`) as plain UUID columns. Clean up orphans in app code. FKs *between our own tables* are fine. | Upstream drops and recreates tables (`recreate_item_templates`, `merge_entities`, the items→entities rename). An FK to them can block upstream migrations or, with cascades, silently wipe our rows. |
| R5 | Tables are prefixed `x_`. API routes go under `/api/v1/x/...`. Env vars are prefixed `HBOX_X_`. Frontend routes go under `/x/...`. | No collisions with future upstream names. |
| R6 | **No new Go or npm dependencies** unless already in `backend/go.mod` / `frontend/package.json`. `golang.org/x/oauth2` and `ardanlabs/conf` are already there. Write the Calendar REST calls and ICS output by hand. | Upstream bumps deps constantly, so `go.sum` and `pnpm-lock.yaml` would conflict on every rebase. |
| R7 | Custom handlers are **not** in upstream swagger (they live in `backend/internal/custom/api`, outside the swag scan dirs). Don't run `task swag` / `task typescript-types` on custom changes. Custom TS types are hand-written in the layer. | `backend/app/api/static/docs/*` and `frontend/lib/api/types/*` are committed generated files. |
| R8 | UI strings live in `frontend/layers/custom/locales/en.json`, merged at runtime. **Never edit `frontend/locales/*.json`.** | Weblate rewrites those files constantly. |
| R9 | **Never edit upstream workflows.** Custom CI = `.github/workflows/x-*.yml`. Unwanted upstream workflows are disabled in the Actions UI. | Same conflict reason. |
| R10 | Calls into upstream Go code go through **adapters in `backend/internal/custom/upstream/`**, one file per upstream package. | When upstream changes a signature, the break is confined to one file, and CI catches it at compile time. |
| R11 | Custom code must work on **Postgres (primary) and SQLite**. Every custom migration exists in both `postgres/` and `sqlite3/`. Tests run on both. | Production is Postgres. Upstream supports both, and its tests default to SQLite. |

## 3. Layout (custom-owned paths)

```
.claude/CLAUDE.md                      # agent entry point (auto-loaded by Claude Code)
.fork/                                 # PLAN.md, TASKS.md, REPO-SETUP.md, SEAMS.txt, rebase.sh, seam-guard.sh
.github/workflows/x-*.yml              # custom CI
backend/app/api/x_register.go          # package main: registerCustom(...) — the only backend entry point
backend/app/api/x_*_test.go            # tests that need package main
backend/internal/custom/
  config/                              # HBOX_X_* env parsing (ardanlabs/conf)
  migrations/{postgres,sqlite3}/*.sql  # own goose FS, table x_goose_db_version
  migrations/migrations.go             # embed + Run(ctx, db, driver)
  ent/schema/*.go  ent/generate.go     # own ent client (generated code committed under ent/)
  upstream/                            # adapters onto upstream repos/services (R10)
  projects/                            # Feature 1: repo + service
  gcal/                                # Feature 2: oauth, REST client, reconciler, (ics)
  api/                                 # chi handlers for /api/v1/x
frontend/layers/custom/                # Nuxt layer (Nuxt 4.4 auto-registers ~~/layers/*, no nuxt.config edit)
  nuxt.config.ts  pages/x/**  pages/item/[id]/index/projects.vue
  components/  composables/  lib/api/  locales/en.json  plugins/x-i18n.ts  nav.ts  item-tabs.ts
```

## 4. Seams (the only upstream edits). Source of truth: `.fork/SEAMS.txt`

| File | Exact change |
|------|--------------|
| `backend/app/api/main.go` | One line after `app.mountRoutes(router, chain, app.repos)` and `runner := graceful.NewRunner()`: `registerCustom(app, cfg, router, chain, runner)`. It runs custom migrations, opens the custom ent client on the same `*sql.DB`, mounts `/api/v1/x`, registers ent hooks, and adds background tasks. |
| `frontend/layouts/default.vue` | Spread `...customNavItems(t, route)` into the nav array (around the `/maintenance` entry), plus its import. |
| `frontend/pages/item/[id]/index.vue` | Spread `...customItemTabs(itemId)` into the tabs array (next to the `global.maintenance` tab), plus its import. |

Imports count as part of the seam. If upstream rewrites one of these areas, re-apply the single spread line. `git rerere` remembers the resolution.

## 5. Facts about upstream that the implementation relies on (verified 2026-11-27)

- **Migrations:** `backend/app/api/setup.go` `runMigrations` calls global `goose.Up` with the default table `goose_db_version`. Our runner must use a goose **Provider** with a custom store (`database.NewStore(<dialect>, "x_goose_db_version")`, `goose.WithStore`). Don't call the global `goose.SetTableName`, because it would affect upstream's runs.
- **Routes:** `backend/app/api/routes.go` uses `r.Route(prefix+"/v1", …)`, with `prefix="/api"`. The auth middleware stack is `a.mwAuthToken, a.mwTenant, a.mwRoles(RoleModeOr, authroles.RoleUser.String())` (+ `a.mwGroupOwner` for owner-only). These are methods on `*app` in package main, which is why `x_register.go` lives in package main. Mount ours with `router.Route("/api/v1/x", …)`, and add a test that proves both `/api/v1/status` and `/api/v1/x/ping` resolve (chi sub-router precedence).
- **Auth context:** `services.UseUserCtx(ctx)` gives the user, and the group/tenant ID comes from the `services.Context` (`GID`). Scope every query by group ID.
- **Background tasks:** `backend/app/api/recurring.go` uses `runner.AddPlugin(NewTask(name, interval, fn))`.
- **Maintenance:** `MaintenanceEntry` has `entity_id`, `date` (completed), `scheduled_date` (planned), `name`, `description`, `cost`. The repo publishes **no event-bus events** for it. Change detection = runtime ent hook (`<upstream ent client>.MaintenanceEntry.Use(...)`, no codegen needed) + periodic reconciliation.
- **Frontend:** Nuxt 4.4.7, `ssr: false`, `components.dirs: []` (no component auto-import, so import explicitly). i18n is a plain `vue-i18n` plugin (`frontend/plugins/i18n.ts`), not `@nuxtjs/i18n`. The layer merges its messages with `i18n.global.mergeLocaleMessage`.
- **Docker:** the stock `Dockerfile` copies all of `frontend/` and builds the whole backend module, so layers and custom packages are included automatically.

## 6. Feature 1: Projects (multi-step upgrades)

A project ("Upgrade NAS to 10GbE") is an ordered checklist of steps, linked to one or more items, with a status lifecycle and cost tracking.

**Tables**
- `x_projects`: id uuid pk, group_id uuid, name varchar(255), description text, status (`idea|planning|in_progress|on_hold|done|cancelled`), priority int (0–3), target_date date null, budget numeric null, started_at / completed_at timestamptz null, created_at / updated_at.
- `x_project_steps`: id, project_id → `x_projects` ON DELETE CASCADE, position int, title varchar(255), notes text, status (`todo|doing|blocked|done|skipped`), due_date date null, est_cost / actual_cost numeric null, url text null, entity_id uuid null, log_as_maintenance bool default false, maintenance_entry_id uuid null, completed_at null, timestamps.
- `x_project_entities`: (project_id → `x_projects` CASCADE, entity_id uuid) PK both; index on entity_id.
- Index `x_projects(group_id, status)`.

**API** (`/api/v1/x`, userMW)
- `GET/POST /projects`, `GET/PUT/DELETE /projects/{id}`
- `POST /projects/{id}/steps`, `PUT/DELETE /projects/{id}/steps/{stepId}`, `PATCH /projects/{id}/steps/order` (body: ordered step IDs)
- `POST /projects/{id}/steps/{stepId}/complete` (body: `{actual_cost?, log_as_maintenance?}`)
- `PUT /projects/{id}/entities` (replace links), `GET /entities/{entityId}/projects`

**Behavior**
- Project status auto-suggest: the first step moving to `doing` sets `in_progress`/`started_at` if the project is in idea/planning. All steps done or skipped → prompt (UI) to mark it `done`. Never auto-close silently.
- Completing a step with `log_as_maintenance` + `entity_id` creates an upstream maintenance entry via `upstream/maintenance.go` (name = step title, description = notes + "From project: …", cost = actual_cost, date = now) and stores `maintenance_entry_id`.
- Validate that every `entity_id` belongs to the caller's group (via adapter) before linking.
- Orphan sweeper (daily task): delete `x_project_entities` rows, and null `entity_id`/`maintenance_entry_id`, where the upstream row is gone.

**UI** (layer)
- Nav "Projects" → `/x/projects`: grouped by status (columns on desktop, sections on mobile). Card = name, progress `done/total`, cost actual-vs-budget, target date, linked item chips.
- `/x/projects/[id]`: header (status select, dates, budget), linked items picker, step checklist with inline edit, up/down reorder (no dnd lib, R6), complete dialog with "log as maintenance" toggle.
- Item page tab "Projects" → `/item/[id]/projects`: linked projects + "New project for this item".

## 7. Feature 2: Google Calendar sync for maintenance

**Decisions:** one **existing** shared household calendar, chosen from a picker. Completed maintenance **stays on the calendar with ✅**. Homebox is publicly reachable, so OAuth redirects work.

**Config (env, `HBOX_X_` prefix)**
- `HBOX_X_PUBLIC_BASE_URL`, e.g. `https://homebox.example.org`. Used for the OAuth redirect and event deep links.
- `HBOX_X_GOOGLE_CLIENT_ID`, `HBOX_X_GOOGLE_CLIENT_SECRET`
- `HBOX_X_SECRET_KEY`: 32-byte base64 key, AES-GCM for refresh tokens at rest. If unset, the feature is disabled and the UI says why.
- `HBOX_X_GCAL_SYNC_INTERVAL`, default `15m`.

**OAuth**
- Web-app client. Scopes `https://www.googleapis.com/auth/calendar.events` + `https://www.googleapis.com/auth/calendar.calendarlist.readonly`. `access_type=offline`, `prompt=consent`. State param = signed, short-lived, bound to user + group.
- Redirect URI: `{PUBLIC_BASE_URL}/api/v1/x/gcal/callback`. This endpoint can't use the Bearer middleware (it's a browser redirect), so it authenticates via the signed state.
- Consent screen must be **"In production"**. In Testing mode, refresh tokens die after 7 days.

**Tables**
- `x_gcal_connections`: group_id pk, connected_by uuid, google_email, calendar_id, calendar_summary, enc_refresh_token bytea/blob, include_completed bool default true, include_project_steps bool default true, reminder_minutes int default 1440, last_sync_at, last_error text, created_at/updated_at.
- `x_gcal_links`: id, group_id, source_type (`maintenance|project_step`), source_id uuid, event_id text, content_hash text, synced_at. Unique (group_id, source_type, source_id).

**Sync engine: reconciliation**
1. Desired set per group = maintenance entries in the group with `scheduled_date` or `date` set, plus (if enabled) project steps with `due_date`.
2. Each source maps to an event and a `content_hash`. Diff against `x_gcal_links`: no link → insert; hash changed → patch; link without a source → delete the event (only ours).
3. Triggers: (a) recurring task every `GCAL_SYNC_INTERVAL`; (b) a runtime ent hook on upstream `MaintenanceEntry` create/update/delete, plus our project-step writes, which enqueue a debounced (~10 s) reconcile for that group; (c) a "Sync now" button. Cascade deletes from item deletion bypass hooks, and (a) covers them.
4. Rebuild: if links are lost, list events with `privateExtendedProperty=homebox_source=homebox` and re-link by `homebox_id`.

**Shared-calendar safety (hard requirement):** Homebox only ever patches or deletes events whose ID is in `x_gcal_links` **and** which carry `extendedProperties.private.homebox_source=homebox`. Disconnect offers "remove Homebox events" (deletes only those). It never deletes or clears the calendar.

**Event mapping**
- Scheduled, not completed: all-day on `scheduled_date`. Title `🔧 {item name}: {task}`. Description = task description, est. cost, deep link `{PUBLIC_BASE_URL}/item/{entity_id}/maintenance`. Popup reminder `reminder_minutes`.
- Completed: all-day on `date`. Title `✅ {item name}: {task}`. Description adds actual cost. No reminders. (Entries logged as completed without a schedule also get a ✅ event, if `include_completed`.)
- Project step with due date: `📋 {project}: {step}` (✅ when done), linking to `/x/projects/{id}`.
- Errors: 401 / `invalid_grant` → mark the connection broken and show "Reconnect". 429 / 5xx → exponential backoff. 404/410 on patch → re-insert. Store `last_error`.

**UI:** `/x/calendar` settings page (link from the Projects page header and from nav). Includes Connect Google, connected account, calendar picker (calendarList filtered to `accessRole` writer/owner), options, last sync and last error, Sync now, and Disconnect (with optional "remove Homebox events").

**Later / optional:** read-only ICS feed (`/api/v1/x/calendar/{token}.ics`); two-way sync via `events.watch`; recurring maintenance.

## 8. Deployment (Unraid)

- Image: `ghcr.io/salthepal/homebox:custom` (pin to `:custom-<sha>` when you want to hold a version).
- Add the env vars from §7 to the Unraid template, alongside the existing Homebox/Postgres vars.
- Cloudflare: the OAuth callback is a normal browser request from the logged-in user, so no Access bypass is needed. (Only the optional ICS feed would need one, for `/api/v1/x/calendar/*`.)
- **Back up Postgres before the first deploy of each task that adds migrations.** Custom migrations are forward-only.
