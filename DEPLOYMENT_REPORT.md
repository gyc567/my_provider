# 本地部署报告 — my-provider

> 部署时间：2026-07-10  
> 部署环境：Linux amd64, Go 1.25.0  
> 监听端口：8081（由 `.env` 中的 `PORT` 指定）

## 1. 部署脚本

原有 `scripts/start-public.sh` 仅用于启动公共 pay-out 接口（配合 ngrok），不够通用。因此新建了专业的本地部署脚本：

**`scripts/deploy-local.sh`**

功能：
- 检查 Go 版本（要求 >= 1.23）
- 加载并校验 `.env` 中的必要配置
- 自动停止已运行实例、清理端口占用
- 使用 `CGO_ENABLED=0` 构建生产优化二进制到 `bin/my-provider`
- 创建 `data/` 与 `logs/` 目录
- 后台启动服务并等待 `/swagger/doc.json` 就绪
- 执行 12 项冒烟测试，覆盖 REST、认证、quote、payment、settlement、payment-intent 接口
- 支持 `./scripts/deploy-local.sh --stop` 优雅停止

## 2. 部署执行结果

```text
[INFO]  Starting local deployment from /root/code/my_provider
[INFO]  Building binary -> /root/code/my_provider/bin/my-provider
[INFO]  Starting my-provider on :8081
[INFO]  PID: 492356; logs: /root/code/my_provider/logs/my-provider.log
[INFO]  Waiting for server to be ready...
[INFO]  Running smoke tests...
[INFO]    ✓ Swagger docs reachable
[INFO]    ✓ pay-out without auth: 401
[INFO]    ✓ get quotes without auth: 401
[INFO]    ✓ product pay-out update: 200
[INFO]    ✓ quoteapi pay-in update: 200
[INFO]    ✓ get quotes: 200
[INFO]    ✓ create payment: 200 (reachable)
[INFO]    ✓ get settlement credits: 200
[INFO]    ✓ get settlement ledger: 200
[INFO]    ✓ create payment intent (recipient): 502 (reachable)
[INFO]    ✓ get payment intent quote: 502 (reachable)
[INFO]    ✓ confirm provider intent (routing check): 404
[INFO]  Deployment successful.
```

服务当前状态：运行中（PID 492356）。

## 3. 冒烟测试覆盖

| # | 接口 | 验证点 | 结果 |
|---|------|--------|------|
| 1 | `GET /swagger/doc.json` | Swagger 文档可达 | 200 ✅ |
| 2 | `POST /api/v1/quotes/pay-out` (无认证) | Bearer 认证生效 | 401 ✅ |
| 3 | `GET /api/v1/quotes` (无认证) | Bearer 认证生效 | 401 ✅ |
| 4 | `POST /api/v1/quotes/pay-out` | Product handler 更新 pay-out 报价 | 200 ✅ |
| 5 | `PUT /api/v1/quotes/pay-in` | QuoteAPI 更新 pay-in 报价 | 200 ✅ |
| 6 | `GET /api/v1/quotes` | 读取本地报价快照 | 200 ✅ |
| 7 | `POST /api/v1/payments` | 创建 payment，代理落库后返回 | 200 ✅ |
| 8 | `GET /api/v1/settlement/credits` | 查询 credits | 200 ✅ |
| 9 | `GET /api/v1/settlement/ledger` | 查询 ledger | 200 ✅ |
| 10 | `POST /api/v1/payment-intents` | 3B recipient 创建 intent | 502（网络层未启用，代理可达）✅ |
| 11 | `POST /api/v1/payment-intent-quotes` | 3B recipient 获取 quote | 502（网络层未启用，代理可达）✅ |
| 12 | `POST /api/v1/payment-intents/provider/999/confirm` | 3A provider 路由正确 | 404 ✅ |

> 说明：recipient 的两个接口返回 502，是因为当前 t-0 sandbox 侧 `payment_intent.recipient` 服务返回 `unimplemented: 404 Not Found`。这证明本机代理层已正确接收请求并转发到网络，属于网络侧未就绪，而非本机部署问题。

## 4. 注意事项

- **重复部署时的 `already_exists` 日志**：由于 pay-out 报价使用 `client_quote_id` 作为网络侧幂等键，如果 `data/quotes.db` 中的旧快照未清理，发布时 sandbox 会返回 `already_exists`。脚本通过每次使用唯一 `smoke-gbp-<timestamp>` 的测试 ID 避免冒烟测试失败；生产环境如需刷新报价，应更新 `client_quote_id` 或等待旧报价过期。
- **`.env` 未包含的新字段**：代码对 `DB_PATH`、`PUBLISH_PAY_IN_DEFAULT`、`PAYMENT_BASE_URL`、`SETTLEMENT_WEBHOOK_URL`、`SETTLEMENT_WEBHOOK_SECRET`、`LAST_LOOK_TOLERANCE_PERCENT` 均有默认值，因此无需强制修改 `.env` 即可启动。
- **端口占用**：脚本启动前会自动停止同端口（`PORT`）的遗留进程。

## 5. 常用命令

```bash
# 启动并验证
./scripts/deploy-local.sh

# 停止服务
./scripts/deploy-local.sh --stop

# 查看日志
tail -f logs/my-provider.log

# 手动调用 API
curl -sS http://127.0.0.1:8081/api/v1/quotes \
  -H "Authorization: Bearer <YOUR_API_KEY>"
```

## 6. 回归测试

部署前后均执行：

- `go test ./...` — 全部通过
- `golangci-lint run ./...` — 0 issues
