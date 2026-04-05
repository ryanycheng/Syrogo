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
  - 不直接承载 provider 选择策略。
  - 不承载复杂路由规则、配置决策或 provider 特定逻辑。

- `internal/router`
  - 负责模型到 provider 的路由决策。
  - 不处理 HTTP 层细节。
  - 不硬编码 provider 特定实现细节。

- `internal/provider`
  - 负责 provider 抽象与具体 provider 实现。
  - 接口设计应围绕当前真实使用场景，避免过大接口面。
  - 不长期承载网关层的公共协议模型与跨层类型。

## Dependency direction
推荐依赖方向：
- `cmd -> app`
- `app -> config/server/gateway/router/provider`
- `gateway -> router/provider`
- `router -> provider`

避免反向依赖和跨层泄漏。

## Common change map
- 新增 HTTP 接口：优先改 `internal/gateway`
- 新增配置项：改 `internal/config` 和 `configs/*.yaml`
- 新增 provider 类型：改 `internal/provider` 与 `internal/app`
- 调整 model 到 provider 的映射：改 `internal/router`
- 调整启动参数或生命周期：改 `cmd/syrogo` / `internal/server`

## Directory hygiene
- 目录要保持少而明确，各目录必须有稳定职责。
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
- 如果公共请求/响应模型开始被多个层共享，应尽快迁移到更中性的归属位置，而不是长期挂在 `provider` 下。
