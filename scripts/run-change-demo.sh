#!/bin/sh

set -eu

repo_dir="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
mode="${1:-}"
if [ "$mode" != "complete" ] && [ "$mode" != "incomplete" ]; then
  printf 'Usage: %s complete|incomplete\n' "$0" >&2
  exit 2
fi

demo_run_id="${DEMO_RUN_ID:-$(date -u '+%Y%m%d-%H%M%S')-$$}"
export DEMO_RUN_ID="$demo_run_id"
export OBSERVABILITY_MODE="$mode"

cd "$repo_dir"
if ! commit_sha="$(git rev-parse --verify HEAD 2>/dev/null)"; then
  printf '[ERROR] A Git repository with at least one commit is required.\n' >&2
  exit 2
fi

printf '=== PRR Change Demo ===\n\n'
printf 'Mode: %s\n' "$mode"
printf 'Run ID: %s\n' "$DEMO_RUN_ID"
printf 'Commit: %s\n' "$commit_sha"
printf 'Version: %.7s\n\n' "$commit_sha"

printf 'Change Tracking\n'
./scripts/record-change.sh
printf '\n'

./scripts/run-demo.sh "$mode"

if [ "$mode" = "complete" ]; then
  readiness_result="READY"
  failed_checks="none"
else
  readiness_result="NOT READY"
  failed_checks="customer.plan, prr-demo-payment"
fi

printf '\nAutopilot Workflow Input:\n\n'
printf 'service:\nprr-demo-checkout\n\n'
printf 'demoRunId:\n%s\n\n' "$DEMO_RUN_ID"
printf 'readinessResult:\n%s\n\n' "$readiness_result"
printf 'failedChecks:\n%s\n' "$failed_checks"
