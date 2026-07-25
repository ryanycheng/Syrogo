# Syrogo

[中文](./README.zh-CN.md) | English

<p align="center">
  <img src="./docs/assets/SyroGo-logo.png" alt="SyroGo" width="500">
</p>

> Syrogo · AI Gateway / Semantic Router
>
> Route model traffic with clearer boundaries, multi-protocol access, and gateway-ready orchestration.

- **Multi-protocol inbounds** — OpenAI Chat, OpenAI Responses, and Anthropic Messages in one gateway.
- **Routing for real scenarios** — route by client tag, model mapping, failover, and round_robin.
- **Provider-ready execution** — connect multiple upstreams without pushing protocol differences into every client.

Syrogo is an AI Gateway / Semantic Router for multi-model scenarios.

It is not just a thin proxy for forwarding a single protocol. It sits between clients and upstream model providers to unify:
- multiple inbound protocols
- multiple upstream providers
- routing by client scenario
- client-side request quota windows
- basic scheduling such as failover, round_robin, and provider health-aware fallback
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
- client-side request quota windows
- per-rule support for:
  - `failover`
  - `round_robin`
  - `weighted_round_robin`
- route-level target model selection and model mapping
- provider health tracking with degraded/probing outbound recovery
- multiple outbound protocols
  - `mock`
  - `openai_chat`
  - `openai_responses`
  - `anthropic_messages`
- OpenAI-compatible and Anthropic-compatible upstream calls
- basic SSE streaming responses
- local replay streaming for some compatibility paths
- a minimal tool-calling loop
- `openai_responses` compatibility declarations
- runtime quota governance with snapshot persistence, recent events, and admin stats
- local development logging and trace debugging
- unit, regression, and flow/integration coverage for key paths

### Protocol and capability matrix

| Area | Current support | Notes |
| --- | --- | --- |
| Inbound protocols | `openai_chat`, `openai_responses`, `anthropic_messages` | Exposed as `/v1/chat/completions`, `/v1/responses`, `/v1/messages` |
| Outbound protocols | `mock`, `openai_chat`, `openai_responses`, `anthropic_messages` | Routing selects outbound by tag |
| Routing | `failover`, `round_robin`, `weighted_round_robin`, `target_model`, `model_map` | Match starts from inbound client tag |
| Streaming | Chat / Responses / Messages SSE serialization | Some compatibility paths replay local `runtime.StreamEvent` instead of upstream frame passthrough |
| Tool calling | Minimal function tool loop and custom tool coverage | Responses and Anthropic bridge paths have regression coverage |
| Responses capabilities | `responses_previous_response_id`, `responses_builtin_tools`, `responses_tool_result_status_error`, `responses_assistant_history_native` | Capability declarations only apply to `openai_responses` outbounds |
| Validation and tests | Config validation, smoke tests, protocol regressions | `make test`, `make build`, `make lint` pass locally |

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

Each `inbounds[].clients[]` entry now also requires a stable `name`. This is the usage-accounting identity for that key. You can rotate `token`, but keep `name` unchanged if you want usage to continue accumulating under the same key identity.

Note: the current implementation does not automatically read `.env` and does not expand `${VAR}`. If placeholder strings remain in the config file, they will be read as-is.

### 2. Choose listeners and inbounds

Both single-listener and multi-listener setups are supported:

- `server.listen`: single listener
- `listeners[]`: multiple listeners

With `listeners[]`, you can expose different inbound protocols on different ports for different scenarios.

### 3. Install from GitHub Releases

If you do not want to build from source, you can download a prebuilt archive from GitHub Releases.

Current release artifacts are planned for:
- Linux amd64 / arm64
- macOS amd64 / arm64

After downloading a release archive, extract it and run the `syrogo` binary directly.

Syrogo also ships with one installer entrypoint that works both locally and remotely:

```bash
sudo bash ./scripts/install.sh
sudo bash ./scripts/install.sh --version v0.1.0
curl -fsSL https://raw.githubusercontent.com/ryanycheng/Syrogo/refs/heads/master/scripts/install.sh | sudo bash -s --
curl -fsSL https://raw.githubusercontent.com/ryanycheng/Syrogo/refs/heads/master/scripts/install.sh | sudo bash -s -- --version v0.1.0
```

