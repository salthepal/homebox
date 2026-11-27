#!/usr/bin/env bash
# Applies the GitHub settings from .fork/REPO-SETUP.md to salthepal/homebox.
# Run from any machine where `gh auth status` shows you logged in as salthepal (repo admin).
# Safe to re-run: rulesets are replaced by name, everything else is idempotent.
#
#   bash .fork/scripts/apply-repo-settings.sh
#
# Not automatable (do in the browser, see REPO-SETUP.md): creating the SYNC_TOKEN PAT,
# making the GHCR package public, Google Cloud setup.
set -euo pipefail

REPO="salthepal/homebox"
UPSTREAM="sysadminsmedia/homebox"
step() { printf '\n==> %s\n' "$*"; }

gh auth status >/dev/null 2>&1 || { echo "Run 'gh auth login' first."; exit 1; }

if [ "${1:-}" = "add-required-checks" ]; then
  # Run after task F1 merged and x-ci / x-seam-guard have reported on custom at least once.
  step "Adding required status checks to 'custom — CI required'"
  id=$(gh api "repos/$REPO/rulesets" --jq '.[] | select(.name=="custom — CI required") | .id')
  [ -n "$id" ] || { echo "Ruleset not found; run the script without arguments first."; exit 1; }
  # Check-run names on the tip of custom, excluding the Docker publish job.
  mapfile -t checks < <(gh api "repos/$REPO/commits/custom/check-runs?per_page=100" \
    --jq '.check_runs[] | select(.app.slug=="github-actions") | .name' | grep -v -i docker | sort -u)
  [ "${#checks[@]}" -gt 0 ] || { echo "No CI check runs on custom yet."; exit 1; }
  printf '  will require: %s\n' "${checks[@]}"
  read -r -p "  Proceed? [y/N] " ok; [ "$ok" = "y" ] || exit 1
  ctx=$(printf '{"context":"%s","integration_id":15368},' "${checks[@]}"); ctx="[${ctx%,}]"
  # Full rule set for custom (same as the create call below, plus status checks).
  cat <<JSON | gh api -X PUT "repos/$REPO/rulesets/$id" --input - --silent
{"rules":[
  {"type":"deletion"},
  {"type":"pull_request","parameters":{"required_approving_review_count":0,"dismiss_stale_reviews_on_push":false,
    "require_code_owner_review":false,"require_last_push_approval":false,"required_review_thread_resolution":false}},
  {"type":"required_status_checks","parameters":{"strict_required_status_checks_policy":false,
    "required_status_checks":$ctx}}
]}
JSON
  echo "  done"
  exit 0
fi

step "Checking main is a clean mirror of upstream"
ahead=$(gh api "repos/$UPSTREAM/compare/main...salthepal:homebox:main" --jq .ahead_by)
behind=$(gh api "repos/$UPSTREAM/compare/main...salthepal:homebox:main" --jq .behind_by)
echo "main is $ahead ahead / $behind behind upstream"
if [ "$ahead" != "0" ]; then
  echo "WARNING: main has commits upstream doesn't. Review before relying on fast-forward sync."
fi

step "General: default branch custom, squash-only merges, auto-delete branches"
gh api -X PATCH "repos/$REPO" \
  -f default_branch=custom \
  -F allow_squash_merge=true -F allow_merge_commit=false -F allow_rebase_merge=false \
  -f squash_merge_commit_title=PR_TITLE -f squash_merge_commit_message=PR_BODY \
  -F delete_branch_on_merge=true -F allow_update_branch=true \
  -F has_issues=true -F has_wiki=false -F has_projects=false \
  --silent

step "Actions: allow all actions, read-only default token, no PR approvals by Actions"
gh api -X PUT "repos/$REPO/actions/permissions" -F enabled=true -f allowed_actions=all --silent
gh api -X PUT "repos/$REPO/actions/permissions/workflow" \
  -f default_workflow_permissions=read -F can_approve_pull_request_reviews=false --silent

