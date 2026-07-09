# 对外 GetQuote 接口设计方案 (产品层 MVP)

> 状态: 设计文档,待批准后实施

## Context

`t-0 Network` 的 SDK 协议里 `GetQuote` 是 `NetworkService`(sandbox 端)的 RPC,provider 端**不能也不应该实现它**——protocol 模型就是 B2B provider ↔ network,GetQuote 永远是 sandbox 找我们要数据。

但**产品侧需要**给公司内部 / 前端提供一个"获取报价"的 HTTP 接口。这是**新增的、与 SDK 协议无关的产品层功能**,需要:

1. 把当前 `PublishQuotes` 推出去的报价从内存里暴露出去
2. 提供一个简单的 GET 接口,前端按 (currency, payment_method, side, amount) 查询
3. 复用 8080 端口,不同前缀,不引入鉴权 / 计费 (MVP)

## 已确认的决策

| 维度 | 决策 |
|---|---|
| 调用方 | 公司内部 / 前端 |
| 数据来源 | 复用本服务 `PublishQuotes` 已推出去的数据(不调 sandbox) |
| 认证 | 不鉴权 (MVP) |
| 路径 | 复用 8080,不同前缀 |

## 设计要点

1. **存储报价** — 用 `sync.Map` 存最后一次 publish 出去的 quote 集合,key 是 `(currency, payment_method, side)` 元组。`PublishQuotes` 在发送给 sandbox **之前**写一次。
2. **查询 API** — `GET /api/quote?currency=EUR&payment_method=SEPA&side=pay_out&amount=500`,内部从 map 里按 currency+payment_method+side 找 best band,做 amount → currency 的换算,返回 JSON。
3. **数据暴露** — 不直接序列化 protobuf(`Timestamp` / `Decimal` / enum int 等格式对前端不友好),自己定义一个干净的 JSON DTO。
4. **挂载点** — `cmd/main.go` 里建一个新的 `http.ServeMux`,把 SDK mux 和我们的 `/api/quote` mux 都挂上去。零侵入 SDK。
5. **amount 计算** — sandbox 那边 rate 永远是 USD/XXX。
   - `pay_out` (off-ramp): 给 X USDT → 给 X * rate 的本地货币
   - `pay_in` (on-ramp): 给 X 本地货币 → 给 X / rate 的 USDT
6. **过期处理** — 报价有 `Expiration` 时间。`GET /api/quote` 时如果 `time.Now() > Expiration`,返回 `410 Gone` + 提示下一轮 publish 还没到。

## 关键文件

### 1. `internal/quotes/state.go` (新建)

报价状态的内存存储 + 简单查询函数。

```go
package quotes

type Side string

const (
    SidePayOut Side = "pay_out" // off-ramp: USDT → 本地货币
    SidePayIn  Side = "pay_in"  // on-ramp:  本地货币 → USDT
)

type Band struct {
    MaxAmount string // USD Decimal 字符串,避免 JSON 序列化时丢精度
    Rate      string // USD/XXX Decimal 字符串
}

type Quote struct {
    Currency      string
    PaymentMethod string
    Side          Side
    Bands         []Band       // 已按 MaxAmount 升序
    Expiration    time.Time
    Timestamp     time.Time
}

type Store struct {
    mu sync.RWMutex
    m  map[quoteKey]*Quote
}

type quoteKey struct {
    Currency      string
    PaymentMethod string
    Side          Side
}

// Update 替换一个 (currency, payment_method, side) 槽位。
func (s *Store) Update(q *Quote)

// Best 找到 amount <= first matching Band 的报价;若 amount 超过所有 band,返回最后一个。
// 同时检查 Expiration,过期返回 ErrExpired。
func (s *Store) Best(currency, paymentMethod string, side Side, amountUSD float64) (*Quote, Band, error)
```

### 2. `internal/publish_quotes.go` (改)

`PublishQuotes` 内部构造 `UpdateQuoteRequest` 之后、调用 `networkClient.UpdateQuote(...)` 之前,**多一步**把 PayOut 和 PayIn 两条 quote 写进 `quotes.Store`。

### 3. `internal/quotes/http.go` (新建)

REST handler。**单独建一个 ServeMux**,不碰 SDK 的 mux。

```go
package quotes

// Handler 返回一个挂好 GET /quote 的 *http.ServeMux
func Handler(s *Store) *http.ServeMux

// GET /quote?currency=EUR&payment_method=SEPA&side=pay_out&amount=500
// 返回:
//  200 {"currency":"EUR","side":"pay_out","payment_method":"SEPA","requested_usd":"500",
//       "rate":"0.86","quote_amount":"430.00","expires_at":"..."}
//  400 参数缺失/格式错
//  404 该 (currency, payment_method, side) 当前没报价(尚未 publish 或 sandbox 没接受)
//  410 报价已过期(等下一轮 publish)
//  500 内部错
```

