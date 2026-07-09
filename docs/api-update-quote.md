# API: 推送 pay-out 报价流 (`POST /api/v1/quotes/pay-out`)

> 状态: 已审计修订版,待实施
>
> 把 SDK 的 `NetworkService.UpdateQuote`(向 t-0 Network sandbox 推 pay-out 报价流)封装成 REST 接口给前端客户调用。

---

## Context

`t-0 Network` 协议中,provider 通过 `tzero.v1.payment.NetworkService/UpdateQuote` 推送 pay-out 报价给 sandbox。该 RPC:

- 一次调用是**完整的原子快照**,整体替换 provider 当前 pay-out 报价集合
- 字段约束严格(`max_amount` 限定 6 个白名单值、`client_quote_id` 跨快照唯一)
- 频率由 provider 自行控制,推荐 1s-5s

**为什么需要产品层 HTTP 接口**:前端业务方(产品/客户)需要能自己控制推给 sandbox 的 pay-out 报价。本服务作为 provider 转发方,接收前端的报价数据,做校验后调用 SDK 上报 sandbox。

**职责划分:**

| 角色 | 责任 |
|---|---|
| 前端 | 收集报价数据(从自家定价系统/人工输入),HTTP 推给我方 |
| 我方(`my-provider`) | 鉴权、限流、校验、幂等、转发 SDK |
| Sandbox(`t-0 Network`) | 接收原子快照,做 quote routing |

---

## 与已有模块的关系

```
my-provider/
├── cmd/main.go                  # 挂载 /api/v1/ mux(本接口)
├── internal/
│   ├── api/                     # 新建:本接口所在包
│   ├── publish_quotes.go        # 改:可配置 PayOutDefault 开关(过渡期兼容)
│   ├── quotes/                  # 已有/未来:GetQuote 查询接口,与本接口并列
│   └── handler/payment.go       # 不动
├── docs/
│   ├── quote-api.md             # 不动:GetQuote 内部查询
│   └── api-update-quote.md      # 本文件
└── .env.example                 # 改:加 PROVIDER_API_KEYS / PUBLISH_PAY_OUT_DEFAULT
```

`/api/v1/` 与 `/api/quote`(GetQuote 内部查询)、`/tzero.v1.payment.ProviderService/`(SDK 回调)三个前缀共存于同一 8080 端口,使用 `rootMux` 包裹。

---

## 接口契约

### `POST /api/v1/quotes/pay-out`

#### 头部

| 头部 | 必填 | 说明 |
|---|---|---|
| `Authorization` | ✅ | `Bearer <api_key>`,从 `.env` 的 `PROVIDER_API_KEYS` 白名单里挑一个 |
| `Idempotency-Key` | 推荐 | string 1-128 字符。不传则按请求体 hash 去重(更弱) |
| `X-Request-Id` | 可选 | 任意字符串,响应里 echo 回去,便于排障 |
| `Content-Type` | ✅ | `application/json` |

#### 请求体

```json
{
  "groups": [
    {
      "currency": "EUR",
      "payment_method": "SEPA",
      "expiration_seconds": 30,
      "bands": [
        { "client_quote_id": "c-2026-07-08-abc1", "max_amount_usd": "1000",  "rate": "0.86" },
        { "client_quote_id": "c-2026-07-08-abc2", "max_amount_usd": "10000", "rate": "0.87" }
      ]
    },
    {
      "currency": "GBP",
      "payment_method": "SWIFT",
      "expiration_seconds": 30,
      "bands": [
        { "client_quote_id": "c-2026-07-08-gbp1", "max_amount_usd": "5000", "rate": "0.79" }
      ]
    }
  ]
}
```

> `Timestamp` 字段**前端不传**,handler 用 server clock 注入,防客户端时钟漂移。

#### 字段规则

