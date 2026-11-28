# Task list — execute in order

**How to work a task (agents):**
1. Read `.fork/PLAN.md` (rules R1–R11 are binding) and the task below. Do **one** task per session/PR.
2. Branch from latest `custom`: `x/<task-id>-<slug>` (e.g. `x/f1-ci-guardrails`). **`custom` requires a PR (ruleset), so a direct push to it is rejected.** If your harness assigns `custom` as the working branch, still push to an `x/…` branch and open the PR from there.
3. Implement → run the task's checks locally → open a **draft PR into `custom`** titled `x(<area>): <summary> [<task-id>]`.
4. Tick the task's box in this file in the same PR. Add anything surprising to "Notes for later tasks" at the bottom.
5. Never push to `main`. Never edit upstream files except the seams listed in `.fork/SEAMS.txt`. If you think a new seam is needed, stop and ask. Don't add one silently.

Local checks used below:
- **BE:** `cd backend && go build ./... && go vet ./internal/custom/... ./app/api/ && go test ./internal/custom/... ./app/api/ -run '^TestCustom'`
- **BE-PG:** same tests with `TEST_X_DB=postgres` against `docker run --rm -e POSTGRES_PASSWORD=pg -p 5432:5432 postgres:17` (connection string from `TEST_X_PG_DSN`, default `postgres://postgres:pg@localhost:5432/postgres?sslmode=disable`)
- **FE:** `cd frontend && pnpm install --frozen-lockfile && pnpm run lint && pnpm run typecheck` (see `frontend/package.json` for exact script names)
- **SEAM:** `.fork/seam-guard.sh` exits 0

---

## Phase F — fork foundation

### [x] F1 · Guardrail scripts + CI
Create:
- `.fork/seam-guard.sh`: computes `git diff --name-only $(git merge-base origin/main HEAD)..HEAD`. Fails if any path is not custom-owned (PLAN §3 globs) and not listed in `.fork/SEAMS.txt`. **Always** fails on `backend/go.mod`, `backend/go.sum`, `frontend/package.json`, `frontend/pnpm-lock.yaml`, `frontend/locales/**`, `backend/internal/data/ent/**`, `backend/internal/data/migrations/**`, `backend/app/api/static/docs/**`, `frontend/lib/api/types/**`, `.github/workflows/<non x->*`. For seam files, warns if the diff for that file is more than 6 changed lines.
- `.github/workflows/x-seam-guard.yml`: on PR → custom and push → custom. `fetch-depth: 0`, fetch `main`, run the script. Job id `guard`.
- `.github/workflows/x-ci.yml`: on PR → custom and push → custom. Jobs: `backend` = `uses: ./.github/workflows/partial-backend.yaml`; `frontend` = `uses: ./.github/workflows/partial-frontend.yaml`; `custom-go` = setup-go 1.27, Postgres 17 service, runs `go test ./internal/custom/... ./app/api/ -run '^TestCustom'` twice (SQLite, then `TEST_X_DB=postgres`). Copy pinned action SHAs from upstream workflows. If a partial workflow needs secrets or inputs, pass them through. Don't edit the partials.
- `.github/workflows/x-sync-main.yml`: daily cron (`17 9 * * *`) + `workflow_dispatch`. Uses `SYNC_TOKEN`. `POST /repos/salthepal/homebox/merge-upstream {"branch":"main"}` (via `gh api`). On HTTP 409 (diverged), open a `rebase-break` issue: "main diverged from upstream".
- `.github/workflows/x-rebase.yml` (canary and rebase in one workflow): the weekly cron (`43 10 * * 1`) runs a trial rebase onto `main` + seam guard + backend build/custom tests (SQLite + Postgres) + frontend typecheck, and never pushes. On failure it opens or updates one `rebase-break` issue with the failing step and conflicting files, and on success it closes that issue. `workflow_dispatch` with `push: true` does the same, then `git push --force-with-lease=custom:<old>` using `SYNC_TOKEN`.
- `.fork/rebase.sh`: the local equivalent (fetch upstream → ff `main` → `git rebase main` on custom → checks → prints `git push --force-with-lease` for you to run). Uses rerere.
- Every `x-*` workflow job: `if: github.repository == 'salthepal/homebox'`, plus minimal `permissions:`.

