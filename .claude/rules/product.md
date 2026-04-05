# Product Rules

## Current stage
项目当前处于骨架优先阶段，不是功能完备阶段。

目标是先把主链路跑通，并在保持代码简单的前提下，为后续能力扩展留下清晰边界。

## Current priorities
当前优先实现和完善：
- 服务启动与优雅退出
- 配置加载与基础校验
- `GET /healthz`
- `POST /v1/chat/completions`
- model -> provider 的最小路由能力
- 用 mock / stub provider 打通调用链路

## Not priorities for now
当前阶段不要主动推进以下方向，除非用户明确要求：
- 复杂插件系统
- 多协议接入（如 gRPC、MCP、WebSocket）
- 完整 semantic routing
- 为未来 provider 场景预先搭建通用平台
- 为假设需求做过度拆分和抽象

## Decision guideline
- 如果需求能在现有骨架上增量实现，就不要增加新的架构层。
- 如果一个抽象只服务当前单一实现，通常先不要抽。
- 优先完成最小可运行闭环，而不是追求一次到位的未来设计。
