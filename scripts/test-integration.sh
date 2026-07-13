#!/usr/bin/env bash
# Integration smoke test for ag.
# Requires: ANTHROPIC_AUTH_TOKEN or ANTHROPIC_API_KEY set (in .env or environment).
# Usage: make test-integration
set -euo pipefail

AG="$(cd "$(dirname "$0")/.." && pwd)/ag"
AGDIR="$(dirname "$AG")"
PASS=0
FAIL=0

# Source .env from ag project root if present.
if [[ -f "$AGDIR/.env" ]]; then
  set -a
  # shellcheck source=/dev/null
  source "$AGDIR/.env"
  set +a
fi

# Load credentials from example ag.yaml files for any vars not already set.
# Try multiple sources in order until we find a non-empty auth_token.
for _yaml in \
    "$AGDIR/examples/photos/ag.yaml" \
    "$AGDIR/examples/mmkr/ag.yaml"; do
  [[ -f "$_yaml" ]] || continue
  _token=$(python3 -c "import yaml; d=yaml.safe_load(open('$_yaml')); print(d.get('auth_token',''))" 2>/dev/null || true)
  _model=$(python3 -c "import yaml; d=yaml.safe_load(open('$_yaml')); print(d.get('model',''))" 2>/dev/null || true)
  _url=$(python3 -c "import yaml; d=yaml.safe_load(open('$_yaml')); print(d.get('base_url',''))" 2>/dev/null || true)
  [[ -z "${ANTHROPIC_AUTH_TOKEN:-}" && -n "$_token" ]] && export ANTHROPIC_AUTH_TOKEN="$_token"
  [[ -z "${ANTHROPIC_MODEL:-}"      && -n "$_model" ]] && export ANTHROPIC_MODEL="$_model"
  [[ -z "${ANTHROPIC_BASE_URL:-}"   && -n "$_url"   ]] && export ANTHROPIC_BASE_URL="$_url"
  [[ -n "${ANTHROPIC_AUTH_TOKEN:-}" ]] && break
done

# Resolve model: prefer env var, fall back to a safe default.
LIVE_MODEL="${ANTHROPIC_MODEL:-anthropic/claude-sonnet-4-5}"

pass() { echo "  PASS: $1"; PASS=$((PASS + 1)); }
fail() { echo "  FAIL: $1"; FAIL=$((FAIL + 1)); }

# Build first if binary missing
if [[ ! -x "$AG" ]]; then
  echo "Building ag..."
  make -C "$AGDIR" build
fi

echo ""
echo "=== ag integration tests ==="
echo ""

# --- Test 1: ag with no args and no ag.yaml shows help ---
echo "1. No ag.yaml — shows help"
TMPDIR=$(mktemp -d)
OUTPUT=$(cd "$TMPDIR" && "$AG" 2>&1 || true)
if echo "$OUTPUT" | grep -q "self-evolving"; then
  pass "ag with no args shows help"
else
  fail "ag with no args: unexpected output: $OUTPUT"
fi
rm -rf "$TMPDIR"

# --- Test 2: ag skills with empty skills ---
echo "2. ag skills (empty)"
TMPDIR=$(mktemp -d)
cat > "$TMPDIR/ag.yaml" << 'EOF'
model: anthropic/claude-sonnet-4-5
tools: []
skills: []
EOF
OUTPUT=$(cd "$TMPDIR" && "$AG" skills 2>&1)
if echo "$OUTPUT" | grep -q "No skills learned yet"; then
  pass "ag skills shows empty message"
else
  fail "ag skills empty: unexpected output: $OUTPUT"
fi
rm -rf "$TMPDIR"

# --- Test 3: ag skills with populated skills ---
echo "3. ag skills (populated)"
TMPDIR=$(mktemp -d)
cat > "$TMPDIR/ag.yaml" << 'EOF'
model: anthropic/claude-sonnet-4-5
tools: []
skills:
  - name: test-skill
    description: A test skill
    type: context
    created_at: "2026-07-11T00:00:00Z"
    updated_at: "2026-07-11T00:00:00Z"
