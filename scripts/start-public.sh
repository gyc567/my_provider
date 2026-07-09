#!/usr/bin/env bash
# 启动本地 pay-out 报价推送 HTTP 接口 (供外网调用)
# 用法: ./scripts/start-public.sh
#
# 前置条件:
#   1. .env 已配置 PROVIDER_API_KEYS,PUBLISH_PAY_OUT_DEFAULT 等
#   2. ngrok 已配置 authtoken
#   3. 端口 8090 空闲(避开 vite 等常见开发端口)
#
# 注意: 原方案用 8080,但 ~/code/t0-sandbox-bridge 的 vite dev 占了它,
# 改用 8090。如果你的 8090 也被占,改 PORT 环境变量。

set -euo pipefail

cd "$(dirname "$0")/.."

PORT=${PORT:-8090}
LOG_DIR="${LOG_DIR:-/tmp}"

# 1. Build
echo "==> Building my-provider"
go build -o "${LOG_DIR}/my-provider" ./cmd/main.go

# 2. Kill old providers on this port (but NOT ngrok, which holds the
#    public endpoint binding).
echo "==> Killing old my-provider processes"
pkill -f "${LOG_DIR}/my-provider" 2>/dev/null || true
sleep 1

# 3. Start provider (background)
echo "==> Starting my-provider on :${PORT}"
PORT="${PORT}" "${LOG_DIR}/my-provider" > "${LOG_DIR}/my-provider.log" 2>&1 &
PROVIDER_PID=$!
echo "   pid=${PROVIDER_PID}"
sleep 2

# 4. Self-test
echo "==> Self-test (no auth -> 401 expected)"
if curl -sS -X POST -o /dev/null -w "%{http_code}" "http://localhost:${PORT}/api/v1/quotes/pay-out" | grep -q '^401$'; then
    echo "   ✓ 401 OK"
else
    echo "   ✗ unexpected response; check ${LOG_DIR}/my-provider.log"
    exit 1
fi

# 5. Report ngrok public URL if running
echo
echo "==> ngrok tunnel status (if running):"
NGROK_URL=$(curl -sS http://127.0.0.1:4040/api/tunnels 2>/dev/null | python3 -c "
import json, sys
try:
    data = json.load(sys.stdin)
    for t in data.get('tunnels', []):
        if t['config']['addr'].endswith(':${PORT}'):
            print(t['public_url'])
except:
    pass
" 2>/dev/null || true)

if [ -n "${NGROK_URL}" ]; then
    echo "    ${NGROK_URL}/api/v1/quotes/pay-out"
    echo
    echo "==> External smoke test:"
    if curl -sS -X POST -o /dev/null -w "%{http_code}" "${NGROK_URL}/api/v1/quotes/pay-out" | grep -q '^401$'; then
        echo "    ✓ public tunnel reachable"
    fi
else
    echo "    (ngrok not running on :4040, or its tunnel points elsewhere)"
    echo "    start it with: env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY \\"
    echo "                    ngrok http ${PORT}"
fi

echo
echo "==> Logs: ${LOG_DIR}/my-provider.log"
echo "==> To stop: kill ${PROVIDER_PID}"
