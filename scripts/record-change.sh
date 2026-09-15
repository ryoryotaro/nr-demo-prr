#!/bin/sh

set -eu

repo_dir="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
requested_run_id="${DEMO_RUN_ID:-}"
requested_mode="${OBSERVABILITY_MODE:-}"

if [ -f "$repo_dir/.env" ]; then
  set -a
  # shellcheck disable=SC1091
  . "$repo_dir/.env"
  set +a
fi

DEMO_RUN_ID="${requested_run_id:-${DEMO_RUN_ID:-}}"
OBSERVABILITY_MODE="${requested_mode:-${OBSERVABILITY_MODE:-}}"

if [ -z "$DEMO_RUN_ID" ]; then
  printf '[ERROR] DEMO_RUN_ID is required.\n' >&2
  exit 2
fi
if [ "$OBSERVABILITY_MODE" != "complete" ] && [ "$OBSERVABILITY_MODE" != "incomplete" ] && [ "$OBSERVABILITY_MODE" != "regression" ]; then
  printf '[ERROR] OBSERVABILITY_MODE must be complete, incomplete, or regression.\n' >&2
  exit 2
fi

cd "$repo_dir"
if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  printf '[ERROR] %s is not a Git repository. Initialize it and create a commit before recording a change.\n' "$repo_dir" >&2
  exit 2
fi
if ! commit_sha="$(git rev-parse --verify HEAD 2>/dev/null)"; then
  printf '[ERROR] The Git repository has no commit. Create a commit before recording a change.\n' >&2
  exit 2
fi
if [ -n "$(git status --porcelain)" ]; then
  printf '[WARNING] Uncommitted changes exist; the Change Tracking event references HEAD, not the working tree.\n' >&2
fi

deep_link="${GITHUB_PR_URL:-${GITHUB_REPOSITORY_URL:-}}"
git_user="$(git config user.name 2>/dev/null || true)"
git_user="${git_user:-prr-demo}"

exec go run ./cmd/change \
  -commit "$commit_sha" \
  -demo-run-id "$DEMO_RUN_ID" \
  -mode "$OBSERVABILITY_MODE" \
  -deep-link "$deep_link" \
  -user "$git_user"
