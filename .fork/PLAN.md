# `custom` branch plan — salthepal/homebox

Goal: add personal features (multi-step upgrade **Projects**, **Google Calendar** sync for
maintenance) on `custom`, while `main` keeps tracking `sysadminsmedia/homebox` and
`git rebase main` stays painless.

---

## 1. Ground rules (the rebase contract)

The rule behind all of these: **add new files, don't edit upstream files.** Every edit to
an upstream file can conflict on a rebase. A new file can only conflict if upstream adds a file
at the same path.

| # | Rule | Why (what in this repo forces it) |
|---|------|-----------------------------------|
| R1 | All custom code lives in **custom-owned paths** (see §2). Upstream files are edited only at listed **seams** (§3), each a single line. | Keeps rebase conflicts to a known handful of lines. |
| R2 | **Never add ent schemas to `backend/internal/data/ent/schema`.** Custom tables get their **own ent client** in `backend/internal/custom/ent`. | Running `go generate` in the upstream ent dir rewrites ~128 generated files, and every one of them would conflict on every rebase. |
| R3 | **Custom migrations use a separate goose version table** (`x_goose_db_version`) and their own embedded FS. Never put files in `backend/internal/data/migrations/*`. | goose fails `Up` when it finds "missing" (out-of-order) versions. An upstream migration dated before ours would brick startup. |
| R4 | **No `ALTER` on upstream tables, and no DB-level foreign keys pointing at upstream tables.** Store upstream IDs (`entity_id`, `group_id`, `maintenance_entry_id`) as plain UUID columns. Clean up orphans in app code. | Upstream rebuilds tables (e.g. `recreate_item_templates`, `merge_entities`, the items→entities rename). In SQLite, dropping a parent table runs an implicit DELETE, so an `ON DELETE CASCADE` would quietly wipe our rows. |
| R5 | Prefix custom tables with `x_` and custom API routes with `/api/v1/x/...`. | No name collisions with future upstream tables or routes. |
| R6 | **No new Go or npm dependencies** unless they are already in `go.mod` / `package.json`. | Upstream bumps deps constantly, so `go.sum` and `pnpm-lock.yaml` would conflict on every rebase. `golang.org/x/oauth2` is already a direct dep. The Calendar REST calls and ICS output are small enough to write by hand. |
| R7 | Leave custom handlers **out of upstream swagger** (they live outside the swag scan dirs). Custom TS types sit in the frontend layer. | `swagger.json`/`docs.go` and `frontend/lib/api/types` are committed generated files. Regenerating them on `custom` causes conflicts. |
| R8 | Custom UI strings are merged at runtime from the layer's own locale files. **Don't touch `frontend/locales/*.json`.** | Weblate rewrites those files constantly. |
| R9 | Don't edit upstream workflows. Turn off the ones you don't want in the Actions UI. Custom CI goes in `.github/workflows/x-*.yml`. | Same conflict reason. |
| R10 | When a custom feature calls upstream Go code (e.g. the maintenance repo), the call goes through **one adapter file** per upstream package (`backend/internal/custom/upstream/*.go`). | When upstream changes a signature, the compile break shows up in one file, and CI catches it. |

## 2. Layout

```
.fork/                         # fork docs + tooling (never upstream)
  PLAN.md  SEAMS.txt  rebase.sh  Taskfile.yml
.github/workflows/x-*.yml      # custom CI
backend/app/api/x_register.go  # package main: registerCustom(...) — the one backend entry point
backend/internal/custom/
  config/        # own env parsing, prefix HBOX_X_ (ardanlabs/conf, already a dep)
  migrations/{sqlite3,postgres}/   # own goose FS + x_goose_db_version
  ent/schema/ + ent/ (generated)   # own ent client, same *sql.DB
  upstream/      # adapters onto upstream repos/services (R10)
  projects/      # feature 1: repo + service
  gcal/          # feature 2: oauth, REST client, ICS, reconciler
  api/           # chi handlers mounted under /api/v1/x (own swag instance, optional)
frontend/layers/custom/        # Nuxt layer: Nuxt 4.4 auto-registers ~~/layers/*, so nuxt.config stays untouched
  pages/x/...  components/  composables/  lib/api/  locales/en.json  plugins/x-i18n.ts
```

## 3. Seams (the only upstream edits) — tracked in `.fork/SEAMS.txt`

| File | Change |
|------|--------|
| `backend/app/api/main.go` (~L242–297) | Add `registerCustom(app, cfg, router, chain, runner)` after `mountRoutes`, before `registerRecurringTasks`. It runs custom migrations, builds the custom ent client, mounts `/api/v1/x`, and registers background tasks. |
| `frontend/layouts/default.vue` (nav array ~L411) | Add one entry: `...customNavItems()`, imported from the layer. |
| `frontend/pages/item/[id]/index.vue` (tabs ~L478) | Add one entry: `...customItemTabs(itemId)`, for the "Projects" tab. The tab page is a new file at `layers/custom/pages/item/[id]/index/projects.vue`. |

Three one-line seams in total. If upstream rewrites one of these areas, the conflict is a single
line that's obvious to re-apply.

