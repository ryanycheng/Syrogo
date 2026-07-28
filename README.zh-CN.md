# Syrogo

中文 | [English](./README.md)

<p align="center">
  <img src="./docs/assets/SyroGo-logo.png" alt="SyroGo" width="500">
</p>

> Syrogo · AI Gateway / Semantic Router
>
> 用更清晰的边界、多协议接入和面向网关的编排能力，承接多模型流量。

- **多协议入口** — 在同一个网关中统一承接 OpenAI Chat、OpenAI Responses 与 Anthropic Messages。
- **面向真实场景的路由** — 支持按 client tag、模型映射、failover 与 round_robin 进行调度。
- **面向上游适配的执行层** — 接多个 provider，而不把协议差异散落到每个客户端里。

Syrogo 是一个面向多模型场景的 AI Gateway / Semantic Router。

它不是只做单一协议转发的代理层，而是一个放在客户端与上游模型之间的中间系统，用来统一承接：
- 多种入口协议
- 多上游 provider 接入
- 按客户端场景进行路由
- client 侧 requests、tokens、cost 单类型配额窗口
- failover / round_robin 与 provider 健康感知 fallback 等基础调度
- 后续额度切换、统计、治理与多节点串接能力

当前项目仍处于 0→1 骨架建设阶段，优先目标是把服务主链路、协议边界与路由模型打稳。

---

## 名字的由来

Syrogo 这个名字结合了神经元 / Synapse 的意象、Router 的路由语义，以及 Go 的实现身份。

它想表达的是：模型流量像神经信号一样被连接、传递与分发，而系统本身则是一个用 Go 构建的网关与路由层。

---

## 为什么做这个项目

真实模型接入场景里，客户端协议、上游协议、模型命名、鉴权方式、稳定性策略都不统一。

Syrogo 想解决的不是“再包一层 HTTP”，而是把这些变化收敛到清晰边界内：
- 客户端按自己熟悉的协议接入
- 系统内部转换成统一中立模型
- 路由层只关注流量该去哪
- provider 层只关注如何对接具体上游
- 最终再按客户端期望的协议输出

这样可以让接入、路由、切换、治理彼此解耦，而不是散落在每个 provider 或每条 handler 分支里。

---

## 设计原则

- 先做最小可运行闭环，再做能力扩展
- 优先稳定 `cmd + internal` 分层，不为了“看起来标准”提前拆 `pkg`
- `gateway` 负责入口协议解析与响应序列化
- `runtime` 负责中立请求、响应与流事件模型
- `router` / `execution` 负责路由决策与执行，不承担协议适配
- `provider` 负责出站协议编码、上游调用与结果解码
- 流式与非流式尽量共享同一套内部抽象，只在边界层做协议映射

---

## 当前已实现能力

当前版本已经支持：

- Go HTTP 服务启动与优雅退出
- 配置加载与基础校验
- `GET /healthz`
- 单监听与多监听配置
- 每个 listener 可绑定不同入口
- 三类入口协议
  - `POST /v1/chat/completions`
  - `POST /v1/responses`
  - `POST /v1/messages`
- 按客户端场景进行 tag-first routing
- client 侧 requests、tokens、cost 单类型配额窗口
- 单条规则内支持：
  - `failover`
  - `round_robin`
  - `weighted_round_robin`
- 支持按路由指定目标模型与模型映射
- provider 健康状态跟踪与 degraded/probing outbound 恢复
- 多类出站协议
  - `mock`
  - `openai_chat`
  - `openai_responses`
  - `anthropic_messages`
- OpenAI-compatible 与 Anthropic-compatible 上游调用
- 基础 SSE 流式返回
- 部分兼容链路采用本地回放流式输出
- 最小 tool calling 闭环
- `openai_responses` 的兼容能力声明
- quota 运行时治理，支持 snapshot 持久化、最近事件与 admin 统计接口
- 本地开发日志与 trace 调试能力
- 关键链路单元测试、回归测试与流程测试

### 协议与能力矩阵

| 维度 | 当前支持 | 说明 |
| --- | --- | --- |
| 入口协议 | `openai_chat`、`openai_responses`、`anthropic_messages` | 对外路径分别是 `/v1/chat/completions`、`/v1/responses`、`/v1/messages` |
| 出站协议 | `mock`、`openai_chat`、`openai_responses`、`anthropic_messages` | 路由按 tag 选择 outbound |
| 路由能力 | `failover`、`round_robin`、`weighted_round_robin`、`target_model`、`model_map` | 从 inbound client tag 开始匹配 |
| 流式能力 | Chat / Responses / Messages 的 SSE 序列化 | 部分兼容链路会本地回放 `runtime.StreamEvent`，不是上游逐帧透传 |
| Tool calling | 最小 function tool loop 与 custom tool 覆盖 | Responses 与 Anthropic bridge 路径已有回归测试 |
| Responses capability | `responses_previous_response_id`、`responses_builtin_tools`、`responses_tool_result_status_error`、`responses_assistant_history_native` | capability 声明仅适用于 `openai_responses` outbound |
| 校验与测试 | 配置校验、smoke test、协议回归 | 本地已通过 `make test`、`make build`、`make lint` |

