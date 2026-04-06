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

## Dependency direction
推荐依赖方向：
- `cmd -> app`
- `app -> config/server/gateway/router/provider/execution`
- `gateway -> router/execution/runtime`
- `router -> provider/runtime`
- `provider -> runtime`
- `execution -> runtime`

避免反向依赖和跨层泄漏。

## Common change map
- 新增 HTTP 入口协议：优先改 `internal/gateway`
- 新增配置项：改 `internal/config` 和 `configs/*.yaml`
- 新增 provider / outbound protocol：改 `internal/provider` 与 `internal/app`
- 调整 tag 路由规则：改 `internal/router`
- 调整执行 / fallback 语义：改 `internal/execution`
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
