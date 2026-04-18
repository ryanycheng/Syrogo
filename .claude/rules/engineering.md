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

## Documentation updates
- README 负责项目定位、原理、能力边界、快速体验与 roadmap，不承担细粒度排障手册职责。
- `.claude/rules/architecture.md` 负责解释系统链路、分层职责与流事件抽象。
- 本文件负责维护约束、验证门槛与关键 guardrails。
- 新增配置项、接口路径、协议或运行方式时，同步更新 `README.md`、配置样例和必要规则文档。
- 如果改动影响主链路抽象、协议语义或对外行为，README 与 rules 必须同步更新，避免产品文档与维护规则分叉。
- 如果新增规则影响 AI 协作方式，更新 `.claude` 规则文件，不要把规则散落在临时说明里。