| 字段 | 规则 | 来源/校验 |
|---|---|---|
| `groups` | 数组,1-50 个 group | 业务上限 |
| `currency` | ISO 4217,3 字母大写,白名单 `{EUR,GBP,BRL,USD,CAD,AUD,JPY,INR,MXN,CHF,SEK,NOK,DKK,SGD,HKD,NZD,KRW,CNY}` | 文档 "Quote group fields" |
| `payment_method` | 枚举:SEPA/SWIFT/ACH/WIRE/FPS/PIX/INDIAN_BANK_TRANSFER/INSTAPAY/PESONET/PAKISTAN_BANK_TRANSFER/PAKISTAN_MOBILE_WALLET/AFRICAN_MOBILE_MONEY/CNAPS/NIP,**拒绝 UNSPECIFIED** | `common.PaymentMethodType` |
| `expiration_seconds` | 整数,5 ≤ N ≤ 300 | 文档推荐 `now+cadence+grace` |
| `bands` | 数组,1-20 条;每条 group 内 `client_quote_id` 必须唯一 | 业务上限 |
| `client_quote_id` | string,1-64 字符;**跨快照 + TTL 窗口内全局唯一** | 文档显式要求 |
| `max_amount_usd` | string 形式 Decimal,**白名单**:`{1000, 5000, 10000, 25000, 250000, 1000000}` 之一 | 文档 §"Band fields" |
| `rate` | string Decimal,> 0,精度 ≤ 8 位小数 | 文档"USD/XXX" |

#### 成功响应 (200)

```json
{
  "status": "OK",
  "applied_at": "2026-07-08T10:15:30.123Z",
  "groups_published": 2,
  "bands_published": 3,
  "expires_at": "2026-07-08T10:16:00.123Z",
  "request_id": "req-abc-123"
}
```

#### 错误响应

所有错误响应用统一 envelope:
```json
{ "error": "<code>", "detail": "<human readable>", "request_id": "..." }
```

| HTTP | error code | 触发条件 | detail 示例 |
|---|---|---|---|
| 400 | `invalid_request` | JSON 解析失败 / 缺字段 / `groups` 空 / 类型错 | `"groups is required"` |
| 400 | `invalid_currency` | currency 不在白名单或非 3 字母大写 | `"currency=eur must be ISO 4217 uppercase"` |
| 400 | `invalid_payment_method` | payment_method 非法或 UNSPECIFIED | `"payment_method=FOO not supported"` |
| 400 | `invalid_expiration` | expiration_seconds 越界 | `"expiration_seconds=400 must be in [5,300]"` |
| 400 | `unsupported_band` | `max_amount_usd` 不在 6 值白名单 | `"max_amount=2000 not in [1000,5000,10000,25000,250000,1000000]"` |
| 400 | `invalid_rate` | rate 解析失败或 ≤ 0 | `"rate=abc: cannot parse decimal"` |
| 400 | `invalid_client_quote_id` | 长度 0 或 > 64 | `"client_quote_id length=65 must be in [1,64]"` |
| 400 | `duplicate_client_quote_id` | 同 group 内 band id 重复 | `"client_quote_id=c-abc1 appears twice in groups[0].bands"` |
| 401 | `unauthorized` | 缺/错的 API key | `"invalid or missing api key"` |
| 409 | `idempotency_conflict` | 同 `Idempotency-Key` TTL 内重放,body hash 不同 | `"idempotency key already used with different body"` |
| 422 | `rejected_by_network` | sandbox 拒收(典型:`unsupported band`) | `"upstream: unsupported band max_amount=2000"` |
| 429 | `rate_limited` | 超过 20 QPS / burst 40 per key | `"retry after 123ms"` |
| 502 | `upstream_error` | sandbox 5xx / 签名被拒等不可恢复 | `"network returned: <code>"` |
| 504 | `upstream_timeout` | sandbox 5s 内未响应 | `"upstream deadline exceeded"` |

#### 头部响应

- `X-Request-Id`: 始终返回,等于入参或生成的 UUID
- `Retry-After`: 429 时返回(秒)
- `Cache-Control`: 错误响应 `no-store`

---

## 关键设计决策

### 1. 双层快照(过渡期)

`t-0 Network` 每次 `UpdateQuote` **原子替换**整份 pay-out 快照。原来的 5s ticker(`internal/publish_quotes.go`)会推一条默认 pay-out 报价给 sandbox,会与本接口**互相覆盖**。

**过渡期方案**(`.env` 控制):

| `PUBLISH_PAY_OUT_DEFAULT` | 行为 |
|---|---|
| `true`(默认) | ticker 推默认 pay-out 报价(sandbox 路由能用),前端可继续推 pay-out 报价覆盖 |
| `false` | ticker 只推 pay-in,pay-out 完全由前端控制 |

**上线顺序**:前端稳定后,把 `.env` 改为 `false`。