step "Disabling upstream workflows that don't apply to the fork"
for wf in docker-publish.yaml docker-publish-hardened.yaml docker-publish-rootless.yaml \
          binaries-publish.yaml clear-stale-docker-images.yml issue-gatekeeper.yml \
          update-currencies.yml copilot-setup-steps.yml upgrade-test.yaml \
          pull-requests.yaml e2e-partial.yaml; do
  if gh workflow disable "$wf" -R "$REPO" >/dev/null 2>&1; then echo "  disabled $wf"
  else echo "  skip $wf (not registered or already disabled)"; fi
done

step "Label rebase-break"
gh label create rebase-break -R "$REPO" --color B60205 \
  --description "custom no longer rebases cleanly onto main" --force

step "Security: secret scanning + push protection, Dependabot alerts on, Dependabot updates off"
gh api -X PATCH "repos/$REPO" --input - --silent <<'JSON' || echo "  (secret scanning settings not available for this repo; skipped)"
{"security_and_analysis":{"secret_scanning":{"status":"enabled"},"secret_scanning_push_protection":{"status":"enabled"}}}
JSON
gh api -X PUT "repos/$REPO/vulnerability-alerts" --silent
gh api -X DELETE "repos/$REPO/automated-security-fixes" --silent || true

step "Rulesets"
# Delete any existing rulesets with our names so re-runs replace, not duplicate.
for name in "main — mirror only" "custom — CI required"; do
  for id in $(gh api "repos/$REPO/rulesets" --jq ".[] | select(.name==\"$name\") | .id"); do
    gh api -X DELETE "repos/$REPO/rulesets/$id" --silent && echo "  removed old '$name' ($id)"
  done
done

# actor_id 5 = Repository admin role. bypass_mode always.
gh api -X POST "repos/$REPO/rulesets" --input - --silent <<'JSON'
{
  "name": "main — mirror only",
  "target": "branch",
  "enforcement": "active",
  "conditions": {"ref_name": {"include": ["refs/heads/main"], "exclude": []}},
  "bypass_actors": [{"actor_id": 5, "actor_type": "RepositoryRole", "bypass_mode": "always"}],
  "rules": [{"type": "update"}, {"type": "deletion"}, {"type": "non_fast_forward"}]
}
JSON
echo "  created 'main — mirror only'"

# Required status checks are added later by add-required-checks (after F1's workflows have run once,
# otherwise merges would wait forever on checks that never report).
gh api -X POST "repos/$REPO/rulesets" --input - --silent <<'JSON'
{
  "name": "custom — CI required",
  "target": "branch",
  "enforcement": "active",
  "conditions": {"ref_name": {"include": ["refs/heads/custom"], "exclude": []}},
  "bypass_actors": [{"actor_id": 5, "actor_type": "RepositoryRole", "bypass_mode": "always"}],
  "rules": [
    {"type": "deletion"},
    {"type": "pull_request", "parameters": {
      "required_approving_review_count": 0,
      "dismiss_stale_reviews_on_push": false,
      "require_code_owner_review": false,
      "require_last_push_approval": false,
      "required_review_thread_resolution": false
    }}
  ]
}
JSON
echo "  created 'custom — CI required' (status checks to be added after task F1)"

step "SYNC_TOKEN secret"
if gh secret list -R "$REPO" | grep -q '^SYNC_TOKEN'; then
  echo "  SYNC_TOKEN already set"
else
  echo "  Not set. Create a fine-grained PAT (repo salthepal/homebox only; Contents RW, Workflows RW,"
  echo "  Issues RW, Metadata R; 1-year expiry) at https://github.com/settings/personal-access-tokens/new"
  read -r -p "  Paste it now to store it (or press Enter to skip): " -s tok; echo
  if [ -n "${tok:-}" ]; then printf '%s' "$tok" | gh secret set SYNC_TOKEN -R "$REPO" && echo "  stored"; fi
  unset tok
fi

step "Done. Remaining manual items:"
echo "  - GHCR: make package 'homebox' public once the first x-docker run succeeds"
echo "    https://github.com/users/salthepal/packages/container/homebox/settings"
echo "  - After task F1 merges: bash .fork/scripts/apply-repo-settings.sh add-required-checks"
