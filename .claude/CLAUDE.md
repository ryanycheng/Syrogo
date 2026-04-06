# Syrogo Claude Rules

## Project positioning
Syrogo 是一个多模型 AI Gateway / Semantic Router。

当前项目仍处于 0→1 骨架建设阶段。当前工作的优先目标是：
- 保持 Go 服务骨架可运行
- 沿现有结构增量演进
- 为后续 provider、routing、governance 能力预留清晰边界

当前已建立的核心方向：
- 使用 Go 构建服务型项目，而不是对外 Go SDK
- 目录结构以 `cmd + internal` 为主，不为了“看起来标准”引入 `pkg`
- `internal` 下按职责拆分装配、配置、入口协议、路由、执行、上游适配，而不是全部堆在单一包中
- inbound 负责外部协议进入系统，outbound 负责标准请求转真实上游协议
- `runtime` 作为中立标准模型，尽量不承载 OpenAI / Anthropic 专属结构

当前优先事项：
- HTTP 服务与优雅退出
- 配置加载与校验
- `/healthz`
- 最小多协议入口（当前已含 `openai_chat` / `anthropic_messages`）
- tag-first routing 与基础 failover / round_robin
- router / provider / gateway 边界稳定

当前不优先事项：
- 复杂插件系统
- 提前抽公共 SDK / `pkg`
- 完整 semantic routing
- 为未来假设需求做过度设计

## Working principles
- 优先小步增量修改，不做无关重构。
- 优先复用现有代码结构，不随意扩目录或改层次。
- 无明确需求时，不主动引入新依赖、插件化或额外抽象。
- 先满足当前可运行目标，再考虑更强的架构演进。
- 新增功能时，优先给出最小可验证路径。
- 测试是交付的一部分，不能只依赖手工验证。
- 新功能要有单元测试，修复问题要有回归测试，关键链路改动要有流程测试或集成测试。
- README 和配置样例是给人看的，新增配置、协议、接口后要同步更新，避免只在代码里“自解释”。

## Code navigation
- 入口：`cmd/syrogo/main.go`
- 应用装配：`internal/app/app.go`
- 配置：`internal/config/config.go`
- HTTP server：`internal/server/http.go`
- API handler / inbound protocol：`internal/gateway/handler.go`
- 路由决策：`internal/router/router.go`
- 执行与 fallback：`internal/execution/dispatcher.go`
- Provider / outbound protocol：`internal/provider/provider.go`
- 中立标准模型：`internal/runtime/runtime.go`

## Rules index
- 产品范围与阶段边界：`.claude/rules/product.md`
- 架构分层与依赖方向：`.claude/rules/architecture.md`
- 工程实践与开发约束：`.claude/rules/engineering.md`
- Git 协作与提交策略：`.claude/rules/git.md`
