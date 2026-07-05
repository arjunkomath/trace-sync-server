#!/usr/bin/env bash
set -euo pipefail

IMAGE="${IMAGE:-trace-sync-server:e2e}"
CONTAINER="${CONTAINER:-trace-sync-server-e2e}"
TOKEN="${TRACE_SYNC_TOKEN:-trace-e2e-token}"

if [[ -z "${PORT:-}" ]]; then
  PORT="$(python3 - <<'PY'
import socket
with socket.socket() as s:
    s.bind(("127.0.0.1", 0))
    print(s.getsockname()[1])
PY
)"
fi

if [[ -z "${DATA_DIR:-}" ]]; then
  DATA_DIR="$(mktemp -d)"
  GENERATED_DATA_DIR=1
else
  GENERATED_DATA_DIR=0
fi

BASE_URL="http://localhost:${PORT}"

cleanup() {
  docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
  if [[ -z "${KEEP_E2E_DATA:-}" && "$GENERATED_DATA_DIR" == "1" ]]; then
    rm -rf "$DATA_DIR"
  fi
}
trap cleanup EXIT

fail() {
  echo "e2e failed: $*" >&2
  echo "\ncontainer logs:" >&2
  docker logs "$CONTAINER" >&2 || true
  exit 1
}

require() {
  command -v "$1" >/dev/null 2>&1 || fail "$1 is required"
}

http_status() {
  curl -sS -o "$1" -w '%{http_code}' "${@:3}" "$2"
}

json_assert() {
  python3 - "$1" "$2" <<'PY'
import json, sys
path, expr = sys.argv[1], sys.argv[2]
with open(path) as f:
    data = json.load(f)
if not eval(expr, {}, {"data": data}):
    raise SystemExit(f"assertion failed: {expr}; data={data!r}")
PY
}

require docker
require curl
require python3

cd "$(dirname "$0")"

echo "building $IMAGE"
docker build -t "$IMAGE" . >/dev/null

mkdir -p "$DATA_DIR"
docker rm -f "$CONTAINER" >/dev/null 2>&1 || true

echo "starting $CONTAINER on $BASE_URL"
docker run -d --name "$CONTAINER" \
  --user "$(id -u):$(id -g)" \
  -p "${PORT}:8787" \
  -v "${DATA_DIR}:/data" \
  -e "TRACE_SYNC_TOKEN=${TOKEN}" \
  "$IMAGE" >/dev/null

for _ in $(seq 1 50); do
  if curl -fsS "$BASE_URL/health" >/dev/null 2>&1; then
    break
  fi
  sleep 0.1
done
curl -fsS "$BASE_URL/health" >/dev/null || fail "server did not become healthy"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"; cleanup' EXIT

echo "checking unauthenticated read is rejected"
status="$(http_status "$TMP_DIR/unauthorized.json" "$BASE_URL/v1/settings")"
[[ "$status" == "401" ]] || fail "expected 401 without token, got $status"
json_assert "$TMP_DIR/unauthorized.json" 'data["error"] == "unauthorized"'

echo "checking empty server returns 404"
status="$(http_status "$TMP_DIR/empty.json" "$BASE_URL/v1/settings" \
  -H "Authorization: Bearer ${TOKEN}")"
[[ "$status" == "404" ]] || fail "expected 404 before first upload, got $status"
json_assert "$TMP_DIR/empty.json" 'data["error"] == "not_found"'

echo "uploading initial settings"
status="$(http_status "$TMP_DIR/upload-v1.json" "$BASE_URL/v1/settings" \
  -X PUT \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"baseVersion":0,"updatedBy":"e2e-mac-a","settings":{"quickLinks":[{"name":"Docs","url":"https://trace.techulus.xyz"}],"hotkeys":{"launcher":"option+space"}}}')"
[[ "$status" == "200" ]] || fail "expected initial upload 200, got $status"
json_assert "$TMP_DIR/upload-v1.json" 'data["version"] == 1 and data["updatedBy"] == "e2e-mac-a" and len(data["sha256"]) == 64'

echo "downloading settings"
status="$(http_status "$TMP_DIR/download-v1.json" "$BASE_URL/v1/settings" \
  -H "Authorization: Bearer ${TOKEN}")"
[[ "$status" == "200" ]] || fail "expected download 200, got $status"
json_assert "$TMP_DIR/download-v1.json" 'data["version"] == 1 and data["settings"]["hotkeys"]["launcher"] == "option+space"'

echo "checking stale upload conflicts"
status="$(http_status "$TMP_DIR/conflict.json" "$BASE_URL/v1/settings" \
  -X PUT \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"baseVersion":0,"updatedBy":"e2e-mac-b","settings":{"quickLinks":[]}}')"
[[ "$status" == "409" ]] || fail "expected conflict 409, got $status"
json_assert "$TMP_DIR/conflict.json" 'data["error"] == "conflict" and data["currentVersion"] == 1'

echo "uploading next version"
status="$(http_status "$TMP_DIR/upload-v2.json" "$BASE_URL/v1/settings" \
  -X PUT \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"baseVersion":1,"updatedBy":"e2e-mac-b","settings":{"quickLinks":[],"hotkeys":{"launcher":"command+space"}}}')"
[[ "$status" == "200" ]] || fail "expected second upload 200, got $status"
json_assert "$TMP_DIR/upload-v2.json" 'data["version"] == 2 and data["updatedBy"] == "e2e-mac-b"'

echo "checking files persisted to volume"
[[ -s "$DATA_DIR/state.json" ]] || fail "state.json was not written"
json_assert "$DATA_DIR/state.json" 'data["version"] == 2 and data["updatedBy"] == "e2e-mac-b" and data["settings"]["hotkeys"]["launcher"] == "command+space"'

echo "e2e passed"