## 4. Branch and rebase workflow

- `main` = clean mirror of upstream (GitHub "Sync fork", or a scheduled sync). **Never commit to main.**
- `custom` = `main` + custom commits. Update it with `git rebase main` (history stays linear and the custom delta stays readable). Push with `--force-with-lease`. Only you use this branch, so force-pushing it is fine.
- Keep commits feature-scoped (`x(projects): …`, `x(gcal): …`, `x(fork): …`) so a broken one is easy to drop or fix during a rebase.
- Enable `git rerere` so a seam conflict you've resolved once gets re-applied automatically.
- `.fork/rebase.sh`: fetch → rebase → `go build ./... && go test ./internal/custom/...` → `pnpm typecheck` → print a seam diff summary.
- Make **`custom` the fork's default branch**. Scheduled workflows only run from the default branch, and the default branch is what you deploy. `main` stays a pure mirror.

## 5. CI (all new files)

| Workflow | Trigger | Does |
|----------|---------|------|
| `x-ci.yml` | push/PR → `custom` | Reuses upstream `partial-backend.yaml` / `partial-frontend.yaml` via `uses:` (no copy-paste), plus `go test ./internal/custom/...` against **both SQLite and Postgres**. Custom migrations need checking on both. |
| `x-seam-guard.yml` | push/PR → `custom` | `git diff --name-only $(git merge-base origin/main HEAD)..HEAD`. Fails if a changed path is neither custom-owned (§2) nor listed in `SEAMS.txt`. Also fails if `go.mod`, `go.sum`, `pnpm-lock.yaml`, `frontend/locales/*` or `backend/internal/data/ent/**` changed. This turns rules R1/R2/R6/R8 into an automated check. |
| `x-rebase-canary.yml` | weekly cron + manual | Fetches upstream `main` and runs a trial rebase plus the build and tests. If anything fails, it opens or updates an issue labelled `rebase-break` with the conflicting files. You learn about breakage before you're mid-upgrade. |
| `x-docker.yml` | push → `custom`, tags `x-v*` | Builds the stock `Dockerfile` (it already picks up `frontend/` and the whole backend module) and pushes to `ghcr.io/salthepal/homebox:custom` (+ `:custom-<sha>`). |
| *Disable in Actions UI* | — | `docker-publish*`, `binaries-publish`, `clear-stale-docker-images`, `issue-gatekeeper`, `update-currencies`, `copilot-setup-steps`. They need upstream secrets or don't apply to a fork. |

Also: add a branch ruleset on `custom` that requires `x-ci` and `x-seam-guard`, with admin bypass.
Add `CLAUDE.md`/`AGENTS`-style rules in `.fork/AGENTS.md` that point agents at this contract,
and a root-level `.github/instructions/x-fork.instructions.md` (a new file) so Copilot and Claude both pick it up.

---

## 6. Feature 1 — Multi-step upgrade Projects

**What it is:** a project ("Upgrade NAS to 10GbE", "Kitchen faucet swap") with an ordered
checklist of steps. It moves through *idea → planning → in progress → on hold → done / cancelled*,
links to one or more items, and tracks estimated vs actual cost.

### Data model (own ent client, R2–R5)
- `x_projects`: id, group_id, name, description (md), status, priority, target_date, budget, started_at, completed_at, timestamps
- `x_project_steps`: id, project_id (**FK OK — our own table**), position, title, notes, status (todo/doing/blocked/done/skipped), due_date, est_cost, actual_cost, url (part link), entity_id (nullable), log_as_maintenance (bool), maintenance_entry_id (nullable), completed_at
- `x_project_entities`: project_id, entity_id (no FK to `entities`, R4)

### Backend
- Repo and service scoped by `group_id` from the existing auth context. Authorization reuses upstream `mwRoles(RoleUser)` and the group-role checks, via the adapter.
- Endpoints under `/api/v1/x/projects`: CRUD, `PATCH steps/reorder`, `POST steps/{id}/complete`, `GET /api/v1/x/entities/{id}/projects`.
- When a step with `log_as_maintenance` and an `entity_id` is completed, the service creates an upstream maintenance entry through `upstream/maintenance.go` (name = step title, cost = actual_cost, date = now) and stores `maintenance_entry_id`. Upgrade history then shows up on the item's normal maintenance log for free.
- Steps with a `due_date` feed into Feature 2's calendar as well.
- Orphan sweeper (daily recurring task): drops `x_project_entities` rows and nulls `entity_id`/`maintenance_entry_id` where the upstream rows no longer exist.

### Frontend (layer)
- `/x/projects`: board grouped by status, or a list with filters. Cards show a progress bar (done/total steps) and cost vs budget.
- `/x/projects/[id]`: header, linked items, step checklist (reorder with up/down buttons; no dnd dependency per R6), inline step editing, and a "complete → log maintenance?" toggle.
- Item page "Projects" tab (seam 3): projects linked to this item, plus "New project for this item".