Without `--version` or `--archive`, the installer resolves the latest GitHub release automatically. It uses `/opt/syrogo/config/config.yaml` as the default config path, creates `/usr/local/bin/syrogo` so `syrogo run ...` is available from normal shells, and keeps the installed config unless you pass `--force-config`.

If GitHub release downloads are slow or unreliable, pass a proxy to the installer itself. Wrapping only the first `curl` does not affect the release archive download that runs inside `sudo bash`. For very slow links, tune `SYROGO_INSTALL_CONNECT_TIMEOUT`, `SYROGO_INSTALL_MAX_TIME`, `SYROGO_INSTALL_RETRY`, `SYROGO_INSTALL_LOW_SPEED_LIMIT`, and `SYROGO_INSTALL_LOW_SPEED_TIME`; set `SYROGO_INSTALL_LOW_SPEED_LIMIT=1` for very slow proxies:

```bash
curl -fsSL https://raw.githubusercontent.com/ryanycheng/Syrogo/refs/heads/master/scripts/install.sh \
  | sudo bash -s -- --proxy http://127.0.0.1:7890

curl -fsSL https://raw.githubusercontent.com/ryanycheng/Syrogo/refs/heads/master/scripts/install.sh \
  | sudo SYROGO_INSTALL_PROXY=http://127.0.0.1:7890 bash -s --

curl -fsSL https://raw.githubusercontent.com/ryanycheng/Syrogo/refs/heads/master/scripts/install.sh \
  | sudo SYROGO_INSTALL_PROXY=http://127.0.0.1:7890 SYROGO_INSTALL_CONNECT_TIMEOUT=120 SYROGO_INSTALL_MAX_TIME=1800 SYROGO_INSTALL_RETRY=10 SYROGO_INSTALL_LOW_SPEED_LIMIT=1 bash -s --
```

For complete deployment examples, see [`docs/deploy.md`](./docs/deploy.md).

For current project risks and suggested next steps, see [`docs/risk.md`](./docs/risk.md).

### 4. Start the service

Prefer:

```bash
make run
```

If you only want the smallest local verification path, you can point a route to the `mock` outbound.

### 5. Check health

```bash
curl http://127.0.0.1:23234/healthz
```

If your listen address is not `:23234`, replace it with your actual config.

### 6. Verify protocol entrypoints

Recommended paths to verify first:
- `POST /v1/chat/completions`
- `POST /v1/responses`
- `POST /v1/messages`

Minimal local verification examples:

```bash
curl http://127.0.0.1:23234/healthz

curl http://127.0.0.1:23234/v1/chat/completions \
  -H 'Authorization: Bearer <chat-token>' \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hello"}]}'

curl http://127.0.0.1:23234/v1/responses \
  -H 'Authorization: Bearer <responses-token>' \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-4o-mini","input":"hello"}'

curl http://127.0.0.1:23234/v1/messages \
  -H 'Authorization: Bearer <anthropic-token>' \
  -H 'anthropic-version: 2023-06-01' \
  -H 'Content-Type: application/json' \
  -d '{"model":"claude-sonnet-4-5","max_tokens":128,"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}'
```

You can also run the smoke script against a running gateway. It checks health, configured protocol entrypoints, stream responses, and usage stats when the corresponding tokens are provided:

```bash
SYROGO_SMOKE_BASE_URL=http://127.0.0.1:23234 \
SYROGO_OPENAI_CLIENT_TOKEN=<chat-token> \
SYROGO_RESPONSES_CLIENT_TOKEN=<responses-token> \
SYROGO_ANTHROPIC_CLIENT_TOKEN=<anthropic-token> \
SYROGO_ACCOUNTING_ADMIN_TOKEN=<accounting-admin-token> \
make smoke
```

### 7. Launch agent clients through Syrogo

You can use `syrogo run` to start common agent CLIs with the right Syrogo environment variables injected:

