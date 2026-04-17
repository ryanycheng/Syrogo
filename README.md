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
- 三种 inbound protocol
  - `openai_chat`
  - `openai_responses`
  - `anthropic_messages`
- 统一转换到内部中立模型 `runtime.Request / Response / StreamEvent`
- 基于 `client token -> active tag` 的入口识别
- 基于 `routing.rules` 的首条规则命中
- 单条规则内的：
  - `failover`
  - `round_robin`
- 三种 outbound protocol
  - `mock`
  - `openai_chat`
  - `openai_responses`
  - `anthropic_messages`
- OpenAI-compatible 上游调用
- Anthropic-compatible 上游调用
- 基础流式 SSE 返回
- 最小 tool calling 映射闭环
- Anthropic inbound 调试快照落盘
- 单元测试、回归测试与关键链路测试

---

## 当前不在范围内

当前还**不追求**：

- 复杂插件系统
- gRPC / MCP / WebSocket 等额外接入方式
- 完整 semantic routing 能力
- Anthropic 上游原生 SSE 逐事件透传
- multimodal 全量支持
- Responses API 全量 item type 无损透传
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
- `openai_responses`
- `anthropic_messages`

### 2. runtime
`internal/runtime` 是中立模型层。

它负责承接：
- 标准请求 `Request`
- 标准响应 `Response`
- 标准流事件 `StreamEvent`
- 路由上下文 `RouteContext`
- 执行计划 `ExecutionPlan`

当前请求层已承接最小公共字段，例如：
- `model`
- `messages`
- `system`
- `max_tokens`
- `stream`

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
- `openai_responses`
- `anthropic_messages`

provider-specific transform 也放在这一层，而不是放到 gateway 或 router。

---

## 配置文件说明

当前仓库默认提供一种配置文件：

### `configs/config.example.yaml`
用途：**功能展示版**。

它用于展示当前已经支持的配置组织方式，包括：
- 一个 listener 同时挂多个 inbound
- `openai_chat`、`openai_responses` 与 `anthropic_messages` 多入口
- Anthropic / Claude Code 入口桥接到 OpenAI-compatible `chat/completions`
- `failover` 与 `round_robin` 两种路由策略
- 多个 OpenAI-compatible outbound
- Anthropic-compatible outbound
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
      - "responses-entry"
      - "anthropic-entry"
```

含义：
- 监听 `:8080`
- 这个 listener 可以同时挂多个入口协议

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

  - name: "responses-entry"
    protocol: "openai_responses"
    path: "/v1/responses"
    clients:
      - token: "responses-token"
        tag: "responses"

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

    - name: "anthropic-to-chat-route"
      from_tags:
        - "anthropic-to-chat"
      to_tags:
        - "openai-primary"
        - "openai-backup"
      strategy: "failover"
      target_model: "gpt-5.4"

    - name: "responses-route"
      from_tags:
        - "responses"
      to_tags:
        - "responses-primary"
      strategy: "failover"
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

  - name: "responses-primary"
    protocol: "openai_responses"
    endpoint: "https://api.openai.com/v1"
    auth_token: "sk-xxx"
    tag: "responses-primary"

  - name: "anthropic-primary"
    protocol: "anthropic_messages"
    endpoint: "https://cliproxyapi.todayto.com/v1"
    auth_token: "sk-ant-xxx"
    tag: "anthropic-primary"
```

含义：
- 一个 outbound 代表一个真实上游
- 一个 key 对应一个 outbound
- 不在单个 outbound 内塞多个 key
- 多 key 轮询应该通过多个 outbound + 路由策略表达

## OpenAI Responses 兼容范围

当前 `openai_responses` 走的是**最小兼容映射**，不是完整无损透传。

### inbound 当前支持
- `input` 为字符串
- `input` 为 item 数组，其中支持：
  - `message`
  - `function_call`
  - `function_call_output`
- `message.content` 支持文本类 part：
  - `input_text`
  - `output_text`
  - `text`

