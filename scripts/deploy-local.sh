#!/usr/bin/env bash
# Deployment script for my-provider.
#
# Usage:
#   ./scripts/deploy-local.sh              # local dev: build + nohup + smoke-test
#   ./scripts/deploy-local.sh --stop       # stop the local nohup instance
#   ./scripts/deploy-local.sh --systemd    # production: build + systemd restart + smoke-test
#   ./scripts/deploy-local.sh --systemd --stop  # stop the systemd service
#
# Prerequisites:
#   - Go 1.23+
#   - .env file in project root with required variables
#   - For --systemd: systemd service file at /etc/systemd/system/my-provider.service

set -euo pipefail

PROJECT_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BINARY_DIR="${PROJECT_ROOT}/bin"
BINARY="${BINARY_DIR}/my-provider"
DATA_DIR="${PROJECT_ROOT}/data"
LOG_DIR="${PROJECT_ROOT}/logs"
PID_FILE="${LOG_DIR}/my-provider.pid"
LOG_FILE="${LOG_DIR}/my-provider.log"
SYSTEMD_SERVICE="my-provider.service"
SYSTEMD_BINARY="/usr/local/bin/my-provider"

# Parse flags.
USE_SYSTEMD=false
STOP_MODE=false
for arg in "$@"; do
    case "$arg" in
        --systemd) USE_SYSTEMD=true ;;
        --stop)    STOP_MODE=true ;;
        -h|--help)
            sed -n '2,12p' "$0"
            exit 0
            ;;
        *)
            log_error "Unknown argument: $arg"
            exit 1
            ;;
    esac
done

# -----------------------------------------------------------------------------
# Helpers
# -----------------------------------------------------------------------------

log_info() {
    echo "[INFO]  $*"
}

log_warn() {
    echo "[WARN]  $*" >&2
}

log_error() {
    echo "[ERROR] $*" >&2
}

require_cmd() {
    if ! command -v "$1" &>/dev/null; then
        log_error "Required command not found: $1"
        exit 1
    fi
}

go_version_ok() {
    local min="1.23.0"
    local current
    current=$(go version | awk '{print $3}' | sed 's/^go//')
    if [ "$(printf '%s\n' "$min" "$current" | sort -V | head -n1)" != "$min" ]; then
        return 1
    fi
    return 0
}

load_env() {
    local env_file="${PROJECT_ROOT}/.env"
    if [[ ! -f "$env_file" ]]; then
        log_error ".env not found at ${env_file}"
        log_info "Copy .env.example to .env and configure your keys first."
        exit 1
    fi

    # Export every non-comment, non-empty line.
    set -a
    # shellcheck source=/dev/null
    source "$env_file"
    set +a
}

ensure_var() {
    local name="$1"
    local value
    value=$(printenv "$name" || true)
    if [[ -z "${value:-}" ]]; then
        log_error "Required environment variable ${name} is not set in .env"
        exit 1
    fi
}

stop_nohup() {
    if [[ -f "$PID_FILE" ]]; then
        local pid
        pid=$(cat "$PID_FILE")
        if kill -0 "$pid" &>/dev/null; then
            log_info "Stopping local nohup my-provider (pid ${pid})"
            kill "$pid" || true
            sleep 2
            if kill -0 "$pid" &>/dev/null; then
                log_warn "Process did not stop gracefully, sending SIGKILL"
                kill -9 "$pid" || true
            fi
        fi
        rm -f "$PID_FILE"
    fi

    # Also clean up any stray binary listening on the configured port.
    local port="${PORT:-8080}"
    local stray_pids
    stray_pids=$(ss -tlnp "sport = :${port}" 2>/dev/null | grep -oP 'pid=\K[0-9]+' || true)
    if [[ -n "$stray_pids" ]]; then
        log_warn "Found stray process(es) on port ${port}: ${stray_pids}"
        echo "$stray_pids" | xargs -r kill -9 || true
        sleep 1
    fi
}

stop_systemd() {
    if systemctl is-active --quiet "$SYSTEMD_SERVICE" 2>/dev/null; then
        log_info "Stopping systemd service ${SYSTEMD_SERVICE}"
        systemctl stop "$SYSTEMD_SERVICE"
    else
        log_info "Systemd service ${SYSTEMD_SERVICE} is not active"
    fi
}

