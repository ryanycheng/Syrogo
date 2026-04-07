# Syrogo

Syrogo 是一个面向多模型场景的 AI Gateway / Semantic Router。

它的目标不是简单转发单一模型接口，而是提供一个清晰可扩展的中间层，用来承接：
- 多种客户端入口协议
- 基于 tag 的路由决策
- 多上游 provider 接入
- failover / round_robin 等基础调度策略
- 后续的额度切换、统计、治理与多节点串接能力

当前仓库仍处于 0→1 骨架建设阶段，但已经具备最小可运行闭环。

---

## 名字由来

`Syrogo` 这个名字目前的理解，可以拆成三层意象：

- `Synapse / 神经元连接`：强调连接、传递、调度
- `Router`：强调它不是单纯代理，而是要做识别、路由、切换与分发
- `Go`：直接对应项目当前的实现技术栈

所以 `Syrogo` 想表达的，不只是一个模型接口转发器，而是一个用 Go 构建的、面向多模型调用链路的路由中枢。

---

## 当前已实现能力

当前版本已经支持：

- Go HTTP 服务启动与优雅退出
- 配置加载与校验
- `GET /healthz`
- 两种 inbound protocol
  - `openai_chat`
  - `anthropic_messages`
- 统一转换到内部中立模型 `runtime.Request / Response / StreamEvent`
- 基于 `client token -> active tag` 的入口识别
- 基于 `routing.rules` 的首条规则命中
- 单条规则内的：
  - `failover`
  - `round_robin`
- 两种 outbound protocol
  - `mock`
  - `openai_chat`
- OpenAI-compatible 上游调用
- 基础流式 SSE 返回
- 单元测试、回归测试与关键链路测试

---

## 当前不在范围内

当前阶段还**不追求**：

- 复杂插件系统
- gRPC / MCP / WebSocket 等额外接入方式
- 完整 semantic routing 能力
- 完整 Anthropic 上游透传
- tool use / multimodal / function calling 全量支持
- 对外 Go SDK 或 `pkg` 级公共库抽象

当前重点是：

**先把服务骨架、协议边界、路由模型和执行主链路打稳。**

---

## 项目结构

```text
cmd/
  syrogo/                    # 程序入口

internal/
  app/                       # 应用装配
  config/                    # 配置定义、加载、校验
  execution/                 # 执行计划消费与 fallback
  gateway/                   # inbound protocol / HTTP handler
  provider/                  # outbound protocol / 上游适配
  router/                    # tag-first 路由决策
  runtime/                   # 中立标准模型
  server/                    # HTTP server 生命周期

configs/
  config.example.yaml        # 功能展示版配置
  config.yaml                # 你本地复制出来的手测配置（已 gitignore）
```

## 核心架构理解

Syrogo 当前的主链路可以理解为：

```text
client request
  -> gateway (inbound protocol)
  -> runtime.Request
  -> router (match rule by active tag)
  -> execution (consume execution plan)
  -> provider (outbound protocol)
  -> upstream model API
```

### 1. inbound
入口负责：
- 接收 HTTP 请求
- 识别协议
- 校验 token
- 找到 client tag
- 把外部请求转成内部标准请求

当前已支持：
- `openai_chat`
- `anthropic_messages`

### 2. runtime
`internal/runtime` 是中立模型层。

它负责承接：
- 标准请求 `Request`
- 标准响应 `Response`
- 标准流事件 `StreamEvent`
- 路由上下文 `RouteContext`
- 执行计划 `ExecutionPlan`

这里尽量不放 OpenAI / Anthropic 的专属结构。

### 3. routing
当前路由模型是：

- inbound client 命中后得到一个 `active tag`
- router 按顺序匹配 `routing.rules`
- 使用**首条命中规则**
- 再把 `to_tags` 展开成实际可执行的 outbounds
- 在单条规则内应用：
  - `failover`
  - `round_robin`