### 2. 一次 HTTP 推多 groups(数组)

SDK `UpdateQuote` 接受 `PayOut: []*Quote`,即一次调用推多个 group。如果本接口设计成"一条 group 一次 HTTP",内部需要循环多次调 SDK,**后调用的 group 会冲掉前面的**(因为原子替换)。

**契约**:`groups` 必须是数组,handler 内部**只发一次** SDK 调用,把全部 group 打包进 `UpdateQuoteRequest.PayOut`。

### 3. Idempotency TTL = `max(2 × expiration, 60s)`

文档要求 `client_quote_id` "unique per provider across publishes",去重窗口必须 ≥ 报价存活周期。30s 太短,改为 `max(2 × max(expiration_seconds in request), 60s)`——覆盖一份完整报价的存活周期 + 一段重试窗口。

### 4. 鉴权

API key 白名单从 `.env` `PROVIDER_API_KEYS=key1,key2,key3` 读,**恒定时间比较**(`crypto/subtle.ConstantTimeCompare`),防计时攻击。

不接 OAuth/JWT/scope,MVP 不做多租户隔离。

### 5. 限流

每 API key 独立 token bucket:`golang.org/x/time/rate`,**20 QPS,burst 40**。超限 429 + `Retry-After`。

### 6. 错误映射(不能用 connect.Code 字符串匹配)

sandbox 的 `unsupported band` / `client_quote_id conflict` 错误格式可能变化。`error_mapping.go`:

| 现象 | 我们的 HTTP |
|---|---|
| `connect.CodeUnauthenticated` | 502(`signature` 类,理论不该发生) |
| `connect.CodeUnavailable` / `context.DeadlineExceeded` | 504 |
| 错误消息含 `"unsupported band"` | 422 + 透传 |
| 错误消息含 `"client_quote_id"` + conflict 语义 | 409 |
| 其它 `connect.Code` | 502 + 透传 code |

兜底逻辑:`errors.Is(err, context.DeadlineExceeded)` + `strings.Contains(err.Error(), "unsupported band")`。

### 7. 校验顺序

1. JSON 解析(失败 → 400 `invalid_request`)
2. 必填字段非空(失败 → 400 `invalid_request`)
3. `groups` 1-50 条(失败 → 400 `invalid_request`)
4. currency 白名单(失败 → 400 `invalid_currency`)
5. payment_method 白名单 + 拒 UNSPECIFIED(失败 → 400 `invalid_payment_method`)
6. expiration_seconds 5-300(失败 → 400 `invalid_expiration`)
7. bands 1-20 条,client_quote_id group 内唯一(失败 → 400 `duplicate_client_quote_id`)
8. 每条 band:`max_amount_usd` 在 6 值白名单(失败 → 400 `unsupported_band`)
9. 每条 band:`rate` 解析为正 Decimal(失败 → 400 `invalid_rate`)
10. 每条 band:`client_quote_id` 长度 1-64(失败 → 400 `invalid_client_quote_id`)
11. Idempotency 查重(命中且 body 一致 → 返回缓存;命中且 body 不一致 → 409)

校验在前,SDK 调用在后。**校验失败永远不打 sandbox**。

### 8. Timestamp 注入

handler 内强制 `Timestamp = timestamppb.New(time.Now())`,**不**让前端传(防客户端时钟漂移导致 sandbox 时间错乱)。

### 9. SDK `Band.Fix` 字段

**当前 SDK v0.19.0 中 `Band` 实际只有 3 个字段**:`ClientQuoteId` / `MaxAmount` / `Rate`,**无 `Fix`**。文档提到 Fix 但 SDK 未生成。本接口**暂不实现** `Fix`,等 SDK 升级后 handler 可后向兼容(见到 unknown field 透传)。

---

## 整体架构