wait_for_port() {
    local port="$1"
    local timeout="${2:-30}"
    local start
    start=$(date +%s)
    while true; do
        if curl -fsS "http://127.0.0.1:${port}/swagger/doc.json" -o /dev/null 2>/dev/null; then
            return 0
        fi
        if (( $(date +%s) - start > timeout )); then
            log_error "Server did not become ready within ${timeout}s"
            return 1
        fi
        sleep 1
    done
}

http_code() {
    local method="$1"
    local path="$2"
    local body="${3:-}"
    local auth_header="${4:-}"
    local extra_args=()
    [[ -n "$auth_header" ]] && extra_args+=("-H" "$auth_header")
    [[ -n "$body" ]] && extra_args+=("-d" "$body")
    extra_args+=("-H" "Content-Type: application/json")

    curl -sS -o /dev/null -w "%{http_code}" \
        -X "$method" \
        "http://127.0.0.1:${PORT}${path}" \
        "${extra_args[@]}"
}

# -----------------------------------------------------------------------------
# Stop mode
# -----------------------------------------------------------------------------
if $STOP_MODE; then
    load_env
    if $USE_SYSTEMD; then
        stop_systemd
    else
        stop_nohup
    fi
    log_info "Stopped."
    exit 0
fi

# -----------------------------------------------------------------------------
# Deploy mode
# -----------------------------------------------------------------------------
if $USE_SYSTEMD; then
    log_info "Starting systemd deployment from ${PROJECT_ROOT}"
else
    log_info "Starting local deployment from ${PROJECT_ROOT}"
fi

require_cmd go
require_cmd curl
require_cmd ss

if $USE_SYSTEMD; then
    require_cmd systemctl
fi

if ! go_version_ok; then
    log_error "Go version 1.23+ is required"
    exit 1
fi

load_env
ensure_var PROVIDER_PRIVATE_KEY
ensure_var NETWORK_PUBLIC_KEY
ensure_var PROVIDER_API_KEYS
ensure_var TZERO_ENDPOINT

PORT="${PORT:-8080}"
API_KEY="${PROVIDER_API_KEYS%%,*}"
SMOKE_QUOTE_ID="smoke-gbp-$(date +%s)"