### Milestones
1. **P0 fork scaffolding** (§1–5): x_register seam, custom migration runner, custom ent client, layer skeleton, i18n merge plugin, seam-guard, CI. *Merge before any feature work.*
2. **P1** schema + migrations (sqlite + postgres) + repo tests on both DBs.
3. **P2** service + API + orphan sweeper.
4. **P3** UI: list/detail pages, nav seam.
5. **P4** item tab seam + maintenance logging on completion.

---

## 7. Feature 2 — Maintenance ↔ Google Calendar

Upstream `MaintenanceEntry` has `scheduled_date` (planned) and `date` (completed). The repo publishes
**no event-bus events** for maintenance changes, so detecting changes has to happen on our side.

### Two-stage approach
**Stage A — ICS feed (small, low risk):**
`GET /api/v1/x/calendar/{token}.ics` returns all-day VEVENTs for scheduled, not-completed
maintenance plus project steps with a due date. Each event has a stable `UID` (`<entry-id>@homebox`)
and a link back to `/item/{id}/maintenance`. The token is per group, random, revocable, and stored hashed.
You subscribe in Google Calendar via "From URL".
- Upside: no OAuth, works with any calendar app.
- Downside: Google refreshes subscribed feeds only every ~8–24 h, the feed is one-way, and the URL must be reachable from Google. Behind a Cloudflare tunnel with Access, that means a bypass policy for only `/api/v1/x/calendar/*`.

**Stage B — Push sync via Calendar API (the real feature):**
- **OAuth:** you use your own Google Cloud OAuth client (Web app, scope `https://www.googleapis.com/auth/calendar.events` + `calendar.app.created`, so we create and own a dedicated "Homebox Maintenance" calendar). Uses `golang.org/x/oauth2`, already a dep, plus a hand-written REST client (no `google.golang.org/api` direct dep, R6).
  - ⚠️ Set the consent screen to **"In production"** (unverified is fine for personal use). In "Testing" mode, refresh tokens expire after 7 days.
- **Config (own env, `HBOX_X_` prefix):** `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `PUBLIC_BASE_URL` (for redirect and event links), `SECRET_KEY` (AES-GCM for refresh tokens at rest).
- **Tables:** `x_gcal_connections` (group_id, connected_by_user, calendar_id, enc_refresh_token, options: include_completed / reminder_minutes / include_project_steps, last_sync_at, last_error) and `x_gcal_links` (source_type `maintenance|project_step`, source_id, group_id, event_id, content_hash, synced_at).
- **Sync = reconciliation, not event-driven:**
  1. A recurring task (every 15 min, `NewTask` pattern from `recurring.go`) runs per connected group. It loads the desired set (scheduled maintenance + optional completed + project steps), hashes each one, and diffs against `x_gcal_links`: insert / patch / delete. Each event carries `extendedProperties.private.homebox_id`, so a lost link table can be rebuilt by listing the calendar.
  2. For a near-instant update, a **runtime ent hook** (`client.MaintenanceEntry.Use(...)`, registered in `registerCustom`, no codegen) triggers a debounced reconcile for that group after any create, update or delete. Cascade deletes from item deletion skip hooks, and step 1 covers those.
- **Event shape:** all-day on `scheduled_date`, title `🔧 {item}: {task}`, description = notes + cost + deep link, popup reminder from the options. When completed: delete the event, or retitle it `✅ …` on the completion `date` if include_completed is on.
- **Failure handling:** treat 401/invalid_grant as "disconnected" and show it in the UI. Back off on 429/5xx. `last_error` is shown on the settings page.
- **UI (layer):** `/collection/x-calendar` settings page, linked from the existing collection settings via a small layer-provided link (or the nav seam). It has Connect Google, calendar status, options, "Sync now", the ICS URL with regenerate, and Disconnect (optionally deleting the calendar).
- **Later (not v1):** two-way sync, where deleting or moving an event in Google updates Homebox, via `events.watch` push channels. That needs a public HTTPS webhook. Also maintenance recurrence (upstream has none).

### Milestones
1. **G1** ICS feed + token management + settings page stub.
2. **G2** OAuth connect/disconnect flow + encrypted token store + calendar creation.
3. **G3** Reconciler + recurring task + ent hook + REST client with tests against an `httptest` fake Google.
4. **G4** Project-step events (needs P2) + options UI + error surfacing.

---

## 8. Suggested order and sizing

| Order | Work | Rough size |
|-------|------|-----------|
| 1 | P0 scaffolding + CI + seam guard | 1 PR, medium |
| 2 | G1 ICS feed (quick win that also proves the custom plumbing end to end) | small |
| 3 | P1–P3 Projects core | 2–3 PRs |
| 4 | G2–G3 Calendar push sync | 2 PRs |
| 5 | P4 + G4 integrations | 1 PR |

Each PR targets `custom` and must be green on `x-ci` + `x-seam-guard`.

## 9. Open questions
1. Deployment: where does Homebox run (Unraid container?), and is it reachable from Google (Cloudflare tunnel / Access)? This decides how practical Stage A is and what the OAuth redirect URL is.
2. SQLite or Postgres in production? (CI tests both either way.)
3. Should "completed maintenance" stay on the calendar as history, or disappear?
4. One shared household calendar per collection (planned), or per-user calendars?