```bash
syrogo run claude --model claude-sonnet-4-6 --dangerously-skip-permissions
syrogo run codex exec "Reply with exactly: syrogo-ok"
```

By default, `claude` selects one `anthropic_messages` inbound client and `codex` selects one `openai_responses` inbound client. `syrogo run` uses `/opt/syrogo/config/config.yaml` when it exists; `./configs/config.yaml` is only a development fallback when the installed config is missing. You can also pass `--config` before or after the subcommand. Put Syrogo launcher flags before native agent arguments; once the first native agent argument appears, the rest are passed through unchanged. If there are multiple matching clients, pass `--client` or `--inbound`:

```bash
syrogo --config /opt/syrogo/config/config.yaml run claude --client anthropic-key --base-url http://127.0.0.1:23234
syrogo run codex --config ./configs/config.yaml --inbound responses-entry --print-env
```

If you want your current shell to use Syrogo directly, evaluate the activation output:

```bash
eval "$(syrogo activate claude --client anthropic-key)"
eval "$(syrogo activate codex --client responses-key)"
```

You can put the same `eval "$(syrogo activate ...)"` line in your shell rc file when you want a persistent default for new shells.

Current scope:

- `claude` injects `ANTHROPIC_BASE_URL` and `ANTHROPIC_AUTH_TOKEN`
- `codex` injects `OPENAI_BASE_URL` and `OPENAI_API_KEY`
- `--print-env` prints the resolved environment in stable key order, with sensitive values redacted, without starting the client
- `activate` prints real shell `export` statements for `eval`, so do not paste its output into logs
- the launched client still runs locally; Syrogo handles its model API traffic

### 8. Map route models

If one route needs to accept several inbound model names and send provider-specific names upstream, use `model_map` on the routing rule:

```yaml
routing:
  rules:
    - name: "responses-route"
      from_tags:
        - "responses"
      to_tags:
        - "responses-primary"
      strategy: "failover"
      model_map:
        gpt-4: "gpt-4o-mini"
        claude-sonnet-4-6: "gpt-5.4"
```

Use `target_model` for one fixed model override, or `model_map` for per-request model mapping. A rule cannot set both.

Each outbound can also declare the canonical models it supports and optional inbound aliases:

```yaml
outbounds:
  - name: "openai-primary"
    protocol: "openai_chat"
    endpoint: "https://api.openai.com/v1"
    auth_token: "${OPENAI_API_KEY_PRIMARY}"
    tag: "openai-primary"
    models:
      - name: "gpt-4o-mini"
        aliases: ["gpt-4-mini", "fast"]
```

An omitted or empty `models` list is unrestricted. Routing applies `target_model` or `model_map` first; each fallback provider then independently resolves that routed name against its own canonical names and aliases. This lets the same alias map to different upstream names on different providers. A provider that does not accept the routed model is skipped; if no planned provider accepts it, the inbound protocol receives HTTP 404 with `model_not_found` (or its protocol-equivalent error envelope).

### 9. Configure outbound proxy

If one upstream provider needs a dedicated network exit, configure a proxy on that outbound:

```yaml
outbounds:
  - name: "openai-primary"
    protocol: "openai_chat"
    endpoint: "https://api.openai.com/v1"
    auth_token: "${OPENAI_API_KEY_PRIMARY}"
    tag: "openai-primary"
    proxy:
      url: "http://127.0.0.1:7890"
```

Scope:

- proxy settings are per outbound, not global
- unset `proxy.url` keeps the default outbound HTTP behavior
- current proxy URL schemes are `http`, `https`, and `socks5`

### 10. Declare Responses compatibility

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

### 11. Declare usage estimation fallback

If an `openai_chat` or `anthropic_messages` upstream omits `usage`, you can let Syrogo fill a heuristic estimate on the outbound:

```yaml
outbounds:
  - name: "openai-primary"
    protocol: "openai_chat"
    endpoint: "https://api.openai.com/v1"
    auth_token: "${OPENAI_API_KEY_PRIMARY}"
    tag: "openai-primary"
    capabilities:
      usage_estimation: true
      usage_estimation_mode: "heuristic"
```

Current scope:

- only applies to non-stream responses
- only supports `openai_chat` and `anthropic_messages` outbounds
- only runs when the upstream response omits `usage`
- returns a platform-side estimate, not provider billing truth

### 12. Limit client request windows

For client-side governance, you can set request quota windows on individual inbound clients:

```yaml
inbounds:
  - name: "openai-entry"
    protocol: "openai_chat"
    path: "/v1/chat/completions"
    clients:
      - name: "office-key"
        token: "${SYROGO_OPENAI_CLIENT_TOKEN}"
        tag: "office"
        quota:
          enabled: true
          windows:
            - name: "hourly"
              duration: "1h"
              max_requests: 100
            - name: "daily"
              duration: "24h"
              max_requests: 1000
```

Scope:

- applies before routing, so exceeded clients receive HTTP 429 directly
- uses `clients[].name` as the stable quota identity
- multiple windows can be active at the same time; any exhausted window blocks the client
- the first version tracks request counts, not token or billing quotas

Read current client quota state with the accounting admin token:

```bash
curl http://127.0.0.1:23234/stats/client-quota \
  -H 'Authorization: Bearer <accounting-admin-token>'
```

### 13. Track outbound quota windows

Outbound quota is an outbound-provider guard, separate from inbound/client request quotas. It can track requests and outbound tokens across overlapping windows:

```yaml
outbounds:
  - name: "openai-primary"
    protocol: "openai_chat"
    endpoint: "https://api.openai.com/v1"
    auth_token: "${OPENAI_API_KEY_PRIMARY}"
    tag: "openai-primary"
    quota:
      enabled: true
      cooldown: "10m"
      probe_interval: "1m"
      windows:
        - name: "rolling-five-hour"
          reset: "rolling"
          duration: "5h"
          max_requests: 1000
          max_tokens: 2000000
        - name: "fixed-five-hour"
          reset: "fixed"
          duration: "5h"
          fixed: {period: "interval", anchor: "2026-01-01T00:00:00Z"}
          max_tokens: 2500000
        - name: "daily"
          reset: "fixed"
          fixed: {period: "daily", time: "04:00", timezone: "America/New_York"}
          max_requests: 5000
        - name: "weekly"
          reset: "fixed"
          fixed: {period: "weekly", weekday: "monday", time: "00:00", timezone: "UTC"}
          max_requests: 20000
          max_tokens: 40000000
      reset_all:
        enabled: true
        schedule: {period: "weekly", weekday: "sunday", time: "00:00", timezone: "UTC"}
```

Semantics and operational limits:

- `reset: rolling` uses a moving `duration`. Fixed windows support three schedules: anchored `interval` (for example 5h), wall-clock `daily`, and wall-clock `weekly`.
- Daily/weekly `timezone` is an IANA zone. Resets follow that zone's civil clock and DST transitions; interval anchors must be RFC3339 with an explicit offset.
- `max_requests` and `max_tokens` are independent optional dimensions, and at least one must be positive. Any exhausted dimension in any window skips the outbound.
- Tokens are counted only for successful terminal outbound attempts. Provider-reported usage is preferred; if the upstream omits usage, tokens are zero unless `capabilities.usage_estimation: true` with `usage_estimation_mode: heuristic` is enabled. Heuristic usage is a platform estimate, not provider billing truth.
- Admission and successful usage recording are separate, so concurrent in-flight requests can overshoot a configured limit by the number of concurrent successes.
- A real upstream 429 enters `cooldown`. After `probe_interval`, one real request may probe recovery; a successful probe clears cooldown. `reset_all` clears all usage windows on its schedule, but does not clear cooldown/probe state.
- Existing quota blocks that omit `reset` remain compatible and are treated as rolling windows.

Read current quota state with the accounting admin token:

```bash
curl http://127.0.0.1:23234/stats/quota \
  -H 'Authorization: Bearer <accounting-admin-token>'
```

### 14. Persist and inspect quota governance

Syrogo can persist runtime quota state locally so restart recovery keeps recent client/outbound windows and outbound cooldowns. Snapshot v2 stores request and token events plus reset metadata; legacy snapshots remain readable. A successful Apply migrates compatible quota state by stable subject/window identity (including cooldown/probe state), while changed or incompatible window definitions start clean:

