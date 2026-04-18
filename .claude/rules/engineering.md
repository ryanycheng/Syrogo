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
- 协议适配边界要明确：inbound transform 放 `gateway`，outbound/provider transform 放 `provider`，中立模型放 `runtime`。

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
- 修改 `POST /v1/messages`、`anthropic_messages`、tools、stream 相关逻辑时，至少补一类能覆盖真实协议往返的流程测试；本地可验证时，优先再做一次真实联调。

## Claude Code / Messages bridge guardrails
- 修改 `messages` 链路时，优先先看 inbound shape、runtime lowering、outbound encode，再看最终响应；不要只盯最终文本结果。
- 改动时必须重点核对这些语义是否仍然成立：`system`、`tool_use`、`tool_result`、`tool_use_id` / `tool_call_id`、`stop_reason`、`usage`、SSE 事件顺序、`input_json_delta` 增量语义。
- `gateway` 负责 inbound 协议解析与响应序列化，`provider` 负责 outbound 协议编码与解码；不要把 Anthropic / OpenAI 专属结构长期塞进 `runtime`。
- 改 `tool_result` 时，不要只验证文本内容；要同时验证错误态、ID 关联、mixed text/json 顺序是否保留。
- 改 streaming 时，不要只验证“有输出”；必须确认事件生命周期、delta 形状、finish/stop reason 是否仍符合目标协议。
- 如果改动影响 `messages`、tool bridge 或 streaming 语义，必须同步更新 `README.md` 里的对应章节，避免文档与真实行为分叉。

## Documentation updates
- 新增配置项、接口路径、协议或运行方式时，同步更新 `README.md`、配置样例和必要规则文档。
- README 默认是给人看的，而不是给熟悉代码的人看的；要写清楚项目是什么、怎么跑、配置怎么理解、当前支持什么、不支持什么。
- 如果新增规则影响 AI 协作方式，更新 `.claude` 规则文件，不要把规则散落在临时说明里。