### outbound 当前支持
- 将 `runtime.Message` 映射到 `/responses` 的 `input`
- 将文本响应映射回 `output[].message.content[].output_text`
- 将工具调用映射回 `output[].function_call`

### 当前限制
- 还不支持 Responses 全量 item type
- 当前 stream 仍是“先拿完整响应，再本地拆成事件”的兼容型伪流式
- 更细粒度的原生事件语义暂未完全保留

### Anthropic 调试快照

当你在排查 `POST /v1/messages` 的兼容性问题时，Syrogo 会在 `tmp/` 下写入 `anthropic-inbound-*.json`。

快照里会包含：
- 原始请求体 `raw_body`
- 解析后的请求结构 `parsed`
- 转换后的中立请求 `runtime`
- 命中的 `planned_model`
- 路由解析后的 `resolved_to`

这适合用于核对客户端真实入参与 gateway / runtime 的转换结果。

---

## 快速开始

### 1. 运行测试

```bash
go test ./...
```

### 2. 准备本地配置

```bash
cp configs/config.example.yaml configs/config.yaml
```

把 `configs/config.yaml` 里的占位值替换成你自己的真实值。

### 3. 开发模式启动（热重载）

推荐本地开发优先使用：

```bash
make dev
```

这会通过 `.air.toml` 启动 Air，并实际运行：

```bash
./tmp/syrogo -config ./configs/config.yaml -dev-log
```

启用 `-dev-log` 后：
- 日志会继续输出到当前终端
- 同时追加写入 `tmp/dev.log`
- `tmp/` 目录不存在时会自动创建

这样在本地复现问题后，你可以直接查看 `tmp/dev.log`，也可以让我一起读日志定位问题。

> `make dev` 依赖你本机已安装 `air`

### 4. 单次启动

如果你只是想单次启动服务排查问题，可以使用：

```bash
make run
```

当前 `make run` 同样会带上 `-dev-log`，因此会同时输出终端日志和 `tmp/dev.log`。

如果你不想写文件日志，也可以直接手动运行：

```bash
go run ./cmd/syrogo -config ./configs/config.yaml
```

> `cmd/syrogo/main.go` 中 `-config` 默认值仍然是 `./configs/config.example.yaml`，但本地开发建议统一通过 `make run` / `make dev` 使用你自己的 `configs/config.yaml`

### 5. 健康检查

```bash
curl http://127.0.0.1:8080/healthz
```

---

## 请求示例

下面这些请求示例默认假设你已经把 `configs/config.yaml` 里的 token 改成了：
- OpenAI Chat 入口：`your-openai-client-token`
- OpenAI Responses 入口：`your-responses-client-token`
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

### OpenAI Chat 流式

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

### OpenAI Responses

```bash
curl -s http://127.0.0.1:8080/v1/responses \
  -H 'Authorization: Bearer your-responses-client-token' \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "gpt-4o-mini",
    "input": "hello"
  }'
```

### OpenAI Responses 流式

```bash
curl -N http://127.0.0.1:8080/v1/responses \
  -H 'Authorization: Bearer your-responses-client-token' \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "gpt-4o-mini",
    "stream": true,
    "input": [
      {
        "type": "message",
        "role": "user",
        "content": [
          {"type": "input_text", "text": "hello"}
        ]
      }
    ]
  }'
```

### OpenAI Responses function calling 一轮示例

```bash
curl -s http://127.0.0.1:8080/v1/responses \
  -H 'Authorization: Bearer your-responses-client-token' \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "gpt-4o-mini",
    "input": [
      {
        "type": "message",
        "role": "user",
        "content": [
          {"type": "input_text", "text": "上海天气怎么样"}
        ]
      },
      {
        "type": "function_call",
        "call_id": "call_123",
        "name": "get_weather",
        "input": {"city": "shanghai"}
      },
      {
        "type": "function_call_output",
        "call_id": "call_123",
        "output": [
          {"type": "output_text", "text": "sunny"}
        ]
      }
    ]
  }'
```