---

## 项目结构

```text
cmd/
  syrogo/                    # 程序入口

internal/
  app/                       # 应用装配
  config/                    # 配置定义、加载、校验
  execution/                 # 执行计划消费与 fallback
  eventstream/               # 中立流事件整理与快照
  gateway/                   # inbound protocol / HTTP handler / 响应序列化
  provider/                  # outbound protocol / 上游适配
  router/                    # tag-first 路由决策
  runtime/                   # 中立标准模型
  server/                    # HTTP server 生命周期

configs/
  config.example.yaml        # 功能展示版配置
  config.yaml                # 本地手测配置（已 gitignore）
```

---

## 快速开始

### 1. 推荐安装 SyrogoConsole 套件

Syrogo 支持直接编辑 YAML；日常配置、Provider、Client、Route、Usage、Logs 和历史回滚更推荐使用官方独立管理控制台 SyrogoConsole。Linux + `systemd` 主机可直接运行一体化安装器：

```bash
curl -fsSL https://raw.githubusercontent.com/ryanycheng/SyrogoConsole/refs/heads/main/scripts/install.sh | sudo bash
```

在空主机上，它会安装同版本 Core 与 Console；已有健康 Core 时会直接复用，不升级、不重启、不改配置；遇到残缺或不健康 Core 会停止。Console 默认监听 `127.0.0.1:23233`，Core 默认监听 `127.0.0.1:23234`。

固定相同版本：

```bash
curl -fsSL https://raw.githubusercontent.com/ryanycheng/SyrogoConsole/refs/heads/main/scripts/install.sh \
  | sudo bash -s -- --version v0.16.3
```

完整的新主机、已有 Core、`--console-only`、升级、SSH tunnel 和 TLS 边界见 [`docs/deploy.zh-CN.md`](./docs/deploy.zh-CN.md)。

### 2. 手工管理 Core 配置

如果不使用 Console，可继续直接管理 YAML：

```bash
cp configs/config.example.yaml configs/config.yaml
make run
```

把 token、endpoint 和 auth_token 替换为真实值。顶层 `clients[]` 负责稳定身份与凭据；`inbounds[].clients[]` binding 的 `ref` 指向 Client name，`tag` 是路由身份。轮换 token 时保持 Client `name` 不变，可维持 Usage 与 quota 连续性。

当前实现不会自动读取 `.env` 或展开 `${VAR}`，残留占位符会作为普通字符串使用。

只安装 Core 时仍可使用 Core installer：

```bash
curl -fsSL https://raw.githubusercontent.com/ryanycheng/Syrogo/refs/heads/master/scripts/install.sh | sudo bash
```

它默认保留 `/opt/syrogo/config/config.yaml`，仅在显式传入 `--force-config` 时覆盖。下载的 release archive 会校验 checksum，安装版本记录在 `/opt/syrogo/VERSION`。

### 3. 选择监听与入口

当前既支持 `server.listen` 单监听，也支持 `listeners[]` 多监听。使用 `listeners[]` 时，可把不同入口挂到不同端口，按场景暴露不同协议。

### 4. 本地开发启动

```bash
make run
```

若只需最小验证，可把 route 指向 `mock` outbound。当前项目风险与建议下一步见 [`docs/risk.zh-CN.md`](./docs/risk.zh-CN.md)。

### 5. 检查健康状态

```bash
curl http://127.0.0.1:23234/healthz
```

如果你的监听端口不是 `:23234`，请按实际配置替换。

### 6. 验证协议入口

当前建议优先验证：
- `POST /v1/chat/completions`
- `POST /v1/responses`
- `POST /v1/messages`

最小本地联调示例：

```bash
curl http://127.0.0.1:23234/healthz

curl http://127.0.0.1:23234/v1/chat/completions \
  -H 'Authorization: Bearer <chat-token>' \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hello"}]}'

curl http://127.0.0.1:23234/v1/responses \
  -H 'Authorization: Bearer <responses-token>' \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-4o-mini","input":"hello"}'

curl http://127.0.0.1:23234/v1/messages \
  -H 'Authorization: Bearer <anthropic-token>' \
  -H 'anthropic-version: 2023-06-01' \
  -H 'Content-Type: application/json' \
  -d '{"model":"claude-sonnet-4-5","max_tokens":128,"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}'
```

也可以对一个正在运行的 gateway 执行 smoke 脚本。脚本会在提供对应 token 时检查健康状态、协议入口、流式响应与 usage 统计：