### 4. outbound / provider
provider 负责：
- 把内部标准请求转换成上游协议请求
- 发送到真实模型 API
- 再把响应转回内部标准响应

当前支持：
- `mock`
- `openai_chat`

provider-specific transform 也放在这一层，而不是放到 gateway 或 router。

---

## 配置文件说明

当前仓库默认提供一种配置文件：

### `configs/config.example.yaml`
用途：**功能展示版**。

它用于展示当前已经支持的配置组织方式，包括：
- 一个 listener 同时挂多个 inbound
- `openai_chat` 与 `anthropic_messages` 双入口
- `failover` 与 `round_robin` 两种路由策略
- 多个 OpenAI-compatible outbound
- `target_model` 覆盖

这个文件偏“展示当前能力边界”，适合阅读和参考，不建议直接拿来作为你的本地手测配置。

### `configs/config.yaml`
用途：**你本地复制出来的手测配置**。

建议做法是：
- 从 `configs/config.example.yaml` 复制一份为 `configs/config.yaml`
- 把里面的 token、endpoint、auth_token 按你的本地环境改成真实可用的值
- 这个文件已经在 `.gitignore` 中，不会误提交

如果你只是想确认“服务是不是已经跑起来了”，可以把 `configs/config.yaml` 改成只走 `mock` outbound 的最小配置。

---

## 环境变量与 `.env`

这里要特别注意当前真实行为。

### 当前是否自动支持 `.env`
**不支持。**

项目根目录放一个 `.env` 文件，当前程序**不会自动读取**。

### 当前是否自动展开 `${VAR}`
**不会。**

当前配置加载只是：
- 读取 YAML 文件
- 直接反序列化到配置结构

没有做：
- `.env` 自动加载
- `${VAR}` 环境变量展开

所以像下面这种写法：

```yaml
auth_token: "${OPENAI_API_KEY_PRIMARY}"
```

在当前实现里会被当成普通字符串原样读入，而不是替换成环境变量值。

### 这意味着什么
- `config.example.yaml` 里出现的 `${VAR}` 目前只是**展示性占位写法**
- 它表达的是“这里通常应该填什么值”
- 不是说当前程序已经实现了自动替换能力

### 你现在该怎么做
如果你要真正运行：

- 复制一份本地配置：`cp configs/config.example.yaml configs/config.yaml`
- 再按你的环境把 `configs/config.yaml` 里的 token、endpoint、auth_token 改成真实值
- 如果只是做最小本地验证，也可以把 `configs/config.yaml` 改成只走 `mock` outbound 的简化配置

---

## 配置模型

当前配置围绕以下概念组织：

- `listeners`
- `inbounds`
- `routing.rules`
- `outbounds`

### 1. listeners
监听地址与挂载入口的关系。

示例：

```yaml
listeners:
  - name: "public-http"
    listen: ":8080"
    inbounds:
      - "openai-entry"
      - "anthropic-entry"
```

含义：
- 监听 `:8080`
- 这个 listener 同时挂两个入口协议

### 2. inbounds
定义入口协议、路径和 client token。

示例：

```yaml
inbounds:
  - name: "openai-entry"
    protocol: "openai_chat"
    path: "/v1/chat/completions"
    clients:
      - token: "client-token"
        tag: "office"

  - name: "anthropic-entry"
    protocol: "anthropic_messages"
    path: "/v1/messages"
    clients:
      - token: "anthropic-token"
        tag: "office"
```

含义：
- 按 `path + Authorization: Bearer <token>` 识别入口
- 每个 client 只携带一个活动 `tag`
- 后续路由围绕这个 tag 展开

### 3. routing.rules
当前是 tag-first 路由模型。

示例：

```yaml
routing:
  rules:
    - name: "office-route"
      from_tags:
        - "office"
      to_tags:
        - "openai-primary"
        - "openai-backup"
      strategy: "failover"
      target_model: "gpt-4o-mini"
```

