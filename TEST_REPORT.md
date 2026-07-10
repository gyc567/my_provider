# 测试报告 — Provider Proxy 新增接口

> 生成时间：2026-07-10  
> 执行命令：`go test ./...`、`go test ./... -race`、`golangci-lint run ./...`、`go test ./... -coverprofile=coverage.out`

## 1. 本次修复的问题

在补齐单元测试与 e2e 测试过程中，发现并修复了以下 3 类问题：

| 问题 | 根因 | 修复位置 |
|------|------|----------|
| payment-intent provider/recipient 单元测试 404 | `Router()` 返回的子 mux 使用相对路径（如 `GET /{id}`），测试直接用完整路径请求，未做 prefix 剥离 | `internal/paymentintent/provider/api_test.go`、`internal/paymentintent/recipient/api_test.go` 中改用 `http.StripPrefix` |
| e2e payment-intent 接口 404 | `cmd/main.go` 与 e2e 中把子 mux 直接挂到 prefix 路径，Go ServeMux 不会自动剥离 prefix | `cmd/main.go`、`internal/e2e/rest_test.go` 中改用 `http.StripPrefix` |
| e2e quote lifecycle 500（`no such table: quote_bands`） | e2e 使用 `:memory:` SQLite，连接池内多个连接各自持有独立内存库，migrate 创建的表对其他连接不可见 | `internal/e2e/rest_test.go` 改用 `t.TempDir()` + 文件数据库 |
| e2e `PUT /api/v1/quotes/pay-out` 400 | 该路径由 product handler（`internal/api`）处理，期望字符串 `rate`/`max_amount_usd` 和 int `expiration_seconds`，但测试发了 `quoteapi` 的对象格式 | `internal/e2e/rest_test.go` 改为 product handler 所需 payload |
| lint errcheck | `readBody` 中 `defer resp.Body.Close()` 未处理返回值 | `internal/e2e/rest_test.go` 改为 `defer func() { _ = resp.Body.Close() }()` |

## 2. 测试结果

### 2.1 单元测试

| 包 | 状态 | 覆盖率 |
|----|------|--------|
| `my-provider/internal` | ✅ pass | 83.3% |
| `my-provider/internal/api` | ✅ pass | 89.9% |
| `my-provider/internal/handler` | ✅ pass | 58.1% |
| `my-provider/internal/payment` | ✅ pass | 52.7% |
| `my-provider/internal/paymentintent` | ✅ pass | 44.7% |
| `my-provider/internal/paymentintent/provider` | ✅ pass | 48.0% |
| `my-provider/internal/paymentintent/recipient` | ✅ pass | 67.0% |
| `my-provider/internal/quote` | ✅ pass | 80.1% |
| `my-provider/internal/quoteapi` | ✅ pass | 92.9% |
| `my-provider/internal/settlement` | ✅ pass | 33.3% |

### 2.2 e2e 测试

| 用例 | 覆盖接口 | 状态 |
|------|----------|------|
| `TestE2E_QuoteLifecycle` | `PUT /api/v1/quotes/pay-out`、`PUT /api/v1/quotes/pay-in`、`GET /api/v1/quotes`、`POST /api/v1/quotes/network` | ✅ pass |
| `TestE2E_PaymentLifecycle` | `POST /api/v1/payments`、`GET /api/v1/payments/{id}`、`POST /api/v1/payments/{id}/aml/approve`、`POST /api/v1/payments/{id}/finalize` | ✅ pass |
| `TestE2E_SettlementREST` | `GET /api/v1/settlement/credits`、`GET /api/v1/settlement/ledger` | ✅ pass |
| `TestE2E_PaymentIntentProvider` | `POST /api/v1/payment-intents/provider/{id}/confirm`（含 `GET` 辅助验证） | ✅ pass |
| `TestE2E_PaymentIntentRecipient` | `POST /api/v1/payment-intents`、`POST /api/v1/payment-intent-quotes` | ✅ pass |
| `TestE2E_Unauthorized` | 未携带 Bearer Token 时返回 401 | ✅ pass |

### 2.3 Race 检测

`go test ./... -race` 全部通过，未发现数据竞争。

### 2.4 Lint

`golangci-lint run ./...` 0 issues。

## 3. 覆盖率总览

- **整体语句覆盖率：63.7%**
- 覆盖率文件：`coverage.out`
- HTML 报告：`coverage.html`

覆盖率较低的包说明：
- `settlement`（33.3%）：webhook notifier 的 `Notify` 错误分支、部分 ledger/credit 查询路径未被当前 e2e 用例命中。
- `payment`（52.7%）/ `paymentintent/provider`（48.0%）：network SDK 回调 handler 的异常分支和部分状态机路径未覆盖。
- `handler`（58.1%）：Provider SDK callback 中错误处理、超时路径未完全覆盖。

这些路径主要与外部网络错误、超时、幂等冲突等异常场景相关，后续可补充针对性的单元测试。

## 4. 测试验证清单

- [x] 所有单元测试通过
- [x] 所有 e2e 测试通过
- [x] `-race` 检测通过
- [x] `golangci-lint` 0 问题
- [x] 覆盖率报告已生成
- [x] `cmd/main.go` 路由挂载方式已修复（避免运行时 panic/404）
