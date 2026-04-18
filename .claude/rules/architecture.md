# Architecture Rules

## Layer responsibilities
- `cmd/syrogo`
  - 负责启动参数、配置加载、服务启动、信号处理和优雅退出。
  - 不承载业务逻辑。

- `internal/app`
  - 负责应用装配与依赖组装。
  - 在这里连接 config、server、gateway、router、provider。
  - 保持为装配层，不演化为初始化杂物堆。

- `internal/config`
  - 负责配置结构定义、加载和校验。
  - 新增配置项时应同步补充校验逻辑。
  - 保持配置简单，避免过早引入复杂配置体系。

- `internal/server`
  - 负责 HTTP server 生命周期管理。
  - 不写业务 handler。

- `internal/gateway`
  - 负责 HTTP 请求解析、参数校验、错误映射和响应序列化。
  - 负责 inbound protocol -> `runtime` 的转换。
  - 可以按协议拆分 OpenAI / Anthropic 等入口处理，但不直接承载 provider 选择策略。
  - 不承载复杂路由规则、配置决策或 provider 特定 transform。

- `internal/router`
  - 负责按 `ActiveTag` 对 `routing.rules` 做匹配与执行计划生成。
  - 负责首条规则命中，以及单条规则内的 `failover` / `round_robin` 展开。
  - 不处理 HTTP 层细节。
  - 不硬编码 provider 特定实现细节。

- `internal/execution`
  - 负责按 `ExecutionPlan` 执行 steps，并按错误类型触发 fallback。
  - 不承担协议适配职责。

- `internal/provider`
  - 负责 provider 抽象与具体 provider 实现。
  - 负责 `runtime` -> 上游 outbound protocol 的转换。
  - provider-specific request/response/stream transform 应显式放在这里，而不是散落到 gateway/router。
  - 接口设计应围绕当前真实使用场景，避免过大接口面。

- `internal/runtime`
  - 负责中立标准模型：请求、响应、流式事件、路由上下文、执行计划。
  - 尽量不承载 OpenAI / Anthropic 专属结构。

- `internal/eventstream`
  - 负责把 `runtime.Response` / `runtime.StreamEvent` 整理成中立事件序列与快照。
  - 为 gateway 的协议响应序列化提供统一流事件中间层。
  - 不直接承担具体 provider 的上游协议适配。

## Unified request path
系统内部统一请求链路应保持为：

```text
inbound protocol
  -> gateway parse / validate
  -> runtime.Request
  -> router match rule by active tag
  -> execution consume ExecutionPlan
  -> provider encode outbound request
  -> upstream model API
```

约束如下：
- inbound 请求先在 `gateway` 中解析、校验、归一化。
- 对外协议中的 message、system、tool、stream 等概念，先转换成中立 `runtime.Request`。
- `router` 只负责基于 tag 的规则匹配与 plan 生成，不关心具体 HTTP 协议。
- `execution` 只负责执行 outbound step、应用模型覆盖、处理 fallback，不做协议转换。
- `provider` 负责把 `runtime.Request` 编码成真实上游协议，并把返回结果解码回中立模型。

## Unified response path
非流式与流式都应先回到中立模型，再从边界层输出：

```text
upstream response
  -> provider decode
  -> runtime.Response / runtime.StreamEvent
  -> eventstream.Event sequence / snapshot
  -> gateway serialize
  -> protocol-specific response or SSE
```

约束如下：
- 非流式统一落在 `runtime.Response`。
- 流式统一落在 `runtime.StreamEvent`。
- `internal/eventstream` 负责把 runtime 层结果整理为稳定事件序列。
- gateway 再根据入口协议，把这些事件序列化为 Anthropic / OpenAI 等协议响应。
- 协议专属帧格式只留在边界层，不应回灌进 `runtime`。

## Runtime model expectations
`internal/runtime` 是系统主干抽象，至少要稳定承接：
- `Request`
- `Message`
- `ContentPart`
- `Response`
- `StreamEvent`
- `RouteContext`
- `ExecutionPlan`

使用原则：
- `runtime` 表达的是跨协议共享语义，而不是某一家上游的原始 schema。
- 如果某个字段只在单一协议里成立，应优先留在 gateway/provider 边界，而不是放进 `runtime`。
- `system`、tool calling、finish reason、usage 等共享语义可以进入 `runtime`，但协议专属命名与帧结构不应直接进入。

## Stream event model
流式链路必须优先围绕统一事件模型思考，而不是直接围绕某家 SSE 帧格式思考。

推荐理解顺序：
1. provider 产出 `runtime.StreamEvent`
2. `internal/eventstream` 将其整理为中立 `Event`
3. gateway 再把 `Event` 序列化为入口协议要求的 stream 帧

这意味着：
- 流式与非流式应尽量共享 message / tool / usage / finish reason 语义。
- 文本 delta、tool call delta、usage、message 生命周期应先在中立事件层对齐。
- “如何发帧”是最后一步，不应倒逼 router / execution / runtime 绑定某种协议结构。

## Dependency direction
推荐依赖方向：
- `cmd -> app`
- `app -> config/server/gateway/router/provider/execution`
- `gateway -> router/execution/runtime/eventstream`
- `router -> provider/runtime`
- `provider -> runtime`
- `execution -> runtime`
- `eventstream -> runtime`

避免反向依赖和跨层泄漏。

## Common change map
- 新增 HTTP 入口协议：优先改 `internal/gateway`
- 新增配置项：改 `internal/config` 和 `configs/*.yaml`
- 新增 provider / outbound protocol：改 `internal/provider` 与 `internal/app`
- 调整 tag 路由规则：改 `internal/router`
- 调整执行 / fallback 语义：改 `internal/execution`
- 调整统一流事件模型：优先改 `internal/runtime` / `internal/eventstream`
- 调整启动参数或生命周期：改 `cmd/syrogo` / `internal/server`

## Directory hygiene
- 目录要保持少而明确，各目录必须有稳定职责。
- 当前服务型项目以 `cmd + internal` 为主；没有真实公共复用需求前，不引入 `pkg`。
- 在没有真实需求前，不新增 `common`、`utils`、`base`、`lib` 这类兜底目录。
- 公共逻辑优先放在已有明确职责的包内，不要建立万能收纳层。
- 根目录只保留项目级入口文件与开发配置，不随意新增松散文件或无归属目录。

## Architecture guardrails
- 不把业务逻辑塞进 `main.go`。
- 不把 HTTP request/response 类型泄漏到 router/provider。
- 不把 provider 特定逻辑硬编码进 router。
- 不在只有一个实现时过早引入多层接口、注册中心或插件体系。
- 不让 `gateway` 演化为协议、业务编排和 provider 适配的混合层。
- 不让 `app` 演化为承载所有初始化细节的集中杂物堆。
- 不为了“看起来标准”提前拆 `pkg`；只有明确要对外复用、并愿意维护稳定 API 时才考虑。
- 如果公共请求/响应模型开始被多个层共享，应尽快迁移到更中性的归属位置，而不是长期挂在具体协议实现下。
- 不把 Anthropic / OpenAI 专属字段长期塞进 `runtime`。
- 不让 router / execution 承担 tool、usage、SSE 帧等协议映射职责。