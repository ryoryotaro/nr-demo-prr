#!/bin/sh

set -eu

readiness_result="${1:-}"
failed_checks="${2:-}"
readiness_evidence="${3:-}"

case "$readiness_result" in
  READY|"NOT READY") ;;
  *)
    printf '[ERROR] Readiness result must be READY or NOT READY.\n' >&2
    exit 2
    ;;
esac

if [ -z "$failed_checks" ]; then
  printf '[ERROR] Failed checks are required.\n' >&2
  exit 2
fi
if [ -z "$readiness_evidence" ]; then
  printf '[ERROR] Readiness evidence is required.\n' >&2
  exit 2
fi

for command_name in curl jq; do
  if ! command -v "$command_name" >/dev/null 2>&1; then
    printf '[ERROR] Required command is not available: %s\n' "$command_name" >&2
    exit 2
  fi
done

if command -v uuidgen >/dev/null 2>&1; then
  idempotency_key="$(uuidgen | tr '[:upper:]' '[:lower:]')"
elif [ -r /proc/sys/kernel/random/uuid ]; then
  idempotency_key="$(tr '[:upper:]' '[:lower:]' </proc/sys/kernel/random/uuid)"
else
  printf '[ERROR] A UUID generator is required for the Workflow idempotency key.\n' >&2
  exit 2
fi

for variable_name in DEMO_RUN_ID NEW_RELIC_USER_KEY NEW_RELIC_ACCOUNT_ID NEW_RELIC_NERDGRAPH_ENDPOINT SLACK_DESTINATION_ID SLACK_CHANNEL; do
  eval "variable_value=\${$variable_name:-}"
  if [ -z "$variable_value" ]; then
    printf '[ERROR] Required environment variable is not set: %s\n' "$variable_name" >&2
    exit 2
  fi
done

case "$NEW_RELIC_ACCOUNT_ID" in
  ''|*[!0-9]*)
    printf '[ERROR] NEW_RELIC_ACCOUNT_ID must be a positive integer.\n' >&2
    exit 2
    ;;
esac

workflow_inputs="$(jq -n \
  --arg service 'prr-demo-checkout' \
  --arg demo_run_id "$DEMO_RUN_ID" \
  --arg readiness_result "$readiness_result" \
  --arg failed_checks "$failed_checks" \
  --arg readiness_evidence "$readiness_evidence" \
  --arg destination_id "$SLACK_DESTINATION_ID" \
  --arg channel "$SLACK_CHANNEL" \
  '[
    {key:"service",value:$service},
    {key:"demoRunId",value:$demo_run_id},
    {key:"readinessResult",value:$readiness_result},
    {key:"failedChecks",value:$failed_checks},
    {key:"readinessEvidence",value:$readiness_evidence},
    {key:"slackDestinationId",value:$destination_id},
    {key:"slackChannel",value:$channel}
  ]')"

query='mutation StartPRRWorkflow($accountId: String!, $idempotencyKey: ID!, $workflowInputs: [WorkflowAutomationWorkflowRunInput!]) {
  workflowAutomationStartWorkflowRun(
    scope: {id: $accountId, type: ACCOUNT}
    definition: {name: "prr-autopilot"}
    workflowInputs: $workflowInputs
    idempotencyKey: $idempotencyKey
    options: {logLevel: INFO}
  ) {
    runId
  }
}'

payload="$(jq -n \
  --arg query "$query" \
  --arg account_id "$NEW_RELIC_ACCOUNT_ID" \
  --arg idempotency_key "$idempotency_key" \
  --argjson workflow_inputs "$workflow_inputs" \
  '{query:$query,variables:{accountId:$account_id,idempotencyKey:$idempotency_key,workflowInputs:$workflow_inputs}}')"

response_file="$(mktemp)"
trap 'rm -f "$response_file"' EXIT HUP INT TERM

if ! http_status="$(curl --silent --show-error \
  --output "$response_file" \
  --write-out '%{http_code}' \
  --request POST "$NEW_RELIC_NERDGRAPH_ENDPOINT" \
  --header 'Content-Type: application/json' \
  --header "API-Key: $NEW_RELIC_USER_KEY" \
  --data "$payload")"; then
  printf '[ERROR] Workflow Automation Start API request failed.\n' >&2
  exit 2
fi

if [ "$http_status" -lt 200 ] || [ "$http_status" -ge 300 ]; then
  printf '[ERROR] Workflow Automation Start API returned HTTP %s.\n' "$http_status" >&2
  exit 2
fi

if jq -e '.errors != null and (.errors | length > 0)' "$response_file" >/dev/null; then
  printf '[ERROR] Workflow Automation Start API returned GraphQL errors.\n' >&2
  jq -r '.errors[] | "- " + (.message // "Unknown GraphQL error")' "$response_file" >&2
  exit 2
fi

workflow_run_id="$(jq -r '.data.workflowAutomationStartWorkflowRun.runId // empty' "$response_file")"
if [ -z "$workflow_run_id" ]; then
  printf '[ERROR] Workflow Automation Start API did not return a run ID.\n' >&2
  exit 2
fi

printf '[PASS] Workflow Automation started\n'
printf 'Workflow Run ID: %s\n' "$workflow_run_id"
