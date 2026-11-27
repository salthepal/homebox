#!/usr/bin/env bash
# Enforces the rebase contract (.fork/PLAN.md R1, R2, R3, R6, R7, R8, R9):
# every file changed on this branch relative to main must be custom-owned,
# or an allowed seam listed in .fork/SEAMS.txt.
#
# Usage: .fork/seam-guard.sh [base-ref]
#   base-ref defaults to $SEAM_BASE, then origin/main, upstream/main, main.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

base_ref="${1:-${SEAM_BASE:-}}"
if [ -z "$base_ref" ]; then
  for c in origin/main upstream/main main; do
    if git rev-parse --verify -q "$c" >/dev/null; then base_ref="$c"; break; fi
  done
fi
[ -n "$base_ref" ] || { echo "seam-guard: no main ref found (git fetch origin main)"; exit 2; }
base=$(git merge-base "$base_ref" HEAD)

# Custom-owned paths (glob patterns matched against repo-relative paths).
set -f  # patterns below are matched with [[ == ]], never expanded as paths
owned=(
  '.claude/*'
  '.fork/*'
  '.github/workflows/x-*.yml'
  'backend/app/api/x_*.go'
  'backend/internal/custom/*'
  'frontend/layers/custom/*'
)

# Never allowed, even if someone lists them in SEAMS.txt.
forbidden=(
  'backend/go.mod'
  'backend/go.sum'
  'frontend/package.json'
  'frontend/pnpm-lock.yaml'
  'frontend/locales/*'
  'backend/internal/data/ent/*'
  'backend/internal/data/migrations/*'
  'backend/app/api/static/docs/*'
  'frontend/lib/api/types/*'
)

seams=()
if [ -f .fork/SEAMS.txt ]; then
  while IFS= read -r line; do
    line="${line%%#*}"; line="${line%"${line##*[![:space:]]}"}"; line="${line#"${line%%[![:space:]]*}"}"
    [ -n "$line" ] && seams+=("$line")
  done < .fork/SEAMS.txt
fi

matches_any() { local p="$1"; shift; local g; for g in "$@"; do [[ "$p" == $g ]] && return 0; done; return 1; }
# Seams are literal paths (they can contain [ ] like pages/item/[id]/index.vue), so compare exactly.
is_seam() { local p="$1"; local s; for s in "${seams[@]+"${seams[@]}"}"; do [ "$p" = "$s" ] && return 0; done; return 1; }

fail=0
warn=0
mapfile -t changed < <(git diff --name-only "$base"..HEAD)

for f in "${changed[@]}"; do
  [ -n "$f" ] || continue
  if matches_any "$f" "${forbidden[@]}"; then
    echo "FORBIDDEN  $f  (generated/dependency/locale file — see PLAN.md R2/R3/R6/R7/R8)"; fail=1
  elif [[ "$f" == .github/workflows/* ]] && ! matches_any "$f" "${owned[@]}"; then
    echo "FORBIDDEN  $f  (upstream workflow — R9; add a new x-*.yml instead)"; fail=1
  elif matches_any "$f" "${owned[@]}"; then
    :
  elif is_seam "$f"; then
    n=$(git diff --numstat "$base"..HEAD -- "$f" | awk '{print $1+$2}')
    if [ "${n:-0}" -gt 6 ]; then
      echo "WARN       $f  seam changed by $n lines (keep seams to a one-line call/spread + import)"; warn=1
    else
      echo "seam       $f  ($n lines)"
    fi
  else
    echo "NOT ALLOWED $f  (upstream file; not custom-owned and not in .fork/SEAMS.txt — R1)"; fail=1
  fi
done

echo "seam-guard: ${#changed[@]} changed file(s) vs $base_ref ($(git rev-parse --short "$base"))"
if [ "$fail" -ne 0 ]; then echo "seam-guard: FAILED"; exit 1; fi
[ "$warn" -ne 0 ] && echo "seam-guard: passed with warnings" || echo "seam-guard: OK"
