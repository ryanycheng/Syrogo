# Engineering Rules

## Change principles
- 优先最小改动满足需求。
- 不顺手重构、不无故重命名。
- 不在没有明确收益时调整目录结构。
- 单次改动尽量围绕一个明确目标。
- 对服务型项目，优先稳定 `cmd + internal` 结构；没有真实复用需求前不主动拆 `pkg`。

## Go practices
- 使用 `gofmt` 和 `goimports` 保持格式统一。
- 错误信息尽量带上下文，必要时使用错误包装。
- 新配置项必须补充基础校验。
- 新增 provider capability 配置时，必须同时更新配置样例与对应回归测试。
- 避免引入全局状态。
- 优先清晰可读，而不是过度技巧化写法。
- 先在已有职责包内增量演进；除非边界已明显失真，否则不要新增目录。

## Abstraction strategy
- 只有在出现第二个明确实现，或确有测试替身需求时，再考虑扩大接口。
- 新抽象必须解决当前问题，而不是假设未来问题。
- 不为未来可能出现的复杂场景提前搭通用框架。
- 协议适配边界要明确：inbound transform 放 `gateway`，outbound/provider transform 放 `provider`，中立模型放 `runtime`，流事件整理放 `eventstream`。

## Testing requirements
- 开发过程必须补充测试，不能只依赖手工运行。
- 新功能必须编写对应的单元测试。
- 修复 bug 或修正行为时，必须补充对应的回归测试，确保同类问题不会再次出现。
- 涉及完整请求链路、模块协作或关键业务路径时，必须补充流程测试或等价的集成测试。
- 修改已有功能时，必须同时检查并更新相关测试，确保改动不会悄悄破坏既有行为。
- 如果当前代码缺少测试基础设施，新增功能时也应优先把最小必要测试补起来，而不是跳过。

## Verification
- 提交前必须完成与本次改动对应的测试验证，目标是尽早发现功能回退。
- 优先使用：
  - `make fmt`
  - `make test`
  - `make run`
- 本地环境具备时，优先运行 `golangci-lint run`。
- `config`、`router`、`gateway` 是优先测试对象。
- 对新增功能至少覆盖：
  - 单元测试
  - 必要的回归测试
  - 必要的流程测试或集成测试
- 如果某类测试暂时无法补齐，必须明确说明风险和缺口，不能默认忽略。
- 修改 `POST /v1/messages`、tools、codec、stream、`anthropic_messages`、`openai_chat` 等关键协议链路时，至少补一类能覆盖真实协议往返的流程测试；本地可验证时，优先再做一次真实联调。
- 修改 `openai_responses` provider capability 或兼容策略时，至少覆盖：配置解析、provider 编码/guard、以及一条真实本地 smoke test。

## Messages / tools / stream guardrails
- 修改主链路时，优先按这个顺序核对：inbound shape -> `runtime` lowering/raising -> outbound encode/decode -> 最终响应或 SSE 序列化。
- 不要只看最终文本结果；要确认中间抽象是否仍然成立。
- 改动时必须重点核对这些语义是否仍然成立：
  - `system`
  - `tool_use`
  - `tool_result`
  - `tool_use_id` / `tool_call_id` / 上游工具 ID 对齐
  - `stop_reason` / finish reason
  - `usage`
  - SSE 事件顺序
  - `input_json_delta` 增量语义
  - mixed text/json 顺序
- `gateway` 负责 inbound 协议解析与响应序列化，`provider` 负责 outbound 协议编码与解码；不要把 Anthropic / OpenAI 专属结构长期塞进 `runtime`。
- 改 `tool_result` 时，不要只验证文本内容；要同时验证错误态、ID 关联、mixed text/json 顺序是否保留。
- 改 streaming 时，不要只验证“有输出”；必须确认 message 生命周期、delta 形状、finish reason、usage、事件顺序是否仍符合目标协议。
- 如果某条流式实现内部采用“完整请求上游后再本地回放事件”，文档必须明确写出这个边界，不能让 README 或规则暗示为原生上游逐帧透传。

## Debugging and local trace
- README 只保留“支持本地调试”的入口说明；具体调试开关和值放在规则或专门文档，不在 README 中展开。
- `--dev-log` 用于把运行日志同时输出到 stdout 与 `tmp/dev.log`，适合本地开发观察服务行为。
- `SYROGO_TRACE=1` 或 `SYROGO_TRACE=full` 时，应输出 inbound / outbound trace 到 `tmp/trace`，用于排查协议转换、provider 编解码与响应序列化问题。
- `SYROGO_TRACE=inbound` 时，只记录入口请求调试快照。
- `SYROGO_TRACE=anthropic_stream` 时，只记录 Anthropic stream 序列化调试输出。
- 涉及 trace 的改动时，必须确认输出不泄露敏感凭据；认证头与 key 类字段应继续脱敏。

## Responses compatibility guardrails
- README 只说明“支持 Responses compatibility 声明”；具体 guard/reject/rewrite 行为留在规则和测试中维护。
- 当上游 `openai_responses` 能力不完整时，应优先依据 `outbound.capabilities` 做显式行为控制，而不是默认假设官方完全兼容。
- 兼容策略如果涉及 request 重写、能力拒绝或字段丢弃，必须补对应回归测试，并在必要时补一条真实 smoke test。

## README writing guideline
- README 应优先贴合业务与产品表达：先说明项目是什么、解决什么问题、适合什么场景，再说明当前已具备能力与如何上手。
- README 中的 feature 描述优先使用用户能理解的能力语言，不优先暴露内部实现名词；除非该名词本身就是用户配置接口的一部分。
- README 可以写协议入口、出站能力、路由能力、配置入口、快速开始、边界、roadmap，但不要承载细粒度实现机制。
- 像 `runtime.Request`、`runtime.Response`、`runtime.StreamEvent`、`ExecutionPlan`、`active tag` 这类内部抽象，不应作为 README 主体叙述重点。
- 像 fallback 错误分类、request rewrite、provider guard/reject 规则、trace 细粒度开关等维护者语义，应放在 rules 或专门文档，而不是 README。
- README 应该承担总览与导航职责；当细节对维护者更重要时，优先链接到 `.claude/rules/architecture.md`、`.claude/rules/engineering.md` 或后续专门文档。
- README 的快速开始应尽量短，优先给出最小可运行路径，而不是一次解释全部内部原理。
- 当 README 与规则发生取舍冲突时：README 优先服务“用户理解产品和上手”，rules 优先服务“维护者理解实现与约束”。
- 若维护双语 README，默认对外首页使用英文 `README.md`，中文内容放在 `README.zh-CN.md`，并在两个文件顶部提供显式语言切换链接。

## Documentation updates
- README 负责项目定位、功能边界、配置用法、快速体验与 roadmap，不承担细粒度排障手册职责。
- `.claude/rules/architecture.md` 负责解释系统链路、分层职责、capability 放置原则与流事件抽象。
- 本文件负责维护约束、验证门槛与关键 guardrails。
- 新增配置项、接口路径、协议或运行方式时，同步更新 `README.md`、配置样例和必要规则文档。
- 如果改动影响主链路抽象、协议语义或对外行为，README 与 rules 必须同步更新，避免产品文档与维护规则分叉。
- 如果新增规则影响 AI 协作方式，更新 `.claude` 规则文件，不要把规则散落在临时说明里。