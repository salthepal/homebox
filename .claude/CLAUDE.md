# salthepal/homebox fork — agent instructions

This is a **personal fork** of sysadminsmedia/homebox. Read these before changing anything:

1. `.fork/PLAN.md`: branch model, the **rebase contract (rules R1–R11, binding)**, architecture, and feature specs.
2. `.fork/TASKS.md`: ordered task list. Do the **next unchecked task only**, one task per PR.
3. `.fork/REPO-SETUP.md`: GitHub settings (human-run). Don't change repo settings yourself.

Non-negotiables:
- Work on a branch from `custom` (or the branch your harness assigns) and PR into `custom`. **Never push to `main`.** It's a pure upstream mirror.
- **Add new files; don't edit upstream files.** The only allowed upstream edits are the one-line seams in `.fork/SEAMS.txt`. Run `.fork/seam-guard.sh` before pushing.
- No new Go/npm deps. Don't regenerate upstream ent, swagger, or TS types. Don't touch `frontend/locales/*.json` or upstream workflows.
- Custom code works on Postgres and SQLite, with tests for both.
- Upstream's own conventions (`.github/AGENTS.md`, `.github/instructions/*`) still apply to code style inside custom paths, but not their "regenerate swagger/types before commit" steps (rule R7).