```bash
SYROGO_SMOKE_BASE_URL=http://127.0.0.1:23234 \
SYROGO_OPENAI_CLIENT_TOKEN=<chat-token> \
SYROGO_RESPONSES_CLIENT_TOKEN=<responses-token> \
SYROGO_ANTHROPIC_CLIENT_TOKEN=<anthropic-token> \
SYROGO_ACCOUNTING_ADMIN_TOKEN=<accounting-admin-token> \
make smoke
```

### 7. 通过 Syrogo 启动 agent 客户端

可以使用 `syrogo run` 启动常见 agent CLI，并自动注入指向 Syrogo 的环境变量：

```bash
syrogo run claude --model claude-sonnet-4-6 --dangerously-skip-permissions
syrogo run codex exec "Reply with exactly: syrogo-ok"
```

默认情况下，`claude` 会选择一个 `anthropic_messages` inbound client，`codex` 会选择一个 `openai_responses` inbound client。`syrogo run` 会优先使用 `/opt/syrogo/config/config.yaml`；只有安装配置不存在时，才把 `./configs/config.yaml` 作为开发 fallback。也可以把 `--config` 放在子命令前或后显式指定配置。Syrogo launcher 自己的参数应放在 agent 原生参数前；遇到第一个 agent 原生参数后，后续参数会原样透传。如果有多个匹配 client，可以传 `--client` 或 `--inbound`：

```bash
syrogo --config /opt/syrogo/config/config.yaml run claude --client anthropic-key --base-url http://127.0.0.1:23234
syrogo run codex --config ./configs/config.yaml --inbound responses-entry --print-env
```

如果希望当前 shell 直接使用 Syrogo，可以执行 activation 输出：

```bash
eval "$(syrogo activate claude --client anthropic-key)"
eval "$(syrogo activate codex --client responses-key)"
```

如果希望新 shell 默认生效，也可以把同样的 `eval "$(syrogo activate ...)"` 放进 shell rc 文件。

当前范围：

- `claude` 注入 `ANTHROPIC_BASE_URL` 和 `ANTHROPIC_AUTH_TOKEN`
- `codex` 注入 `OPENAI_BASE_URL` 和 `OPENAI_API_KEY`
- `--print-env` 按稳定 key 顺序打印解析后的环境变量，并默认脱敏敏感值，不启动客户端
- `activate` 会输出真实 shell `export` 语句供 `eval` 使用，不要把输出贴到日志里
- 被启动的客户端仍在本地运行，Syrogo 负责承接它的模型 API 流量

### 8. 匹配并映射路由模型

规则按配置顺序依次判断，`from_tags` 和可选 `match.models` 都命中的第一条规则获胜。因此同一个 Client binding tag 可以按 Haiku、Sonnet、Opus 分层，并保留最后一条无条件 fallback：

```yaml
routing:
  rules:
    - name: "anthropic-haiku"
      from_tags: ["anthropic-to-responses"]
      match:
        models: ["claude-*-haiku-*"]
      to_tags: ["responses-primary"]
      strategy: "failover"
      target_model: "gpt-5.4-mini"

    - name: "anthropic-sonnet"
      from_tags: ["anthropic-to-responses"]
      match:
        models: ["claude-*-sonnet-*"]
      to_tags: ["responses-primary"]
      strategy: "failover"
      target_model: "gpt-5.4"

    - name: "anthropic-opus"
      from_tags: ["anthropic-to-responses"]
      match:
        models: ["claude-*-opus-*"]
      to_tags: ["responses-primary"]
      strategy: "failover"
      target_model: "gpt-5.4-pro"

    # 最后一条无条件规则保留 bootstrap 旧 fallback 行为。
    - name: "anthropic-fallback"
      from_tags: ["anthropic-to-responses"]
      to_tags: ["responses-primary"]
      strategy: "failover"
      target_model: "gpt-5.4"
```

`match.models` 只检查客户端原始请求中的 `model`，并且发生在任何模型改写之前。它不会识别 Claude Code 的 agent 模式或 Plan 模式，也不会检查 prompt、messages、tool 声明或 tool result。匹配区分大小写并覆盖完整 model 字符串；仅 `*` 表示零个或多个字符，不支持正则、`?` 或字符类。省略 `match` 或设为 null 表示对匹配 tag 无条件命中，因此 fallback 必须放在更具体规则之后。未知 model 不会自动降级，只能命中显式 pattern 或后续无条件 fallback。

模型处理分为三个独立阶段：

1. `match.models` 使用客户端原始 requested model 选择 route。
2. 选中的 route 使用 `target_model` 或精确 key 的 `model_map` 改写模型（两者不能同时配置）。
3. 执行计划中的每个 outbound 分别用自己的 canonical `models[].name` 和 aliases 解析改写后的模型。

例如，多模型映射 route 可以配置为：

```yaml
routing:
  rules:
    - name: "responses-route"
      from_tags: ["responses"]
      to_tags: ["responses-primary"]
      strategy: "failover"
      model_map:
        gpt-4: "gpt-4o-mini"
        claude-sonnet-4-6: "gpt-5.4"
```