```
┌──────────────┐  POST /api/v1/quotes/pay-out  ┌──────────────────────────┐
│  前端/客户   │ ──────────────────────────▶   │  net/http ServeMux       │
└──────────────┘  Bearer + Idempotency-Key     │  (api.NewRouter)         │
                                                └──────────┬───────────────┘
                                                           │
                                                ┌──────────▼───────────────┐
                                                │  Middleware chain        │
                                                │  - RequestID             │
                                                │  - Recover (panic→500)   │
                                                │  - Auth (API key)        │
                                                │  - RateLimit 20/40       │
                                                │  - Body size limit 64KB  │
                                                └──────────┬───────────────┘
                                                           │
                                                ┌──────────▼───────────────┐
                                                │  Validate (DTO → struct) │
                                                │  - schema                │
                                                │  - Decimal parse         │
                                                │  - max_amount 白名单     │
                                                │  - payment_method 白名单 │
                                                └──────────┬───────────────┘
                                                           │
                                                ┌──────────▼───────────────┐
                                                │  Idempotency check       │
                                                │  (sync.Map, TTL 滑动)    │
                                                │  - 命中且 body 一致→缓存 │
                                                │  - 命中且 body 不一致→409│
                                                └──────────┬───────────────┘
                                                           │
                                                ┌──────────▼───────────────┐
                                                │  Build UpdateQuoteReq    │
                                                │  PayOut = [groups...]    │
                                                │  PayIn  = nil            │
                                                │  Timestamp = now()       │
                                                │  (1 次 SDK call)         │
                                                └──────────┬───────────────┘
                                                           │
                                                ┌──────────▼───────────────┐
                                                │  sandbox UpdateQuote     │
                                                │  (5s timeout)            │
                                                └──────────┬───────────────┘
                                                           │
                                                ┌──────────▼───────────────┐
                                                │  Error mapping           │
                                                │  - 解析 upstream msg     │
                                                │  - 写 audit log          │
                                                │  - 记录 metric           │
                                                └──────────────────────────┘
```

---

## 文件结构

```
my-provider/
├── internal/
│   ├── api/                                  ← 新建
│   │   ├── server.go                         ← 路由工厂,中间件串联
│   │   ├── middleware_auth.go                ← API key 校验
│   │   ├── middleware_ratelimit.go           ← golang.org/x/time/rate
│   │   ├── middleware_requestid.go           ← X-Request-Id 注入
│   │   ├── middleware_recover.go             ← panic→500
│   │   ├── middleware_maxbody.go             ← 64KB 上限
│   │   ├── handler_update_pay_out.go         ← 业务主流程
│   │   ├── dto.go                            ← 请求/响应结构
│   │   ├── validate.go                       ← 业务校验
│   │   ├── validate_test.go
│   │   ├── decimal.go                        ← string ↔ common.Decimal
│   │   ├── decimal_test.go
│   │   ├── idempotency.go                    ← 滑动窗口去重
│   │   ├── idempotency_test.go
│   │   ├── error_mapping.go                  ← connect error → HTTP
│   │   ├── error_mapping_test.go
│   │   └── http_test.go                      ← 集成测试 (httptest)
│   ├── publish_quotes.go                     ← 改:可配置 PayIn only
│   └── handler/payment.go                    ← 不动
├── cmd/
│   └── main.go                               ← 改:挂载 api router
├── docs/
│   ├── quote-api.md                          ← 不动
│   └── api-update-quote.md                   ← 本文件
├── .env.example                              ← 改:加 PROVIDER_API_KEYS,PUBLISH_PAY_OUT_DEFAULT
└── go.mod                                    ← 改:+ golang.org/x/time
```

**依赖增量**:仅 `golang.org/x/time/rate`(已是大版本 Go stdlib 周边)。**不引入 web 框架**,纯 `net/http`,与项目其它 handler 一致。

---

## 关键实现细节

### 1. Decimal 解析(`internal/api/decimal.go`)

```go
// ParseDecimal 解析 string → common.Decimal
// 规则:精度 ≤ 8 位小数,值必须 > 0
func ParseDecimal(s string) (common.Decimal, error)

// String 反向
func String(d *common.Decimal) string
```

**实现**用 `math/big.Rat` 解析 + 截断到 8 位小数,再转 `Unscaled int64` + `Exponent int32`。**绝不用 `float64`**(与 `phase3-payment-intent.md` §"Decimal helpers" 一致)。

### 2. Idempotency 窗口(`internal/api/idempotency.go`)

```go
type IdempotencyRecord struct {
    BodyHash [32]byte  // SHA-256 of canonical JSON
    Response []byte
    Status   int
    Expires  time.Time
}

type Store struct {
    mu   sync.RWMutex
    recs map[string]*IdempotencyRecord  // key = apiKey+idemKey
}

func NewStore() *Store
func (s *Store) Lookup(scope, idemKey string) (*IdempotencyRecord, bool)
func (s *Store) Save(scope, idemKey string, bodyHash [32]byte, status int, body []byte, ttl time.Duration)
// GC:每 5 分钟一次
func (s *Store) GC() int  // 返回清理数
```