JSON 字段用**字符串**承载 Decimal,前端用 string-based BigDecimal 解析(避免 IEEE 754 精度问题)。

### 4. `cmd/main.go` (改)

两件事:

- 启动时构造 `quotes.Store` 单例
- 启动时构造顶层 mux:**SDK handler 走 `/tzero.v1.payment.ProviderService/`,quotes handler 走 `/api/`**

具体做法——因为 `provider.NewHttpHandler` 返回 `http.Handler`(实现是 `*http.ServeMux`),我用一个**外层** mux 把 SDK handler 和 quotes handler 都挂上:

```go
sdkMux, err := provider.NewHttpHandler(...)          // 不变
qStore := quotes.NewStore()
quotesMux := quotes.Handler(qStore)

rootMux := http.NewServeMux()
rootMux.Handle("/tzero.v1.payment.ProviderService/", sdkMux)
rootMux.Handle("/api/", quotesMux)

provider.StartServer(rootMux, provider.WithAddr(...))  // 把 rootMux 喂给 SDK 的 server
```

零侵入 SDK——SDK mux 我不 type-assert、不修改它,只在外层包一个 mux。

## 实施步骤

1. **新建 `internal/quotes/state.go`**: `Store` + `Update` + `Best`,`sync.RWMutex` 保护
2. **新建 `internal/quotes/http.go`**: handler + ServeMux;`encoding/json` + `time.Time` ISO8601
3. **改 `internal/publish_quotes.go`**: 在 `publish()` 函数构造好 quote 对象后、写 `networkClient.UpdateQuote(...)` 之前,调 `qStore.Update(...)` 两条
4. **改 `cmd/main.go`**: 构造 store + quotesMux,组装 rootMux,喂给 StartServer
5. **build + vet**
6. **运行时验证**:
   - `curl http://localhost:8080/api/quote?currency=EUR&payment_method=SEPA&side=pay_out&amount=500` → 200 + JSON
   - `curl 'http://localhost:8080/api/quote?currency=EUR&payment_method=SEPA&side=pay_out'` (缺 amount) → 400
   - `curl 'http://localhost:8080/api/quote?currency=GBP&payment_method=SEPA&side=pay_out&amount=100'` → 404 (我们没 publish GBP/SEPA)
   - sandbox 通讯照旧:`Published quote: ...` 日志继续每 5 秒一次
   - ngrok URL 加上 `/api/quote?...` 也应直接可用(sandbox callback 路径不变)

## 不做的事

- **不**做鉴权 / API key / JWT(MVP)
- **不**做限流 / 计费
- **不**做持久化(报价本来就 30 秒过期,进程重启丢就丢)
- **不**做更多币种(只暴露 `PublishQuotes` 推出去过的)
- **不**做 sandbox GetQuote 代理(选了复用本服务数据)

## 风险

- `publish_quotes.go` 改完后如果某次 publish 失败,ticker 会重试,但 `qStore.Update` 不会被回滚——这意味着报价状态可能短暂反映"还没成功发出去"的报价。可接受:前端用户看到的价格和 sandbox 即将处理的价一致,失败重试期间 sandbox 那边看到的是上一次成功的报价,前端反而更新鲜。这是 MVP 行为,真实业务要加事件驱动重写。
- 报价竞态:Publisher 写、`HTTP GET` 读并发。`sync.RWMutex` 保护下不会撕裂;但读者可能看到 ticker 中间状态(刚更新 PayOut 还没更新 PayIn)。可接受,前端不会同时查两边。

## 验证

- `go build ./...` + `go vet ./...` 干净通过
- 重启服务后,日志同时包含:
  - `Published quote: ...` (Phase 1 不变)
  - `✅ Step 1.1: Provider server initialized on :8080` (启动)
- 三个 curl 用例分别返回 200 / 400 / 404,响应体内容符合预期
- ngrok URL 上 `/api/quote?currency=EUR&payment_method=SEPA&side=pay_out&amount=500` 同样可用

## API 示例

### 请求

```http
GET /api/quote?currency=EUR&payment_method=SEPA&side=pay_out&amount=500 HTTP/1.1
Host: localhost:8080
```

### 200 响应

```json
{
  "currency": "EUR",
  "payment_method": "SEPA",
  "side": "pay_out",
  "requested_usd": "500",
  "matched_band": {
    "max_amount": "1000",
    "rate": "0.86"
  },
  "quote_amount": "430.00",
  "published_at": "2026-07-08T15:30:00Z",
  "expires_at": "2026-07-08T15:30:30Z"
}
```

### 400 响应 (缺 amount)

```json
{
  "error": "missing required query parameter: amount"
}
```

### 404 响应 (该币种未发布)

```json
{
  "error": "no quote published for EUR/SEPA/pay_out"
}
```

### 410 响应 (报价过期)

```json
{
  "error": "quote expired; next publish in ~5s"
}
```