每个 outbound 还可以声明自己支持的 canonical 模型名和可选 alias：

```yaml
outbounds:
  - name: "openai-primary"
    protocol: "openai_chat"
    endpoint: "https://api.openai.com/v1"
    auth_token: "${OPENAI_API_KEY_PRIMARY}"
    tag: "openai-primary"
    models:
      - name: "gpt-4o-mini"
        aliases: ["gpt-4-mini", "fast"]
```

省略 `models` 或配置为空列表表示不限制模型。完成 route 匹配与 route 级改写后，每个 fallback provider 再根据自己的 canonical 名称与 alias 独立解析 routed model，因此同一 alias 可以在不同 provider 上对应不同的上游模型名。不接受该模型的 provider 会被跳过，系统不会自动降级；如果执行计划中的 provider 都不接受，入口协议会收到 HTTP 404 和 `model_not_found`（或对应协议的错误 envelope）。

通过 Admin API 或内置 Admin UI 创建、更新、删除 Route 时，会原子保存配置并立即热 Apply。规则顺序就是运行时优先级；`POST /admin/config/routes/reorder` 使用 `GET /admin/config/routes` 返回的 `order_revision` 检测并发冲突，保存全局顺序后立即热 Apply，不需要额外点击 Apply。外部工具或人工直接修改 YAML 后仍需调用 `/admin/config/apply`。

### 9. 配置 outbound 代理

如果某个上游 provider 需要独立网络出口，可以在该 outbound 上配置代理：

```yaml
outbounds:
  - name: "openai-primary"
    protocol: "openai_chat"
    endpoint: "https://api.openai.com/v1"
    auth_token: "${OPENAI_API_KEY_PRIMARY}"
    tag: "openai-primary"
    proxy:
      url: "http://127.0.0.1:7890"
```

当前范围：

- 代理配置是 outbound 级别，不是全局配置
- 不设置 `proxy.url` 时保持默认出站 HTTP 行为
- 当前 proxy URL scheme 支持 `http`、`https` 和 `socks5`

### 10. 声明 Responses 兼容能力

如果某个 `openai_responses` 上游只兼容官方 Responses 的一部分能力，可以在 outbound 上显式声明能力边界：

```yaml
outbounds:
  - name: "responses-primary"
    protocol: "openai_responses"
    endpoint: "https://api.openai.com/v1"
    auth_token: "${OPENAI_RESPONSES_API_KEY_PRIMARY}"
    tag: "responses-primary"
    capabilities:
      responses_previous_response_id: true
      responses_builtin_tools: true
      responses_tool_result_status_error: true
      responses_assistant_history_native: true
```

### 11. 声明 usage estimation 回补

如果某个 `openai_chat` 或 `anthropic_messages` 上游没有返回 `usage`，可以在 outbound 上开启平台侧启发式补算：

```yaml
outbounds:
  - name: "openai-primary"
    protocol: "openai_chat"
    endpoint: "https://api.openai.com/v1"
    auth_token: "${OPENAI_API_KEY_PRIMARY}"
    tag: "openai-primary"
    capabilities:
      usage_estimation: true
      usage_estimation_mode: "heuristic"
```

当前范围：

- 只作用于非流式响应
- 目前仅支持 `openai_chat` 与 `anthropic_messages` outbound
- 只在上游缺失 `usage` 时触发
- 返回的是平台侧近似值，不是 provider 账单真值

### 12. 限制 Client quota 窗口

对于 Client 侧治理，应在顶层 Client 配置 quota，并单独把它绑定到一个或多个 Inbound。下面的 canonical 示例分别展示三种支持的 window type：

```yaml
clients:
  - name: "office-key"
    token: "${SYROGO_OPENAI_CLIENT_TOKEN}"
    quota:
      enabled: true
      windows:
        - name: "hourly-requests"
          type: "requests"
          duration: "1h"
          max_requests: 1000
        - name: "daily-tokens"
          type: "tokens"
          duration: "24h"
          max_tokens: 1000000
        - name: "monthly-cost"
          type: "cost"
          duration: "720h"
          max_cost_usd: 25

inbounds:
  - name: "openai-entry"
    protocol: "openai_chat"
    path: "/v1/chat/completions"
    clients:
      - ref: "office-key"
        tag: "office"
  - name: "responses-entry"
    protocol: "openai_responses"
    path: "/v1/responses"
    clients:
      - ref: "office-key"
        tag: "automation"
```

字段归属与行为：