TTL = `max(2 × maxExpirationSeconds, 60s)`。

### 3. 错误映射(`internal/api/error_mapping.go`)

```go
type APIError struct {
    HTTPStatus int
    Code       string
    Detail     string
}

func MapError(err error) APIError {
    if err == nil { return APIError{HTTPStatus: 200, Code: "OK"} }
    
    // context.DeadlineExceeded / context.Canceled
    if errors.Is(err, context.DeadlineExceeded) { return APIError{504, "upstream_timeout", err.Error()} }
    if errors.Is(err, context.Canceled)         { return APIError{504, "upstream_canceled", err.Error()} }
    
    var connErr *connect.Error
    if errors.As(err, &connErr) {
        msg := connErr.Error()
        if strings.Contains(msg, "unsupported band") { return APIError{422, "rejected_by_network", msg} }
        if strings.Contains(msg, "client_quote_id") { return APIError{409, "client_quote_id_conflict", msg} }
        switch connErr.Code() {
        case connect.CodeUnauthenticated: return APIError{502, "upstream_error", msg}
        case connect.CodeUnavailable:     return APIError{502, "upstream_unavailable", msg}
        case connect.CodeDeadlineExceeded: return APIError{504, "upstream_timeout", msg}
        }
        return APIError{502, "upstream_error", fmt.Sprintf("code=%s msg=%s", connErr.Code(), msg)}
    }
    return APIError{502, "upstream_error", err.Error()}
}
```

### 4. Handler 主流程(`internal/api/handler_update_pay_out.go`)

```go
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    // 1. 解析 JSON → DTO
    var req UpdatePayOutRequest
    if err := json.NewDecoder(io.LimitReader(r.Body, h.maxBody)).Decode(&req); err != nil {
        writeError(w, 400, "invalid_request", err.Error(), requestID(r))
        return
    }
    
    // 2. 校验
    if err := req.Validate(); err != nil {
        writeError(w, err.HTTPStatus, err.Code, err.Detail, requestID(r))
        return
    }
    
    // 3. Idempotency
    idemKey := r.Header.Get("Idempotency-Key")
    if idemKey == "" { idemKey = hashCanonicalJSON(req) }
    bodyHash := sha256.Sum256(canonicalJSON(req))
    if rec, ok := h.idem.Lookup(apiKeyFromCtx(r), idemKey); ok {
        if rec.BodyHash != bodyHash {
            writeError(w, 409, "idempotency_conflict", "idempotency key already used with different body", requestID(r))
            return
        }
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(rec.Status)
        w.Write(rec.Response)
        return
    }
    
    // 4. 构造 UpdateQuoteRequest
    sdkReq := req.ToSDKRequest()
    
    // 5. SDK 调用
    ctx, cancel := context.WithTimeout(r.Context(), h.upstreamTimeout)
    defer cancel()
    _, err := h.networkClient.UpdateQuote(ctx, connect.NewRequest(sdkReq))
    
    // 6. 错误映射
    if err != nil {
        apiErr := MapError(err)
        writeError(w, apiErr.HTTPStatus, apiErr.Code, apiErr.Detail, requestID(r))
        return
    }
    
    // 7. 写成功响应 + 缓存幂等记录
    resp := UpdatePayOutResponse{...}
    body, _ := json.Marshal(resp)
    ttl := max(2*maxExpiration(req), 60*time.Second)
    h.idem.Save(apiKeyFromCtx(r), idemKey, bodyHash, 200, body, ttl)
    
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(200)
    w.Write(body)
}
```

### 5. 鉴权(`internal/api/middleware_auth.go`)

