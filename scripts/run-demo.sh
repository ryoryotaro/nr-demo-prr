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
initial_wait="${NEW_RELIC_INGEST_WAIT_SECONDS:-10}"
retry_wait="${NEW_RELIC_INGEST_RETRY_SECONDS:-5}"
max_attempts="${NEW_RELIC_INGEST_ATTEMPTS:-25}"

case "$initial_wait:$retry_wait:$max_attempts" in
  *[!0-9:]*|:*|*::*|*:) printf 'Ingest wait settings must be non-negative integers.\n' >&2; exit 2 ;;
esac

export OBSERVABILITY_MODE="$mode"
export DEMO_RUN_ID="$demo_run_id"

printf '=== PRR Demo ===\n\n'
printf 'Mode: %s\n' "$mode"
printf 'Run ID: %s\n\n' "$DEMO_RUN_ID"

cd "$repo_dir"
docker compose build payment-service
docker compose build checkout-service
docker compose up --force-recreate -d

printf 'Functional Test\n'
./scripts/smoke-test.sh
printf '\nFunctional Test: PASS\n\n'

printf 'Waiting %s seconds for New Relic ingest...\n' "$initial_wait"
sleep "$initial_wait"

attempt=1
readiness_output=""
readiness_status=2
while [ "$attempt" -le "$max_attempts" ]; do
  if ! ./scripts/smoke-test.sh >/dev/null 2>&1; then
    printf '\nFunctional Test failed while waiting for telemetry.\n' >&2
    exit 2
  fi

  set +e
  if [ -n "${READINESS_JSON_OUTPUT:-}" ]; then
    readiness_output="$(./scripts/check-readiness.sh --json-output "$READINESS_JSON_OUTPUT" 2>&1)"
  else
    readiness_output="$(./scripts/check-readiness.sh 2>&1)"
  fi
  readiness_status=$?
  set -e

  if [ "$readiness_status" -eq 2 ]; then
    break
  fi
  if [ "$mode" = "complete" ] && [ "$readiness_status" -eq 0 ]; then
    break
  fi
  if { [ "$mode" = "incomplete" ] || [ "$mode" = "regression" ]; } && [ "$readiness_status" -eq 1 ] &&
     printf '%s' "$readiness_output" | grep -q '\[PASS\] tenant.id' &&
     printf '%s' "$readiness_output" | grep -q '\[FAIL\] customer.plan' &&
     printf '%s' "$readiness_output" | grep -q '\[FAIL\] prr-demo-payment'; then
    break
  fi

  attempt=$((attempt + 1))
  if [ "$attempt" -le "$max_attempts" ]; then
    sleep "$retry_wait"
  fi
done

printf '\n%s\n' "$readiness_output"

if [ "$readiness_status" -eq 2 ]; then
  printf '\nDemo stopped: readiness check returned ERROR.\n' >&2
  exit 2
fi

if [ "$mode" = "complete" ] && [ "$readiness_status" -eq 0 ]; then
  printf '\nEXPECTED RESULT: READY\n'
  exit 0
fi

if { [ "$mode" = "incomplete" ] || [ "$mode" = "regression" ]; } && [ "$readiness_status" -eq 1 ] &&
   printf '%s' "$readiness_output" | grep -q '\[PASS\] tenant.id' &&
   printf '%s' "$readiness_output" | grep -q '\[FAIL\] customer.plan' &&
   printf '%s' "$readiness_output" | grep -q '\[FAIL\] prr-demo-payment'; then
  printf '\nEXPECTED RESULT: NOT READY\n'
  exit 0
fi

printf '\nUNEXPECTED RESULT for %s mode\n' "$mode" >&2
exit 1
