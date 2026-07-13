# Agent Coding Rules

> 所有 AI 工具在为本项目生成、修改、测试代码前，必须阅读并遵守以下规则。

---

## 一、核心执行原则

### 1. 先想清楚再写代码
- 陈述假设，不确定就问，**杜绝猜测**。
- 写第一行代码前，把模糊指令转化为**可验证的成功标准**。
- 对需求有疑问时，先停下来确认，不要继续写。

### 2. 从最简方案入手
- 只写能解决问题的**最少代码**。
- 不加任何多余抽象、中间层或“未来可能需要”的代码。
- 遵循 **KISS** 原则，保持代码整洁。

### 3. 像手术一样精准修改
- 不碰与需求无关的代码。
- 每行改动都必须对应一条明确要求。
- 高内聚、低耦合，使用精简的设计模式。

### 4. 以目标驱动执行
- 所有任务必须有可验证的完成标准。
- 新增功能必须有测试，且测试必须能证明功能正确。
- 不能影响其他无关功能。

---

## 二、测试要求

1. **所有新增功能代码都要测试**，保证测试覆盖到核心路径。
2. 保留所有已有测试用例，**不得删除现有测试**。
3. 修改后必须运行相关测试，并给出测试报告。
4. 测试未通过前，不得标记任务完成。

---

## 三、Go 项目 AI Coding 规范

本项目采用三层体系，不引入不必要的框架。

```
HTTP / CLI / Job
       │
  Layer 3: Data
  go-playground/validator
       │
  Layer 2: Contract
  Validate() / ValidateInvariant()
       │
  Layer 1: Type
  Go Compiler + golangci-lint
       │
 Business Logic
```

### Layer 1：Type（编译期）

- 依赖 Go 编译器完成类型检查、interface、generic、nil、import、unused 等检查。
- 必须启用 `golangci-lint`，推荐开启以下 linter：
  - `staticcheck`
  - `errcheck`
  - `govet`
  - `unused`
  - `ineffassign`
  - `gosimple`

### Layer 2：Contract（业务契约）

- 每个领域对象必须实现 `Validate() error`。
- 复杂对象可增加 `ValidateInvariant() error`。
- 不引入 Go 契约框架（如 icontract），使用显式方法调用。

示例：

```go
type Order struct {
    Amount int
    Items  []Item
}

func (o Order) Validate() error {
    if o.Amount <= 0 {
        return ErrInvalidAmount
    }
    if len(o.Items) == 0 {
        return ErrEmptyItems
    }
    return nil
}
```

统一调用点：
- 创建对象后 → `Validate()`
- 修改后 → `Validate()`
- 保存/发布前 → `Validate()`

### Layer 3：Data（输入数据校验）

- 使用 `go-playground/validator/v10` 校验外部输入。
- 覆盖 HTTP、JSON、YAML、配置、LLM 输出等所有外部数据。

示例：

```go
type User struct {
    Email string `validate:"required,email"`
    Age   int    `validate:"gte=18"`
}

if err := validate.Struct(user); err != nil {
    return err
}
```

---

## 四、标准处理流水线

所有 Exported API 和业务入口统一遵循：

```
Request
  ↓
Bind (HTTP/JSON/YAML)
  ↓
go-playground/validator
  ↓
Domain.Validate()
  ↓
Business Logic
  ↓
Response
```

禁止：

```
JSON
  ↓
直接 Save()
```

---

## 五、函数签名规范

所有 Exported API 尽量统一为：

```go
func CreateUser(
    ctx context.Context,
    req CreateUserRequest,
) (*User, error)
```

- Context 为第一参数。
- 请求使用 struct。
- 返回结果和 error。

---

## 六、技术栈

| 目标 | 推荐工具 | 是否必需 |
| --- | --- | --- |
| 类型安全 | Go Compiler | ✅ |
| 静态分析 | golangci-lint（staticcheck / govet / errcheck） | ✅ |
| 数据校验 | go-playground/validator/v10 | ✅ |
| 业务契约 | `Validate()` / `ValidateInvariant()` | ✅ |
| 测试 | 标准库 `testing` + 表驱动测试 | ✅ |

---

