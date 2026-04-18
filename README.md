# Syrogo

Syrogo 是一个面向多模型场景的 AI Gateway / Semantic Router。

它不是只做单一协议转发的代理层，而是一个放在客户端与上游模型之间的中间系统，用来统一承接：
- 多种入口协议
- 中立请求/响应抽象
- 基于 tag 的路由决策
- 多上游 provider 接入
- failover / round_robin 等基础调度
- 后续额度切换、统计、治理与多节点串接能力

当前项目仍处于 0→1 骨架建设阶段，优先目标是把服务主链路、协议边界与路由模型打稳。

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
- 三类 inbound protocol
  - `openai_chat`
  - `openai_responses`
  - `anthropic_messages`
- 统一转换到内部中立模型
  - `runtime.Request`
  - `runtime.Response`
  - `runtime.StreamEvent`
- 基于 `client token -> active tag` 的入口识别
- 基于 `routing.rules` 的首条规则命中
- 单条规则内的：
  - `failover`
  - `round_robin`
- 多类 outbound protocol
  - `mock`
  - `openai_chat`
  - `openai_responses`
  - `anthropic_messages`
- OpenAI-compatible 与 Anthropic-compatible 上游调用
- 基础 SSE 流式返回
- 最小 tool calling 映射闭环
- 关键链路单元测试、回归测试与流程测试

---

## 当前主链路

系统内部主链路可以概括为：

```text
client request
  -> gateway (inbound protocol)
  -> runtime.Request
  -> router (match by active tag)
  -> execution (consume execution plan)
  -> provider (outbound protocol)
  -> upstream model API
  -> runtime.Response / runtime.StreamEvent
  -> gateway serialize
  -> client protocol response
```

其中：
- 非流式统一落在 `runtime.Response`
- 流式统一落在 `runtime.StreamEvent`
- 不同协议之间真正变化的部分，尽量只留在入口解析、出口序列化与 provider 编解码边界

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

### 1. 准备配置

从示例配置复制一份本地配置：

```bash
cp configs/config.example.yaml configs/config.yaml
```

然后把 `configs/config.yaml` 中的 token、endpoint、auth_token 改成你本地可用的真实值。

注意：当前实现不会自动读取 `.env`，也不会自动展开 `${VAR}`。如果配置文件里保留占位符字符串，它会被原样读入。

### 2. 启动服务

优先使用：

```bash
make run
```

如果只想做最小本地验证，也可以把某个 route 指到 `mock` outbound。

### 3. 检查健康状态

```bash
curl http://127.0.0.1:8080/healthz
```

如果你的监听端口不是 `:8080`，请按实际配置替换。

### 4. 验证协议入口

当前建议优先验证：
- `POST /v1/chat/completions`
- `POST /v1/responses`
- `POST /v1/messages`

具体协议细节、链路抽象与维护约束，请看：
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
  - 多节点串接
- 在不破坏主链路抽象的前提下，再扩展更多 provider 与协议能力

---

## 说明

README 主要面向项目介绍、原理和使用入口。

更细的链路维护知识、协议边界、流式抽象、测试与改动 guardrails，统一沉淀在 `.claude/rules` 中，避免产品说明与开发规则混写。
