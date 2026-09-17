#!/bin/sh

set -eu

repo_dir="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
mode="${1:-}"
if [ "$mode" != "complete" ] && [ "$mode" != "incomplete" ] && [ "$mode" != "regression" ]; then
  printf 'Usage: %s complete|incomplete|regression\n' "$0" >&2
  exit 2
fi

if [ -f "$repo_dir/.env" ]; then
  set -a
  # shellcheck disable=SC1091
  . "$repo_dir/.env"
  set +a
fi

demo_run_id="${DEMO_RUN_ID:-$(date -u '+%Y%m%d-%H%M%S')-$$}"
export DEMO_RUN_ID="$demo_run_id"
export OBSERVABILITY_MODE="$mode"

if [ -n "${READINESS_JSON_OUTPUT:-}" ]; then
  readiness_report="$READINESS_JSON_OUTPUT"
else
  readiness_report="$(mktemp)"
  trap 'rm -f "$readiness_report"' EXIT HUP INT TERM
fi
export READINESS_JSON_OUTPUT="$readiness_report"

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

if ! jq -e '.result == "READY" or .result == "NOT READY"' "$readiness_report" >/dev/null; then
  printf '[ERROR] Readiness report is missing or invalid.\n' >&2
  exit 2
fi

readiness_result="$(jq -r '.result' "$readiness_report")"
failed_checks="$(jq -r '[.checks[] | select(.status == "FAIL") | .name] | if length == 0 then "none" else join(", ") end' "$readiness_report")"
readiness_evidence="$(jq -r 'if .result == "READY" then "All required checks passed for demo.run_id=" + .demoRunId else [.checks[] | select(.status == "FAIL") | .name + ": " + .evidence] | join("\n") end' "$readiness_report")"

printf '\nAutopilot Workflow Input:\n\n'
printf 'service:\nprr-demo-checkout\n\n'
printf 'demoRunId:\n%s\n\n' "$DEMO_RUN_ID"
printf 'readinessResult:\n%s\n\n' "$readiness_result"
printf 'failedChecks:\n%s\n' "$failed_checks"
printf '\nreadinessEvidence:\n%s\n' "$readiness_evidence"
printf '\nslackDestinationId:\n%s\n' "${SLACK_DESTINATION_ID:-<set in .env>}"
printf '\nslackChannel:\n%s\n' "${SLACK_CHANNEL:-<set in .env>}"

printf '\nWorkflow Automation\n'
./scripts/start-prr-workflow.sh "$readiness_result" "$failed_checks" "$readiness_evidence"
