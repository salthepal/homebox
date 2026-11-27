# GitHub repo setup — salthepal/homebox

One-time configuration. **Sections 1, 2, 3 (except creating the PAT) and 5 are automated:**

```bash
bash .fork/scripts/apply-repo-settings.sh
```

Run it from any machine where `gh` is logged in as salthepal. It's idempotent. After task F1 merges and CI has run once on `custom`:

```bash
bash .fork/scripts/apply-repo-settings.sh add-required-checks
```

Still manual: creating the PAT (the script prompts you to paste it), making the GHCR package public, and Google Cloud.
The checklist below documents what the script sets.

## 1. Branches
- [ ] **Default branch → `custom`**: Settings → General → Default branch → switch to `custom`.
  Scheduled workflows (sync, canary) only run from the default branch, and it's what gets deployed and cloned.
- [ ] Confirm `main` is a clean mirror. Compare on GitHub: `sysadminsmedia/homebox:main...salthepal/homebox:main` should show "0 commits ahead".
  If main has commits of its own, reset it to upstream (Sync fork → Discard commits) **only after confirming nothing on it is needed**.
- [ ] Settings → General → Pull Requests: **allow squash merging only** (untick merge commits and rebase merging). Default commit message = "Pull request title and description". Tick **Automatically delete head branches**.

## 2. Rulesets (Settings → Rules → Rulesets)
**Ruleset "main — mirror only"**, target `main`
- [ ] Restrict updates ✔, Restrict deletions ✔, Block force pushes ✔
- [ ] Bypass list: **Repository admin** (you, plus the sync workflow's PAT, which acts as you).
  Result: agents and the GitHub App can never push to `main` by accident.

**Ruleset "custom — CI required"**, target `custom`
- [ ] Restrict deletions ✔
- [ ] Require status checks to pass: `x-ci / backend`, `x-ci / frontend`, `x-seam-guard / guard` (add once those workflows have run once, so the check names exist)
- [ ] Require a pull request before merging ✔ (0 approvals)
- [ ] **Do NOT block force pushes for the bypass actor**. Rebasing custom needs `--force-with-lease`.
- [ ] Bypass list: **Repository admin**, mode "Always". This lets you or the rebase PAT push the rebased branch, while agent PRs still need green CI.

## 3. Actions (Settings → Actions → General)
- [ ] Actions permissions: allow all actions (upstream pins actions by SHA).
- [ ] Workflow permissions: **Read repository contents** (default read-only). Each `x-*` workflow declares what it needs.
- [ ] Disable upstream workflows that don't apply to the fork (Actions tab → workflow → ··· → Disable workflow):
  `Docker publish` (+ hardened, rootless), `binaries-publish`, `clear-stale-docker-images`, `issue-gatekeeper`,
  `update-currencies`, `copilot-setup-steps`, `upgrade-test`, `Pull Request CI`, `e2e-partial`.
  (`partial-backend.yaml` and `partial-frontend.yaml` are `workflow_call`-only. Leave them enabled, because `x-ci` reuses them.)
- [ ] Secret **`SYNC_TOKEN`** (Settings → Secrets and variables → Actions):
  a fine-grained PAT, resource owner `salthepal`, repo `salthepal/homebox` only. Permissions: **Contents: RW, Workflows: RW, Issues: RW, Metadata: R**. Expiry 1 year, with a calendar reminder to rotate it.
  *Why a PAT:* `GITHUB_TOKEN` can't push commits that change `.github/workflows/*`. Upstream changes workflows often, so syncing `main` and rebasing `custom` both need the Workflows permission.

## 4. Packages (GHCR)
- [ ] After the first successful `x / Docker (custom)` run: github.com/salthepal?tab=packages → `homebox` → Package settings →
  **Change visibility → Public** (Unraid pulls without credentials), and **Manage Actions access** → make sure `salthepal/homebox` has Write.
- [ ] Optional: a package retention rule, or periodically delete old `custom-<sha>` tags.

## 5. Security and housekeeping
- [ ] Settings → Code security: enable secret scanning + push protection. Dependabot alerts on. **Dependabot version-update PRs off** (deps come from upstream via rebase, R6).
- [ ] Issues: enabled (the canary files `rebase-break` issues). Create label `rebase-break` (red).
- [ ] Optional: turn off Wiki/Projects/Discussions to cut noise.

## 6. Local clone (each machine you develop on)
```bash
git remote add upstream https://github.com/sysadminsmedia/homebox.git
```
```bash
git config rerere.enabled true
```
```bash
git config rerere.autoupdate true
```

## 7. Google Cloud (before task G1)
- [ ] Create a project, e.g. "homebox-salitude". Enable the **Google Calendar API**.
- [ ] OAuth consent screen: External, add yourself as a user. Scopes `calendar.events`, `calendar.calendarlist.readonly`. **Publishing status → In production** (unverified is fine for personal use; Testing mode expires refresh tokens after 7 days).
- [ ] Credentials → OAuth client ID → Web application. Authorized redirect URI `https://<homebox-host>/api/v1/x/gcal/callback`.
- [ ] Put client ID and secret into the Unraid template as `HBOX_X_GOOGLE_CLIENT_ID` / `HBOX_X_GOOGLE_CLIENT_SECRET`. Generate `HBOX_X_SECRET_KEY` with `openssl rand -base64 32`. Set `HBOX_X_PUBLIC_BASE_URL`.

## 8. Ongoing routine
| When | What |
|------|------|
| Daily (auto) | `x-sync-main` fast-forwards `main` from upstream. |
| Weekly (auto) | `x-rebase-canary` trial-rebases `custom` onto `main` and runs tests. On failure it opens or updates a `rebase-break` issue. |
| After upstream changes you want, ~monthly | Run **x-rebase** (Actions → Run workflow), or `.fork/rebase.sh` locally. Clean + green → it force-pushes `custom`. Otherwise fix conflicts locally (see TASKS.md "Rebase procedure"). |
| After each merge to `custom` | `x-docker` publishes `:custom` → update the container in Unraid. |
