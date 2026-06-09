# Real E2E Verification Rules


当修改协议、provider、routing、stream、tools、usage、accounting，或验证 Claude Code / Codex 真实接入时，必须按本规则做真实端到端验收。

## 范围

真实 E2E 通过不等于“能返回一次响应”。网关必须能跑通真实开发工作流：

- 健康检查
- 非流式与流式协议调用
- 多轮对话上下文
- 工具调用完整往返
- Claude Code 通过 Syrogo 使用
- Codex 通过 Syrogo 使用
- 启用 accounting 时能看到 usage
- 能定位上游 capability 或模型映射导致的失败

## 前置条件

- 使用真实 outbound 配置启动 Syrogo，不只使用 mock。
- 使用未被占用的端口。
- 命令输出和日志中不要打印 token。
- 可用时打开本地开发日志 `-dev-log`。
- 排查 trace 时只打开最小必要的 `SYROGO_TRACE` 模式，并确认认证头和 key 类字段继续脱敏。

推荐启动命令：

```bash
go run ./cmd/syrogo -config ./configs/config.yaml -dev-log
```

如果 `:23234` 已被占用，把配置复制到 `tmp/real-smoke.yaml`，修改监听端口后再测。

## 1. 健康检查与 smoke 脚本

```bash
curl http://127.0.0.1:23234/healthz
```

然后对正在运行的 gateway 执行 smoke：

```bash
SYROGO_SMOKE_BASE_URL=http://127.0.0.1:23234 \
SYROGO_OPENAI_CLIENT_TOKEN=<chat-token> \
SYROGO_RESPONSES_CLIENT_TOKEN=<responses-token> \
SYROGO_ANTHROPIC_CLIENT_TOKEN=<anthropic-token> \
SYROGO_ACCOUNTING_ADMIN_TOKEN=<accounting-admin-token> \
make smoke
```

预期：

- healthz 返回 `200`
- 已配置的协议入口返回 `2xx`
- stream 检查返回 SSE 帧
- accounting HTTP 启用时，usage stats 返回 `2xx`

## 2. 协议路由矩阵

按当前配置意图验证矩阵。

| Inbound | Outbound | 是否必须通过 |
| --- | --- | --- |
| OpenAI Chat | `openai_chat` | 是 |
| OpenAI Chat | `openai_responses` | 是 |
| OpenAI Chat | `anthropic_messages` | 上游模型映射支持时必须通过 |
| OpenAI Responses | `openai_chat` | 是 |
| OpenAI Responses | `openai_responses` | 是 |
| OpenAI Responses | `anthropic_messages` | 上游模型映射支持时必须通过 |
| Anthropic Messages | `openai_chat` | 是 |
| Anthropic Messages | `openai_responses` | 是 |
| Anthropic Messages | `anthropic_messages` | 是 |

每一行都要确认：

- HTTP status 是 `2xx`
- 响应结构符合入口协议
- 路由后的模型是配置里的 `target_model`
- `tmp/dev.log` 里 `active_tag`、`matched_rule`、`resolved_to` 符合预期
- 失败时能判断是 Syrogo 映射问题、provider capability guard，还是上游不可用

## 3. 流式链路

至少验证：

- OpenAI Chat stream 经过真实上游
- Anthropic Messages stream 经过真实上游

预期：

- 响应 `Content-Type` 是 `text/event-stream`
- OpenAI Chat stream 以 `data: [DONE]` 结束
- Anthropic Messages stream 有完整 message 生命周期事件，并以 `event: done` 结束
- text delta 顺序保留
- finish reason 存在
- 上游提供 usage 或启用 estimation 时，usage 存在

## 4. 多轮对话

单次响应不算完整通过。每条日常工具路由至少验证一次多轮上下文。

### OpenAI Chat 风格

请求 1：

```json
{
  "model": "ignored-by-route",
  "messages": [
    {"role": "user", "content": "Remember this word: syrogo-e2e."}
  ]
}
```

请求 2：

```json
{
  "model": "ignored-by-route",
  "messages": [
    {"role": "user", "content": "Remember this word: syrogo-e2e."},
    {"role": "assistant", "content": "I will remember the word syrogo-e2e."},
    {"role": "user", "content": "What word did I ask you to remember? Reply with only the word."}
  ]
}
```

预期：最终回答包含 `syrogo-e2e`。

### Anthropic Messages 风格

请求 1：

```json
{
  "model": "ignored-by-route",
  "max_tokens": 128,
  "messages": [
    {"role": "user", "content": [{"type": "text", "text": "Remember this word: syrogo-e2e."}]}
  ]
}
```

请求 2：

```json
{
  "model": "ignored-by-route",
  "max_tokens": 128,
  "messages": [
    {"role": "user", "content": [{"type": "text", "text": "Remember this word: syrogo-e2e."}]},
    {"role": "assistant", "content": [{"type": "text", "text": "I will remember the word syrogo-e2e."}]},
    {"role": "user", "content": [{"type": "text", "text": "What word did I ask you to remember? Reply with only the word."}]}
  ]
}
```