含义：
- 当 active tag 属于 `from_tags` 时命中该规则
- 规则命中后，将请求发往 `to_tags` 对应的 outbounds
- `strategy` 当前支持：
  - `failover`
  - `round_robin`
- `target_model` 可覆盖请求中的 model

### 4. outbounds
定义真实上游。

示例：

```yaml
outbounds:
  - name: "openai-primary"
    protocol: "openai_chat"
    endpoint: "https://api.openai.com/v1"
    auth_token: "sk-xxx"
    tag: "openai-primary"
```

含义：
- 一个 outbound 代表一个真实上游
- 一个 key 对应一个 outbound
- 不在单个 outbound 内塞多个 key
- 多 key 轮询应该通过多个 outbound + 路由策略表达

---

## 快速开始

### 1. 运行测试

```bash
go test ./...
```

### 2. 准备本地配置并启动服务

```bash
cp configs/config.example.yaml configs/config.yaml
```

把 `configs/config.yaml` 里的占位值替换成你自己的真实值后，再启动：

```bash
go run ./cmd/syrogo -config ./configs/config.yaml
```

> `-config` 默认值仍然是 `./configs/config.example.yaml`，本地联调时建议显式指定你自己的 `configs/config.yaml`

### 3. 健康检查

```bash
curl http://127.0.0.1:8080/healthz
```

---

## 请求示例

下面这些请求示例默认假设你已经把 `configs/config.yaml` 里的 token 改成了：
- OpenAI 入口：`your-openai-client-token`
- Anthropic 入口：`your-anthropic-client-token`

请按你本地实际配置替换。

### OpenAI Chat Completions

```bash
curl -s http://127.0.0.1:8080/v1/chat/completions \
  -H 'Authorization: Bearer your-openai-client-token' \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "gpt-4",
    "messages": [
      {"role": "user", "content": "hello"}
    ]
  }'
```

### OpenAI 流式

```bash
curl -N http://127.0.0.1:8080/v1/chat/completions \
  -H 'Authorization: Bearer your-openai-client-token' \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "gpt-4",
    "stream": true,
    "messages": [
      {"role": "user", "content": "hello"}
    ]
  }'
```

### Anthropic Messages

```bash
curl -s http://127.0.0.1:8080/v1/messages \
  -H 'Authorization: Bearer your-anthropic-client-token' \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "claude-sonnet-4-5",
    "messages": [
      {"role": "user", "content": "hello"}
    ]
  }'
```

### Anthropic 流式

```bash
curl -N http://127.0.0.1:8080/v1/messages \
  -H 'Authorization: Bearer your-anthropic-client-token' \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "claude-sonnet-4-5",
    "stream": true,
    "messages": [
      {"role": "user", "content": "hello"}
    ]
  }'
```

---

## 当前实现约束

当前需要注意：

- 规则之间按顺序匹配，使用首条命中规则
- `clients` 当前是 `token + tag`
- tag 当前按单字符串活动 tag 处理，而不是多标签并行求值
- `mock` outbound 主要用于打通链路与测试
- `anthropic_messages` 当前已支持作为 inbound protocol
- 当前还没有实现 anthropic outbound provider
- 当前流式能力以最小 SSE 闭环为主，不追求完整上游事件透传
- 当前还没有实现 `.env` 自动加载与 `${VAR}` 自动展开

---

## 开发建议

开发时建议优先使用：

```bash
make fmt
make test
make run
```

如果本地有环境，也建议执行：

```bash
golangci-lint run
```

---

## 后续演进方向

后续更可能推进的方向包括：

- 更多 inbound / outbound protocol
- 更丰富的 provider-specific transform
- 更完整的 key 管理与额度切换
- 更强的统计、治理与审计能力
- 多节点串接与中继部署能力
- 环境变量展开与更顺手的本地配置体验

但这些都会建立在当前骨架和边界继续保持清晰的前提上。
