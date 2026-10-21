#!/usr/bin/env bash
set -euo pipefail

SHORTENER_URL=${SHORTENER_URL:-http://localhost:8080}
ANALYTICS_URL=${ANALYTICS_URL:-http://localhost:8081}
TARGET=https://example.com/smoke
CODE="smoke-$(date +%s)-$RANDOM"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

echo "creating $CODE"
created=$(curl -fsS -X POST "$SHORTENER_URL/api/links" \
  -H 'Content-Type: application/json' \
  -d "{\"url\":\"$TARGET\",\"code\":\"$CODE\"}")
[ "$(jq -r .code <<<"$created")" = "$CODE" ] || fail "unexpected create response: $created"

status=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$SHORTENER_URL/api/links" \
  -d "{\"url\":\"$TARGET\",\"code\":\"$CODE\"}")
[ "$status" = 409 ] || fail "duplicate code returned $status"

for _ in 1 2 3; do
  location=$(curl -s -o /dev/null -w '%{redirect_url}' -H 'Referer: https://github.com/piliotov' "$SHORTENER_URL/$CODE")
  [ "$location" = "$TARGET" ] || fail "redirect went to '$location'"
done

status=$(curl -s -o /dev/null -w '%{http_code}' "$SHORTENER_URL/does-not-exist")
[ "$status" = 404 ] || fail "missing code returned $status"

echo "waiting for analytics"
for _ in $(seq 1 30); do
  stats=$(curl -fsS "$ANALYTICS_URL/api/stats/$CODE")
  total=$(jq -r .total <<<"$stats")
  if [ "$total" -ge 3 ]; then
    ref=$(jq -r '.top_referrers[0].host' <<<"$stats")
    [ "$ref" = github.com ] || fail "unexpected top referrer $ref"
    echo "ok: $stats"
    exit 0
  fi
  sleep 1
done
fail "analytics total stayed at $total"