```yaml
governance:
  quota:
    snapshot:
      enabled: true
      dir: "./tmp/quota"
      flush_interval: "5s"
    events:
      enabled: true
      max_entries: 200
```

Use the accounting admin token to inspect quota state and recent quota events in one response:

```bash
curl http://127.0.0.1:23234/stats/governance \
  -H 'Authorization: Bearer <accounting-admin-token>'
```

This includes provider health, outbound quota, client quota, and recent events such as `client_limited`, `outbound_limited`, `outbound_quota_exceeded`, `outbound_probe_succeeded`, `provider_health_limited`, and `provider_probe_succeeded`. Degraded outbounds are skipped during execution, then move into `probing` when the next real request is allowed to test recovery.

Use the same admin token to inspect recent request latency timelines:

```bash
curl http://127.0.0.1:23234/stats/latency \
  -H 'Authorization: Bearer <accounting-admin-token>'
```

For a compact view of the current latency distribution, query the summary endpoint:

```bash
curl http://127.0.0.1:23234/stats/latency/summary \
  -H 'Authorization: Bearer <accounting-admin-token>'
```

The timeline response includes request metadata, HTTP status, total duration, and spans such as `route_plan`, `provider_dispatch`, `upstream_round_trip`, `upstream_read`, `upstream_stream_read`, and `egress_write`. The summary response aggregates recent requests into `count`, `avg_ms`, `p50_ms`, `p95_ms`, `p99_ms`, and `max_ms` for total latency and each span. When an outbound uses a proxy, `upstream_round_trip` measures the Syrogo-to-proxy path until response headers arrive; proxy-to-upstream work is included in that wait unless the proxy exposes its own metrics.

Open the built-in Admin UI for a lightweight browser view of health, usage, quota, latency, logs, and config operations:

```text
http://127.0.0.1:23234/admin/
```

Enable it with a dedicated `admin.token`. This token must be different from business inbound client tokens and from `accounting.admin_token`, because browser administration and model traffic are separate trust boundaries:

```yaml
admin:
  enabled: true
  token: "${SYROGO_ADMIN_UI_TOKEN}"
  logs:
    enabled: true
    path: "./tmp/dev.log"
    max_bytes: 65536
    rotation:
      max_size_mb: 100
      max_files: 20
      max_age_days: 14
      max_total_size_mb: 1024
      compress: true
```

Log files rotate before a write would exceed `max_size_mb` and at the first write on a new local calendar day. Historical files are compressed with gzip and cleaned by age, file count, and total disk usage; the active file is never removed. `/admin/logs` automatically searches the active file and retained archives. Queries fully covered by the bounded recent cache (last 5 minutes, up to 8 MiB) use memory; cursor, older, incomplete, or multi-page queries fall back to files so results are not omitted. Status filters accept exact codes and families such as `4xx` and `5xx`. Successful `/admin/logs` polling does not emit an `admin_audit` entry.

The UI stores the Admin UI token only in browser local storage and uses `/admin/overview`, `/admin/usage`, `/admin/quota`, `/admin/latency`, `/admin/latency/summary`, `/admin/logs`, `/admin/config`, `/admin/config/validate`, `/admin/config/update`, `/admin/config/apply`, `/admin/config/history`, `/admin/config/history/diff`, `/admin/config/rollback`, `/admin/debug/traces`, `/admin/debug/route-dry-run`, and `/admin/debug/providers`. The Overview page shows request, error, fallback, latency, quota, provider health, recent governance event, and Admin self-check summaries such as config path and log availability. Provider editing round-trips model canonical names and aliases; an empty list is shown as unrestricted. Provider save, enable/disable, and delete mutations atomically update the config and hot-apply the result immediately, so no separate Apply click is needed. Usage defaults to the last seven UTC calendar days and supports explicit UTC date ranges plus `group_by` filters. Debug shows recent traces with matched rule, routing strategy, planned steps, fallback count, spans, a side-effect-free route dry-run form, and provider health/quota/event/latency aggregates. Logs are read only from the configured local log path, support line/byte limits, show path/truncation/read-limit metadata, and are redacted for common token, key, authorization, and secret fields. Current config loading returns a redacted display copy; config updates show a redacted diff preview plus browser confirmation before writing; Apply hot-reloads safe runtime changes and History/Rollback keeps recent local config versions with redacted diff viewing. Admin API operations emit `admin_audit` log entries without recording Authorization headers, tokens, request bodies, config content, or log content. It is an embedded single-page console and does not require a frontend build step.