预期：最终回答包含 `syrogo-e2e`。

## 5. 工具调用完整往返

工具调用必须验证完整 loop，不能只看模型吐出了 tool-call 帧。

### 需要确认

- 模型发出 tool call
- gateway 保留 tool name
- gateway 保留 tool input JSON
- tool ID 在 tool result 阶段保持一致
- tool result 能再次发回模型
- 最终模型回答使用了 tool result
- 流式路径启用时，tool delta 顺序没有丢失

### 最小工具场景

使用简单函数工具，例如：

```json
{
  "name": "get_city_weather",
  "description": "Return weather for a city.",
  "input_schema": {
    "type": "object",
    "properties": {
      "city": {"type": "string"}
    },
    "required": ["city"]
  }
}
```

Prompt：

```text
Use the weather tool for Shanghai, then answer with the returned weather.
```

Tool result：

```json
{"weather":"sunny","temperature_c":26}
```

预期最终响应：

- 提到 `sunny`
- 提到 `26`
- 没有 tool ID mismatch
- 没有 malformed JSON argument
- 没有丢失 mixed text/tool 顺序

## 6. Claude Code 通过 Syrogo

Claude Code 走 Anthropic Messages inbound。

注意：Claude Code 指向 Syrogo 时，client token 使用 `ANTHROPIC_AUTH_TOKEN`。

```bash
ANTHROPIC_BASE_URL=http://127.0.0.1:23234 \
ANTHROPIC_AUTH_TOKEN=<syrogo-anthropic-client-token> \
claude --bare -p --model claude-sonnet-4-6 \
  "Reply with exactly: syrogo-claude-ok"
```

预期：

- 输出 `syrogo-claude-ok`
- `tmp/dev.log` 显示 `/v1/messages`
- 路由命中预期 outbound

然后跑真实工具工作流：

```bash
ANTHROPIC_BASE_URL=http://127.0.0.1:23234 \
ANTHROPIC_AUTH_TOKEN=<syrogo-anthropic-client-token> \
claude --bare -p --model claude-sonnet-4-6 \
  "Read the Makefile and tell me the available make targets. Do not modify files."
```

预期：

- Claude Code 能完成本地 tool use
- 最终回答引用真实 Makefile targets
- Syrogo 记录成功模型调用
- `tmp/dev.log` 没有协议或 stream 错误

## 7. Codex 通过 Syrogo

Codex 走 OpenAI Responses inbound。

推荐日常路由：Responses inbound 到 `openai_chat` outbound，除非 `openai_responses` outbound 明确支持 Codex 会发送的 builtin tools。

使用临时 `CODEX_HOME`，避免修改用户全局配置：

```bash
CODEX_HOME=/path/to/tmp/codex-syrogo-home \
codex exec --skip-git-repo-check -s read-only \
  --output-last-message /tmp/syrogo-codex.out \
  "Reply with exactly: syrogo-codex-ok"
```

预期：

- 输出 `syrogo-codex-ok`
- `tmp/dev.log` 显示 `/v1/responses`
- 路由命中预期 outbound

然后跑真实工具工作流：

```bash
CODEX_HOME=/path/to/tmp/codex-syrogo-home \
codex exec --skip-git-repo-check -s read-only \
  --output-last-message /tmp/syrogo-codex-tools.out \
  "Inspect the Makefile and list the available targets. Do not modify files."
```

预期：

- Codex 能完成本地 file/tool work
- 最终回答引用真实 Makefile targets
- Syrogo 模型调用成功
- 如果使用 `openai_responses` outbound，builtin-tool capability guard 不应拒绝请求
- Codex stderr 不应反复出现 malformed function-argument 错误；如果出现但最终工作流成功，应记录为兼容性 warning

## 8. Usage 与 accounting

启用 accounting HTTP 时：

```bash
curl 'http://127.0.0.1:23234/stats/usage?group_by=key' \
  -H 'Authorization: Bearer <accounting-admin-token>'

curl 'http://127.0.0.1:23234/stats/usage?group_by=provider' \
  -H 'Authorization: Bearer <accounting-admin-token>'
```

预期：

- E2E 检查后 client key 请求数增加
- E2E 检查后 provider 请求数增加
- success/error 计数和观察到的结果一致
- 上游提供 usage 时记录 provider-native usage
- 只有显式启用 estimation 时才记录 estimated usage

## 9. 通过/失败规则

真实 E2E 只有满足以下条件才算通过：

- 日常 Claude Code 路由通过单轮、多轮和工具工作流
- 日常 Codex 路由通过单轮、多轮和工具工作流
- 至少一条真实流式路由通过
- 预期支持的协议矩阵行通过；不支持的行必须明确归因到上游 capability 或模型映射
- 启用 usage/accounting 时能反映请求
- 日志和命令输出没有泄露敏感凭据

只返回一次成功响应，不算完整验证通过。
