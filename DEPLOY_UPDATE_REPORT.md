# 生产环境更新报告 — api.agtpay.xyz

> 更新时间：2026-07-10（最新部署 01:58 UTC）  
> 更新内容：Caddy 透传 Swagger、新增 /version 与 /health 接口、deploy-local.sh 支持 systemd 模式

## 1. 代码变更

### 1.1 `cmd/main.go`

- 新增构建期变量：`BuildVersion`、`BuildCommit`、`BuildTime`、`BuildGoVersion`
- 新增 `GET /version`：返回版本、git commit、构建时间、Go 版本
- 新增 `GET /health`：返回运行状态与 commit
- `/swagger/*` 继续由 `http-swagger` 自动生成并提供

### 1.2 `scripts/deploy-local.sh`

- 构建时通过 `-ldflags` 注入 git commit 和构建时间
- 冒烟测试新增 `/version`、`/health` 可达性检查

### 1.3 `Dockerfile`

- 增加 `BUILD_VERSION`、`BUILD_COMMIT`、`BUILD_TIME` build args
- 构建时通过 `-ldflags` 注入版本信息

### 1.4 `/etc/caddy/Caddyfile`

移除静态 Swagger 文件服务：

```caddyfile
api.agtpay.xyz {
    # All traffic, including /swagger/*, is served by the Go provider service
    # so that Swagger docs are auto-generated from the latest source code.
    reverse_proxy localhost:8081
}
```

## 2. 生产部署过程

统一使用 systemd 模式部署：

```bash
./scripts/deploy-local.sh --systemd
```

该命令执行：
1. `go test ./...` 已通过
2. 构建生产二进制并注入 commit：`23c172c`
3. 安装到 `/usr/local/bin/my-provider`
4. 执行 `systemctl daemon-reload && systemctl restart my-provider.service`
5. 等待服务就绪
6. 运行 12 项冒烟测试

## 3. 生产验证结果

| 检查项 | URL | 结果 |
|--------|-----|------|
| systemd 状态 | `systemctl status my-provider.service` | active (running), PID 502328 ✅ |
| `/version` | `https://api.agtpay.xyz/version` | `{"version":"23c172c","commit":"23c172c","build_time":"2026-07-10T01:58:03Z","go_version":"go1.25.0"}` ✅ |
| `/health` | `https://api.agtpay.xyz/health` | `{"status":"ok","commit":"23c172c","version":"23c172c"}` ✅ |
| Swagger UI | `https://api.agtpay.xyz/swagger/` | 301 → index.html ✅ |
| Swagger doc (auto-generated) | `https://api.agtpay.xyz/swagger/doc.json` | 200 ✅ |
| 旧静态 openapi.yaml | `https://api.agtpay.xyz/swagger/openapi.yaml` | 404 ✅（已不再静态服务） |
| Quotes API | `GET /api/v1/quotes` | 200 ✅ |
| Settlement credits | `GET /api/v1/settlement/credits` | 200 ✅ |
| Settlement ledger | `GET /api/v1/settlement/ledger` | 200 ✅ |
| Payment-intent provider routing | `POST /api/v1/payment-intents/provider/999/confirm` | 404 ✅ |

## 4. 注意事项

Swagger 文档现在由 Go 应用自动生成，但**目前只包含已有 swaggo 注解的接口**（`/api/v1/quotes/*` 等）。新增的 `/api/v1/payments/*`、`/api/v1/settlement/*`、`/api/v1/payment-intents/*`、`/api/v1/payment-intent-quotes` 等接口虽然已部署并可调用，但尚未添加 swaggo 注解，因此不会出现在 Swagger UI 中。

如需完整 Swagger 文档，下一步需要为上述新 handler 补充 `// @Summary`、`// @Router` 等 swaggo 注释并重新生成 `docs/docs.go`。
