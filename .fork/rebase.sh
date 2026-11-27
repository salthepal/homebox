#!/usr/bin/env bash
# Local rebase of custom onto upstream main (see .fork/PLAN.md §1, TASKS.md "Rebase procedure").
#   1. fetch upstream + origin
#   2. fast-forward local main to upstream/main (and push it to origin unless --no-push-main)
#   3. rebase custom onto main (rerere on)
#   4. run checks
#   5. print the force-push command (never pushes custom itself)
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

PUSH_MAIN=1
[ "${1:-}" = "--no-push-main" ] && PUSH_MAIN=0

git remote get-url upstream >/dev/null 2>&1 || git remote add upstream https://github.com/sysadminsmedia/homebox.git
git config rerere.enabled true
git config rerere.autoupdate true

[ -z "$(git status --porcelain)" ] || { echo "Working tree not clean; commit or stash first."; exit 1; }

echo "==> fetching"
git fetch --no-tags upstream main
git fetch --no-tags origin main custom

echo "==> fast-forwarding main to upstream/main"
git checkout -q -B main origin/main
git merge --ff-only upstream/main
if [ "$PUSH_MAIN" = 1 ] && [ "$(git rev-parse main)" != "$(git rev-parse origin/main)" ]; then
  git push origin main
fi

echo "==> rebasing custom onto main"
if git rev-parse --verify -q refs/heads/custom >/dev/null &&
   [ -n "$(git rev-list origin/custom..custom)" ]; then
  echo "Local custom has commits not on origin/custom; push or move them first:"
  git log --oneline origin/custom..custom
  exit 1
fi
git checkout -q -B custom origin/custom
old=$(git rev-parse HEAD)
if ! git rebase main; then
  echo
  echo "Rebase stopped on a conflict. Expected only in .fork/SEAMS.txt files:"
  git diff --name-only --diff-filter=U
  echo "Resolve, 'git add' the files, then 'git rebase --continue' and re-run the checks:"
  echo "  .fork/seam-guard.sh main && (cd backend && go build ./... && go test ./internal/custom/...)"
  exit 1
fi

echo "==> checks"
.fork/seam-guard.sh main
(cd backend && go build ./... && { [ ! -d internal/custom ] || { go vet ./internal/custom/... && go test -count=1 ./internal/custom/...; }; })
(cd frontend && pnpm install --frozen-lockfile >/dev/null && pnpm run typecheck)

echo
echo "All checks passed. custom: $(git rev-parse --short "$old") -> $(git rev-parse --short HEAD)"
echo "Push with:"
echo "  git push --force-with-lease=custom:$old origin custom"
