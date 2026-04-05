# Syrogo Claude Rules

## Project positioning
Syrogo 是一个多模型 AI Gateway / Semantic Router。

当前项目仍处于 0→1 骨架建设阶段。当前工作的优先目标是：
- 保持 Go 服务骨架可运行
- 沿现有结构增量演进
- 为后续 provider、routing、governance 能力预留清晰边界

当前优先事项：
- HTTP 服务与优雅退出
- 配置加载与校验
- `/healthz`
- 最小 `/v1/chat/completions`
- router / provider 抽象边界

当前不优先事项：
- 复杂插件系统
- 多协议接入
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

## Code navigation
- 入口：`cmd/syrogo/main.go`
- 应用装配：`internal/app/app.go`
- 配置：`internal/config/config.go`
- HTTP server：`internal/server/http.go`
- API handler：`internal/gateway/handler.go`
- 路由决策：`internal/router/router.go`
- Provider 抽象：`internal/provider/provider.go`

## Rules index
- 产品范围与阶段边界：`.claude/rules/product.md`
- 架构分层与依赖方向：`.claude/rules/architecture.md`
- 工程实践与开发约束：`.claude/rules/engineering.md`
- Git 协作与提交策略：`.claude/rules/git.md`