```go
func AuthMiddleware(validKeys []string) func(http.Handler) http.Handler {
    set := make(map[string]struct{}, len(validKeys))
    for _, k := range validKeys { set[k] = struct{}{} }
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w, r) {
            h := r.Header.Get("Authorization")
            const prefix = "Bearer "
            if !strings.HasPrefix(h, prefix) {
                writeError(w, 401, "unauthorized", "missing bearer token", requestID(r))
                return
            }
            key := h[len(prefix):]
            if !containsKey(set, key) {
                writeError(w, 401, "unauthorized", "invalid api key", requestID(r))
                return
            }
            ctx := context.WithValue(r.Context(), ctxKeyAPIKey{}, key)
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}

// 恒定时间比较
func containsKey(set map[string]struct{}, key string) bool {
    // 找到精确匹配,再用 subtle.ConstantTimeCompare
    found := ""
    for k := range set { if subtle.ConstantTimeCompare([]byte(k), []byte(key)) == 1 { found = k; break } }
    return found != ""
}
```

### 6. 限流(`internal/api/middleware_ratelimit.go`)

每 API key 一个 `*rate.Limiter`,从 ctx 拿 key。
```go
limiter := rate.NewLimiter(rate.Limit(20), 40)
if !limiter.Allow() {
    w.Header().Set("Retry-After", "1")
    writeError(w, 429, "rate_limited", "...", requestID(r))
    return
}
```

### 7. 与 `internal/publish_quotes.go` 协同

```go
// internal/publish_quotes.go 改造
type PublishConfig struct {
    PayOutDefault bool  // true:推默认 pay-out(过渡期);false:ticker 只推 pay-in
}

func PublishQuotes(ctx context.Context, networkClient paymentconnect.NetworkServiceClient, cfg PublishConfig) {
    // ... 构造 req ...
    if !cfg.PayOutDefault {
        req.PayOut = nil
    }
    // PayIn 永远推
    networkClient.UpdateQuote(ctx, connect.NewRequest(req))
}
```

---

## `.env` 改动

```bash
# 现有
PROVIDER_PRIVATE_KEY=...
PROVIDER_PUBLIC_KEY=...
NETWORK_PUBLIC_KEY=...
TZERO_ENDPOINT=...
PORT=8080

# 新增
PROVIDER_API_KEYS=key1,key2,key3           # 逗号分隔,前端用其中一个
PUBLISH_PAY_OUT_DEFAULT=true               # 过渡期:true=继续推默认;false=ticker 只推 PayIn
```

`.env.example` 同步更新。

---

## 测试策略

| 层 | 用例 | 工具 |
|---|---|---|
| **单元** Decimal | round-trip、各种 exponent、边界(±0、极大、极小、负数、非法输入) | table-driven |
| **单元** Validate | bands 空、max 不在白名单、rate=0、currency=eur(小写)、payment_method=UNSPECIFIED | table-driven |
| **单元** Idempotency | 100 goroutine 同 key 并发,只 1 个真正调用;body hash 改变返 409;TTL 过期清掉 | `-race` |
| **单元** RateLimit | 50 次连续调用,前 40 通后续 429 | `-race` |
| **单元** Auth | 空 header、错 key、暴力破解(constant time) | std |
| **单元** ErrorMapping | 各 connect.Code + 错误 msg 串 → 期望 HTTP | table |
| **集成** handler | 200 / 400 / 401 / 409 / 422 / 504 全覆盖 | httptest + 假 SDK client |
| **E2E** | sandbox 真实环境:curl → 我们 → sandbox 日志确认原子替换 | 人工 |

**覆盖率目标**:`internal/api/` ≥ 80%(符合 `rules/testing.md`)。

---

## `cmd/main.go` 改动

```go
// 现状:1 个 SDK mux,1 个 quotes mux(未来)
// 改造:加 1 个 api mux
func main() {
    config := loadConfig()                      // + APIKeys, PublishPayOutDefault 字段
    
    networkClient := initNetworkClient(config)  // 不动
    
    qStore := quotes.NewStore()                 // quotes API(若已实现)
    
    // 新增
    apiMux := api.NewRouter(api.Deps{
        NetworkClient:        networkClient,
        APIKeys:              config.APIKeys,
        MaxBodyBytes:         64 << 10,
        RequestsPerSecond:    20,
        Burst:                40,
        UpstreamTimeout:      5 * time.Second,
        IdempotencyTTLMin:    60 * time.Second,
    })
    
    // 顶层 mux
    rootMux := http.NewServeMux()
    rootMux.Handle("/tzero.v1.payment.ProviderService/", sdkHandler)
    rootMux.Handle("/api/v1/", apiMux)                     // 新增
    rootMux.Handle("/api/quote", quotes.Handler(qStore))   // 已有/未来
    
    shutdownFunc, _ := provider.StartServer(rootMux, provider.WithAddr(config.ServerAddr))
    
    // 启动 ticker
    go internal.PublishQuotes(ctx, networkClient, internal.PublishConfig{
        PayOutDefault: config.PublishPayOutDefault,
    })
}
```