## 七、架构分层参考（Clean Architecture）

参考 [bxcodec/go-clean-arch](https://github.com/bxcodec/go-clean-arch) 的 Clean Architecture 实践，本项目当前三层体系与其核心思想对应如下：

```
Delivery / Handler / CLI / Job  ← 外层，依赖内层
       │
  Usecase / Service               ← 业务编排，定义所需接口
       │
   Domain / Models                ← 核心业务对象与规则
       │
  Repository / Store              ← 接口由 Usecase 定义，具体实现可替换
```

### 7.1 分层原则

1. **依赖向内**：外层（Handler/Delivery）可以依赖内层（Usecase/Domain），内层**不得**依赖外层。
2. **接口定义在使用方**：Repository / Store / Client 等接口应由调用它的 Usecase/Service 定义，而不是由实现方定义。
3. **业务逻辑可独立测试**：Usecase 层单元测试不依赖真实数据库、HTTP 服务或外部 SDK。
4. **可替换实现**：今天用 SQLite，明天用 Postgres 或 mock，不应影响 Domain/Usecase 代码。

### 7.2 与现有代码的对应关系

| Clean Architecture | 本项目当前位置 | 说明 |
|-------------------|--------------|------|
| Domain/Models | `internal/payment/models.go`、`internal/quote/models.go` 等 | 领域对象、状态、值对象 |
| Usecase/Service | `internal/handler/payment.go` 中的业务编排 | 目前业务逻辑多在 handler，后续复杂业务应抽出独立 Service |
| Repository | `internal/payment/store.go`、`internal/quote/store.go` 等接口 | 建议接口由使用方（Service/Usecase）拥有 |
| Delivery | `internal/payment/api.go`、`internal/quoteapi/handlers.go` 等 | HTTP 入口，只做绑定、校验、调用内层 |

### 7.3 实施建议

- **新建功能**优先按 `Domain → Usecase → Repository → Delivery` 顺序思考，但**不要为了分层而分层**。
- 当某个 handler 中业务代码超过 100 行或涉及多个 store/外部调用时，应考虑抽出 `Service`/`Usecase`。
- 测试优先覆盖 Usecase/Domain，再用集成测试覆盖 Repository 和 Delivery。
- 保持 KISS：若功能简单（CRUD + 单次调用），handler 直接调用 store 仍然可接受，不必强行引入 Service 层。

---

## 八、禁止事项

- 不要为了“完整”或“可扩展”而增加不必要的层数。
- 不要为了 Clean Architecture 而过度抽象，导致简单功能需要跨越多个包。
- 不要引入生态不成熟或违反 Go 哲学的框架。
- 不要删除或绕过现有测试。
- 不要修改与需求无关的代码。
- 不要留下未验证的假设或未运行的代码。
- 不要让内层（Domain/Usecase）依赖外层（Repository 的具体实现、HTTP handler、SDK client）。

---

## 九、生产部署规则

### 9.1 统一使用 systemd 部署

生产环境必须统一通过 systemd 服务启动，禁止直接运行 `./scripts/deploy-local.sh`（nohup 模式）到生产环境，以避免出现并行进程或端口冲突。

标准生产部署命令：

```bash
./scripts/deploy-local.sh --systemd
```

该命令会：
1. 构建生产优化二进制并注入 git commit / build time
2. 安装到 `/usr/local/bin/my-provider`
3. 执行 `systemctl daemon-reload && systemctl restart my-provider.service`
4. 等待服务就绪并运行冒烟测试

### 9.2 服务管理

- 服务文件：`/etc/systemd/system/my-provider.service`
- 查看状态：`systemctl status my-provider.service`
- 查看日志：`journalctl -u my-provider.service -f`
- 停止服务：`./scripts/deploy-local.sh --systemd --stop`

### 9.3 本地开发

本地开发可使用 nohup 模式：

```bash
./scripts/deploy-local.sh
./scripts/deploy-local.sh --stop
```

---

## 十、一句话总结

> **先想清楚，再写最少且精准的代码；每层输入必须校验，每个功能必须测试；保持 KISS、高内聚、低耦合；分层向内依赖，接口由使用方定义。**
