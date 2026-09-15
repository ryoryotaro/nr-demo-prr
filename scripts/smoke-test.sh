#!/bin/sh

set -u

checkout_url="${CHECKOUT_URL:-http://localhost:8080/checkout}"
response_file="$(mktemp)"
trap 'rm -f "$response_file"' EXIT

printf 'Smoke Test: %s に checkout リクエストを送信します\n' "$checkout_url"

status_code="$(curl --silent --show-error \
  --output "$response_file" \
  --write-out '%{http_code}' \
  --request POST \
  --header 'Content-Type: application/json' \
  --data '{"tenantId":"tenant-001","customerPlan":"enterprise","amount":12000}' \
  "$checkout_url")"
curl_result=$?

if [ "$curl_result" -ne 0 ]; then
  printf 'FAIL: checkout-service に接続できませんでした。\n' >&2
  exit 1
fi

response="$(tr -d '[:space:]' < "$response_file")"

if [ "$status_code" != "200" ]; then
  printf 'FAIL: HTTP 200 を期待しましたが、HTTP %s でした。\n' "$status_code" >&2
  printf 'Response: %s\n' "$response" >&2
  exit 1
fi

if ! printf '%s' "$response" | grep -q '"success":true'; then
  printf 'FAIL: レスポンスが checkout 成功を示していません。\n' >&2
  printf 'Response: %s\n' "$response" >&2
  exit 1
fi

if ! printf '%s' "$response" | grep -q '"status":"checkout_completed"'; then
  printf 'FAIL: checkout の完了ステータスを確認できませんでした。\n' >&2
  printf 'Response: %s\n' "$response" >&2
  exit 1
fi

if ! printf '%s' "$response" | grep -q '"paymentStatus":"paid"'; then
  printf 'FAIL: payment-service の成功結果を確認できませんでした。\n' >&2
  printf 'Response: %s\n' "$response" >&2
  exit 1
fi

printf 'PASS: checkout-service が HTTP 200 を返しました。\n'
printf 'PASS: payment-service の決済成功を確認しました。\n'
printf 'PASS: checkout が正常に完了しました。\n'
printf 'Response: %s\n' "$response"