---

## 实施步骤(项目可随时编译)

1. **`internal/api/decimal.go`** + `decimal_test.go` — 双向转换
2. **`internal/api/validate.go`** + `validate_test.go` — schema 校验
3. **`internal/api/idempotency.go`** + `idempotency_test.go` — 滑窗去重
4. **`internal/api/error_mapping.go`** + `error_mapping_test.go` — connect error 映射
5. **`internal/api/middleware_*.go`** + 对应测试 — auth / ratelimit / requestid / recover / maxbody
6. **`internal/api/dto.go`** — JSON 结构
7. **`internal/api/handler_update_pay_out.go`** — 业务主流程
8. **`internal/api/server.go`** + `http_test.go` — 路由 + 集成测试
9. **`cmd/main.go`** — 挂载 + 改 ticker 签名
10. **`internal/publish_quotes.go`** — 接受 `PublishConfig{PayOutDefault bool}`
11. **`.env.example`** — 改
12. **`docs/api-update-quote.md`** — 本文档
13. **build + vet + race + cover**:
    ```bash
    go build ./... && go vet ./... && \
      go test -race -count=1 ./... && \
      go test -cover ./internal/api/...
    ```

**预估工作量:** ~7 小时(原方案 +1h,加 idempotency 改造、error_mapping 单元测试、ticker 配置化)。

---

## 风险与决策

| # | 风险/决策 | 决策 |
|---|---|---|
| R1 | 过渡期 PayOut 默认报价是否保留 | 保留(`.env` 控制),前端上线完手动 `false` |
| R2 | 一次 HTTP 推多 groups vs 多 group 串行 | **多 groups(数组),一次 SDK 调用**——否则后调用的 group 冲掉前面的 |
| R3 | Idempotency TTL | `max(2 × expiration, 60s)`,与报价存活周期匹配 |
| R4 | `Band.Fix` 字段 | 暂不实现,等 SDK 加上再补 |
| R5 | 鉴权方式 | API key + 恒定时间比较;OAuth/JWT 推迟 |
| R6 | 限流档位 | 20 QPS / burst 40 / key——若不够再调 |
| R7 | 心跳保活(前端不调时 ticker 兜底) | 暂不做,文档列 TODO |
| R8 | sandbox `unsupported band` 错误格式可能变 | 错误映射用 `strings.Contains` 兜底,加监控告警 |
| R9 | 进程重启丢失 idempotency 记录 | 接受;幂等窗口内丢失 → sandbox 报 client_quote_id 冲突,我们返 409 让前端重试 |
| R10 | Decimal 解析精度 | 用 `math/big` + 字符串解析,绝不用 `float64` |

---

## 明确推迟(不做)

- OAuth/JWT/scope 鉴权
- 多租户配额、计费
- 持久化(idempotency、audit log) — MVP 内存
- PayIn 接口(对称) — 等 PayOut 稳定再做
- 心跳保活(前端不调时 ticker 兜底 PayOut)
- SDK `Fix` 字段(等 SDK 升级)
- Prometheus `/metrics` 端点 — MVP `expvar` 即可
- gzip 请求体支持
- 审计日志(单独 JSONL 文件)

---

## 验证

### 编译 + 静态分析

```bash
cd /Users/eric/dreame/code/my-provider
go build ./...
go vet ./...
go test -race -count=1 ./...
go test -cover ./internal/api/...   # expect >=80%
```

### 沙箱冒烟测试

```bash
export $(grep -v '^#' .env | xargs)
go run ./cmd
# 期望日志:
#   "Provider server initialized on :8080"
#   "Published quote: EUR/SEPA off-ramp=0.86 on-ramp=0.88" 每 5s
```

#### 正常推送

