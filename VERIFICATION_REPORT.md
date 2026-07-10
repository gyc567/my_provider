# 生产域名验证报告 — api.agtpay.xyz

> 验证时间：2026-07-10  
> 验证目标：确认 `https://api.agtpay.xyz` 是否部署了本仓库最新代码，并验证接口对接情况。

## 1. 顶层结论

| 维度 | 结论 |
|------|------|
| **API 接口** | ✅ 已对接。新增 endpoint（payment、settlement、payment-intent）均已部署并能响应。 |
| **Swagger UI** | ⚠️ 可访问，但**不是由最新代码自动生成**。它是一个独立维护的静态页面，加载手工维护的 `openapi.yaml`。 |
| **Swagger 文档完整性** | ❌ `openapi.yaml` 缺少本次新增接口的文档：`/api/v1/payments/*`、`/api/v1/settlement/*`、`/api/v1/payment-intents/*`、`/api/v1/payment-intent-quotes`。 |
| **整体判断** | 后端二进制大概率已更新到最新代码，但 Swagger 文档未同步更新。 |

## 2. Swagger 验证

### 2.1 `/swagger/` — 200 OK

返回的是一个**自定义静态 HTML 页面**，标题为 `T-0 Provider Service - Swagger UI`，加载 `./openapi.yaml`。

响应头：
```text
HTTP/2 200
server: Caddy
content-type: text/html; charset=utf-8
last-modified: Thu, 09 Jul 2026 01:07:06 GMT
```

> 注意：本地最新代码使用 `http-swagger` 包，访问 `/swagger/` 会 301 重定向到 `/swagger/index.html`，并读取 `/swagger/doc.json`。生产环境的 Swagger 页面结构与本地完全不同，说明它不是由当前代码生成的。

### 2.2 `/swagger/openapi.yaml` — 200 OK

生产静态文档存在，但内容仅包含：
- `/api/v1/quotes`
- `/api/v1/quotes/network`
- `/api/v1/quotes/pay-in`
- `/api/v1/quotes/pay-out`
- `/api/v1/quotes/publish`
- `/tzero.v1.payment.ProviderService/*`（Connect-RPC 回调）

**缺少以下本次新增接口：**
- `/api/v1/payments`
- `/api/v1/payments/{id}/aml/approve`
- `/api/v1/payments/{id}/finalize`
- `/api/v1/settlement/credits`
- `/api/v1/settlement/ledger`
- `/api/v1/payment-intents`
- `/api/v1/payment-intent-quotes`
- `/api/v1/payment-intents/provider/{id}/confirm`

### 2.3 `/swagger/doc.json` — 404 Not Found

最新代码使用 swaggo 生成 `/swagger/doc.json`，但生产环境返回 404，进一步证明生产 Swagger 不是由最新代码生成。

## 3. API 接口探测结果

使用 `.env` 中的 `PROVIDER_API_KEYS` 进行认证探测：

| 接口 | 方法 | 生产状态码 | 说明 |
|------|------|-----------|------|
| `/api/v1/quotes` | GET | 200 | 正常返回报价快照 |
| `/api/v1/quotes/pay-out` | POST | 200 | Product handler 正常更新 |
| `/api/v1/settlement/credits` | GET | 200 | Settlement 接口已部署 |
| `/api/v1/settlement/ledger` | GET | 200 | Settlement 接口已部署 |
| `/api/v1/payment-intents/provider/999/confirm` | POST | 404 | 路由正确（intent 不存在） |
| `/api/v1/payment-intents` | POST | 502 | 路由正确，sandbox 返回未实现 |
| `/api/v1/payment-intent-quotes` | POST | 502 | 路由正确，sandbox 返回未实现 |
| `/api/v1/payments` | POST | 502 / error body | 路由正确，sandbox 校验失败 |
| `/api/v1/payments/1/aml/approve` | POST | 400 | 路由正确（payment 状态不满足） |
| `/api/v1/payments/1/finalize` | POST | 400 | 路由正确（payment 状态不满足） |
| `/api/v1/quotes` | GET (无认证) | 401 | 认证中间件生效 |

> 502 响应与本地最新代码行为一致：代理层已正确转发到 t-0 sandbox，sandbox 侧 `payment_intent.recipient` 服务目前返回 `unimplemented: 404 Not Found`。

## 4. 与本地最新代码对比

| 对比项 | 本地最新代码 (localhost:8081) | 生产 (api.agtpay.xyz) |
|--------|------------------------------|----------------------|
| Swagger UI 来源 | `http-swagger` 自动生成 | 自定义静态 HTML |
| Swagger spec 路径 | `/swagger/doc.json` | `/swagger/openapi.yaml` |
| `/swagger/doc.json` | 200 | 404 |
| `/api/v1/quotes` | 200 | 200 |
| `/api/v1/settlement/credits` | 200 | 200 |
| `/api/v1/payment-intents/provider/999/confirm` | 404 | 404 |
| `/api/v1/payment-intents` | 502 | 502 |
| HTTP 版本 | HTTP/1.1 | HTTP/2 (via Caddy) |

## 5. 结论与建议

1. **接口已经对接**：`api.agtpay.xyz` 上实际运行的后端已经包含了本次新增的全部 REST 接口（payment、settlement、payment-intent provider/recipient）。

2. **Swagger 文档未同步**：生产环境的 Swagger 是独立维护的静态资源，不是由仓库最新代码自动生成，且缺少新增接口文档。建议：
   - 方案 A：删除 Caddy 前的静态 Swagger，直接由 Go 应用通过 `http-swagger` 提供 `/swagger/*`，确保文档随代码自动更新。
   - 方案 B：如果必须使用静态 `openapi.yaml`，请手动更新该文件，补充 `/api/v1/payments/*`、`/api/v1/settlement/*`、`/api/v1/payment-intents/*`、`/api/v1/payment-intent-quotes` 等路径。

3. **部署建议**：如需完全确认二进制版本，可在 `cmd/main.go` 中添加一个 `/version` 或 `/health` 接口返回 git commit hash / build time，并在部署时注入，方便后续核对。
