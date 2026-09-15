#!/bin/sh

set -eu

repo_dir="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
readiness_report="$(mktemp)"
trap 'rm -f "$readiness_report"' EXIT HUP INT TERM

export READINESS_JSON_OUTPUT="$readiness_report"
export PRR_ACCEPT_ANY_RESULT=1

cd "$repo_dir"
if ! ./scripts/run-change-demo.sh complete; then
  printf '[ERROR] PRR execution failed before a gate result was available.\n' >&2
  exit 2
fi

if ! jq -e '.result == "READY" or .result == "NOT READY"' "$readiness_report" >/dev/null; then
  printf '[ERROR] Readiness report is missing or invalid.\n' >&2
  exit 2
fi

readiness_result="$(jq -r '.result' "$readiness_report")"
failed_checks="$(jq -r '[.checks[] | select(.status == "FAIL") | .name] | if length == 0 then "none" else join(", ") end' "$readiness_report")"

if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
  {
    printf '# Production Readiness Review\n\n'
    printf -- '- Service: `prr-demo-checkout`\n'
    printf -- '- PR: `%s`\n' "${PR_NUMBER:-unknown}"
    printf -- '- Commit: `%s`\n' "${PR_HEAD_SHA:-$(git rev-parse HEAD)}"
    printf -- '- Functional Test: `PASS`\n'
    printf -- '- PRR Result: `%s`\n' "$readiness_result"
    printf -- '- Failed checks: `%s`\n' "$failed_checks"
    printf -- '- Workflow Automation: `STARTED`\n'
    printf -- '- Slack notification: delegated to Workflow Automation\n'
  } >>"$GITHUB_STEP_SUMMARY"
fi

if [ "$readiness_result" = "READY" ]; then
  printf '\n[PASS] Pull Request PRR Gate: READY\n'
  exit 0
fi

printf '\n[FAIL] Pull Request PRR Gate: NOT READY\n' >&2
printf 'Failed checks: %s\n' "$failed_checks" >&2
exit 1