### 15. Validate config changes

Use either the dedicated Admin UI token or the existing accounting admin token to dry-run a YAML config before replacing a live config file:

```bash
curl http://127.0.0.1:23234/admin/config/validate \
  -H 'Authorization: Bearer <admin-ui-token-or-accounting-admin-token>' \
  --data-binary @configs/config.yaml
```

This endpoint only parses and validates the submitted config. It does not reload or apply changes to running traffic.

To validate and replace the config file used at startup, post the YAML to the update endpoint:

```bash
curl http://127.0.0.1:23234/admin/config/update \
  -H 'Authorization: Bearer <admin-ui-token-or-accounting-admin-token>' \
  --data-binary @configs/config.yaml
```

This writes the validated YAML to the active config path atomically. The response includes `"applied": false`; call `/admin/config/apply` or use the Admin UI Apply button to hot-reload safe runtime changes.

```bash
curl -X POST http://127.0.0.1:23234/admin/config/apply \
  -H 'Authorization: Bearer <admin-ui-token>'
```

Apply rebuilds providers (including their model canonical names and aliases), routing, quota trackers, health tracking, Admin/accounting tokens, and listener-bound inbounds without restarting the listening sockets. Use Apply for external or manual edits to the active config file; Provider mutations made through the Admin UI/API already save and hot-apply atomically. Listener count, listen address, listener name, listener inbound binding, or logging path/rotation configuration changes return `"restart_required": true` and keep the current runtime unchanged. Successful apply creates a local config history entry under `.syrogo-history/` next to the config file; `/admin/config/history` lists recent entries, `/admin/config/history/diff?id=<history-id>` returns redacted current/history YAML for comparison, and `/admin/config/rollback` writes one back and applies it.

To validate routing without changing round-robin state or sending a model request, use route dry-run:

```bash
curl -X POST http://127.0.0.1:23234/admin/debug/route-dry-run \
  -H 'Authorization: Bearer <admin-ui-token>' \
  -H 'Content-Type: application/json' \
  -d '{"inbound":"openai-entry","client":"office-key","model":"gpt-4"}'
```

The dry-run response includes the matched rule, strategy, resolved tags, and ordered outbound steps. It accepts only inbound/client/model/stream metadata and does not accept request bodies, headers, tokens, or replay content.

### 16. Read usage aggregates

Syrogo now exposes a dedicated accounting read-only endpoint for usage aggregates.

It does not reuse business inbound tokens. Use a separate admin token instead:

```bash
curl http://127.0.0.1:23234/stats/usage?group_by=key \
  -H 'Authorization: Bearer <accounting-admin-token>'

curl http://127.0.0.1:23234/stats/usage?group_by=provider \
  -H 'Authorization: Bearer <accounting-admin-token>'

curl 'http://127.0.0.1:23234/stats/usage?group_by=key&start_date=2026-04-21&end_date=2026-04-28' \
  -H 'Authorization: Bearer <accounting-admin-token>'

curl 'http://127.0.0.1:23234/stats/usage?group_by=key&window=day&bucket=2026-04-27' \
  -H 'Authorization: Bearer <accounting-admin-token>'

curl 'http://127.0.0.1:23234/stats/usage?group_by=provider&window=month&bucket=2026-04' \
  -H 'Authorization: Bearer <accounting-admin-token>'

curl http://127.0.0.1:23234/stats/usage?group_by=error_kind \
  -H 'Authorization: Bearer <accounting-admin-token>'
```

Current `group_by` values:

- `key`
- `provider`
- `model`
- `inbound`
- `source`
- `outbound`
- `error_kind`
- `date`
- `agent`
- `session`

Current `window` values:

- `total`
- `day`
- `week`
- `month`

Notes:

- when no time parameters are provided, the API defaults to the last seven UTC calendar days
- `start_date` is inclusive and `end_date` is exclusive; both use strict `YYYY-MM-DD` UTC dates and must be provided together
- `start_date`/`end_date` cannot be combined with legacy `window`/`bucket` parameters
- explicit `window=total` preserves the all-history behavior
- `bucket` is optional when `window=total`
- `window=day` uses buckets like `2026-04-27`
- `window=week` uses ISO week buckets like `2026-W18`
- `window=month` uses buckets like `2026-04`

Response shape:

```json
{
  "items": [
    {
      "value": "office-key",
      "request_count": 12,
      "success_count": 12,
      "error_count": 0,
      "fallback_count": 0,
      "input_tokens": 1234,
      "output_tokens": 567,
      "cached_input_read_tokens": 42,
      "cached_input_write_tokens": 8,
      "cache_read_tokens": 42,
      "cache_create_tokens": 8,
      "total_tokens": 1851,
      "cost_usd": 0.0012,
      "provider_usage_count": 12,
      "estimated_usage_count": 0,
      "last_seen_at": "2026-04-25T09:00:00Z"
    }
  ]
}
```

Notes:

- `clients[].name` remains the stable accounting identity
- `value` depends on the selected `group_by`
- `group_by=session` uses the active session registered by `syrogo run claude` when available, or explicit `X-Syrogo-Session-ID` / `X-Syrogo-Agent` headers
- `cost_usd` uses Syrogo's embedded LiteLLM pricing snapshot when the executed model matches; entries in `accounting.pricing` override embedded defaults
- the embedded snapshot records its LiteLLM revision in `internal/accounting/pricing_default.json`; run `make update-pricing` after intentionally updating the locked revision in `scripts/update_pricing.py`
- `group_by=error_kind` uses `none` for successful requests and values such as `quota_exceeded`, `timeout`, `upstream_server_error`, `auth_failed`, or `capability_unsupported` for failures
- failover only continues on recoverable errors such as quota, timeout, transient, or upstream 5xx failures; auth, capability, and request-shape failures are surfaced directly
- queries always read the in-memory aggregate view instead of scanning disk on request
- `local_file` persists append-only records as day-partitioned JSONL files and periodically writes snapshots for restart recovery

Config example:

```yaml
accounting:
  enabled: true
  backend: "local_file"
  expose_http: true
  admin_token: "${SYROGO_ACCOUNTING_ADMIN_TOKEN}"
  pricing:
    - provider: "openai"
      model: "gpt-4o-mini"
      input_per_million_usd: 0.15
      output_per_million_usd: 0.60
      cache_create_per_million_usd: 0
      cache_read_per_million_usd: 0.075
  local_file:
    dir: "./tmp/accounting"
    rotate_max_size_mb: 64
    retention_days: 30
    snapshot_retention_days: 30
    write_buffer_records: 128
    flush_interval: "2s"
    queue_size: 4096
```

### 16. Local debugging

For local development, you can use:

- `--dev-log`: write logs to both stdout and `tmp/dev.log`
- `SYROGO_TRACE=1` or `SYROGO_TRACE=full`: write trace files to `tmp/trace`

To enable local commit checks in this repository:

- run `git config core.hooksPath .githooks`
- make sure `golangci-lint v2` is installed locally
- the bundled `pre-commit` hook will run `go test ./...` and `golangci-lint run`

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
  - multi-node relay / hop mode, for example domestic Syrogo A forwarding normalized traffic to overseas Syrogo B before B calls real upstream providers
- extend more providers and protocol capabilities without breaking the main abstraction path

---

## Notes

This README focuses on project positioning, capability boundaries, configuration usage, and entry-level usage.

More detailed maintenance knowledge about protocol boundaries, stream abstractions, test thresholds, and change guardrails is documented in `.claude/rules` to keep product-facing documentation separate from development rules.