```bash
curl -X POST http://localhost:8080/api/v1/quotes/pay-out \
  -H "Authorization: Bearer key1" \
  -H "Idempotency-Key: $(uuidgen)" \
  -H "X-Request-Id: req-001" \
  -H "Content-Type: application/json" \
  -d '{
    "groups": [{
      "currency": "EUR",
      "payment_method": "SEPA",
      "expiration_seconds": 30,
      "bands": [
        {"client_quote_id":"c-test-1","max_amount_usd":"1000","rate":"0.86"},
        {"client_quote_id":"c-test-2","max_amount_usd":"10000","rate":"0.87"}
      ]
    }]
  }'
# 期望:200 {"status":"OK","groups_published":1,"bands_published":2,...}
```

#### 错误用例

```bash
# 缺 API key
curl -X POST ... → 401 unauthorized

# max_amount 不在白名单
... -d '{"groups":[{"currency":"EUR","payment_method":"SEPA","expiration_seconds":30,"bands":[{"client_quote_id":"c1","max_amount_usd":"2000","rate":"0.86"}]}]}'
# 400 unsupported_band "max_amount=2000 not in [1000,5000,10000,25000,250000,1000000]"

# rate 非法
... -d '{"groups":[...,"rate":"abc"]}'
# 400 invalid_rate

# 重复 client_quote_id(同请求内)
... -d '{"groups":[{...,"bands":[{"client_quote_id":"c1",...},{"client_quote_id":"c1",...}]}]}'
# 400 duplicate_client_quote_id

# Idempotency 冲突(同 key 不同 body)
curl -X POST ... -H "Idempotency-Key: k1" -d '<body A>'   # 200
curl -X POST ... -H "Idempotency-Key: k1" -d '<body B>'   # 409 idempotency_conflict

# Idempotency 重放(同 key 同 body)
curl -X POST ... -H "Idempotency-Key: k1" -d '<body A>'   # 200 cached
```

#### 沙箱验证

通过 sandbox 端日志或 admin 工具确认:
- 我们发出的 `UpdateQuote` RPC 中 `PayOut` 字段是前端提交的内容(完整 group 列表)
- 5s 后 ticker 推送时,如果 `PUBLISH_PAY_OUT_DEFAULT=false`,`PayOut` 字段为空(我们主动撤回)
- 若 `PUBLISH_PAY_OUT_DEFAULT=true`,ticker 会把前端的报价**冲掉**(过渡期行为,需协调)

---

## 审计记录(对原方案的修正)

| # | 原方案问题 | 修订 |
|---|---|---|
| **A1** | `Band.Fix` 字段在原方案出现 | SDK v0.19.0 中 `Band` 实际只有 3 个字段,删除 Fix |
| **A2** | `client_quote_id` 30s 滑窗去重 | 改成 TTL = `max(2 × expiration_seconds, 60s)` 滑窗 |
| **A3** | `max_amount` 列出 4 个值 | 改 6 个值白名单:`1000/5000/10000/25000/250000/1000000` |
| **A4** | `payment_method` 校验 | 明确拒绝 `PAYMENT_METHOD_TYPE_UNSPECIFIED = 0`,加白名单映射 |
| **A5** | 接口契约"一次只一条 group" | **硬伤,已修正**:sandbox 每次 `UpdateQuote` 原子替换整份快照。必须一次 HTTP 调用上传多 groups(数组),内部只发一次 SDK 调用 |
| **A6** | R1 风险描述"前端不发就无 PayOut 报价" | 改为"sandbox 看到的是 ticker 上次推的或过期空集",加"前端心跳保活"机制(TODO) |
| **A7** | 缺 `Timestamp` 字段 | 补上,handler 用 server clock 注入,不接受前端传 |
| **A8** | `QuoteType` 没说 | 补:仅 `QUOTE_TYPE_REALTIME` 合法,SDK 内部固定,前端不传 |
| **A9** | 限流 5 QPS,burst 10 | 提高到 20 QPS,burst 40(per-key) |
| **A10** | "Phase 2 默认双侧都推,改完变只推 pay-in" 表述不严谨 | 改为:`PublishPayOutDefault` 配置开关,默认 `true`(过渡期),上线前端后再 `false` 关掉 |
| **A11** | sandbox 错误码 `CodeInvalidArgument` 提到 max_amount 非法 | 实际 sandbox 用 `unsupported band` 错误,不是标准 gRPC code。**handler 不能依赖 `connect.Code` 字符串匹配**,最终方案用 `errors.Is` + `strings.Contains` 兜底 |
| **A12** | 缺 timestamp 字段处理 | 补:handler 用 server clock 生成 `Timestamp = now()`,不让前端传 |
