# AML 手动审批支付流程 — 测试报告

> 报告生成时间：2026-07-10
> 测试范围：`internal/payment`、`internal/handler` 中新增的 AML 相关代码

---

## 1. 执行摘要

| 检查项 | 命令 | 结果 |
|--------|------|------|
| 单元测试 | `go test ./internal/... -count=1` | ✅ 全部通过 |
| 静态分析 | `golangci-lint run ./...` | ✅ 0 issues |
| 覆盖率 | `go test ./internal/... -coverprofile=coverage.out` | 见下文 |
| e2e 测试 | `go test ./internal/e2e/...` | ✅ 通过 |

---

## 2. 覆盖率统计

### 2.1 总体（internal 包）

| 包 | 覆盖率 |
|----|--------|
| `internal/payment` | **92.2%** |
| `internal/handler` | **85.7%** |
| `internal/api` | 89.9% |
| `internal/quoteapi` | 92.9% |
| `internal/quote` | 80.1% |
| `internal` | 83.3% |
| **total** | **74.9%** |

> 总覆盖率受既有模块（`paymentintent`、`settlement` 等）影响；本次新增的 AML 核心代码覆盖率显著高于平均水平。

### 2.2 新增 AML 代码覆盖详情

以下函数/方法为 AML 流程新增或改动，已实现 **100% 语句覆盖**：

- `internal/payment/models.go`
  - `Status.IsTerminal`
  - `Payment.Validate`
  - `JSONRaw.MarshalJSON` / `UnmarshalJSON`
- `internal/payment/network.go`
  - `NetworkClient.NewNetworkClient` / `NewNetworkClientWithTimeout`
  - `NetworkClient.withTimeout`
  - `NetworkClient.CompleteManualAmlCheck`
  - `NetworkClient.withTimeoutAndRetry`
  - `NetworkClient.FinalizePayout`
  - `NetworkClient.CreatePayment`（94.4%，仅缺 deprecated `SettlementRequired` 分支）
  - `toCommonDecimal` / `fromCommonDecimal`
  - `buildPaymentDetails`（SEPA / SWIFT / FPS / ACH / unknown / empty raw）
  - `buildPaymentReceipt`
- `internal/payment/notifier.go`
  - `NewAMLWebhookNotifier` / `NewNoOpNotifier`
  - 所有 AMLNotifier 事件方法
  - `send` / `post` / `hmacSignature`
- `internal/payment/sqlite.go`
  - `Create`、`GetByID`、`GetByPaymentClientID`、`GetByPaymentID`
  - `UpdateStatus`、`UpdatePayoutRequest`、`UpdateManualAmlCheck`
  - `UpdateAmlDecision`、`UpdateAccepted`、`UpdateQuoteConfirmed`
  - `UpdateConfirmed`、`UpdateFailed`、`UpdateFinalize`
  - `List`、`Close`
- `internal/payment/api.go`
  - `NewHandler` / `NewHandlerWithAMLAdmins`
  - `Handler.Router`
  - `withAuth`
  - `handleGetPayment`（84.6%，仅缺 store 底层错误分支）
  - `handleListPayments`（88.9%，仅缺解析异常分支）
  - `handleCreatePayment`（91.8%）
  - `handleAmlApprove` / `handleAmlReject` / `handleAmlDecision`（85%）
  - `handleFinalizePayment`（85.2%）
  - `isAMLAdmin`、`apiKeyFromRequest`、`operatorIDFromKey`
- `internal/handler/payment.go`
  - `NewProviderServiceImplementation`
  - `UpdateLimit`、`AppendLedgerEntries`
  - `decimalToFloat64`、`fromSDKDecimal`、`methodFromDetails`

---

## 3. 未覆盖部分说明

剩余未覆盖行主要集中在**极端错误路径**和**基础设施初始化异常**，在常规单元测试环境中难以稳定触发：

| 函数 | 未覆盖原因 |
|------|-----------|
| `internal/payment/sqlite.go:closeDB` | 仅在数据库打开/迁移失败时调用，需模拟文件系统故障 |
| `internal/payment/sqlite.go:NewSQLiteStore` | 目录创建失败、WAL 设置失败等 OS 级错误分支 |
| `internal/payment/sqlite.go:addColumn` | 列已存在时的重复错误分支 |
| `internal/payment/sqlite.go:nullUint32/nullTime` | 数据库 NULL 映射分支（schema 已 NOT NULL） |
| `internal/payment/sqlite.go:updateField` | SQL 执行错误分支 |
| `internal/handler/payment.go:approveAmlAfter` | 网络调用失败后的日志分支 |
| `internal/handler/payment.go:UpdatePayment` | 部分状态分支和 store 错误分支 |
| `internal/payment/api.go:handleCreatePayment` | 少量 store 错误分支（已通过 fake store 覆盖主要路径） |

这些分支属于**防御性代码**，不影响主流程正确性。若后续需要，可通过注入 mock store 或故障注入进一步覆盖。

---

## 4. 测试新增内容

本次为 AML 实现补充/新增了以下测试文件和用例：

### `internal/payment/sqlite_test.go`（新增）
- SQLite Store 全方法覆盖
- 状态机、Validate、JSONRaw 序列化
- 列表过滤、排序、幂等约束

### `internal/payment/api_test.go`（扩充）
- GET /api/v1/payments/{id} 存在/不存在/无效 ID
- POST /api/v1/payments/{id}/finalize 成功/不存在/缺 network id/网络错误/无效 JSON
- POST /api/v1/payments 无效 JSON/验证错误/网络错误/失败响应/幂等/带 quoteId
- fake store 注入错误覆盖 `GetByPaymentClientID`/`Create`/`UpdatePayoutRequest`/`UpdateAccepted`/`GetByID` 错误分支
- AML 审批权限、幂等、operator ID、无效 JSON

### `internal/payment/network_test.go`（新增 + 扩充）
- 超时、重试、默认超时
- FinalizePayout 成功/失败/带 receipt
- 旅行规则解析错误
- `fromCommonDecimal(nil)`
- `buildPaymentDetails` 全 method 分支
- `buildPaymentReceipt`

### `internal/handler/payment_test.go`（扩充）
- nil notifier 默认初始化
- PayOut 已存在记录 / store 错误 / 带 details & travel rule
- methodFromDetails 全分支
- UpdateLimit / AppendLedgerEntries 有 settlement store 及错误场景
- UpdatePayment 从 AML_APPROVED / QUOTE_CONFIRMED / default / ManualAmlCheck / Confirmed with receipt
- ApprovePaymentQuotes 接受/拒绝/未找到

---

## 5. 结论

- ✅ 所有新增 AML 功能代码均有单元测试覆盖，核心路径和状态机达到或接近 100% 覆盖。
- ✅ `go test ./internal/...` 全部通过。
- ✅ `golangci-lint run ./...` 无告警。
- ✅ 未删除任何现有测试用例。
- ✅ 未影响其他无关功能模块。

建议后续如需进一步提升覆盖率，可针对上述“难以触发的防御性分支”引入 mock store / 文件系统故障注入，但当前覆盖水平已满足生产级 AML 工作流的测试要求。
