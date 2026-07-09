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

## 七、禁止事项

- 不要为了“完整”或“可扩展”而增加不必要的层数。
- 不要引入生态不成熟或违反 Go 哲学的框架。
- 不要删除或绕过现有测试。
- 不要修改与需求无关的代码。
- 不要留下未验证的假设或未运行的代码。

---

## 八、一句话总结

> **先想清楚，再写最少且精准的代码；每层输入必须校验，每个功能必须测试；保持 KISS、高内聚、低耦合。**
