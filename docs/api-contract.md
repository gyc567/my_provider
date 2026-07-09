# my-provider API 合约文档

> 本文件从项目源码与 Swagger (`docs/swagger.json`) 整理而来，用于前端 `quote-mapper.ts` 和 `OfiT0Client.getQuotes()` 的实现。
>
> Swagger UI 地址：`https://api.agtpay.xyz/swagger/index.html`  
> OpenAPI JSON：`https://api.agtpay.xyz/swagger/doc.json`
>
> Swagger UI 中的 `APIDecimal` 已经正确渲染为 `{ unscaled: number, exponent: number }` 对象。本文件与 OpenAPI JSON 一致。

---

## 1. `getQuotes` endpoint 合约

项目里有两个名字相近但用途不同的接口，请按你的场景选择：

### 1.1 取本地已保存的报价快照

```text
GET /api/v1/quotes
```

| 位置 | 参数名 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| header | `Authorization` | string | 是 | `Bearer <apiKey>` |

- 无 query、无 path、无 body。
- 返回的是 provider 本地 SQLite 里存的 `payOut` / `payIn` 报价组，不是 t-0 网络实时价。

### 1.2 向 t-0 Network 取实时报价（推荐用于 `OfiT0Client.getQuotes()`）

```text
POST /api/v1/quotes/network
```