- 顶层 `clients[]` 只负责 `name`、`token`、`quota`；`inbounds[].clients[]` 只负责 binding 的 `ref`、`tag`
- 同一个 Client 可以绑定多个 Inbound；鉴权会通过 binding `ref` 解析到同一个顶层 token
- binding `tag` 由实际进入的 Inbound 决定，并用于匹配 `routing.rules[].from_tags`，因此同一 Client 可通过不同 binding 进入不同路由场景
- quota 与 Usage/accounting 按稳定 Client `name` 全局聚合，不会按 Inbound 或 tag 分开计算
- quota 在路由前生效，因此已确定耗尽的 Client 会在所有 bindings 上直接收到 HTTP 429
- 多个窗口可以同时生效；每个窗口只能有一个 `type`（`requests`、`tokens` 或 `cost`），并且只能配置对应的 limit（`max_requests`、`max_tokens` 或 `max_cost_usd`）；任意窗口耗尽都会阻止该 Client
- 旧 request window 省略 `type` 但设置了 `max_requests` 时保持兼容，保存后会 canonicalize 为 `type: requests`
- Requests 在请求入口准入时计数。Tokens 与 Cost 只在成功 terminal response 后计入；由于准入与 terminal 记账分离，并发 in-flight 成功请求可能超过 token/cost 上限，下一次请求才会收到 HTTP 429
- Cost 来自 Syrogo 配置或内置的模型 pricing，不是 Provider 账单。成功 terminal usage 的模型没有价格时按 `$0` 计入；runtime quota 输出会携带 `unpriced_count` 和 warning，提示运维补齐 pricing
- Provider/outbound quota 只支持 requests 与 tokens，不支持 `type: cost` 或 `max_cost_usd`
- 旧版将 Client `name`/`token`/`quota` 嵌套在 Inbound 下的 YAML 仍可读取以便迁移；下一次保存配置时会输出顶层 `clients` 加 `ref`/`tag` bindings 的 canonical 形式
- 当 binding tag 是 `routing.rules[].from_tags` 引用的最后一个 Client 来源时，该来源会被保护。要解除或改 tag，必须先新增/更新另一条 binding 提供相同 tag，或者从错误列出的全部 route 的 `from_tags` 中删除/修改该 tag；结构化 Admin 错误会返回 tag 与 route names

使用 accounting admin token 查看当前 client quota 状态：

```bash
curl http://127.0.0.1:23234/stats/client-quota \
  -H 'Authorization: Bearer <accounting-admin-token>'
```

### 13. 跟踪 outbound 配额窗口

Outbound quota 是 outbound provider 侧保护，与 inbound/client 请求配额相互独立。它可以在多个重叠窗口中同时跟踪请求数与 outbound token：

```yaml
outbounds:
  - name: "openai-primary"
    protocol: "openai_chat"
    endpoint: "https://api.openai.com/v1"
    auth_token: "${OPENAI_API_KEY_PRIMARY}"
    tag: "openai-primary"
    quota:
      enabled: true
      cooldown: "10m"
      probe_interval: "1m"
      windows:
        - name: "rolling-five-hour"
          reset: "rolling"
          duration: "5h"
          max_requests: 1000
          max_tokens: 2000000
        - name: "fixed-five-hour"
          reset: "fixed"
          duration: "5h"
          fixed: {period: "interval", anchor: "2026-01-01T00:00:00Z"}
          max_tokens: 2500000
        - name: "daily"
          reset: "fixed"
          fixed: {period: "daily", time: "04:00", timezone: "America/New_York"}
          max_requests: 5000
        - name: "weekly"
          reset: "fixed"
          fixed: {period: "weekly", weekday: "monday", time: "00:00", timezone: "UTC"}
          max_requests: 20000
          max_tokens: 40000000
      reset_all:
        enabled: true
        schedule: {period: "weekly", weekday: "sunday", time: "00:00", timezone: "UTC"}
```

语义与运行限制：

- `reset: rolling` 使用移动 `duration`。Fixed 窗口支持三类：带锚点的 `interval`（例如 5h）、自然日 `daily`、自然周 `weekly`。
- Daily/weekly 的 `timezone` 必须是 IANA 时区，reset 按该时区的自然时间和 DST 切换执行；interval anchor 必须是带显式 offset 的 RFC3339。
- `max_requests` 与 `max_tokens` 是相互独立的可选维度，至少一个必须大于 0；任一窗口任一维度耗尽都会跳过 outbound。
- Token 只在 outbound 成功终态计入。优先使用 provider 返回的 usage；上游缺失 usage 时默认为 0，只有启用 `capabilities.usage_estimation: true` 和 `usage_estimation_mode: heuristic` 才使用启发式估算。估算值只是平台侧估计，不是 provider 账单真值。
- 准入检查与成功 usage 记账分开，因此并发 in-flight 请求可能按同时成功数超过配置上限。
- 上游真实 429 会进入 `cooldown`；到 `probe_interval` 后允许一个真实请求探测，探测成功才解除 cooldown。`reset_all` 按计划清空所有 usage 窗口，但不会清除 cooldown/probe 状态。
- 旧 quota 配置省略 `reset` 时保持兼容，按 rolling 窗口处理。