### Anthropic Messages

这是当前推荐的 Claude Code 最小桥接请求形状：

```bash
curl -s http://127.0.0.1:8080/v1/messages \
  -H 'Authorization: Bearer your-anthropic-client-token' \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "claude-sonnet-4-6",
    "max_tokens": 256,
    "system": [
      {"type": "text", "text": "You are Claude Code, Anthropic's official CLI for Claude."}
    ],
    "messages": [
      {
        "role": "user",
        "content": [
          {"type": "text", "text": "只回复 pong"}
        ]
      }
    ]
  }'
```

如果你本地已经把 Syrogo 热重载跑在 `127.0.0.1:23234`，也可以直接让 Claude Code 走这条入口做端到端验证：

```bash
ANTHROPIC_BASE_URL=http://127.0.0.1:23234 \
ANTHROPIC_AUTH_TOKEN='your-anthropic-client-token' \
claude -p "只回复 pong"
```

### Anthropic Tools Bridge

当前第二阶段支持的是：
- `anthropic_messages` / Claude Code 风格请求里的顶层 `tools`
- 透传普通 function tools 到 `openai_chat` 上游
- 上游返回 `tool_calls` 后，再回写成 Anthropic `tool_use`
- 后续 `tool_result` 再入站时，继续桥接到 OpenAI chat 历史消息

当前明确会过滤：
- Claude Code builtin / control tools，例如 `Bash`、`Read`、`Edit`、`Write`、`Glob`、`Grep`、`Task*`、`Cron*` 等
- 非 `type: object` 的工具输入 schema

也就是说，当前能力是“远端工具桥接”，不是“Syrogo 本地执行 Claude Code 工具”。

你可以先用自定义 function tool 做验证，例如：

```bash
curl -s http://127.0.0.1:8080/v1/messages \
  -H 'Authorization: Bearer your-anthropic-client-token' \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "claude-sonnet-4-6",
    "max_tokens": 256,
    "tools": [
      {
        "name": "get_weather",
        "description": "Get weather by city",
        "input_schema": {
          "type": "object",
          "properties": {
            "city": {"type": "string"}
          },
          "required": ["city"]
        }
      }
    ],
    "messages": [
      {
        "role": "user",
        "content": [
          {"type": "text", "text": "查询上海天气，必要时调用工具。"}
        ]
      }
    ]
  }'
```

如果你直接用 Claude Code 做端到端，请注意：Claude Code 自带 builtin tools 不会被透传给上游；当前更适合验证普通消息链路，或配合自定义 function tool 请求体验证 bridge 行为。

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
- `anthropic_messages` 当前已支持作为 inbound 与 outbound protocol
- Anthropic stream 当前是兼容型伪流式，会输出 Anthropic 风格事件序列，但不是上游原生 SSE 透传
- 调试 Anthropic 入口请求时，会在 `tmp/` 下写入 `anthropic-inbound-*.json` 快照
- `openai_responses` 当前已支持作为 inbound 与 outbound protocol
- 当前流式能力以最小 SSE 闭环为主，不追求完整上游事件透传
- Responses stream 当前是兼容型伪流式，不是上游原生 SSE 透传
- 当前还没有实现 `.env` 自动加载与 `${VAR}` 自动展开

---

## 开发建议

开发时建议优先使用：

```bash
make fmt
make test
make dev
```

如果你只是想单次启动当前配置，也可以使用：

```bash
make run
```

如果本地有环境，也建议执行：

```bash
golangci-lint run
```

> 热重载只替代运行流程，不替代测试或格式化。

---

## 后续演进方向

后续更可能推进的方向包括：

- 更多 inbound / outbound protocol
- 更丰富的 provider-specific transform
- 更完整的 key 管理与额度切换
- 更强的统计、治理与审计能力
- 多节点串接与中继部署能力
- 环境变量展开与更顺手的本地配置体验
- Responses item-oriented runtime 演进

但这些都会建立在当前骨架和边界继续保持清晰的前提上。
