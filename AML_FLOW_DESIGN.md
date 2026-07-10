# T-0 Network 手动 AML 支付流程设计方案

> 设计目标：在现有 my-provider 中间件基础上，完整实现 [Payment Flow with Manual AML Check](https://docs.t-0.network/docs/network/payment-flow-aml/) 所需接口与状态机。  
> 范围：方案设计阶段，不写代码。

---

## 1. 流程理解（基于官方文档）

手动 AML 流程是标准支付流程的扩展，核心差异：

1. **Payout Provider 可以要求人工 AML 审查**（而非立即接受/拒绝）。
2. **AML 审查期间原报价可能过期**，网络会在 AML 批准后重新拉取最新报价。
3. **Pay-in Provider 拥有 Last Look 机制**：对更新后的报价做最终确认或拒绝。
4. **两阶段确认**：AML 审批通过 → 网络刷新报价 → Pay-in 确认 → Payout Provider 执行出金。

官方 14 步流程中，与本中间件直接相关的交互点：

| 步骤 | 角色 | 动作 | 当前实现状态 |
|------|------|------|-------------|
| 1 | Payout Provider | `UpdateQuote` 推送报价 | ✅ 已实现 |
| 2-3 | Pay-in Provider / Network | `GetQuote` 获取报价 | ✅ 已实现 |
| 4 | Pay-in Provider | `CreatePayment` 创建支付 | ✅ 已实现 |
| 5-6 | Network | 选择/校验报价并确认支付 | ✅ 已通过 SDK 调用 |
| 7 | Network → Payout Provider | `PayOut` 请求出金 | ✅ 已实现，返回 `ManualAmlCheck` |
| 8 | Payout Provider | 响应 `ManualAmlCheck` | ✅ 已实现（硬编码自动审批） |
| 9 | Payout Provider → Network | `CompleteManualAmlCheck` | ✅ SDK 方法已封装 |
| 10 | Network → Pay-in Provider | `ApprovePaymentQuotes` 报价确认 | ✅ 已实现，带 last-look tolerance |
| 11 | Pay-in Provider | 确认/拒绝新报价 | ✅ 自动 Accept |
| 12 | Network → Payout Provider | 报价确认结果 | ✅ 通过 `UpdatePayment` 处理 |
| 13 | Payout Provider | 执行本地出金 + `FinalizePayout` | ✅ 已实现 |
| 14 | Network | `UpdatePayment(Confirmed)` 通知 | ✅ 已处理 |

---

## 2. 当前实现缺口分析

### 2.1 状态机缺失 AML 专用状态

当前 `internal/payment.Status`：

```go
StatusCreated
StatusAccepted
StatusPayoutRequested
StatusPayoutAccepted
StatusConfirmed
StatusFailed
```

缺少能表达以下中间状态的值：
- 等待 AML 人工审批
- AML 已批准、等待报价确认（Last Look）
- 报价已确认、等待执行出金

这会导致运营方无法从数据库判断一笔支付究竟卡在哪个环节。

### 2.2 生产级 AML 审批机制未实现

当前 `PayOut` 返回 `ManualAmlCheck` 后，在 `approveAmlAfter` 里固定等待 3 秒就自动调用 `CompleteManualAmlCheck(Approved)`。这是 Phase 2 冒烟测试行为，不能用于生产。

生产需要：
- 当 Payout Provider 收到 `PayOut` 时，把支付标记为 **待 AML 审批**，并通知运营方。
- 提供 REST/Webhook/队列等机制，让合规团队人工审核后，再调用 `CompleteManualAmlCheck(Approved/Rejected)`。

### 2.3 缺少运营审批接口

现有 `/api/v1/payments/{id}/aml/approve` 只能由 **OFI 角色**使用（按本地 ID 查记录）。当本节点作为 **Payout Provider** 收到 `PayOut` 时，本地也会写入一条 `RoleProvider` 记录，但：
- 没有按角色过滤的列表查询接口，运营方找不到待审订单。
- 没有区分 OFI-AML 操作与 Provider-AML 操作的语义。

### 2.4 Last Look 数据未持久化

`ApprovePaymentQuotes` 收到网络刷新后的 `PayOutAmount` / `SettlementAmount` 时，仅做 tolerance 比较并返回 Accept/Reject，没有把新报价金额写回数据库。运营审计时无法追溯“网络最终确认的金额”。

### 2.5 缺少 AML 事件通知

当支付进入待审、审批完成、报价确认、被拒绝等关键节点时，没有 webhook 或事件机制通知外部系统（如内部风控平台、消息队列、邮件系统）。

### 2.6 Travel Rule / 合规字段校验弱

AML 流程依赖完整的 sender/recipient KYC 信息（OpenVASP Travel Rule）。当前代码仅做 JSON 透传，未在应用层校验必填字段，容易因字段缺失被网络拒绝。

---

## 3. 设计方案

### 3.1 状态机扩展

建议将 `internal/payment.Status` 扩展为：

```go
const (
    StatusCreated              Status = "CREATED"               // 初始创建
    StatusAccepted             Status = "ACCEPTED"              // 网络已接受
    StatusPayoutRequested      Status = "PAYOUT_REQUESTED"      // 已收到 PayOut
    StatusManualAmlCheck       Status = "MANUAL_AML_CHECK"      // 等待 AML 审批（新增）
    StatusAmlApproved          Status = "AML_APPROVED"          // AML 已通过，等待报价确认（新增）
    StatusQuoteConfirmed       Status = "QUOTE_CONFIRMED"       // 报价已确认，等待出金（新增）
    StatusPayoutAccepted       Status = "PAYOUT_ACCEPTED"       // 出金已执行/已上报
    StatusConfirmed            Status = "CONFIRMED"             // 网络已确认完成
    StatusFailed               Status = "FAILED"                // 失败/拒绝
)
```

状态流转：

```
CREATED
  → ACCEPTED (UpdatePayment Accepted)
  → PAYOUT_REQUESTED (PayOut received)
  → MANUAL_AML_CHECK (return ManualAmlCheck)
  → AML_APPROVED (CompleteManualAmlCheck Approved)
  → QUOTE_CONFIRMED (ApprovePaymentQuotes accepted)
  → PAYOUT_ACCEPTED (FinalizePayout success)
  → CONFIRMED (UpdatePayment Confirmed)

任意状态 → FAILED (Rejected / network Failed)
```

### 3.2 Provider 侧 AML 审批流程改造

改造 `internal/handler/payment.go` 的 `PayOut`：

1. 保存/更新 provider 侧 payment 记录，状态置为 `MANUAL_AML_CHECK`。
2. **移除**硬编码的 3 秒自动审批（或改为可配置开关，默认关闭）。
3. 触发 AML 待审通知（Webhook / 事件）。
4. 返回 `PayoutResponse_ManualAmlCheck_`。

新增/改造 REST 运营接口（供合规团队使用）：

```
GET    /api/v1/payments?role=provider&status=MANUAL_AML_CHECK  # 列出待审订单
GET    /api/v1/payments/{id}                                   # 查看详情（含 travel rule）
POST   /api/v1/payments/{id}/aml/approve                       # 批准 AML（调用 CompleteManualAmlCheck Approved）
POST   /api/v1/payments/{id}/aml/reject                        # 拒绝 AML（调用 CompleteManualAmlCheck Rejected）
```

注意：当前 `handleAmlDecision` 已经能调用 `CompleteManualAmlCheck`，只需：
- 允许对 `RoleProvider` 且 `StatusManualAmlCheck` 的记录进行操作。
- 操作后更新本地状态为 `AML_APPROVED`（网络会随后发送 `ApprovePaymentQuotes`）。

### 3.3 Last Look 数据持久化

在 `ApprovePaymentQuotes` 处理中：

1. 从请求中读取 `PayOutAmount`、`SettlementAmount`、`QuoteId` 等最新报价字段。
2. 与本地记录做 tolerance 比较（当前已实现）。
3. **新增**：把最新报价金额和 quote ID 更新到 payment 记录。
4. 返回 Accept 或 Rejected。

数据库可新增字段（可选）：
- `confirmed_payout_amount`
- `confirmed_settlement_amount`
- `confirmed_quote_id`

如果希望保持最小改动，也可以直接覆盖 `payout_amount` / `settlement_amount`，但保留原始金额更利于审计。

### 3.4 UpdatePayment 状态细化

当前 `UpdatePayment` 对 `Accepted` 统一更新为 `StatusAccepted`。应区分：

- `UpdatePaymentRequest_Accepted_`：
  - 若当前状态是 `CREATED` → `ACCEPTED`（初始接受）。
  - 若当前状态是 `AML_APPROVED` / `QUOTE_CONFIRMED` → `QUOTE_CONFIRMED` 或 `PAYOUT_ACCEPTED`（确认后的更新）。
- `UpdatePaymentRequest_Confirmed_` → `CONFIRMED`。
- `UpdatePaymentRequest_Failed_` → `FAILED`。

### 3.5 AML 事件通知机制

新增 `internal/payment/notifier.go`（或复用 `internal/settlement/notifier` 模式）：

```go
type AMLNotifier interface {
    ManualAmlCheckRequired(ctx context.Context, p Payment) error
    AmlApproved(ctx context.Context, p Payment) error
    AmlRejected(ctx context.Context, p Payment, reason string) error
    QuoteConfirmed(ctx context.Context, p Payment) error
}
```

默认提供 `NoOpNotifier`，可通过 env 配置 webhook URL + secret（类似 settlement webhook）。

事件 payload 示例：

```json
{
  "event": "manual_aml_check_required",
  "payment_id": 42,
  "payment_client_id": "client-xxx",
  "role": "provider",
  "currency": "GBP",
  "amount": {"unscaled": 1000, "exponent": 0},
  "timestamp": "2026-07-10T02:00:00Z"
}
```

### 3.6 Travel Rule 数据校验

在 `CreateRequest.Validate()` 或 handler 层增加对 `travelRuleData` 的结构化校验。建议：

- 定义最小必填字段（基于 OpenVASP + t-0 自定义字段）。
- 仅校验字段存在性和基本格式（不引入完整 IVMS101 解析库，除非项目已有）。
- 缺失时返回 400，避免发送到网络后被拒绝。

示例必填字段：
- `originator`（汇款人）：姓名、地址、账户
- `beneficiary`（收款人）：姓名、账户
- `originatingVasp` / `beneficiaryVasp`（若适用）

### 3.7 配置项扩展

`.env.example` 增加：

```bash
# AML 配置
# true：PayOut 收到后自动模拟审批（仅用于冒烟测试）
# false：进入 MANUAL_AML_CHECK 状态，等待运营接口审批（生产默认）
AML_AUTO_APPROVE=false

# AML 事件通知 webhook
AML_WEBHOOK_URL=https://example.com/webhooks/aml
AML_WEBHOOK_SECRET=

# Last-look tolerance（%）
LAST_LOOK_TOLERANCE_PERCENT=1.0
```

---

## 4. 需要新增/修改的接口清单

### 4.1 SDK Callback 处理（已存在，需增强）

| Handler | 文件 | 变更 |
|---------|------|------|
| `PayOut` | `internal/handler/payment.go` | 写 `MANUAL_AML_CHECK` 状态；移除/可配置自动审批；触发通知 |
| `UpdatePayment` | `internal/handler/payment.go` | 根据当前状态细化状态流转 |
| `ApprovePaymentQuotes` | `internal/handler/payment.go` | 持久化最新报价金额 |
| `CompleteManualAmlCheck` | `internal/payment/network.go` | 已封装，无需改动 |

### 4.2 REST 运营接口（已有基础，需增强）

| 接口 | 变更 |
|------|------|
| `GET /api/v1/payments` | 新增列表查询，支持 `role`、`status` 过滤 |
| `GET /api/v1/payments/{id}` | 无需改动 |
| `POST /api/v1/payments/{id}/aml/approve` | 支持 provider 角色、更新状态为 `AML_APPROVED` |
| `POST /api/v1/payments/{id}/aml/reject` | 支持 provider 角色、更新状态为 `FAILED` |
| `POST /api/v1/payments/{id}/finalize` | 无需改动 |

### 4.3 内部能力

| 能力 | 新增文件/位置 |
|------|--------------|
| AML 事件通知 | `internal/payment/notifier.go` + config |
| 状态机扩展 | `internal/payment/models.go` |
| 持久化更新 | `internal/payment/store.go` / `sqlite.go` |
| Travel Rule 校验 | `internal/payment/validate.go` 或 `models.go` |

---

## 5. 数据模型变更

### 5.1 `payments` 表

新增字段（可选但建议）：

```sql
ALTER TABLE payments ADD COLUMN confirmed_payout_amount_unscaled INTEGER;
ALTER TABLE payments ADD COLUMN confirmed_payout_amount_exponent INTEGER NOT NULL DEFAULT 0;
ALTER TABLE payments ADD COLUMN confirmed_settlement_amount_unscaled INTEGER;
ALTER TABLE payments ADD COLUMN confirmed_settlement_amount_exponent INTEGER NOT NULL DEFAULT 0;
ALTER TABLE payments ADD COLUMN confirmed_quote_id INTEGER;
ALTER TABLE payments ADD COLUMN aml_decision_at TIMESTAMP;
```

或者最小化方案：直接复用现有 `payout_amount` / `settlement_amount` 字段，在网络刷新报价后覆盖写入。

### 5.2 `status` 枚举

直接扩展 `internal/payment/models.go` 的 `Status` 常量。SQLite 用字符串存储状态，无需 DB 枚举约束。

---

## 6. 测试策略

| 测试层级 | 覆盖内容 |
|---------|---------|
| 单元测试 | 状态流转、Tolerance 计算、AML notifier、Travel Rule 校验 |
| Handler 测试 | `PayOut` 写 `MANUAL_AML_CHECK`、`UpdatePayment` 分支、`ApprovePaymentQuotes` 持久化新金额 |
| REST API 测试 | `GET /api/v1/payments?role=provider&status=...`、`aml/approve` 对 provider 记录生效 |
| e2e | 完整 AML 流程：Create → PayOut → ManualAmlCheck → Approve → ApprovePaymentQuotes → Finalize → Confirmed |

---

## 7. 实施优先级建议

### P0（最小可用）
1. 扩展状态机（`MANUAL_AML_CHECK`、`AML_APPROVED`、`QUOTE_CONFIRMED`）。
2. 改造 `PayOut`：写入 `MANUAL_AML_CHECK`，移除硬编码自动审批（改为配置开关，生产默认关闭）。
3. 改造 `handleAmlDecision`：支持 provider 角色，操作后更新状态。
4. 增加 `GET /api/v1/payments?role=&status=` 查询。

### P1（生产可靠）
5. `ApprovePaymentQuotes` 持久化最新报价。
6. `UpdatePayment` 根据状态细化流转。
7. 增加 AML 事件 webhook notifier。

### P2（体验与合规）
8. Travel Rule 字段校验。
9. 历史金额审计字段（confirmed_*）。
10. 运营 dashboard 友好的响应格式（例如把 travel rule 解析成结构化字段返回）。

---

## 8. 审计与优化建议

以下是在原方案基础上补充的工程、安全、运维和合规层面的优化意见。

### 8.1 幂等与并发控制

- **AML 审批接口必须幂等**：运营人员可能重复点击 approve/reject，或网络超时后重试。`CompleteManualAmlCheck` 对网络来说是否幂等未知，因此本地应在调用前检查 `status`，仅允许从 `MANUAL_AML_CHECK` 进入 `AML_APPROVED` / `FAILED`。
- **状态转换加锁**：在 `handleAmlDecision` 中先 `SELECT ... FOR UPDATE`（SQLite 支持 `BEGIN IMMEDIATE`）再检查状态，防止并发请求导致重复调用网络或状态错乱。
- **拒绝重复的网络调用**：若状态已是 `AML_APPROVED` 或 `FAILED`，直接返回当前记录，不再调用 `CompleteManualAmlCheck`。

### 8.2 审计日志

- 每笔 AML 决策必须记录不可篡改的审计日志，字段包括：
  - 本地 payment ID、网络 payment ID
  - 决策人（API key 或 operator ID，当前只有 API key，可扩展为 `X-Operator-Id` header）
  - 决策类型（approve / reject）
  - 原因（reject 必填）
  - 决策时间
  - 变更前后状态
- 建议新增 `payment_audit_log` 表，或在 `payments` 表中保留 `aml_decision_at`、`aml_decision_by`、`aml_reason`。
- 审计日志禁止修改和删除。

### 8.3 权限隔离（RBAC）

当前所有接口共用同一套 `PROVIDER_API_KEYS`，存在风险：
- 建议将 AML 审批权限与普通只读/支付创建权限分离。
- 最小可行方案：在 `.env` 中增加 `AML_ADMIN_API_KEYS`，仅这些 key 可调用 `aml/approve` 和 `aml/reject`。
- 更优方案：引入基于角色的中间件，key 分为 `read`、`payment`、`aml_admin` 等角色。

### 8.4 Webhook 可靠性

AML 事件通知比 settlement 更关键，建议：
- 实现带指数退避的重试机制（最多 5 次，间隔 1s/2s/4s/8s/16s）。
- 使用 HMAC-SHA256 签名（与 settlement webhook 统一）。
- 记录每次 webhook 投递状态，便于排查。
- 考虑使用本地队列（SQLite 轻量级任务表或内存 channel）避免阻塞主流程。
- 增加 webhook 超时配置，默认 10 秒。

### 8.5 并发与竞态场景

需要重点处理以下竞态：
- `PayOut` 与运营 approve 几乎同时发生：状态机检查确保只处理一次。
- `CompleteManualAmlCheck(Approved)` 已发出，但网络尚未发送 `ApprovePaymentQuotes`，此时运营再次 reject：本地状态已变为 `AML_APPROVED`，应拒绝再次 reject 并提示“已批准，无法撤回”。
- `ApprovePaymentQuotes` 与 `FinalizePayout` 并发：若运营在 Last Look 通过后立即 finalize，需确保 `QUOTE_CONFIRMED` 状态才允许 finalize。

### 8.6 超时与重试策略

- `CompleteManualAmlCheck` 网络调用应设置 10 秒超时，失败时返回 502 并保留 `MANUAL_AML_CHECK` 状态，允许运营重试。
- `ApprovePaymentQuotes` 处理必须在 5 秒内完成，避免网络端超时。
- 对网络侧所有 RPC 调用统一添加 context timeout 和重试（幂等操作可重试 1 次）。

### 8.7 可观测性

- **Metrics**：在关键状态转换处埋点
  - `aml_check_required_total`（按 currency、method 分标签）
  - `aml_decision_total`（decision=approved/rejected）
  - `aml_decision_duration_seconds`（从 required 到 decision 的耗时，即实际审批耗时）
  - `approve_payment_quotes_accepted_total`、`approve_payment_quotes_rejected_total`
- **Logging**：所有 AML 相关日志使用结构化日志，包含 `payment_id`、`payment_client_id`、`status_from`、`status_to`。
- **Alerting**：`aml_decision_duration_seconds > 5min` 触发告警；`aml_check_required_total` 积压过多触发告警。

### 8.8 数据隐私与合规

- Travel Rule 数据包含姓名、地址、账户等 PII，建议：
  - 数据库字段 `travel_rule_data_json` 考虑加密存储（AES-256-GCM，密钥从 env 读取）。
  - 日志中禁止打印完整的 travel rule 数据。
  - 返回给运营接口时，按需脱敏（如隐藏部分账户号）。
- 定义数据保留策略：已确认/失败订单 7 年后归档或删除，待审订单长期保留。

### 8.9 运营兜底机制

- **最大待审时间**：配置 `AML_MAX_PENDING_DURATION`（如 24h），超过后自动 reject 并通知 OFI，避免资金无限期锁定。
- **Stale payment 清理**：每日定时任务清理卡在网络终态（如 `FAILED` 超过 30 天）的记录，或转冷存储。
- **手动补偿接口**：提供 `POST /api/v1/payments/{id}/sync` 强制从网络同步最新状态，用于异常恢复。

### 8.10 测试策略增强

- **并发测试**：使用 `-race` + 多个 goroutine 同时调用 approve/reject，验证状态机无竞态。
- **故障注入**：模拟 `CompleteManualAmlCheck` 网络超时、ApprovePaymentQuotes 金额突变、UpdatePayment 乱序到达。
- **边界测试**：tolerance = 0、tolerance = 100、金额为 0、非常大金额。
- **e2e 完整链路**：覆盖 approve 和 reject 两条完整路径，验证最终状态和回调次数。

### 8.11 数据库迁移策略

SQLite 对 `ALTER TABLE` 支持有限，新增字段简单，但：
- 新增 `confirmed_*` 字段时，使用 `ALTER TABLE ADD COLUMN`，失败则忽略重复列错误（已有类似模式）。
- 若后续需要拆分 travel rule 到独立表，应使用创建新表 + 数据迁移 + 重命名的方式。
- 部署前建议对生产 `quotes.db` 做备份。

### 8.12 Quote 刷新异常处理

AML 批准后，网络可能找不到 Payout Provider 的最新报价（例如报价已过期且未刷新）。此时：
- `ApprovePaymentQuotes` 可能不会被调用，或网络直接返回 Failed。
- 本地应通过 `UpdatePayment(Failed)` 处理，状态变为 `FAILED`，并记录原因。
- 建议在 `UpdatePayment` 中明确处理“quote expired / not found”类的失败原因。

### 8.13 拒绝后的级联通知

当 Payout Provider 拒绝 AML 或 Last Look 时：
- 网络会向 OFI 发送失败通知。
- 本地作为 Provider 时，应触发 `AmlRejected` / `QuoteRejected` webhook，便于内部系统及时释放资金或通知客户。

### 8.14 与现有 Settlement 模块的边界

- AML 流程本身不直接操作 settlement ledger，但支付完成后网络会通过 `AppendLedgerEntries` 更新余额。
- 建议在 AML 决策和支付确认时，不阻塞 settlement 处理，两者独立消费网络回调。

### 8.15 配置项补充

除原方案配置外，建议增加：

```bash
# AML 审批权限 key（逗号分隔），不设置则与 PROVIDER_API_KEYS 相同（不推荐生产）
AML_ADMIN_API_KEYS=

# 最大待审时间，超过自动拒绝（如 24h）
AML_MAX_PENDING_DURATION=24h

# AML webhook 超时与重试
AML_WEBHOOK_TIMEOUT_SECONDS=10
AML_WEBHOOK_MAX_RETRIES=5

# 是否启用自动审批（仅测试）
AML_AUTO_APPROVE=false

# 是否对 travel rule JSON 加密存储
TRAVEL_RULE_ENCRYPT=false
TRAVEL_RULE_ENCRYPTION_KEY=
```

---

## 9. 实施优先级建议（优化后）

### P0（最小可用）
1. 扩展状态机（`MANUAL_AML_CHECK`、`AML_APPROVED`、`QUOTE_CONFIRMED`）。
2. 改造 `PayOut`：写入 `MANUAL_AML_CHECK`，移除硬编码自动审批（改为配置开关，生产默认关闭）。
3. 改造 `handleAmlDecision`：支持 provider 角色、幂等、状态锁、审计字段。
4. 增加 `GET /api/v1/payments?role=&status=` 查询。
5. `ApprovePaymentQuotes` 持久化最新报价并细化 `UpdatePayment` 状态流转。

### P1（生产可靠）
6. 增加 AML 事件 webhook notifier（带重试和签名）。
7. 为 `CompleteManualAmlCheck` 等网络调用增加超时和重试。
8. 增加关键 metrics 和结构化日志。
9. 实现 `AML_ADMIN_API_KEYS` 权限隔离。

### P2（体验与合规）
10. Travel Rule 字段校验。
11. 历史金额审计字段（confirmed_*）。
12. Travel Rule 加密存储与脱敏展示。
13. 最大待审时间、stale payment 清理、手动同步接口。
14. 运营 dashboard 友好的响应格式。

---

## 10. 一句话总结

> **把当前“3 秒自动审批”的冒烟测试行为升级为由运营接口驱动、带权限隔离/审计日志/可靠通知/幂等控制的真实 AML 工作流，补全状态机、Last Look 数据持久化和事件通知，使中间件能安全承载生产级手动 AML 审查。**