| 位置 | 参数名 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| header | `Authorization` | string | 是 | `Bearer <apiKey>` |
| body | `amount` | object | 是 | `{ unscaled: number, exponent: number }`，见 [Decimal](#decimal-表示) |
| body | `amountType` | string | 是 | enum: `pay_out`, `settlement` |
| body | `payOutCurrency` | string | 是 | ISO 4217 3 位大写，如 `EUR`、`GBP` |
| body | `payOutMethod` | string | 是 | t-0 枚举字符串，如 `PAYMENT_METHOD_TYPE_SEPA`、`PAYMENT_METHOD_TYPE_SWIFT` |

示例 body：

```json
{
  "amount": { "unscaled": 500, "exponent": 0 },
  "amountType": "settlement",
  "payOutCurrency": "GBP",
  "payOutMethod": "PAYMENT_METHOD_TYPE_SWIFT"
}
```

### 1.3 远程 pay-out push 端点（带幂等、限流、错误映射）

```text
POST /api/v1/quotes/pay-out
```

这是远程版本已经实现的生产级 pay-out 推送端点，**直接调 t-0 Network 的 `UpdateQuote`**，不经过本地 SQLite。

| 位置 | 参数名 | 类型 | 必填 | 说明 |
|---|---|---|---|---|
| header | `Authorization` | string | 是 | `Bearer <apiKey>` |
| header | `Idempotency-Key` | string | 否 | 可选幂等键；不传则根据 body hash 自动生成 |
| body | `groups` | array | 是 | 完整的 pay-out 报价快照（会原子替换上游已有快照） |

`groups[].UpdatePayOutGroup`：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `currency` | string | 是 | ISO 4217 3 位大写，业务白名单，如 `EUR`、`GBP` |
| `payment_method` | string | 是 | 短枚举：`SEPA`、`SWIFT`、`ACH`、`WIRE`、`FPS`、`G_CASH`、`INDIAN_BANK_TRANSFER`、`PESONET`、`INSTAPAY`、`PAKISTAN_BANK_TRANSFER`、`PAKISTAN_MOBILE_WALLET`、`PIX`、`AFRICAN_MOBILE_MONEY`、`CNAPS`、`NIP`、`M_PESA` |
| `expiration_seconds` | integer | 是 | 报价有效期秒数，范围 `[5, 300]` |
| `bands` | array | 是 | 报价档位 |

`bands[].UpdatePayOutBand`：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `client_quote_id` | string | 是 | 档位唯一 ID |
| `max_amount_usd` | string | 是 | 最大金额（USD），必须是 `1000`、`5000`、`10000`、`25000`、`250000`、`1000000` 之一 |
| `rate` | string | 是 | 汇率字符串，如 `"0.86"`；最多 8 位小数 |

示例 body：

```json
{
  "groups": [
    {
      "currency": "EUR",
      "payment_method": "SEPA",
      "expiration_seconds": 30,
      "bands": [
        {
          "client_quote_id": "eur-sepa-1k",
          "max_amount_usd": "1000",
          "rate": "0.86"
        }
      ]
    }
  ]
}
```

---

## 2. 成功响应 body schema

### 2.1 `GET /api/v1/quotes` 响应

```json
{
  "payOut": [QuoteGroup],
  "payIn": [QuoteGroup]
}
```

`QuoteGroup`：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `currency` | string | 是 | ISO 4217 3 位大写，如 `EUR` |
| `paymentMethod` | string | 是 | t-0 方法枚举字符串 |
| `expiration` | string | 是 | ISO 8601 / RFC3339，如 `2099-01-01T00:00:00Z` |
| `timestamp` | string | 是 | ISO 8601 / RFC3339 |
| `bands` | Band[] | 是 | 报价档位 |

`Band`：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `clientQuoteId` | string | 是 | 档位唯一 ID |
| `maxAmount` | Decimal | 是 | 该档位最大金额 |
| `rate` | Decimal | 是 | 汇率 |
| `fix` | Decimal \| null | 否 | 固定费用 |

### 2.2 `POST /api/v1/quotes/network` 响应

```json
{
  "result": {
    "success": { ... },
    "failure": { ... }
  },
  "allQuotes": [ProviderQuote]
}
```

`result.success`：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `rate` | Decimal | 是 | 汇率：settlement_currency / pay_out_currency |
| `expiration` | string | 是 | ISO 8601 / RFC3339 |
| `quoteId` | QuoteID | 是 | `{ quoteId: number, providerId: number }` |
| `payOutAmount` | Decimal | 是 | 出金币种金额 |
| `settlementAmount` | Decimal | 是 | 结算币种金额（USD） |

`result.failure`：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `reason` | string | 是 | `REASON_UNSPECIFIED` 或 `REASON_QUOTE_NOT_FOUND` |

`allQuotes[]`（ProviderQuote）：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `quoteId` | QuoteID | 是 | `{ quoteId: number, providerId: number }` |
| `rate` | Decimal | 是 | 汇率 |
| `expiration` | string | 是 | ISO 8601 / RFC3339 |
| `payOutAmount` | Decimal | 是 | 出金金额 |
| `settlement` | Settlement | 是 | 结算详情 |
| `executable` | boolean | 是 | 是否可立即执行 |

`Settlement`：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `amount` | Decimal | 是 | 所需结算金额 |
| `creditLimit` | Decimal | 是 | 总信用额度 |
| `totalUsed` | Decimal | 是 | 已用额度 |
| `prefundingAmount` | Decimal | 是 | 还需预充值金额 |

### 2.3 `POST /api/v1/quotes/pay-out` 响应

```json
{
  "status": "OK",
  "applied_at": "2099-01-01T00:00:00.000000000Z",
  "expires_at": "2099-01-01T00:00:30.000000000Z",
  "groups_published": 1,
  "bands_published": 1,
  "request_id": "req_xxx"
}
```

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `status` | string | 是 | 固定为 `"OK"` |
| `applied_at` | string | 是 | 应用时间，RFC3339Nano |
| `expires_at` | string | 是 | 最晚过期时间，RFC3339Nano |
| `groups_published` | integer | 是 | 发布的 group 数量 |
| `bands_published` | integer | 是 | 发布的 band 数量 |
| `request_id` | string | 否 | 请求 ID |

### 2.4 `GetQuoteResponse` 完整字段（来自 t-0 SDK 源码）

`POST /api/v1/quotes/network` 直接透传 t-0 的 `GetQuoteResponse`。下面是 SDK (`provider-sdk-go@v0.19.0`) 里的真实字段定义。

#### 根对象 `GetQuoteResponse`

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `result` | object | 是 | oneOf：只可能是 `success` 或 `failure` 中的一个 |
| `allQuotes` | `ProviderQuote[]` | 是 | 所有有授信额度的 provider 的最佳报价；即使 `result.failure` 也存在，可用于对比或查看不可执行的选项 |

#### `result.success`

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `rate` | Decimal | 是 | 汇率：`settlement_currency / pay_out_currency`（即 USD / 出金币种） |
| `expiration` | string | 是 | 报价过期时间，ISO 8601 / RFC3339 |
| `quoteId` | QuoteID | 是 | 报价唯一标识 |
| `payOutAmount` | Decimal | 是 | 如果使用该报价，实际出金币种金额 |
| `settlementAmount` | Decimal | 是 | 如果使用该报价，结算币种（USD）金额 |

#### `result.failure`

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `reason` | string | 是 | `REASON_UNSPECIFIED` 或 `REASON_QUOTE_NOT_FOUND` |

#### `QuoteID`

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `quoteId` | integer | 是 | 在该 provider 内的唯一报价 ID |
| `providerId` | integer | 是 | provider ID |

#### `ProviderQuote`（`allQuotes` 数组元素）

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `quoteId` | QuoteID | 是 | 可用于发起支付的报价 ID |
| `rate` | Decimal | 是 | 汇率：USD / 出金币种 |
| `expiration` | string | 是 | 报价有效期，ISO 8601 / RFC3339 |
| `payOutAmount` | Decimal | 是 | 出金币种金额 |
| `settlement` | Settlement | 是 | 结算/授信详情 |
| `executable` | boolean | 是 | `true` 表示可立即发起支付，无需预先充值 |

#### `Settlement`

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `amount` | Decimal | 是 | 该笔支付所需的结算金额（USD） |
| `creditLimit` | Decimal | 是 | 出金 provider 给出的总授信额度（USD） |
| `totalUsed` | Decimal | 是 | 已使用的授信额度（已完成 + 已预留） |
| `prefundingAmount` | Decimal | 是 | 支付前还需要额外充值的金额（`amount - max_executable`） |

#### `Decimal`

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `unscaled` | integer | 是 | 无缩放整数 |
| `exponent` | integer | 是 | 10 的指数 |

示例：

```json
{ "unscaled": 86, "exponent": -2 }   // 0.86
{ "unscaled": 500, "exponent": 0 }   // 500
```

### 关键问题回答

| 问题 | 答案 |
|---|---|
| `quoteId` / `id` 字段名 | `quoteId`，类型是对象 `{ quoteId: number, providerId: number }` |
| `rate` 是 string 还是 number | **都不是**，是 Decimal 对象 `{ unscaled, exponent }`。不要再 `String(quote.rate)` |
| `currency` 是枚举还是 ISO 字符串 | **ISO 4217 3 位大写字符串**。服务端只校验长度和大小写，不是枚举 |
| `expiresAt` / `expiration` 格式 | **ISO 8601 字符串**（RFC3339），不是 epoch ms |
| payout / local 金额字段名 | `payOutAmount` |
| settlement / usd 金额字段名 | `settlementAmount` |
| 返回单条还是多条 | `result.success` 是单条最佳报价；`allQuotes` 是多家 provider 的报价数组。你可以直接用 `result.success`，也可以把 `allQuotes` 透传给 UI |

### Decimal 表示

项目中所有金额/汇率都使用 t-0 的 `Decimal` 类型，JSON 形状为：

```json
{ "unscaled": 86, "exponent": -2 }   // 表示 0.86
{ "unscaled": 500, "exponent": 0 }   // 表示 500
```

还原公式：`value = unscaled * 10^exponent`。

---

## 3. 失败响应合约

### 3.1 本地 API 错误

- **策略**：直接用 HTTP status code 表示错误，不是“HTTP 200 + body error”。
- **Body**：

```json
{ "error": "human readable message" }
```

常见状态码：

| 状态码 | 场景 |
|---|---|
| 401 | 缺少或错误的 `Authorization` |
| 400 | JSON 非法或字段校验失败 |
| 500 | 数据库/发布失败 |
| 502 | 调用 t-0 network 失败 |

### 3.2 远程 pay-out push 端点错误

`POST /api/v1/quotes/pay-out` 的错误响应也是用 HTTP status code 表示，body 形状为：

```json
{
  "error": "invalid_request",
  "detail": "groups is required and must be non-empty",
  "request_id": "req_xxx"
}
```

常见状态码与错误码：

| 状态码 | `error` 字段值 | 场景 |
|---|---|---|
| 400 | `invalid_request` / `invalid_currency` / `invalid_payment_method` / `invalid_expiration` | 请求体校验失败 |
| 401 | `unauthorized` | `Authorization` 缺失或错误 |
| 409 | `idempotency_conflict` / `client_quote_id_conflict` | 幂等键冲突或 client_quote_id 重复 |
| 422 | `rejected_by_network` | 上游拒绝（如 unsupported band） |
| 502 | `upstream_error` | 上游网络/鉴权错误 |
| 504 | `upstream_timeout` / `upstream_canceled` | 调用上游超时 |

### 3.3 t-0 Network 业务失败

`POST /api/v1/quotes/network` 如果 t-0 返回业务失败，HTTP 状态码仍是 200，body 中：

```json
{
  "result": {
    "failure": {
      "reason": "REASON_QUOTE_NOT_FOUND"
    }
  }
}
```

当前 SDK 暴露的错误码字符串只有：

| 错误码字符串 |
|---|
| `REASON_UNSPECIFIED` |
| `REASON_QUOTE_NOT_FOUND` |

映射建议：
- `REASON_QUOTE_NOT_FOUND` → `NO_QUOTE_AVAILABLE`（也包含 limit 超限场景，按注释）
- `REASON_UNSPECIFIED` 或未识别 → `REASON_UPSTREAM_ERROR`

---

## 4. 认证方式

- **Header**：`Authorization: Bearer <apiKey>`
- **Token 来源**：`.env` 中的 `PROVIDER_API_KEYS`，多个 key 用逗号分隔
- **是否和 `HttpT0Client.updateQuote` 的 apiKey 相同**：
  - **不是同一个**。`Authorization: Bearer <key>` 是**本地 provider API** 的鉴权 key（`PROVIDER_API_KEYS`）。
  - `HttpT0Client.updateQuote` 如果直接调的是 t-0 Network 端点，用的是 t-0 网络层鉴权，两者概念不同。
- **是否需要 `Idempotency-Key`**：
  - `POST /api/v1/quotes/pay-out`：可选，推荐带上以防止重复提交。
  - 其他 `/api/v1/quotes/*` 端点：不需要。

---

## 5. 生成/刷新 Swagger

修改 API 后请执行：

```bash
cd /root/code/my_provider
/root/go/bin/swag init -g cmd/main.go
```

会重新生成 `docs/docs.go`、`docs/swagger.json`、`docs/swagger.yaml`。
