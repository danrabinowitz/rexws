#!/bin/sh
# Build the scratch world for the rexws demo assets.
#
# Everything lives under /tmp/rexws-demo (override with REXWS_DEMO_DIR). The
# script deletes and recreates that directory, uses a repo-local Git identity,
# and never reads or writes the real rexws config, Rex sessions, or worktrees.
set -eu

ROOT=${REXWS_DEMO_DIR:-/tmp/rexws-demo}
REPO_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
REPO="$ROOT/src/atlas"

export GIT_AUTHOR_NAME='Demo'
export GIT_AUTHOR_EMAIL='demo@example.invalid'
export GIT_COMMITTER_NAME='Demo'
export GIT_COMMITTER_EMAIL='demo@example.invalid'
export GIT_AUTHOR_DATE='2026-11-26T09:00:00Z'
export GIT_COMMITTER_DATE='2026-11-26T09:00:00Z'

rm -rf "$ROOT"
mkdir -p "$ROOT/bin" "$ROOT/config" "$ROOT/src" "$ROOT/wt" "$ROOT/state" "$ROOT/shots"

git init -q -b main "$REPO"
printf '# Atlas\n' > "$REPO/README.md"
git -C "$REPO" add README.md
git -C "$REPO" commit -q -m 'Initial commit'
git -C "$REPO" branch chore/old-docs

# feat/old-nav is merged into main, so it is a safe cleanup candidate.
git -C "$REPO" worktree add -q -b feat/old-nav "$ROOT/wt/feat-old-nav" main
printf 'Nav cleanup\n' > "$ROOT/wt/feat-old-nav/nav.txt"
git -C "$ROOT/wt/feat-old-nav" add nav.txt
git -C "$ROOT/wt/feat-old-nav" commit -q -m 'Nav cleanup'
git -C "$REPO" merge -q --ff-only feat/old-nav

# The remaining branches carry one commit each and stay unmerged.
for branch in feat/auth feat/billing-v2 feat/sparse-api fix/flaky-tests; do
  slug=$(printf '%s' "$branch" | tr '/' '-')
  dir="$ROOT/wt/$slug"
  git -C "$REPO" worktree add -q -b "$branch" "$dir" main
  printf 'Work on %s\n' "$branch" > "$dir/work.txt"
  git -C "$dir" add work.txt
  git -C "$dir" commit -q -m "Work on $branch"
done

# One dirty worktree gives cleanup a needs-review item. It is never removed.
printf 'uncommitted\n' > "$ROOT/wt/feat-sparse-api/notes.txt"

# The fake Rex writes this flag when the demo creates atlas/main.
main_live="$ROOT/state/main-live"
rm -f "$main_live"

cp "$REPO_ROOT/scripts/demo/fake-rex" "$ROOT/bin/rex"
cp "$REPO_ROOT/scripts/demo/run.sh" "$ROOT/run.sh"
chmod +x "$ROOT/bin/rex" "$ROOT/run.sh"

# Canonical paths match what rexws stores in state.json (macOS resolves /tmp to
# /private/tmp).
canon() { (CDPATH= cd -- "$1" && pwd -P); }
PROJECT_PATH=$(canon "$REPO")
old_nav_path=$(canon "$ROOT/wt/feat-old-nav")
auth_path=$(canon "$ROOT/wt/feat-auth")
billing_path=$(canon "$ROOT/wt/feat-billing-v2")
sparse_path=$(canon "$ROOT/wt/feat-sparse-api")
flaky_path=$(canon "$ROOT/wt/fix-flaky-tests")

cat > "$ROOT/config/config.json" <<JSON
{
  "default_provider": "git",
  "rex_bin": "$ROOT/bin/rex",
  "projects": [
    {
      "name": "atlas",
      "path": "$PROJECT_PATH",
      "provider": "git",
      "worktree_root": "$ROOT/wt"
    }
  ],
  "statuses": [
    { "id": "in_progress", "label": "In Progress" },
    { "id": "blocked", "label": "Blocked" },
    { "id": "in_review", "label": "In Review" },
    { "id": "ready", "label": "Ready" },
    { "id": "done", "label": "Done" }
  ],
  "default_status": "ready"
}
JSON

cat > "$ROOT/config/state.json" <<JSON
{
  "workstreams": {
    "$auth_path": { "status": "in_progress", "updated_at": "2026-11-26T09:00:00Z" },
    "$flaky_path": { "status": "in_progress", "updated_at": "2026-11-26T09:00:00Z" },
    "$sparse_path": { "status": "in_progress", "updated_at": "2026-11-26T09:00:00Z" },
    "$billing_path": { "status": "in_review", "updated_at": "2026-11-26T09:00:00Z" },
    "$PROJECT_PATH": { "status": "ready", "updated_at": "2026-11-26T09:00:00Z" },
    "$old_nav_path": { "status": "done", "updated_at": "2026-11-26T09:00:00Z" }
  }
}
JSON

go build -o "$ROOT/bin/rexws" "$REPO_ROOT/cmd/rexws"

printf 'Scratch world ready: %s\n' "$ROOT"
