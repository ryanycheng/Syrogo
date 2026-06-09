#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${SYROGO_SMOKE_BASE_URL:-http://127.0.0.1:23234}"
CHAT_TOKEN="${SYROGO_OPENAI_CLIENT_TOKEN:-}"
RESPONSES_TOKEN="${SYROGO_RESPONSES_CLIENT_TOKEN:-}"
MESSAGES_TOKEN="${SYROGO_ANTHROPIC_CLIENT_TOKEN:-}"
ADMIN_TOKEN="${SYROGO_ACCOUNTING_ADMIN_TOKEN:-}"
CHAT_MODEL="${SYROGO_SMOKE_CHAT_MODEL:-gpt-4o-mini}"
RESPONSES_MODEL="${SYROGO_SMOKE_RESPONSES_MODEL:-gpt-4o-mini}"
MESSAGES_MODEL="${SYROGO_SMOKE_MESSAGES_MODEL:-claude-sonnet-4-5}"
RUN_STREAM="${SYROGO_SMOKE_STREAM:-1}"
STRICT="${SYROGO_SMOKE_STRICT:-0}"
TMP_DIR=""
RAN_PROTOCOLS=0
FAILED=0
SKIPPED=0

usage() {
  cat <<'USAGE'
Usage:
  scripts/smoke.sh

Environment:
  SYROGO_SMOKE_BASE_URL            Gateway URL, default http://127.0.0.1:23234
  SYROGO_OPENAI_CLIENT_TOKEN       Token for /v1/chat/completions
  SYROGO_RESPONSES_CLIENT_TOKEN    Token for /v1/responses
  SYROGO_ANTHROPIC_CLIENT_TOKEN    Token for /v1/messages
  SYROGO_ACCOUNTING_ADMIN_TOKEN    Optional token for /stats/usage
  SYROGO_SMOKE_CHAT_MODEL          Chat model, default gpt-4o-mini
  SYROGO_SMOKE_RESPONSES_MODEL     Responses model, default gpt-4o-mini
  SYROGO_SMOKE_MESSAGES_MODEL      Messages model, default claude-sonnet-4-5
  SYROGO_SMOKE_STREAM              Run stream checks, default 1
  SYROGO_SMOKE_STRICT              Fail when a protocol token is missing, default 0
USAGE
}

log() {
  printf '[smoke] %s\n' "$*"
}

fail() {
  printf '[smoke] FAIL: %s\n' "$*" >&2
  FAILED=1
}

skip() {
  printf '[smoke] SKIP: %s\n' "$*"
  SKIPPED=$((SKIPPED + 1))
  if [ "$STRICT" = "1" ]; then
    fail "$1"
  fi
}

cleanup() {
  if [ -n "$TMP_DIR" ] && [ -d "$TMP_DIR" ]; then
    rm -rf "$TMP_DIR"
  fi
}
trap cleanup EXIT

write_body() {
  local name="$1"
  TMP_DIR="${TMP_DIR:-$(mktemp -d)}"
  printf '%s' "$2" > "$TMP_DIR/$name.json"
  printf '%s' "$TMP_DIR/$name.json"
}

request_json() {
  local name="$1" method="$2" path="$3" token="$4" body_file="$5" auth_header="$6"
  local out status
  out="${TMP_DIR:-$(mktemp -d)}/$name.out"
  TMP_DIR="$(dirname "$out")"
  if [ -n "$body_file" ]; then
    status="$(curl -sS -o "$out" -w '%{http_code}' -X "$method" "$BASE_URL$path" \
      -H "$auth_header: $token" \
      -H 'Content-Type: application/json' \
      --data-binary "@$body_file")"
  else
    status="$(curl -sS -o "$out" -w '%{http_code}' -X "$method" "$BASE_URL$path" \
      -H "$auth_header: $token")"
  fi
  if [ "$status" -lt 200 ] || [ "$status" -ge 300 ]; then
    fail "$name returned HTTP $status: $(tr '\n' ' ' < "$out" | cut -c 1-300)"
    return
  fi
  log "PASS: $name HTTP $status"
}

request_stream() {
  local name="$1" path="$2" token="$3" body_file="$4" auth_header="$5"
  local out status
  out="${TMP_DIR:-$(mktemp -d)}/$name.out"
  TMP_DIR="$(dirname "$out")"
  status="$(curl -sS -N -o "$out" -w '%{http_code}' -X POST "$BASE_URL$path" \
    -H "$auth_header: $token" \
    -H 'Content-Type: application/json' \
    --data-binary "@$body_file")"
  if [ "$status" -lt 200 ] || [ "$status" -ge 300 ]; then
    fail "$name returned HTTP $status: $(tr '\n' ' ' < "$out" | cut -c 1-300)"
    return
  fi
  if ! grep -q '^data:\|^event:' "$out"; then
    fail "$name did not return SSE frames"
    return
  fi
  log "PASS: $name stream HTTP $status"
}