使用 accounting admin token 查看当前 quota 状态：

```bash
curl http://127.0.0.1:23234/stats/quota \
  -H 'Authorization: Bearer <accounting-admin-token>'
```

### 14. 持久化与查看 quota 治理状态

Syrogo 可以把运行时 quota 状态持久化到本地，重启后恢复最近的 client/outbound 窗口与 outbound cooldown。Snapshot v2 会保存请求/token 事件及 reset 元数据，并继续兼容读取旧 snapshot。成功 Apply 会按稳定的 subject/window 身份迁移兼容状态（包括 cooldown/probe）；定义已变化或不兼容的窗口会从空状态开始：

```yaml
governance:
  quota:
    snapshot:
      enabled: true
      dir: "./tmp/quota"
      flush_interval: "5s"
    events:
      enabled: true
      max_entries: 200
```

使用 accounting admin token 可以在一个响应里查看 quota 状态与最近 quota 事件：

```bash
curl http://127.0.0.1:23234/stats/governance \
  -H 'Authorization: Bearer <accounting-admin-token>'
```

响应包含 provider health、outbound quota、client quota，以及 `client_limited`、`outbound_limited`、`outbound_quota_exceeded`、`outbound_probe_succeeded`、`provider_health_limited`、`provider_probe_succeeded` 等最近事件。处于 degraded 状态的 outbound 会在执行时被跳过，等下一次真实请求允许探测恢复时进入 `probing`。

也可以使用同一个 admin token 查看最近请求的 latency timeline：

```bash
curl http://127.0.0.1:23234/stats/latency \
  -H 'Authorization: Bearer <accounting-admin-token>'
```

如果要查看当前延迟分布的聚合摘要，可以查询 summary 端点：

```bash
curl http://127.0.0.1:23234/stats/latency/summary \
  -H 'Authorization: Bearer <accounting-admin-token>'
```

Timeline 响应包含请求元信息、HTTP status、总耗时，以及 `route_plan`、`provider_dispatch`、`upstream_round_trip`、`upstream_read`、`upstream_stream_read`、`egress_write` 等阶段 span。Summary 响应会对最近请求的总耗时和各阶段 span 聚合输出 `count`、`avg_ms`、`p50_ms`、`p95_ms`、`p99_ms`、`max_ms`。配置 outbound proxy 时，`upstream_round_trip` 统计的是 Syrogo 到代理并收到响应头的耗时；代理到真实上游的内部耗时会包含在这段等待中，除非代理自身额外暴露指标，否则 Syrogo 侧无法继续拆分。

推荐使用独立 SyrogoConsole 访问这些 Admin API，在浏览器中查看 health、usage、quota、latency、logs 并管理配置。Console 默认地址为：

```text
http://127.0.0.1:23233
```

Core 仍通过独立的 `admin.token` 保护 Admin API。该 token 必须与业务 inbound client token、`accounting.admin_token` 不同，因为浏览器管理入口和模型流量属于不同权限边界：

```yaml
admin:
  enabled: true
  token: "${SYROGO_ADMIN_UI_TOKEN}"
  logs:
    enabled: true
    path: "./tmp/dev.log"
    max_bytes: 65536
    rotation:
      max_size_mb: 100
      max_files: 20
      max_age_days: 14
      max_total_size_mb: 1024
      compress: true
```

日志会在下一次写入将超过 `max_size_mb` 时轮转，并在本地自然日首次写入时按日轮转。历史文件使用 gzip 压缩，并按保留天数、文件数量和总磁盘占用清理；当前日志文件永不删除。`/admin/logs` 会自动查询当前文件和仍保留的归档。完整落在有界近期缓存内的查询（最近 5 分钟、最多 8 MiB）优先从内存返回；cursor、历史、覆盖不完整或需要分页的查询会自动回退文件，避免遗漏结果。状态筛选支持精确状态码以及 `4xx`、`5xx` 状态族。成功的 `/admin/logs` 自动轮询不会生成 `admin_audit` 日志。

SyrogoConsole 只会把 Admin token 保存在浏览器 local storage 中，并通过同源 `/admin/*` 代理访问 Core Admin API。Provider 和 Client 保存/删除都会原子更新配置并立即热应用，不需要再点击 Apply。Client CRUD 只处理顶层 `name`、`token`、`quota`；binding 使用 `inbound`、`ref`、`tag` 独立管理。删除 Client 前必须先解除它在所有 Inbound 上的 bindings。Client name 是稳定的 quota/accounting identity：轮换 token 时保持 name 不变，可保留跨全部 bindings 的全局连续性；编辑已有 Client 时 token 留空或填 `<redacted>` 会保留原值。Client quota 会完整 round-trip。若变更或删除 binding 会让 `routing.rules[].from_tags` 引用的 tag 失去最后一个来源，操作会被拒绝，应先让其他 binding 提供该 tag。

