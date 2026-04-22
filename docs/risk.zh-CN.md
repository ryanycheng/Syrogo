# 风险清单

中文 | [English](./risk.md)

本文总结当前 Syrogo 项目阶段的主要风险。

项目已经具备 0→1 网关骨架的可用性，但距离面向大规模或高稳定性场景的 production-grade 成熟度还有明显距离。

---

## 高优先级风险

### 1. 协议兼容性仍然容易出边角问题

当前 Syrogo 已经打通 OpenAI Chat、OpenAI Responses、Anthropic Messages 的入口与出站主链路。

现在最大的风险已经不是“主路径能不能跑”，而是边缘语义在真实客户端与真实上游之间能不能持续保持正确，例如：

- tool calling 字段形状
- `content: null` 与空字符串行为
- `finish_reason` / `stop_reason` 映射
- `usage` 映射
- mixed text/json 内容顺序
- streaming 事件顺序与 delta 语义
- tool result 错误态保留

这意味着：某条请求在 happy path 上看似兼容，但在更严格的 SDK 或另一家上游实现上，仍然可能暴露不兼容问题。

### 2. OpenAI-compatible 上游未必真的行为一致

很多上游会宣称自己兼容 OpenAI，但细节行为往往并不一致。

常见风险包括：

- 请求 shape 一样，但 tool 字段解释不同
- stream chunk 格式不同
- 缺失 usage 或 finish 字段
- assistant history / tool replay 处理方式不同
- 只部分支持 Responses API 特性

项目目前已经对 `openai_responses` 做了部分 capability guard，但整体 provider 兼容面仍然是高风险区域。

### 3. 真实流式联调仍需更多 soak testing

streaming 是整个网关里最脆弱的一段。

虽然当前已经补了回归测试，但真实联调风险仍集中在：

- OpenAI Chat tool-call arguments delta 语义
- Anthropic 事件顺序
- upstream、runtime、eventstream、gateway 序列化之间的 message 生命周期对齐
- 对 SSE 帧形状或顺序更严格的客户端

一条流在本地测试里“看起来正常”，并不代表它在真实 SDK 集成里一定稳定。

### 4. 治理与运行期保护能力还偏轻

当前项目已经具备路由与基本执行能力，但 production 控制面还比较轻。

主要缺口包括：

- 更强的 provider / route 级超时策略
- retry 策略与错误分类细化
- 限流与并发保护
- 熔断 / 背压行为
- 配额切换与预算控制
- 更完整的流量与失败统计

对于当前阶段这可以接受，但如果过早把它当成稳定多租户网关来使用，这会是实际风险。

---

## 中优先级风险

### 5. 可观测性可用，但还不到生产级

项目已经有 `--dev-log` 和 trace 输出，这对本地调试已经有帮助。

但要支撑生产运维，仍然缺少更强的可观测能力，例如：

- 结构化 metrics
- 更清晰的 request / route / provider 维度
- 延迟与错误 dashboard
- 更适合告警的失败计数器
- 更容易串联 gateway 与 upstream 的 trace 关联能力

如果没有这些能力，很多问题仍然只能靠人工翻日志定位。

### 6. 配置误用风险仍然不低

虽然当前已经有配置加载与基础校验，但仍有几类误配成本较高：

- token 或 endpoint 配错
- routing rule 语法合法，但运行上不安全
- capability 声明与真实上游行为不一致
- listener / inbound 组合暴露了不该暴露的协议
- model mapping 看起来合法，但实际导向了错误上游行为

当前阶段的配置能力已经不弱，但也因此更容易被误用。

### 7. 发布成熟度已经快于运行成熟度

当前发布路径已经相对完整：

- 有 release workflow
- 有 tag 驱动发布
- 有本地 pre-release check
- 有安装器入口

但“能稳定发包”比“能稳定承接异构上游流量”更容易做到。

真正的风险不在于发版本本身，而在于发布节奏可能跑在兼容性与运行信心前面。

---

## 低优先级但值得注意的风险

### 8. 安全加固目前仍然偏基础

当前工作重点仍然是可运行骨架与协议闭环，而不是完整安全加固。

后续大概率仍需要持续补强：

- 边界输入的更保守校验
- secret 处理与脱敏保证
- trace / log 内容的更严格保护
- 面向生产部署的更明确安全建议

### 9. 项目仍依赖持续的架构纪律

当前代码库最强的一点之一，就是分层边界相对清晰。

未来真正的风险在于边界漂移，例如：

- 把 provider 细节塞回 gateway 或 router
- 把协议专属字段泄漏进 runtime
- 过早增加抽象层
- 破坏当前 `gateway -> runtime -> router/execution -> provider` 的主链路

这不是当前 bug，但会是未来维护成本的重要来源。

---

## 建议的下一步

### 短期

1. 增加真实 provider 的 smoke test，覆盖 OpenAI-compatible 与 Anthropic-compatible 上游。
2. 扩充协议边角回归测试，重点放在 stream 顺序与 tool 语义。
3. 加强 capability 声明与高风险路由组合的配置校验。
4. 补一份简洁的兼容性矩阵，明确哪些是 fully supported、partially supported、intentionally unsupported。

### 中期

1. 增加 metrics 与更适合生产的 observability。
2. 加强 timeout / retry / fallback / concurrency 控制。
3. 增加更清晰的 provider capability 治理与错误分类体系。
4. 补齐更明确的生产部署与运行建议。

---

## 结论

Syrogo 现在已经是一个边界清晰、主链路可运行的 0→1 gateway skeleton。

它当前最大的风险，已经不再是启动、路由或基本闭环，而是协议边角兼容性、异构 provider 行为差异，以及 production-grade 治理与可观测成熟度不足。