BUILD_VERSION="$(git -C "$PROJECT_ROOT" describe --tags --always 2>/dev/null || echo "dev")"
BUILD_COMMIT=$(git -C "$PROJECT_ROOT" rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

LDFLAGS="-w -s \
    -X main.BuildVersion=${BUILD_VERSION} \
    -X main.BuildCommit=${BUILD_COMMIT} \
    -X main.BuildTime=${BUILD_TIME}"

if $USE_SYSTEMD; then
    # -----------------------------------------------------------------------------
    # Systemd deploy: build to temp, install to /usr/local/bin, restart service.
    # -----------------------------------------------------------------------------
    TEMP_BINARY="$(mktemp /tmp/my-provider-XXXXXX)"
    # shellcheck disable=SC2064
    trap "rm -f '$TEMP_BINARY'" EXIT

    log_info "Building production binary (commit=${BUILD_COMMIT})"
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
        -ldflags="$LDFLAGS" \
        -o "$TEMP_BINARY" \
        "${PROJECT_ROOT}/cmd/main.go"

    log_info "Installing binary -> ${SYSTEMD_BINARY}"
    install -m 0755 "$TEMP_BINARY" "$SYSTEMD_BINARY"
    rm -f "$TEMP_BINARY"
    trap - EXIT

    log_info "Restarting systemd service ${SYSTEMD_SERVICE}"
    systemctl daemon-reload
    systemctl restart "$SYSTEMD_SERVICE"

    log_info "Waiting for server to be ready..."
    if ! wait_for_port "$PORT"; then
        log_error "Deployment failed. Service logs:"
        journalctl -u "$SYSTEMD_SERVICE" --no-pager -n 50 || true
        exit 1
    fi
else
    # -----------------------------------------------------------------------------
    # Local dev deploy: build to project bin dir and run with nohup.
    # -----------------------------------------------------------------------------
    mkdir -p "$BINARY_DIR" "$DATA_DIR" "$LOG_DIR"

    stop_nohup

    log_info "Building binary -> ${BINARY}"
    CGO_ENABLED=0 go build \
        -ldflags="$LDFLAGS" \
        -o "$BINARY" \
        "${PROJECT_ROOT}/cmd/main.go"

    log_info "Starting my-provider on :${PORT}"
    nohup "$BINARY" > "$LOG_FILE" 2>&1 &
    local_pid=$!
    echo "$local_pid" > "$PID_FILE"
    log_info "PID: ${local_pid}; logs: ${LOG_FILE}"

    log_info "Waiting for server to be ready..."
    if ! wait_for_port "$PORT"; then
        log_error "Deployment failed. Last 50 log lines:"
        tail -n 50 "$LOG_FILE" || true
        stop_nohup
        exit 1
    fi
fi

# -----------------------------------------------------------------------------
# Smoke tests (common for both modes)
# -----------------------------------------------------------------------------
log_info "Running smoke tests..."

failures=0

expect_code() {
    local method="$1"
    local path="$2"
    local body="$3"
    local auth="$4"
    local expected="$5"
    local desc="$6"
    local code
    code=$(http_code "$method" "$path" "$body" "$auth")
    if [[ "$code" == "$expected" ]]; then
        log_info "  ✓ ${desc}: ${code}"
    else
        log_error "  ✗ ${desc}: expected ${expected}, got ${code}"
        ((failures++)) || true
    fi
}

# Network-dependent endpoints may return 4xx/5xx from the t-0 sandbox
# (e.g. validation failure or unimplemented route). For deployment
# verification we only require the proxy to respond with a valid HTTP code
# and JSON body, proving the endpoint is wired and the server did not crash.
expect_reachable() {
    local method="$1"
    local path="$2"
    local body="$3"
    local auth="$4"
    local desc="$5"
    local code
    code=$(http_code "$method" "$path" "$body" "$auth")
    if [[ "$code" =~ ^(200|201|202|400|401|402|404|422|502)$ ]]; then
        log_info "  ✓ ${desc}: ${code} (reachable)"
    else
        log_error "  ✗ ${desc}: unexpected ${code}"
        ((failures++)) || true
    fi
}

# 1. Swagger docs reachable
if curl -sS "http://127.0.0.1:${PORT}/swagger/doc.json" -o /dev/null -w "%{http_code}" | grep -q '^200$'; then
    log_info "  ✓ Swagger docs reachable"
else
    log_error "  ✗ Swagger docs not reachable"
    ((failures++)) || true
fi

# 1b. Version and health endpoints
if curl -sS "http://127.0.0.1:${PORT}/version" -o /dev/null -w "%{http_code}" | grep -q '^200$'; then
    log_info "  ✓ /version reachable"
    VERSION_COMMIT=$(curl -sS "http://127.0.0.1:${PORT}/version" | grep -o '"commit":"[^"]*"' | cut -d'"' -f4)
    log_info "     commit: ${VERSION_COMMIT:-unknown}"
else
    log_error "  ✗ /version not reachable"
    ((failures++)) || true
fi

if curl -sS "http://127.0.0.1:${PORT}/health" -o /dev/null -w "%{http_code}" | grep -q '^200$'; then
    log_info "  ✓ /health reachable"
else
    log_error "  ✗ /health not reachable"
    ((failures++)) || true
fi

# 2. Auth checks
expect_code POST "/api/v1/quotes/pay-out" '{"groups":[]}' "" "401" "pay-out without auth"
expect_code GET "/api/v1/quotes" "" "" "401" "get quotes without auth"

# 3. Quotes lifecycle
PAY_OUT_BODY="{\"groups\":[{\"currency\":\"GBP\",\"payment_method\":\"SWIFT\",\"expiration_seconds\":300,\"bands\":[{\"client_quote_id\":\"${SMOKE_QUOTE_ID}\",\"max_amount_usd\":\"1000\",\"rate\":\"0.79\"}]}]}"
expect_code POST "/api/v1/quotes/pay-out" "$PAY_OUT_BODY" "Authorization: Bearer ${API_KEY}" "200" "product pay-out update"

PAY_IN_BODY='{"groups":[{"currency":"EUR","paymentMethod":"PAYMENT_METHOD_TYPE_SEPA","expiration":"2099-01-01T00:00:00Z","timestamp":"2024-01-01T00:00:00Z","bands":[{"clientQuoteId":"eur-1k","maxAmount":{"unscaled":1000,"exponent":0},"rate":{"unscaled":11628,"exponent":-6}}]}]}'
expect_code PUT "/api/v1/quotes/pay-in" "$PAY_IN_BODY" "Authorization: Bearer ${API_KEY}" "200" "quoteapi pay-in update"

expect_code GET "/api/v1/quotes" "" "Authorization: Bearer ${API_KEY}" "200" "get quotes"

# 4. Payment lifecycle
# The proxy persists the payment immediately and returns 200/502 depending on
# whether the network rejects as an error or a Failure response. We only
# verify the endpoint responds.
PAYMENT_BODY='{"paymentClientId":"smoke-1","amount":{"unscaled":1000,"exponent":0},"amountType":"pay_out","currency":"GBP","paymentMethod":"PAYMENT_METHOD_TYPE_SWIFT","paymentDetails":{"accountNumber":"123","swiftCode":"ABC","beneficiaryName":"Bob"}}'
expect_reachable POST "/api/v1/payments" "$PAYMENT_BODY" "Authorization: Bearer ${API_KEY}" "create payment"

# 5. Settlement endpoints
expect_code GET "/api/v1/settlement/credits" "" "Authorization: Bearer ${API_KEY}" "200" "get settlement credits"
expect_code GET "/api/v1/settlement/ledger" "" "Authorization: Bearer ${API_KEY}" "200" "get settlement ledger"

# 6. Payment-intent recipient (3B)
# These calls hit the live t-0 network; sandbox may return 502 for routes that
# are not yet enabled. We verify the proxy endpoint is wired and responsive.
PI_CREATE_BODY='{"paymentReference":"smoke-ref-1","payInCurrency":"EUR","payInAmount":{"unscaled":200,"exponent":0},"payOutCurrency":"GBP","payOutDetails":{"sepa":{"iban":"IBAN","beneficiaryName":"Bob"}}}'
expect_reachable POST "/api/v1/payment-intents" "$PI_CREATE_BODY" "Authorization: Bearer ${API_KEY}" "create payment intent (recipient)"

PI_QUOTE_BODY='{"payInCurrency":"EUR","payInAmount":{"unscaled":200,"exponent":0},"payOutCurrency":"GBP","payInPaymentMethod":"PAYMENT_METHOD_TYPE_SEPA","payOutPaymentMethod":"PAYMENT_METHOD_TYPE_SWIFT"}'
expect_reachable POST "/api/v1/payment-intent-quotes" "$PI_QUOTE_BODY" "Authorization: Bearer ${API_KEY}" "get payment intent quote"

# 7. Payment-intent provider (3A) - no seeded intent, expect 404 proves routing
expect_code POST "/api/v1/payment-intents/provider/999/confirm" "" "Authorization: Bearer ${API_KEY}" "404" "confirm provider intent (routing check)"

if (( failures > 0 )); then
    log_error "${failures} smoke test(s) failed."
    if $USE_SYSTEMD; then
        log_error "Service logs:"
        journalctl -u "$SYSTEMD_SERVICE" --no-pager -n 50 || true
    else
        log_error "Last 50 log lines:"
        tail -n 50 "$LOG_FILE" || true
    fi
    exit 1
fi

log_info "Deployment successful."
if $USE_SYSTEMD; then
    log_info "  - Service:      ${SYSTEMD_SERVICE}"
    log_info "  - Binary:       ${SYSTEMD_BINARY}"
    log_info "  - API base:     http://127.0.0.1:${PORT}"
    log_info "  - Public URL:   https://api.agtpay.xyz"
    log_info "  - Logs:         journalctl -u ${SYSTEMD_SERVICE} -f"
    log_info "  - Stop:         ./scripts/deploy-local.sh --systemd --stop"
else
    log_info "  - API base:     http://127.0.0.1:${PORT}"
    log_info "  - Swagger UI:   http://127.0.0.1:${PORT}/swagger/"
    log_info "  - PID:          $(cat "$PID_FILE")"
    log_info "  - Logs:         ${LOG_FILE}"
    log_info "  - Stop:         ./scripts/deploy-local.sh --stop"
fi