EOF
OUTPUT=$(cd "$TMPDIR" && "$AG" skills 2>&1)
if echo "$OUTPUT" | grep -q "test-skill"; then
  pass "ag skills shows skill name"
else
  fail "ag skills populated: unexpected output: $OUTPUT"
fi
rm -rf "$TMPDIR"

# --- Test 4: bootstrap with no tools with bin ---
echo "4. ag bootstrap (no tools)"
TMPDIR=$(mktemp -d)
cat > "$TMPDIR/ag.yaml" << 'EOF'
model: anthropic/claude-sonnet-4-5
tools: []
skills: []
EOF
OUTPUT=$(cd "$TMPDIR" && "$AG" bootstrap 2>&1)
if echo "$OUTPUT" | grep -q "No tools"; then
  pass "ag bootstrap no tools prints helpful message"
else
  fail "ag bootstrap no tools: unexpected output: $OUTPUT"
fi
rm -rf "$TMPDIR"

# --- Test 5: one-shot live (requires API key) ---
echo "5. One-shot live (requires API key)"
if [[ -z "${ANTHROPIC_AUTH_TOKEN:-}" && -z "${ANTHROPIC_API_KEY:-}" ]]; then
  echo "  SKIP: no API key set (set ANTHROPIC_AUTH_TOKEN to run live tests)"
else
  TMPDIR=$(mktemp -d)
  cat > "$TMPDIR/ag.yaml" << EOF
model: $LIVE_MODEL
auth_token: ${ANTHROPIC_AUTH_TOKEN:-}
base_url: ${ANTHROPIC_BASE_URL:-}
tools:
  - name: echo
    bin: echo
    description: Echo text to stdout
    parameters:
      type: object
      properties:
        args:
          type: string
          description: Text to echo
skills: []
EOF
  OUTPUT=$(cd "$TMPDIR" && "$AG" "echo hello from ag test" 2>/dev/null)
  if echo "$OUTPUT" | grep -qi "hello"; then
    pass "one-shot live: agent responded"
  else
    fail "one-shot live: unexpected output: $OUTPUT"
  fi
  rm -rf "$TMPDIR"
fi

# --- Test 6: bootstrap enriches tool spec (live) ---
echo "6. Bootstrap enriches ag.yaml (requires API key)"
if [[ -z "${ANTHROPIC_AUTH_TOKEN:-}" && -z "${ANTHROPIC_API_KEY:-}" ]]; then
  echo "  SKIP: no API key set"
else
  TMPDIR=$(mktemp -d)
  cat > "$TMPDIR/ag.yaml" << EOF
model: $LIVE_MODEL
auth_token: ${ANTHROPIC_AUTH_TOKEN:-}
base_url: ${ANTHROPIC_BASE_URL:-}
tools:
  - name: echo
    bin: echo
    hint: echoes text to stdout
skills: []
EOF
  cd "$TMPDIR" && "$AG" bootstrap > /dev/null 2>&1 || true
  DESC=$(python3 -c "import yaml; d=yaml.safe_load(open('ag.yaml')); print(d['tools'][0].get('description',''))" 2>/dev/null || echo "")
  if [[ -n "$DESC" ]]; then
    pass "bootstrap enriched tool spec (description: ${DESC:0:40}...)"
  else
    fail "bootstrap: tool description not written to ag.yaml"
  fi
  rm -rf "$TMPDIR"
fi

# --- Test 7: web tool YAML roundtrip (unit test) ---
echo "7. Web tool YAML roundtrip"
unset TMPDIR
if (cd "$AGDIR" && go test ./config/... -run TestToolSpecWebRoundtrip -count=1 2>&1); then
  pass "web tool config roundtrip"
else
  fail "web tool config roundtrip"
fi

# --- Test 8: ag help includes 'add web' ---
echo "8. ag help includes 'add web'"
OUTPUT=$("$AG" help 2>&1 || true)
if echo "$OUTPUT" | grep -q "add web"; then
  pass "ag help shows 'add web'"
else
  fail "ag help missing 'add web'"
fi

echo ""
echo "=== Results: $PASS passed, $FAIL failed ==="
echo ""
[[ $FAIL -eq 0 ]]
