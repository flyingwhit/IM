#!/bin/bash
# Load test for IM backend using vegeta.
#
# Installation:
#   go install github.com/tsenart/vegeta@latest
#
# Usage:
#   ./scripts/loadtest.sh                          # default: 50 req/s for 30s
#   ./scripts/loadtest.sh -rate 100 -duration 60s  # 100 req/s for 60s
#   ./scripts/loadtest.sh -skip-register            # skip user registration
#
# Prerequisites:
#   - IM server running (go run ./cmd/server or docker compose up -d)
#   - vegeta in PATH

set -euo pipefail

# ─── Configuration ──────────────────────────────────────────────

BASE_URL="${BASE_URL:-http://localhost:8082}"
RATE="${RATE:-50}"           # requests per second
DURATION="${DURATION:-30s}"  # vegeta duration format

SKIP_REGISTER=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    -rate)      RATE="$2"; shift 2 ;;
    -duration)  DURATION="$2"; shift 2 ;;
    -skip-register) SKIP_REGISTER=true; shift ;;
    *) echo "unknown flag: $1"; exit 1 ;;
  esac
done

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

echo "=== IM Backend Load Test ==="
echo "Target: $BASE_URL"
echo "Rate:   $RATE req/s"
echo "Duration: $DURATION"
echo ""

# ─── Step 1: Health check (always works, no auth) ───────────────

echo "--- Health Check ---"
echo "GET $BASE_URL/health" | vegeta attack -rate=10 -duration=5s | vegeta report
echo ""

# ─── Step 2: Register test user (if auth tests enabled) ─────────

if ! $SKIP_REGISTER; then
  USERNAME="loadtest-$(date +%s)"
  echo "--- Register User ($USERNAME) ---"

  REGISTER_BODY="{\"username\":\"$USERNAME\",\"email\":\"$USERNAME@test.local\",\"password\":\"testpass123\",\"nickname\":\"LoadTest\"}"
  REGISTER_RESP=$(curl -s -X POST "$BASE_URL/api/v1/auth/register" \
    -H "Content-Type: application/json" \
    -d "$REGISTER_BODY")

  TOKEN=$(echo "$REGISTER_RESP" | jq -r '.access_token // empty' 2>/dev/null || true)

  if [ -z "$TOKEN" ]; then
    echo "Warning: Registration failed, skipping authenticated tests"
    echo "Response: $REGISTER_RESP"
  else
    echo "Got token: ${TOKEN:0:20}..."
    echo ""

    # ─── Step 3: Authenticated endpoints ────────────────────────

    echo "--- Get Profile (GET /api/v1/users/me) ---"
    echo "GET $BASE_URL/api/v1/users/me
Authorization: Bearer $TOKEN" | vegeta attack -rate="$RATE" -duration="$DURATION" | vegeta report

    echo ""
    echo "--- List Friends (GET /api/v1/friends) ---"
    echo "GET $BASE_URL/api/v1/friends
Authorization: Bearer $TOKEN" | vegeta attack -rate="$RATE" -duration="$DURATION" | vegeta report

    echo ""
    echo "--- List Groups (GET /api/v1/groups) ---"
    echo "GET $BASE_URL/api/v1/groups
Authorization: Bearer $TOKEN" | vegeta attack -rate="$RATE" -duration="$DURATION" | vegeta report
  fi
fi

echo ""
echo "=== Done ==="
