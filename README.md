# Syrogo

[中文](./README.zh-CN.md) | English

<p align="center">
  <img src="./docs/assets/SyroGo-logo.png" alt="SyroGo" width="500">
</p>

> Syrogo · AI Gateway / Semantic Router
>
> Route model traffic with clearer boundaries, multi-protocol access, and gateway-ready orchestration.

- **Multi-protocol inbounds** — OpenAI Chat, OpenAI Responses, and Anthropic Messages in one gateway.
- **Routing for real scenarios** — route by client tag, target model, failover, and round_robin.
- **Provider-ready execution** — connect multiple upstreams without pushing protocol differences into every client.

Syrogo is an AI Gateway / Semantic Router for multi-model scenarios.

It is not just a thin proxy for forwarding a single protocol. It sits between clients and upstream model providers to unify:
- multiple inbound protocols
- multiple upstream providers
- routing by client scenario
- basic scheduling such as failover and round_robin
- future governance capabilities such as quota switching, usage statistics, and multi-node chaining

The project is still in the 0→1 bootstrap stage. The current priority is to stabilize the main service path, protocol boundaries, and routing model.

---

## Why the name Syrogo

Syrogo combines the imagery of synapses and neurons with the ideas of routing and Go.

The name is meant to suggest connection, transfer, and dispatch across model traffic, while still making it clear that this is a gateway system built in Go.

---

## Why this project exists

In real-world model access scenarios, client protocols, upstream protocols, model naming, authentication methods, and reliability strategies are often inconsistent.

Syrogo is not trying to be “just another HTTP wrapper”. Its goal is to keep these moving parts within clear boundaries:
- clients connect using the protocol they already know
- requests are normalized into a unified internal model
- routing focuses only on where traffic should go
- providers focus only on how to talk to specific upstreams
- responses are returned in the protocol the client expects

This keeps access, routing, failover, and governance decoupled instead of scattering them across every provider and handler branch.

---

## Design principles

- Build the smallest runnable loop first, then expand capabilities
- Keep the `cmd + internal` structure stable instead of introducing `pkg` too early just to look “standard”
- Let `gateway` handle inbound protocol parsing and response serialization
- Let `runtime` hold the neutral request, response, and stream models
- Let `router` / `execution` handle routing and execution rather than protocol adaptation
- Let `provider` handle outbound encoding, upstream calls, and result decoding
- Reuse the same internal abstractions for streaming and non-streaming as much as possible, and keep protocol-specific mapping at the boundary

---

## Current capabilities

The current version supports:

- Go HTTP service startup and graceful shutdown
- config loading and basic validation
- `GET /healthz`
- single-listener and multi-listener configuration
- binding different inbounds to different listeners
- three inbound protocols
  - `POST /v1/chat/completions`
  - `POST /v1/responses`
  - `POST /v1/messages`
- tag-first routing by client scenario
- per-rule support for:
  - `failover`
  - `round_robin`
- route-level target model selection
- multiple outbound protocols
  - `mock`
  - `openai_chat`
  - `openai_responses`
  - `anthropic_messages`
- OpenAI-compatible and Anthropic-compatible upstream calls
- basic SSE streaming responses
- a minimal tool-calling loop
- `openai_responses` compatibility declarations
- local development logging and trace debugging
- unit, regression, and flow/integration coverage for key paths

---

## Project structure

```text
cmd/
  syrogo/                    # program entry

internal/
  app/                       # application wiring
  config/                    # config definitions, loading, validation
  execution/                 # execution plan consumption and fallback
  eventstream/               # neutral stream event normalization and snapshots
  gateway/                   # inbound protocol / HTTP handler / response serialization
  provider/                  # outbound protocol / upstream adaptation
  router/                    # tag-first routing decisions
  runtime/                   # neutral runtime model
  server/                    # HTTP server lifecycle

configs/
  config.example.yaml        # feature-oriented example config
  config.yaml                # local manual test config (gitignored)
```

---

## Quick start

### 1. Prepare config

Copy the example config to a local config file:

```bash
cp configs/config.example.yaml configs/config.yaml
```

Then replace the token, endpoint, and auth_token fields in `configs/config.yaml` with real values available in your environment.

Note: the current implementation does not automatically read `.env` and does not expand `${VAR}`. If placeholder strings remain in the config file, they will be read as-is.

### 2. Choose listeners and inbounds

Both single-listener and multi-listener setups are supported:

- `server.listen`: single listener
- `listeners[]`: multiple listeners

With `listeners[]`, you can expose different inbound protocols on different ports for different scenarios.

### 3. Start the service

Prefer:

```bash
make run
```

If you only want the smallest local verification path, you can point a route to the `mock` outbound.

### 4. Check health

```bash
curl http://127.0.0.1:8080/healthz
```

If your listen address is not `:8080`, replace it with your actual config.

### 5. Verify protocol entrypoints

Recommended paths to verify first:
- `POST /v1/chat/completions`
- `POST /v1/responses`
- `POST /v1/messages`

### 6. Declare Responses compatibility

If an `openai_responses` upstream only supports part of the official Responses API, you can declare compatibility boundaries explicitly on the outbound:

```yaml
outbounds:
  - name: "responses-primary"
    protocol: "openai_responses"
    endpoint: "https://api.openai.com/v1"
    auth_token: "${OPENAI_RESPONSES_API_KEY_PRIMARY}"
    tag: "responses-primary"
    capabilities:
      responses_previous_response_id: true
      responses_builtin_tools: true
      responses_tool_result_status_error: true
      responses_assistant_history_native: true
```

### 7. Local debugging

For local development, you can use:

- `--dev-log`: write logs to both stdout and `tmp/dev.log`
- `SYROGO_TRACE=1` or `SYROGO_TRACE=full`: write trace files to `tmp/trace`

For more detailed protocol semantics, debug switches, and maintenance constraints, see:
- `.claude/rules/architecture.md`
- `.claude/rules/engineering.md`

---

## Current boundaries

At the current stage, Syrogo is **not** trying to optimize for:

- complex plugin systems
- extra access layers such as gRPC / MCP / WebSocket
- full semantic routing
- a public Go SDK or `pkg`-level shared library surface
- a platform layer built in advance for hypothetical future needs
- full-fidelity multimodal support
- one-to-one passthrough for every upstream protocol feature

What matters more right now is:

**stabilizing protocol entrypoints, internal abstractions, routing execution, and provider boundaries first.**

---

## Roadmap

The next priorities are:

- keep strengthening the multi-inbound / multi-outbound closed loop
- improve the verifiability of routing, fallback, and round_robin behavior
- refine provider adaptation boundaries and error classification
- gradually add governance-related capabilities
  - quota switching
  - statistics
  - multi-node chaining
- extend more providers and protocol capabilities without breaking the main abstraction path

---

## Notes

This README focuses on project positioning, capability boundaries, configuration usage, and entry-level usage.

More detailed maintenance knowledge about protocol boundaries, stream abstractions, test thresholds, and change guardrails is documented in `.claude/rules` to keep product-facing documentation separate from development rules.
