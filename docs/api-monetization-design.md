# my-provider 接口 monetization 设计方案

> 基于 [Ethereum 支付](https://ethereum.org/zh/payments/) 与 [Cloudflare x402](https://blog.cloudflare.com/zh-cn/x402/) 理念，为 my-provider 设计一套**可选、独立、可配置**的 API 按次收费方案。
> 范围：方案设计，不写代码。

---

## 1. 背景与目标

### 1.1 背景

- **Ethereum 支付**：提供全球性、全天候、可编程、低成本的资产转移能力，稳定币（USDC/USDT）可作为计价单位。
- **x402 标准**：通过 HTTP `402 Payment Required` 状态码实现机器对机器（M2M）微支付，支持按次付费、延迟结算、签名承诺。

### 1.2 目标

为 my-provider 的 REST API 增加**可选的按调用付费能力**：

- 仅对指定端点收费，其他业务接口完全不受影响。
- 支持基于稳定币的微支付和延迟结算。
- 支持 AI 代理、爬虫、第三方系统等机器客户端自动付费。
- 与本项目现有业务（quote、payment、settlement、payment-intent）**解耦**。

---

## 2. 设计原则

1. **独立性**：新增 `billing` 包和中间件，不修改现有 handler 业务逻辑。
2. **可选性**：通过环境变量开关，未开启时所有接口免费，行为与现在一致。
3. **最小侵入**：仅在 `cmd/main.go` 中对需要收费的 router 做包装。
4. **可配置**：每个端点可独立设置价格、结算方式、免费白名单。
5. **安全**：防重放、防篡改、短期有效凭证、请求绑定。
6. **无阻塞**：计费校验在请求进入业务 handler 前完成，结算确认可异步化。
7. **可审计**：完整记录每笔扣费、每次 402 响应、每次结算，支持导出。
8. **可灰度**：支持按 API key、按端点、按百分比逐步开启收费。

---

## 3. 系统架构

```
Client Request
      │
      ▼
┌─────────────────┐
│  Payment Gate   │  ← 新增：解析 X-Payment-Authorization，校验余额/凭证
│   Middleware    │
└────────┬────────┘
         │ 已付费 / 免费 / 白名单
         ▼
┌─────────────────┐
│  Existing API   │  ← 现有：quotes / payments / payment-intents / settlement
│     Handler     │
└─────────────────┘
         │
         ▼
    Response
```

新增组件：

| 组件 | 位置 | 职责 |
|------|------|------|
| `billing.Middleware` | `internal/billing/middleware.go` | 拦截请求，解析支付凭证，校验权限 |
| `billing.Store` | `internal/billing/store.go` | 账户余额、消费记录、nonce 记录 |
| `billing.Pricer` | `internal/billing/pricer.go` | 根据端点和请求体计算价格 |
| `billing.Verifier` | `internal/billing/verifier.go` | 验证链下签名承诺或链上支付 proof |
| `billing.API` | `internal/billing/api.go` | 充值、查询余额、查询账单、条款 |
| `billing.RateLimiter` | `internal/billing/ratelimiter.go` | 按账户限流，防止异常调用冲击 |
| `billing.Cache` | `internal/billing/cache.go` | nonce/余额/价格缓存，降低数据库压力 |

---

## 4. 收费端点与定价模型

### 4.1 建议收费端点

按业务价值从高到低选择：

| 端点 | 方法 | 默认价格 | 计费维度 |
|------|------|---------|---------|
| `POST /api/v1/payments` | 创建支付 | $0.10 / 次 | 固定费用 |
| `POST /api/v1/payment-intents` | 创建支付意图 | $0.05 / 次 | 固定费用 |
| `POST /api/v1/payment-intent-quotes` | 获取支付意图报价 | $0.02 / 次 | 固定费用 |
| `POST /api/v1/quotes/publish` | 批量发布报价 | $0.01 / 次 | 固定费用 |
| `POST /api/v1/quotes/network` | 查询网络报价 | $0.005 / 次 | 固定费用 |
| `GET /api/v1/quotes` | 查询本地报价快照 | 免费 | — |
| `GET /api/v1/settlement/credits` | 查询结算额度 | 免费 | — |
| SDK callback 端点（`/tzero.v1.payment.*`） | — | 免费 | 网络回调，不应收费 |

> 实际启用哪些端点、价格多少，全部通过 `.env` 配置决定。

### 4.2 定价模型

1. **固定费用（Flat）**：每次调用固定 USDC 金额，例如 `$0.10`。
2. **按比例（Percentage）**：按交易金额的一定比例收费，例如支付金额的 `0.1%`。适用于支付类接口。
3. **阶梯（Tiered）**：按调用频次或金额区间设置不同价格。例如：每月前 1000 次 $0.01，超出部分 $0.005。
4. **免费额度（Quota）**：每个 API key 每月 N 次免费调用，超出后收费。
5. **封顶（Cap）**：单次调用最高收费金额，防止大额交易按比例计费过高。
6. **最低收费（Floor）**：单次调用最低收费金额，避免微额交易无法覆盖 Gas/运营成本。

### 4.3 价格计算示例

```json
{
  "POST /api/v1/payments": {
    "type": "percentage",
    "rate": "0.001",
    "cap_usdc": "500000",
    "floor_usdc": "10000"
  }
}
```

表示：按支付金额的 0.1% 收费，最低 $0.01，最高 $5.00。

---

## 5. 请求/响应流程

### 5.1 首次请求（未付费）

```http
POST /api/v1/payments
Authorization: Bearer <api-key>
Content-Type: application/json

{ ... }
```

服务端返回：

```http
HTTP/1.1 402 Payment Required
Content-Type: application/json
X-Payment-Scheme: usdc-base-sepolia

{
  "accepts": [
    {
      "scheme": "immediate",
      "network": "base-sepolia",
      "token": "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
      "token_symbol": "USDC",
      "decimals": 6,
      "amount": "100000",
      "recipient": "0xYourProviderWalletAddress",
      "resource": "POST /api/v1/payments",
      "request_body_hash": "sha256:abcd...",
      "expires_at": "2026-07-10T15:30:00Z",
      "payment_id": "pay_abc123",
      "offer_signature": "0xServerSignatureOfAboveFields"
    },
    {
      "scheme": "deferred",
      "network": "deferred-billing",
      "terms_url": "https://api.agtpay.xyz/billing/terms",
      "resource": "POST /api/v1/payments",
      "request_body_hash": "sha256:abcd...",
      "expires_at": "2026-07-10T15:30:00Z",
      "payment_id": "pay_abc123",
      "offer_signature": "0xServerSignatureOfAboveFields"
    }
  ]
}
```

### 5.2 客户端支付后重试

**即时结算（immediate）**：

```http
POST /api/v1/payments
Authorization: Bearer <api-key>
X-Payment-Authorization: scheme=immediate;network=base-sepolia;tx=0xTransactionHash;payment_id=pay_abc123;offer_signature=0xServerSignatureOfAboveFields
Content-Type: application/json

{ ... same body as the 402 request ... }
```

**延迟结算（deferred）**：

```http
POST /api/v1/payments
Authorization: Bearer <api-key>
X-Payment-Authorization: scheme=deferred;id=pay_abc123;sig=0xSignedCommitment;offer_signature=0xServerSignatureOfAboveFields
Signature-Input: sig=("payment" "@method" "@target-uri" "content-digest");created=...;keyid=...
Signature: sig=...
Content-Type: application/json

{ ... same body as the 402 request ... }
```

> 关键约束：客户端重试时必须携带与 402 响应一致的 `payment_id` 和 `offer_signature`，且请求体必须与 402 响应中的 `request_body_hash` 匹配。

### 5.3 成功响应

```http
HTTP/1.1 200 OK
Payment-Response: scheme=immediate;id=pay_abc123;status=settled

{ ... existing response body ... }
```

---

## 6. 数据模型

新增 `billing` 表，与现有 `payments/quotes/settlement` 表完全隔离：

```sql
-- 计费账户（按 API key 或按钱包地址）
CREATE TABLE billing_accounts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    api_key_hash TEXT UNIQUE NOT NULL,  -- SHA-256(api_key)，不存明文
    wallet_address TEXT,
    balance_usdc_unscaled INTEGER NOT NULL DEFAULT 0,
    balance_exponent INTEGER NOT NULL DEFAULT 6,
    free_quota_remaining INTEGER NOT NULL DEFAULT 0,
    free_quota_total INTEGER NOT NULL DEFAULT 0,
    billing_status TEXT NOT NULL DEFAULT 'active',  -- active / suspended / closed
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

-- 消费记录（不可删除，用于对账和审计）
CREATE TABLE billing_charges (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id INTEGER NOT NULL,
    payment_id TEXT UNIQUE NOT NULL,
    resource TEXT NOT NULL,
    request_body_hash TEXT,  -- 绑定具体请求内容，防止 proof 被复用到其他请求
    amount_usdc_unscaled INTEGER NOT NULL,
    scheme TEXT NOT NULL,  -- immediate / deferred
    status TEXT NOT NULL,  -- pending / settled / failed / refunded
    tx_hash TEXT,
    settled_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL
);

-- 支付凭证/要约记录
CREATE TABLE billing_payment_offers (
    payment_id TEXT PRIMARY KEY,
    account_id INTEGER NOT NULL,
    resource TEXT NOT NULL,
    amount_usdc_unscaled INTEGER NOT NULL,
    scheme TEXT NOT NULL,
    expires_at TIMESTAMP NOT NULL,
    offer_signature TEXT,  -- 服务端对价格的签名，防止客户端篡改
    created_at TIMESTAMP NOT NULL
);

-- 防止重放
CREATE TABLE billing_nonces (
    nonce TEXT PRIMARY KEY,
    used_at TIMESTAMP NOT NULL
);

-- 充值记录
CREATE TABLE billing_topups (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id INTEGER NOT NULL,
    amount_usdc_unscaled INTEGER NOT NULL,
    source TEXT NOT NULL,  -- onchain / manual / facilitator
    tx_hash TEXT,
    status TEXT NOT NULL,  -- pending / confirmed / failed
    confirmed_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL
);

-- 索引优化
CREATE INDEX idx_billing_charges_account_created ON billing_charges(account_id, created_at);
CREATE INDEX idx_billing_charges_payment_id ON billing_charges(payment_id);
CREATE INDEX idx_billing_topups_account_created ON billing_topups(account_id, created_at);
```

---

## 7. 配置项

在 `.env.example` 中新增：

```bash
# Billing / API monetization
ENABLE_API_BILLING=false

# Provider wallet for receiving USDC payments
BILLING_WALLET_ADDRESS=0xYourProviderWalletAddress

# Supported chains/tokens for immediate settlement (JSON array)
BILLING_ACCEPTED_TOKENS='[
  {"network":"base-sepolia","token":"0x036CbD53842c5426634e7929541eC2318f3dCF7e","symbol":"USDC","decimals":6}
]'

# Pricing config (JSON map: endpoint -> pricing rule)
BILLING_PRICING='{
  "POST /api/v1/payments": {"type":"flat","amount_usdc":"100000"},
  "POST /api/v1/payment-intents": {"type":"flat","amount_usdc":"50000"},
  "POST /api/v1/payment-intent-quotes": {"type":"flat","amount_usdc":"20000"},
  "POST /api/v1/quotes/publish": {"type":"flat","amount_usdc":"10000"},
  "POST /api/v1/quotes/network": {"type":"flat","amount_usdc":"5000"}
}'

# Free quota per API key per calendar month
BILLING_FREE_QUOTA_PER_MONTH=100

# API keys exempt from billing (comma-separated)
BILLING_FREE_API_KEYS=

# Deferred settlement terms URL
BILLING_DEFERRED_TERMS_URL=https://api.agtpay.xyz/billing/terms

# x402 facilitator endpoint (optional; leave empty for self-verification)
X402_FACILITATOR_URL=

# Number of blockchain confirmations required for immediate settlement
BILLING_ONCHAIN_CONFIRMATIONS=3

# Maximum deferred credit allowed per account (USDC unscaled, 6 decimals)
BILLING_DEFERRED_CREDIT_LIMIT_USDC=50000000

# Billing admin keys for topup/chargeback endpoints (comma-separated)
BILLING_ADMIN_API_KEYS=

# Rate limit per account: requests per minute
BILLING_RATE_LIMIT_RPM=60

# Cache TTL for balance/nonce/price (seconds)
BILLING_CACHE_TTL_SECONDS=60

# Sandbox mode: use testnet tokens and do not perform real settlement
BILLING_SANDBOX_MODE=false

# Enable billing only for specific API keys (comma-separated; empty means all non-free keys)
BILLING_ENABLED_API_KEYS=
```

---

## 8. 新增 REST 接口

为运营方和客户提供查询/充值能力：

| 端点 | 方法 | 说明 |
|------|------|------|
| `GET /api/v1/billing/account` | 查询当前 API key 的余额、额度、账单汇总 | |
| `GET /api/v1/billing/charges` | 查询消费明细 | |
| `POST /api/v1/billing/topup` | 链下充值确认（运营方手动确认或 facilitator 回调） | |
| `GET /api/v1/billing/terms` | 返回延迟结算条款 | |

这些接口同样受 `Authorization: Bearer <api-key>` 保护，且自身**免费**。

---

## 9. 与现有代码的结合点

仅需在 `cmd/main.go` 中新增一行包装：

```go
import "my-provider/internal/billing"

// 在 router 组装完成后、StartServer 前包装需要收费的 router
billingCfg := billing.ConfigFromEnv()
billingGate := billing.NewMiddleware(billingCfg, billingStore)

// 只包装特定子路由
chargeableMux := http.NewServeMux()
chargeableMux.Handle("/api/v1/payments", billingGate.Wrap(paymentHandler.Router()))
chargeableMux.Handle("/api/v1/payments/", billingGate.Wrap(paymentHandler.Router()))
chargeableMux.Handle("/api/v1/payment-intents/", billingGate.Wrap(piMux))
chargeableMux.Handle("/api/v1/payment-intent-quotes", billingGate.Wrap(piQuoteMux))
chargeableMux.Handle("/api/v1/quotes/publish", billingGate.Wrap(quoteapiHandler.Router()))
chargeableMux.Handle("/api/v1/quotes/network", billingGate.Wrap(quoteapiHandler.Router()))

// 其余接口保持原样
rootMux.Handle("/api/v1/quotes", quoteapiHandler.Router())
rootMux.Handle("/api/v1/quotes/", quoteapiHandler.Router())
rootMux.Handle("/api/v1/settlement", settlementHandler.Router())
rootMux.Handle("/api/v1/settlement/", settlementHandler.Router())
// ...
```

> 注意：billing 接口自身、`/version`、`/health`、`/swagger`、SDK callback 端点均不包装。

---

## 10. 安全设计

1. **防重放**：
   - `X-Payment-Authorization` 必须包含一次性 `payment_id`。
   - 数据库记录已使用 `payment_id`，重复提交返回 `409 Conflict`。
   - 延迟结算签名包含 `created` 和 `expires` 时间戳，过期拒绝。
   - nonce 表定期清理过期记录。

2. **防篡改**：
   - 延迟结算采用 HTTP Message Signatures（与 Cloudflare 提案一致）。
   - 签名字段覆盖 `payment`、`@method`、`@target-uri`、`created`、`keyid`。
   - 402 响应中的价格由服务端私钥签名，客户端无法篡改金额或收款地址。

3. **请求绑定**：
   - 支付 proof 必须包含当前请求的请求体哈希（`request_body_hash`）。
   - 同一 proof 不能用于不同参数的请求，防止 proof 被复用。

4. **并发安全**：
   - 余额扣减采用数据库原子操作（`UPDATE ... WHERE balance >= amount`）。
   - 或通过行级锁（`BEGIN IMMEDIATE`）防止同一账户并发超扣。

5. **权限隔离**：
   - `BILLING_FREE_API_KEYS` 中的 key 直接放行。
   - 内部 SDK callback 端点永不收费。
   - billing 管理接口需单独的 `BILLING_ADMIN_API_KEYS`。

6. **最小资金风险**：
   - 默认关闭（`ENABLE_API_BILLING=false`）。
   - 即时结算依赖链上交易确认，可配置确认数（建议 L2 上 3-12 个）。
   - 延迟结算采用签名承诺，运营方按周期汇总后统一结算。
   - 支持账户欠费上限（credit limit），防止无限赊账。

7. **限流保护**：
   - 按 account 限流，防止异常或恶意调用耗尽资源。
   - 对 402 响应也计入限流，避免被用作免费探针。

---

## 11. 部署与验证

### 11.1 部署步骤

1. 配置 `.env` 中的 `BILLING_*` 变量。
2. 启动服务，billing 表随 `billing.NewSQLiteStore` 自动迁移。
3. 为需要付费的 API key 充值或设置免费额度。

### 11.2 验证用例

| 用例 | 预期结果 |
|------|---------|
| 未开启 billing 时调用任意接口 | 行为与现在完全一致 |
| 开启 billing，未付费调用 `POST /api/v1/payments` | 返回 `402 Payment Required` + 支付指令 |
| 白名单 API key 调用收费接口 | 直接返回业务响应 |
| 已充值账户调用收费接口 | 扣减余额并返回业务响应 |
| 重复提交同一 `payment_id` | 返回 `409 Conflict` |
| 篡改 402 响应中的价格后重试 | 返回 `403 Forbidden` |
| 用 A 请求的 proof 调用 B 请求 | 返回 `403 Forbidden` |
| 并发调用余额刚好够的账户 | 余额不会扣成负数 |
| 欠费超过 `BILLING_DEFERRED_CREDIT_LIMIT_USDC` | 返回 `402` 且不再接受延迟结算 |

---

## 12. 风险与后续优化

| 风险 | 缓解措施 |
|------|---------|
| 链上 Gas 成本高于微支付金额 | 主推延迟结算 + 批量上链；选择 Base/Arbitrum 等 L2 |
| 客户端无钱包支持 | 提供预充值账户模式：客户先法币转账，运营方手动/回调充值 |
| 价格波动 | 以 USDC 计价，与美元 1:1；必要时引入 USDT 等多币种 |
| 高频调用导致数据库压力 | nonce/余额/价格走内存缓存；定期批量落库 |
| 并发超扣 | 原子扣减或行级锁；设置账户级并发控制 |
| proof 跨请求复用 | 绑定请求体哈希；支付 proof 与 resource + payment_id 强关联 |
| 价格被篡改 | 服务端私钥签名 402 响应；客户端重试时必须携带原签名 |
| 合规风险 | 完整账单日志、不可删除、支持导出、记录法币等价金额 |
| 客户欠费逃逸 | 延迟结算设置信用上限；预充值账户要求先付款后使用 |
| API key 泄露导致盗刷 | 支持 key 紧急吊销；账单实时告警；绑定请求指纹 |

---

## 13. 灰度发布策略

避免一次性全量开启收费导致线上故障或客户中断：

1. **按环境**：先在 staging / testnet 验证完整流程。
2. **按 API key**：仅对 `BILLING_ENABLED_API_KEYS` 中的 key 开启收费，其他 key 保持免费。
3. **按端点**：先对影响面小的端点（如 `/payment-intent-quotes`）开启，再逐步扩大到 `/payments`。
4. **按比例**：支持 `BILLING_SHADOW_MODE=true` 仅记录应扣费金额但不实际扣费，用于评估影响。
5. **按金额**：初始阶段设置极低价格或 100% 免费额度，观察后再调整。

## 14. 测试策略

| 测试类型 | 覆盖内容 |
|---------|---------|
| 单元测试 | `Pricer` 各种定价规则、`Verifier` 签名验证、防重放、余额并发扣减 |
| 集成测试 | middleware 拦截顺序、402 响应格式、proof 校验通过后放行 |
| 并发测试 | 同一账户多 goroutine 同时调用，验证无超扣 |
| 安全测试 | 篡改价格、复用 proof、重放 nonce、过期签名 |
| e2e 测试 | 完整流程：未付费 → 402 → 支付/签名 → 200 → 账单查询 |
| 沙盒测试 | `BILLING_SANDBOX_MODE=true` 下使用测试网 token |

## 15. 运营与合规

1. **账单导出**：支持按 API key / 按月份导出 CSV/PDF，包含每笔 charge 的 payment_id、resource、amount、status、tx_hash。
2. **余额告警**：当账户余额低于阈值或欠费达到上限时，通过 webhook 或邮件通知。
3. **税务合规**：稳定币收入按法币等价入账，账单系统记录 USD 金额和结算时间。
4. **隐私保护**：不存储明文 API key，只存 hash；不存储用户钱包私钥。
5. **争议处理**：`billing_charges` 表不可删除，支持标记 `refunded` 状态并记录原因。
6. **审计追踪**：所有充值、扣费、退款、配置变更记录到 `billing_audit_log` 表。

## 16. 审计与优化建议总结

经过审计，原方案在以下方面需要加强：

1. **请求绑定**：支付 proof 必须与请求体哈希绑定，防止 proof 跨请求复用。
2. **价格签名**：402 响应中的价格和收款地址需要服务端签名，防止客户端篡改。
3. **并发安全**：余额扣减必须使用原子操作或行级锁，避免超扣。
4. **数据完整性**：增加 `billing_payment_offers`、`billing_topups`、`billing_audit_log` 等表，支持完整审计。
5. **灰度能力**：增加 shadow mode、按 API key 启用、按端点启用等灰度手段。
6. **缓存与限流**：增加余额/nonce 缓存和按账户限流，降低数据库压力并防止滥用。
7. **沙盒模式**：支持测试网和无真实扣费的沙盒环境，便于集成测试。
8. **合规与运营**：增加账单导出、余额告警、税务记录、争议处理等运营能力。
9. **延迟结算风控**：设置每个账户的延迟结算信用上限，防止无限赊账。
10. **错误细分**：区分 `402`（未付费）、`402.1`/`402.2`（余额不足/价格过期）、`403`（proof 无效）、`409`（重放）。

## 17. 一句话总结

> 在 my-provider 现有 router 外侧新增一个可开关、可灰度、带审计的 `billing` 中间件层，利用 x402 风格的 `402 Payment Required` + 稳定币/延迟结算机制，对指定 REST 端点按调用收费；所有新增代码独立成包，不改动现有业务逻辑，并具备请求绑定、价格签名、并发安全和完整运营审计能力。