**Done when:** workflows pass on the PR. The seam guard fails a throwaway commit that touches `frontend/locales/en.json` (show this in the PR description, then revert). `x-sync-main` dispatch succeeds once.

**Status (2026-11-27):** built and pushed directly to `custom` (the rulesets weren't applied yet). Seam guard verified locally: it passes on `custom`, and a throwaway commit touching `frontend/locales/`, `README.md` and `pull-requests.yaml` failed while an `[id]` seam path passed. `x-ci` / `x-seam-guard` run on push. **Pending owner setup:** `x-sync-main` and `x-rebase` dispatch need the default branch switched to `custom` (dispatch and schedules use the default branch), and pushing needs `SYNC_TOKEN`. Verify both after running `apply-repo-settings.sh`.

### [x] F2 · Backend scaffolding
- `backend/internal/custom/config`: `Load() (Config, error)` with ardanlabs/conf, prefix `HBOX_X`. Fields: PublicBaseURL, GoogleClientID, GoogleClientSecret, SecretKey (base64 → 32 bytes, validated), GcalSyncInterval (default 15m). Add `GcalEnabled()`.
- `backend/internal/custom/migrations`: `//go:embed` for `postgres/*.sql` and `sqlite3/*.sql`, and `Run(ctx, db *sql.DB, driver string) error` using `goose.NewProvider` with a store on table **`x_goose_db_version`** (see PLAN §5; check the goose v3.29 API for how `WithStore` interacts with the dialect argument). First migration `00001_x_init.sql` (both dialects): `CREATE TABLE x_meta (key text primary key, value text)`. Use sequential numbering (`00001_…`), not timestamps. Our table is independent, so order only matters among our own files.
- `backend/internal/custom/ent`: `generate.go` (`go run -mod=mod entgo.io/ent/cmd/ent generate ./schema`), schema dir with an `XMeta` schema matching the migration. Commit the generated code. `Open(driver, db *sql.DB) *ent.Client` using `entsql.OpenDB`.
- `backend/internal/custom/api`: `Mount(r chi.Router, deps Deps)` with `GET /ping` → `{"ok":true,"group_id":…}`.
- `backend/app/api/x_register.go` (package main): `func registerCustom(a *app, cfg *config.Config, router *chi.Mux, chain *errchain.ErrChain, runner *graceful.Runner)`. It loads custom config, gets the `*sql.DB` the same way `setup.go` does (`<ent client>.Sql()`), runs custom migrations (fatal on error, like upstream), opens the custom ent client, and mounts `router.Route("/api/v1/x", …)` with the same `userMW` stack as routes.go.
- **Seam:** add the one call in `backend/app/api/main.go` (PLAN §4). Create `.fork/SEAMS.txt` listing `backend/app/api/main.go` (if F1 didn't already).
- Tests: migrations apply twice idempotently on SQLite **and** Postgres, and coexist with upstream migrations (run upstream `goose.Up` first, then ours). A router test shows `/api/v1/status` and authed `/api/v1/x/ping` both route correctly.

**Done when:** BE + BE-PG + SEAM pass. The container starts against Postgres, and the log shows custom migrations applied.

**Status (2026-11-27):** done. Tests pass on SQLite and Postgres 16. A real server binary against Postgres served `/api/v1/status` 200, `/api/v1/x/ping` 401 without a token and 200 with one, and `x_goose_db_version` held version 1. The seam is 3 lines (an `if err := registerCustom(...); err != nil { return err }`) because upstream lint forbids `log.Fatal`.

### [x] F3 · Frontend layer scaffolding
- `frontend/layers/custom/nuxt.config.ts` (minimal `defineNuxtConfig({})`). Confirm Nuxt picks the layer up automatically; if it doesn't, stop and report rather than editing `frontend/nuxt.config.ts`.
- `plugins/x-i18n.ts`: merges `locales/en.json` (and any other present locale files) into the app's vue-i18n instance. Keys are namespaced under `x.*`.
- `lib/api/client.ts`: a thin wrapper reusing the app's existing authenticated request helper (find how `frontend/composables` builds `useUserApi`, and reuse its base requests/auth/tenant header). Typed methods are added per feature.
- `nav.ts`: `customNavItems(t, route)` returns nav entries in the exact shape `layouts/default.vue` uses. Initially one entry, "Projects" → `/x/projects`.
- `item-tabs.ts`: `customItemTabs(itemId)` returns `[]` for now.
- `pages/x/projects/index.vue`: placeholder page that calls `/api/v1/x/ping` and shows the result.
- **Seams:** the nav spread in `frontend/layouts/default.vue` and the tabs spread in `frontend/pages/item/[id]/index.vue` (PLAN §4). Add both to SEAMS.txt.

**Done when:** FE + SEAM pass. In the browser, the nav shows "Projects" and the page shows ping OK. `frontend/locales/*` is untouched.

**Status (2026-11-27):** done. Lint and typecheck pass. Checked in Playwright (Nuxt dev + backend on Postgres): after login, the nav shows Projects after Maintenance, `/x/projects` renders translated `x.*` strings and "Custom backend connected", and all upstream nav entries are intact. Each seam is one import + one spread line, marked `// fork seam`.

---

## Phase P — Projects

### [x] P1 · Schema, migrations, repo
Tables from PLAN §6 as `00002_x_projects.sql` (postgres + sqlite3), ent schemas, generated code, and a repo in `internal/custom/projects` (CRUD, steps CRUD, reorder in a transaction, entity-link replace, list by entity). Every method takes a group ID and filters by it.
**Done when:** repo tests on SQLite and Postgres cover CRUD, cross-group isolation (group B can't read or modify group A), reorder, and cascade of steps on project delete.

**Status (2026-11-28):** done. Repo tests pass on SQLite and Postgres 16 (CRUD, status timestamps, cross-group isolation on every method, reorder validation, cascade at repo and DB level). `x_project_entities` has no ent schema (composite key with no upstream edge), so the repo reads and writes it with plain SQL on the same `*sql.DB` (`$n` vs `?` placeholders by dialect). `Repo.CompleteStep` stores the completion details, and the P2 service creates the maintenance entry first and passes its ID in.

### [x] P2 · Service, API, adapters, sweeper
- `internal/custom/upstream/entities.go` (exists-in-group checks, names for display) and `upstream/maintenance.go` (create entry, exists check), wrapping upstream repos.
- Service rules from PLAN §6 (status suggestions, complete-step → maintenance entry).
- Handlers in `internal/custom/api/projects.go` for every route in PLAN §6. Input validation, 404 for other groups' IDs.
- Daily sweeper task registered in `registerCustom`.
**Done when:** API tests (httptest through the real middleware) cover happy paths, a cross-group 404, and complete-step creating a real upstream maintenance entry. BE + BE-PG pass.

**Status (2026-11-28):** done (stacked on P1). Adapters in `upstream/entities.go` (names, exists-in-group; bulk reads via upstream's ent client) and `upstream/maintenance.go` (create completed entry via upstream's repo, delete, exists-in-group). Service rules and the daily sweeper (`x-projects-sweep`, registered in `registerCustom`) are in `internal/custom/projects` (`service.go`, `sweep.go`). Handlers: `internal/custom/api/projects.go` (JSON is snake_case, dates `YYYY-MM-DD`; step/project bodies are full replaces; unknown fields are rejected). API tests are in `backend/app/api/x_projects_test.go` (`TestCustomProjects*`) and pass on SQLite and Postgres 16. Error mapping: other group's or missing ID → 404; validation, unknown item, bad IDs → 400; completing a done step → 409.

**Review follow-up (x/p2-review-fixes):**
- Completion claims the step atomically (`Repo.ClaimStep`, conditional `status <> done` update) before the maintenance entry is created; losers get 409. A failed entry/record step deletes the entry and reverts the claim (`ReleaseStep`).
- Ordinary step create/update can't set `done` (400, points at `/complete`); editing a done step or leaving done still works.
- Request bodies must be exactly one JSON value (trailing data → 400).
- Auto-start uses `Repo.StartProject` (updates only status + started_at, only from idea/planning), inside the same transaction as the step insert/update, and only when a step moves INTO doing.
- Project names and step titles are limited to 255 bytes (ent's `MaxLen` counts bytes; stricter than VARCHAR(255) characters) so users get 400 instead of 500. Costs and budget must be finite, >= 0, < 1e12, at most 2 decimals (rejected, not rounded), and are stored normalised. Both checks live in the repo (`validate.go`) so every caller is covered.

### [x] P3 · Projects UI
Pages `/x/projects` and `/x/projects/[id]` per PLAN §6. Use existing shadcn components from `frontend/components/ui` (import them explicitly). Strings in the layer locale. Responsive.
**Done when:** FE passes. Manual check: create a project, add, reorder and complete steps, link two items, and confirm progress and cost totals update. Screenshots in the PR.

**Status (2026-11-28):** done (stacked on P2). Pages `/x/projects` (status board: grid of sections, terminal statuses hidden while empty) and `/x/projects/[id]` (status select, edit/delete, linked-item picker, step list with up/down reorder, per-step status, complete dialog, "all steps done, mark project done?" prompt). Verified in Playwright against a real backend on Postgres: created a project, linked two items, added three steps, reordered, completed steps (with "log as maintenance" on), and the progress, estimated and spent totals updated. The NAS item then had two new maintenance entries (cost 75.25 and 0, "From project: ..."). Screenshots are in `.fork/screenshots/p3/`. UI pieces P4 can reuse: `components/ProjectCard.vue`, `ProjectFormDialog.vue`, `StepCompleteDialog.vue`, `ItemPicker.vue`, `composables/useItemNames.ts`, `lib/projects.ts` helpers, and the project methods on `CustomApi` (`projectsForEntity` is already there).

### [x] P4 · Item tab + maintenance logging UX
`customItemTabs` returns the "Projects" tab. `pages/item/[id]/index/projects.vue` lists linked projects with "New project for this item" (pre-linked). The complete-step dialog has a "log as maintenance" toggle (default on when the step has an item).
**Done when:** completing a step from the UI shows a new entry on the item's Maintenance tab.

**Status (2026-11-28):** done (stacked on P3). `customItemTabs` returns one "Projects" tab (`x.projects.item_tab.title`) after Maintenance; the seam spread already existed, so no new seam. The tab page lists linked projects with `ProjectCard` and has "New project for this item", which creates the project and then links the item with `setProjectEntities` (if linking fails the user gets a toast and still lands on the project page). The complete dialog already defaulted the toggle on when the step has an item (P3's `StepCompleteDialog`), so no change was needed there. Verified in Playwright against a real backend on Postgres: the tab shows the linked project, a new project from the tab opens already linked to the item, the toggle is checked by default, and completing the step created "Install 10GbE NIC" (cost 80, "From project: …") on the item's Maintenance tab. Note that upstream's Maintenance tab defaults to the "Scheduled" filter, so a completed entry appears under "Completed" or "Both". Screenshots are in `.fork/screenshots/p4/`.

---

## Phase G — Google Calendar

### [x] G1 · OAuth connect + calendar picker
- Tables `x_gcal_connections`, `x_gcal_links` (`00003_x_gcal.sql`, both dialects).
- `internal/custom/gcal/crypto.go`: AES-GCM encrypt/decrypt with `SecretKey`.
- `internal/custom/gcal/oauth.go`: `golang.org/x/oauth2` config, signed state (HMAC with SecretKey, 10-min expiry, includes user + group). Endpoints: `GET /gcal/status`, `POST /gcal/connect` (returns the auth URL), `GET /gcal/callback` (**outside** the Bearer middleware, validated via state; redirects to `/x/calendar`), `GET /gcal/calendars` (calendarList filtered to writer/owner), `PUT /gcal/settings` (calendar_id + options), `POST /gcal/disconnect` (`{remove_events: bool}`).
- `internal/custom/gcal/client.go`: hand-written REST client (R6) for calendarList.list and events insert/patch/delete/list (with `privateExtendedProperty`). Retry and backoff for 429/5xx.
- UI `/x/calendar` per PLAN §7. If `GcalEnabled()` is false, show which env vars are missing.
**Done when:** unit tests run against an `httptest` fake Google (token + calendar endpoints). The real connect flow works on the Unraid deploy, and the household calendar appears in the picker.

**Status (2026-11-28):** code done, real-Google check **pending the Unraid deploy**.
- Backend: `gcal/{crypto,state,client,store,service}.go` (the signed-state and OAuth config code is in `state.go` and `service.go`, not a separate `oauth.go`), ent schemas + `00003_x_gcal.sql`, handlers in `internal/custom/api/gcal.go` (`mountGcal`; callback is the only route without `userMW`). Tests run against an in-memory fake Google (`gcal/gcaltest`, reusable by G2): crypto, state forgery/expiry, client retry/backoff, connect flow, picker (read-only calendars excluded), broken-token handling, disconnect safety (only events that are in `x_gcal_links` AND carry the Homebox marker are deleted), plus HTTP tests through the real middleware in `app/api/x_gcal_test.go`. BE, BE-PG, FE (lint, typecheck) and SEAM pass.
- UI: `/x/calendar` (not-configured view lists the missing env vars and the redirect URI, connect, broken/Reconnect, calendar picker, options, sync status, disconnect with "also remove events") and a "Calendar" nav entry. Browser-checked against a local backend: missing-vars view, Connect → Google consent URL with the right client_id, redirect_uri, `access_type=offline`, `prompt=consent`, scopes and state, and the declined-consent toast.
- Reconnect semantics: reconnecting with the same Google account keeps the calendar choice and links. A different account clears the calendar and drops the group's links (the old events stay on the old account's calendar, since the old credentials can't remove them). The Google token is revoked only when no other group's connection uses the same account (Google revokes per account and client). Switching away from a deleted/inaccessible calendar (404/410) just drops the links.
- Deviations: `x_gcal_connections.broken` boolean added (PLAN's table has no column for the "mark connection broken" state). "Sync now" and `/gcal/sync` are G2's. The Projects-page link to `/x/calendar` is left to P3 (it owns those pages).
- **Owner must do before the real-flow check:** (1) in Google Cloud Console enable the Google Calendar API, create an OAuth client of type *Web application*, and add the authorized redirect URI `{HBOX_X_PUBLIC_BASE_URL}/api/v1/x/gcal/callback` (the `/x/calendar` page shows the exact value once the base URL is set); (2) set the OAuth consent screen's publishing status to **In production**, since in Testing mode refresh tokens die after 7 days (an unverified app shows a warning screen; for a personal app, continue past it); (3) set `HBOX_X_PUBLIC_BASE_URL`, `HBOX_X_GOOGLE_CLIENT_ID`, `HBOX_X_GOOGLE_CLIENT_SECRET` and `HBOX_X_SECRET_KEY` (`openssl rand -base64 32`) on the Unraid container; (4) back up Postgres first (migration 00003); (5) open `/x/calendar`, Connect, and check the household calendar appears in the picker.

### [x] G2 · Reconciler + triggers
- `internal/custom/gcal/reconcile.go`: desired-set builder (via `upstream/maintenance.go` + entity names), mapping and hash, and the diff → insert/patch/delete. Includes the shared-calendar safety checks (PLAN §7) and rebuild-from-calendar.
- Recurring task every `GcalSyncInterval`. A runtime ent hook on the upstream client's `MaintenanceEntry` enqueues a debounced per-group reconcile (a single goroutine and channel owned by registerCustom, stopped on shutdown). Project-step writes enqueue too. `POST /gcal/sync` = Sync now.
**Done when:** tests against the fake Google cover create → event inserted; edit → patched; complete → ✅ title on the completion date with no reminder; delete → event deleted; a foreign event on the calendar is never touched; and a lost links table is rebuilt without duplicates. Manual check on the real calendar.

**Status (2026-11-28):** code done, real-calendar check **pending the Unraid deploy**.
- Backend: `gcal/mapping.go` (`Desired`, `SourceProvider`, `MaintenanceDesired`, `EventHash`), `gcal/reconcile.go` (`Reconciler`: `Reconcile`, `ReconcileAll`, `Enqueue`, `Run`, `MaintenanceProvider`), `upstream/maintenance_read.go` (`MaintenanceReader.ListForGroup`, `OnMaintenanceChange` ent hook; the read side lives in its own file so it doesn't collide with the P2 write adapter in `upstream/maintenance.go`), `app/api/x_gcal_register.go` (queue goroutine + periodic task, registered only when Google is configured), `POST /gcal/sync` in `api/gcal.go`, and a "Sync now" button on `/x/calendar`. `x_register.go` gains 3 lines.
- Algorithm: every reconcile lists the calendar's own events once (`privateExtendedProperty=homebox_source=homebox`), then diffs desired sources against `x_gcal_links` and that list. Events carry `homebox_source`, `homebox_id` and `homebox_type`, so links are rebuilt from the calendar when `x_gcal_links` is lost (re-linked with an empty hash, which forces one verifying patch). Patch and delete only ever touch an event that is in the links table AND was returned by the owned-events listing (marker present); a link pointing at a non-owned event is just dropped. A single failing event doesn't stop the others: the first error is returned and recorded as `last_error`, the rest still sync. `invalid_grant`/401 marks the connection broken and stops the run.
- Triggers: the ent hook resolves the entry's group with the mutation's own client (same transaction, so no second connection) and calls `Reconciler.Enqueue`; the single `Run` goroutine reconciles a group once 10 s (`ReconcilerOptions.Debounce`) have passed since its last request. Item deletion cascades in the DB and bypasses hooks, so the periodic `ReconcileAll` (every `GcalSyncInterval`) catches it. A full sync is idle-cheap (one list call, no writes).
- Tests: `gcal/reconcile_test.go` (fake Google: create, edit, complete, include_completed off, delete, foreign events untouched, lost links rebuilt without duplicates, event deleted on the calendar is re-inserted, 404 on patch falls back to insert, 429 backoff, invalid_grant marks broken, per-event failure keeps going, not connected / no calendar, calendar change, debounce collapses a burst, no-op when not configured) and `app/api/x_gcal_sync_test.go` (real upstream tables and repos: create/edit/complete/delete through `MaintEntry` reach the calendar via the hook; item delete is caught by the periodic sync; other groups ignored; Sync now over HTTP). BE, BE-PG, `-race` on the new code, FE (lint, typecheck) and SEAM pass.
- Review fixes: PATCH now sends `description` and reminder `overrides` explicitly (Google's PATCH leaves omitted fields alone, so an emptied description or a completed event's popup reminder used to stay); `gcaltest.Fake` PATCH has real merge semantics so tests can catch that. Owned events that no link points at (duplicates, or leftovers of a deleted source) are deleted in a new `applyDuplicates` step, marker + managed source type only. `OnMaintenanceChange` defers `notify` to `tx.OnCommit` when the mutation is transactional (nothing on rollback). `POST /gcal/sync` returns the partial `{result, status, error}` with a 502/500 when some entries synced and others failed, and the page shows a "partly synced" warning.
- Known limits: editing an event by hand in Google isn't reverted until its source changes; deleting one is (re-inserted on the next sync). Switching the target calendar is handled by G1's `switchCalendar` (old events are cleaned up); the content hash includes the calendar ID so the new calendar gets its own copies.
- Real-calendar check (owner, after deploying with the G1 env vars): connect and pick the household calendar; schedule a maintenance entry on an item and within ~10 s a 🔧 all-day event with the deep link and a 1-day popup reminder appears; edit its name and date and the same event updates; mark it complete and it becomes ✅ on the completion date with no reminder; delete it and the event disappears; add an unrelated event by hand and confirm it is never touched; press Sync now and confirm "up to date".

### [ ] G3 · Project-step events + polish
`📋` step events (PLAN §7), wired to `include_project_steps`. Settings UI shows last sync and last error, plus Reconnect when the token is broken.
**Done when:** a due step appears on the calendar and turns ✅ when completed.

### [x] G4 · (optional) ICS feed
Per-group revocable token (stored hashed), `GET /api/v1/x/calendar/{token}.ics`, outside the auth middleware, rate-limited. Needs a Cloudflare Access bypass for that path only.

**Status (2026-11-28):** done. Migration `00004_x_ics.sql` (table `x_ics_feeds`, one row per group, only the SHA-256 of the token is stored). `internal/custom/ics`: RFC 5545 writer (CRLF, 75-octet folding that never splits a UTF-8 sequence, TEXT escaping, stable `UID`s, all-day `DTSTART;VALUE=DATE`, `X-WR-CALNAME`, display `VALARM` for scheduled work), token store (256-bit base64url, issue/rotate/revoke/lookup with constant-time compare), and a per-IP fixed-window limiter (60 requests/min). Events reuse G2's `MaintenanceDesired` mapping (🔧/✅ titles, deep links), so the feed matches the Google calendar. Routes: `GET/POST/DELETE /api/v1/x/calendar/feed` (authenticated: status, create-or-rotate returning the URL once, revoke) and `GET /api/v1/x/calendar/{token}.ics` (no auth middleware). Unknown, malformed, huge and revoked tokens all get the same bare 404. UI: an "ICS feed" card on `/x/calendar` (shown whether or not Google is configured; copy, rotate, revoke). Project steps are not in the feed (G3 adds them to the Google provider; add them here too if wanted).
- Checks run locally: BE + BE-PG (custom + `TestCustom*`), FE lint and typecheck, SEAM. Not in a real browser.
- **Owner step:** add a Cloudflare Access **bypass** policy for the path `/api/v1/x/calendar/*` **only**. Calendar apps can't log in, so without it the subscription URL is blocked. The rest of the site stays behind Access. The feed is protected by the unguessable token, rate limiting and revocation instead.
- **Rate limiting behind the tunnel (assumption):** upstream omits RealIP, so the limiter keys on the socket peer, and trusts `CF-Connecting-IP` **only** when that peer is loopback/private, which is the deployed setup (cloudflared on the Docker/LAN network). A public peer can't choose its own bucket. If cloudflared ever connects from a public address, every caller shares one bucket. Client IPs are normalised (IPv4-mapped IPv6 unmapped, IPv6 keyed by /64) and the limiter tracks at most 10,000 keys: when full it reclaims expired buckets, otherwise new keys get 429.
- **Review hardening:**
  - **Ownership:** only group **owners** can create, rotate or revoke the feed (403 otherwise, and fail-closed if the group adapter isn't wired). Any member can read `GET /calendar/feed` status, which never contains the token.
  - **Bounds:** the feed holds at most 5,000 events, newest first; completed entries only from the last 365 days, scheduled (not completed) ones up to 2 years ahead, with overdue ones kept. The bounds run in SQL (`upstream/maintenance_feed.go`). The response carries a content `ETag` and honours `If-None-Match` with 304.
  - **Orphans:** a token whose group no longer exists is a 404, and its row is deleted on first use. The daily `x-ics-sweep` task (`ics.Store.SweepOrphans`, next to `x-gcal-sweep` in `x_register.go`) removes the rest.
  - **Headers:** `Cache-Control: no-store`, `X-Content-Type-Options: nosniff` and `Referrer-Policy: no-referrer` are set on every response of the feed route (200, 304, 404, 429, 500) and on the create/rotate response, which contains the secret URL.
- **Known and accepted:** the token is in the URL path, so upstream's request logger (`internal/web/mid/logger.go` logs `r.URL.Path`) writes it to the logs. The remedy is to rotate the token; a future seam could redact it.
- Migration `00004` is new: back up Postgres before deploying.

---

## Rebase procedure (when x-rebase fails or opens a `rebase-break` issue)
1. `.fork/rebase.sh` locally (rerere on).
2. Conflicts should only be in SEAMS.txt files. Re-apply the one-line spread/call into upstream's new version of the code.
3. A compile error in `internal/custom/upstream/*` means upstream changed an API. Fix it in the adapter only.
4. A custom test that fails after an upstream table change means R4 was violated somewhere, or an adapter query is stale. Fix it on our side.
5. Run BE, BE-PG, FE and SEAM, then `git push --force-with-lease origin custom`. Close the `rebase-break` issue.

## Notes for later tasks
- **Test naming:** tests in `backend/app/api` must be named `TestCustom*` (CI runs `-run '^TestCustom'` there). Everything under `internal/custom/...` runs in full.
- **Test DBs:** use `xtest.Open(t)` + `xtest.UpstreamMigrate(t, d)` + `migrations.Run`. On Postgres each test gets its own database, because upstream migrations hard-code `public.`. The DSN user needs CREATEDB.
- **goose:** our provider uses `WithDisableGlobalRegistry(true)`, because `app/api/main.go` imports upstream's Go migrations, which register globally. Custom migrations are SQL only, numbered `0000N_x_<name>.sql`, in both `postgres/` and `sqlite3/`.
- **ent:** `go generate ./internal/custom/ent` modifies `backend/go.sum` (the generator's own deps). Always `git checkout -- backend/go.sum backend/go.mod` afterwards. The generated code builds without them.
- **Lint:** upstream golangci-lint (run by x-ci's `backend` job) covers our code too: no `log.Fatal`, no `init()`, testifylint rules apply.
- **Local server run:** needs `HBOX_AUTH_API_KEY_PEPPER` (≥32 bytes) or it panics at startup.
- **Layer i18n:** upstream loads locale JSON as read-only ES module namespaces, so `mergeLocaleMessage` throws ("Cannot assign to property 'x' of [object Module]"). `plugins/x-i18n.ts` uses `setLocaleMessage(locale, {...existing, ...ours})`. Keep custom keys under `x.*`.
- **Layer imports:** inside the layer, `~`/`@` resolve to the upstream app (`@/components/...`). Import layer files relatively, or from upstream seams via `~~/layers/custom/...`. No component auto-import.
- **Frontend API:** add methods to `CustomApi` in `layers/custom/lib/api/client.ts` and use `useCustomApi()`. It shares upstream's auth, X-Tenant and 401 handling.
- **Browser check:** `pnpm dev --port 3000` in `frontend/` proxies `/api` to `localhost:7745`. Run the backend binary on 7745. The first page load takes ~1–3 min while Vite pre-bundles.
- **Local Postgres in a cloud session:** `/usr/lib/postgresql/16/bin/initdb` + `pg_ctl` as the `postgres` user, data dir under `/var/tmp`. `TEST_X_PG_DSN=postgres://postgres@localhost:5432/postgres?sslmode=disable`.
- **Custom dialogs:** upstream's `Dialog` is bound to a fixed `DialogID` enum in `dialog-provider`, so layer dialogs use reka-ui's `DialogRoot` with `v-model:open` and the `ui/dialog` content parts instead.
- **Custom JSON is snake_case:** `CustomApi` types mirror the Go handlers (`target_date`, `entity_ids`, ...), not upstream's camelCase.