Clients 列表独立请求配置与 metrics，因此 metrics 失败只显示 warning，CRUD 仍可用。紧凑 **Usage** 是全量历史，**Frequency** 是所选最近 7/30/90 个 UTC 自然日。点击 Client 可查看 contribution heatmap 和 Requests/Tokens/Cost/Errors 每日聚合。日期范围采用 UTC 前闭后开（含 `start_date`、不含 `end_date`）；当前 UTC 日标为 `partial`，已知 coverage 之前的旧日期标为 `unknown`，不能当作零值。“Daily records”是每日聚合，不是逐请求审计。

### 15. 校验配置变更

使用独立 Admin UI token 或既有 accounting admin token 可以在替换线上配置文件前，先 dry-run 校验一份 YAML 配置：

```bash
curl http://127.0.0.1:23234/admin/config/validate \
  -H 'Authorization: Bearer <admin-ui-token-or-accounting-admin-token>' \
  --data-binary @configs/config.yaml
```

这个端点只会解析并校验提交的配置，不会 reload，也不会把变更应用到当前运行流量。

如果要在校验通过后替换启动时使用的配置文件，可以把 YAML 提交到 update 端点：

```bash
curl http://127.0.0.1:23234/admin/config/update \
  -H 'Authorization: Bearer <admin-ui-token-or-accounting-admin-token>' \
  --data-binary @configs/config.yaml
```

这个端点会把校验通过的 YAML 原子写入当前启动配置路径。响应中会包含 `"applied": false`；调用 `/admin/config/apply` 或在 Admin UI 点击 Apply current file 后，可以热加载安全的运行时变更。

```bash
curl -X POST http://127.0.0.1:23234/admin/config/apply \
  -H 'Authorization: Bearer <admin-ui-token>'
```

Apply 会在不重启监听 socket 的前提下重建 provider（包括模型 canonical 名称与 alias）、routing、quota tracker、health tracking、Admin/accounting token 和 listener 绑定的 inbound。外部工具或人工修改当前配置文件后，需要执行 Apply；通过 Admin UI/API 发起的 Provider mutation 已经会原子保存并自动热应用。listener 数量、listen 地址、listener 名称、listener inbound 绑定，或日志 path/rotation 配置发生变化时，会返回 `"restart_required": true`，并保持当前运行态不变。成功 apply 会在配置文件同目录的 `.syrogo-history/` 中创建本地配置历史；`/admin/config/history` 可以列出最近版本，`/admin/config/history/diff?id=<history-id>` 会返回脱敏后的当前/历史 YAML 用于对比，`/admin/config/rollback` 可以写回指定版本并 apply。

如果要在不改变 round-robin 状态、也不发送模型请求的前提下验证路由结果，可以使用 route dry-run：

```bash
curl -X POST http://127.0.0.1:23234/admin/debug/route-dry-run \
  -H 'Authorization: Bearer <admin-ui-token>' \
  -H 'Content-Type: application/json' \
  -d '{"inbound":"openai-entry","client":"office-key","model":"gpt-4"}'
```

Dry-run 响应会包含 matched rule、strategy、resolved tags 和有序 outbound steps。它只接受 inbound/client/model/stream 元数据，不接收请求 body、header、token 或 replay 内容。

### 16. 查看 usage 聚合统计

Syrogo 现在提供一个独立的 accounting 只读端点，用于查看 usage 聚合结果。

它不复用业务 inbound token，而是使用单独的 admin token：

```bash
curl http://127.0.0.1:23234/stats/usage?group_by=key \
  -H 'Authorization: Bearer <accounting-admin-token>'

curl http://127.0.0.1:23234/stats/usage?group_by=provider \
  -H 'Authorization: Bearer <accounting-admin-token>'

curl 'http://127.0.0.1:23234/stats/usage?group_by=key&start_date=2026-04-21&end_date=2026-04-28' \
  -H 'Authorization: Bearer <accounting-admin-token>'

curl 'http://127.0.0.1:23234/stats/usage?group_by=key&window=day&bucket=2026-04-27' \
  -H 'Authorization: Bearer <accounting-admin-token>'

curl 'http://127.0.0.1:23234/stats/usage?group_by=provider&window=month&bucket=2026-04' \
  -H 'Authorization: Bearer <accounting-admin-token>'

curl http://127.0.0.1:23234/stats/usage?group_by=error_kind \
  -H 'Authorization: Bearer <accounting-admin-token>'
```

当前支持的 `group_by`：

- `key`
- `provider`
- `model`
- `inbound`
- `source`
- `outbound`
- `error_kind`
- `date`
- `agent`
- `session`

当前支持的 `window`：

- `total`
- `day`
- `week`
- `month`

说明：