check_healthz() {
  local out status
  TMP_DIR="${TMP_DIR:-$(mktemp -d)}"
  out="$TMP_DIR/healthz.out"
  status="$(curl -sS -o "$out" -w '%{http_code}' "$BASE_URL/healthz")"
  if [ "$status" != "200" ]; then
    fail "healthz returned HTTP $status: $(tr '\n' ' ' < "$out" | cut -c 1-300)"
    return
  fi
  log "PASS: healthz HTTP 200"
}

check_chat() {
  if [ -z "$CHAT_TOKEN" ]; then
    skip 'SYROGO_OPENAI_CLIENT_TOKEN is not set; skipping /v1/chat/completions'
    return
  fi
  local body stream_body
  body="$(write_body chat "{\"model\":\"$CHAT_MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"hello from syrogo smoke\"}]}")"
  request_json openai_chat POST /v1/chat/completions "Bearer $CHAT_TOKEN" "$body" Authorization
  RAN_PROTOCOLS=$((RAN_PROTOCOLS + 1))
  if [ "$RUN_STREAM" = "1" ]; then
    stream_body="$(write_body chat_stream "{\"model\":\"$CHAT_MODEL\",\"stream\":true,\"messages\":[{\"role\":\"user\",\"content\":\"hello from syrogo smoke stream\"}]}")"
    request_stream openai_chat_stream /v1/chat/completions "Bearer $CHAT_TOKEN" "$stream_body" Authorization
  fi
}

check_responses() {
  if [ -z "$RESPONSES_TOKEN" ]; then
    skip 'SYROGO_RESPONSES_CLIENT_TOKEN is not set; skipping /v1/responses'
    return
  fi
  local body
  body="$(write_body responses "{\"model\":\"$RESPONSES_MODEL\",\"input\":\"hello from syrogo smoke\"}")"
  request_json openai_responses POST /v1/responses "Bearer $RESPONSES_TOKEN" "$body" Authorization
  RAN_PROTOCOLS=$((RAN_PROTOCOLS + 1))
}

check_messages() {
  if [ -z "$MESSAGES_TOKEN" ]; then
    skip 'SYROGO_ANTHROPIC_CLIENT_TOKEN is not set; skipping /v1/messages'
    return
  fi
  local body stream_body
  body="$(write_body messages "{\"model\":\"$MESSAGES_MODEL\",\"max_tokens\":128,\"messages\":[{\"role\":\"user\",\"content\":[{\"type\":\"text\",\"text\":\"hello from syrogo smoke\"}]}]}")"
  request_json anthropic_messages POST /v1/messages "Bearer $MESSAGES_TOKEN" "$body" Authorization
  RAN_PROTOCOLS=$((RAN_PROTOCOLS + 1))
  if [ "$RUN_STREAM" = "1" ]; then
    stream_body="$(write_body messages_stream "{\"model\":\"$MESSAGES_MODEL\",\"stream\":true,\"max_tokens\":128,\"messages\":[{\"role\":\"user\",\"content\":[{\"type\":\"text\",\"text\":\"hello from syrogo smoke stream\"}]}]}")"
    request_stream anthropic_messages_stream /v1/messages "Bearer $MESSAGES_TOKEN" "$stream_body" Authorization
  fi
}

check_usage() {
  if [ -z "$ADMIN_TOKEN" ]; then
    skip 'SYROGO_ACCOUNTING_ADMIN_TOKEN is not set; skipping /stats/usage'
    return
  fi
  request_json usage_by_key GET '/stats/usage?group_by=key' "Bearer $ADMIN_TOKEN" '' Authorization
  request_json usage_by_provider GET '/stats/usage?group_by=provider' "Bearer $ADMIN_TOKEN" '' Authorization
}

main() {
  if [ "${1:-}" = "-h" ] || [ "${1:-}" = "--help" ]; then
    usage
    exit 0
  fi

  command -v curl >/dev/null 2>&1 || { printf '[smoke] curl is required\n' >&2; exit 1; }
  log "base url: $BASE_URL"
  check_healthz
  check_chat
  check_responses
  check_messages
  check_usage

  if [ "$RAN_PROTOCOLS" -eq 0 ]; then
    fail 'no protocol checks were executed; set at least one client token environment variable'
  fi

  if [ "$FAILED" -ne 0 ]; then
    exit 1
  fi
  log "done; protocol checks: $RAN_PROTOCOLS, skipped: $SKIPPED"
}

main "$@"