- 未提供任何时间参数时，API 默认查询最近 7 个 UTC 自然日
- `start_date` 包含当天，`end_date` 不包含当天；两者必须同时提供，并使用严格的 UTC `YYYY-MM-DD` 格式
- `start_date`/`end_date` 不能与旧版 `window`/`bucket` 参数混用
- 显式传入 `window=total` 时仍查询全部历史
- `window=total` 时可省略 `bucket`
- `window=day` 时，`bucket` 形如 `2026-04-27`
- `window=week` 时，`bucket` 形如 `2026-W18`
- `window=month` 时，`bucket` 形如 `2026-04`

返回结构示例：

```json
{
  "items": [
    {
      "value": "office-key",
      "request_count": 12,
      "success_count": 12,
      "error_count": 0,
      "fallback_count": 0,
      "input_tokens": 1234,
      "output_tokens": 567,
      "cached_input_read_tokens": 42,
      "cached_input_write_tokens": 8,
      "cache_read_tokens": 42,
      "cache_create_tokens": 8,
      "total_tokens": 1851,
      "cost_usd": 0.0012,
      "provider_usage_count": 12,
      "estimated_usage_count": 0,
      "last_seen_at": "2026-04-25T09:00:00Z"
    }
  ]
}
```

其中：

- `clients[].name` 仍是稳定统计身份
- `value` 的含义由 `group_by` 决定
- `group_by=session` 会优先使用 `syrogo run claude` 注册的活跃 session，也支持显式传入 `X-Syrogo-Session-ID` / `X-Syrogo-Agent` header
- `cost_usd` 会在执行模型命中 Syrogo 内嵌的 LiteLLM 价格快照时自动计算；`accounting.pricing` 中的条目会覆盖内嵌默认价格
- 内嵌快照会在 `internal/accounting/pricing_default.json` 中记录 LiteLLM revision；需要更新时，先有意修改 `scripts/update_pricing.py` 中锁定的 revision，再运行 `make update-pricing`
- `group_by=error_kind` 会把成功请求归到 `none`，失败请求归到 `quota_exceeded`、`timeout`、`upstream_server_error`、`auth_failed`、`capability_unsupported` 等分类
- fallback 只会在 quota、timeout、临时错误或上游 5xx 等可恢复错误上继续切换；鉴权、capability 与请求结构错误会直接暴露
- 查询始终读取内存聚合视图，不会在请求时扫盘
- `local_file` 将 append-only 原始记录按天保存为 JSONL，并按 `retention_days` 清理；每日聚合 snapshot 使用独立的 `snapshot_retention_days`，因此聚合 coverage 可以长于原始记录。此类聚合用于趋势和 “Daily records”，不是逐请求审计

对应配置示例：

```yaml
accounting:
  enabled: true
  backend: "local_file"
  expose_http: true
  admin_token: "${SYROGO_ACCOUNTING_ADMIN_TOKEN}"
  pricing:
    - provider: "openai"
      model: "gpt-4o-mini"
      input_per_million_usd: 0.15
      output_per_million_usd: 0.60
      cache_create_per_million_usd: 0
      cache_read_per_million_usd: 0.075
  local_file:
    dir: "./tmp/accounting"
    rotate_max_size_mb: 64
    retention_days: 30
    snapshot_retention_days: 30
    write_buffer_records: 128
    flush_interval: "2s"
    queue_size: 4096
```

本地开发时可使用：

- `--dev-log`：把日志同时输出到 stdout 与 `tmp/dev.log`
- `SYROGO_TRACE=1` 或 `SYROGO_TRACE=full`：输出 trace 调试文件到 `tmp/trace`

更细的协议语义、调试开关与维护约束，请看：
- `.claude/rules/architecture.md`
- `.claude/rules/engineering.md`

---

## 当前边界

当前阶段还**不追求**：

- 复杂插件系统
- gRPC / MCP / WebSocket 等额外接入层
- 完整 semantic routing
- 对外 Go SDK 或 `pkg` 级公共库抽象
- 为未来假设需求提前搭建平台层
- multimodal 全量无损支持
- 所有上游协议能力的一比一透传

当前更重要的是：

**先把协议入口、内部抽象、路由执行与 provider 边界稳定下来。**

---

## Roadmap

接下来优先推进的方向：

- 持续稳固多协议入口与多协议出站闭环
- 继续增强 routing、fallback、round_robin 的可验证性
- 完善 provider 适配边界与错误分类
- 逐步补齐治理相关能力
  - 额度切换
  - 统计
  - 多节点 relay / hop 模式，例如国内 Syrogo A 把标准化流量转发到海外 Syrogo B，再由 B 访问真实上游 provider
- 在不破坏主链路抽象的前提下，再扩展更多 provider 与协议能力

---

## 说明

这份 README 主要面向项目介绍、功能边界、配置用法和使用入口。

更细的链路维护知识、协议边界、流式抽象、测试门槛与改动 guardrails，统一沉淀在 `.claude/rules` 中，避免产品说明与开发规则混写。
