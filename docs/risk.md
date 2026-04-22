# Risk checklist

[中文](./risk.zh-CN.md) | English

This document summarizes the main risks in the current Syrogo project state.

The project is usable as a 0→1 gateway skeleton, but it is not yet at a production-grade maturity level for large-scale or high-stability workloads.

---

## High priority risks

### 1. Protocol compatibility is still easy to break

Syrogo currently supports OpenAI Chat, OpenAI Responses, and Anthropic Messages across inbound and outbound boundaries.

The main remaining risk is not whether the basic path works, but whether edge semantics stay correct across real clients and real upstreams:

- tool calling field shapes
- `content: null` vs empty string behavior
- `finish_reason` / `stop_reason` mapping
- `usage` mapping
- mixed text and JSON content ordering
- streaming event order and delta semantics
- tool result error-state preservation

This means a request may look compatible at a happy-path level while still failing for a stricter SDK or a different upstream implementation.

### 2. OpenAI-compatible upstreams may not actually behave the same

Different providers often claim OpenAI compatibility but diverge in subtle behavior.

Common failure patterns include:

- accepting the same request shape but interpreting tool fields differently
- returning different stream chunk formats
- omitting usage or finish fields
- handling assistant history and tool replay differently
- partially supporting Responses API features

Syrogo already has some capability guards for `openai_responses`, but the overall provider compatibility surface is still a major risk area.

### 3. Real streaming interoperability still needs more soak testing

Streaming is one of the most fragile parts of the gateway.

Although the current implementation has regression coverage, real interoperability risk remains around:

- OpenAI Chat tool-call arguments delta behavior
- Anthropic event sequencing
- message lifecycle alignment between upstream, runtime, eventstream, and gateway serialization
- clients that are strict about SSE frame shape or ordering

A stream that looks fine in local tests can still break in real SDK integrations.

### 4. Governance and runtime protection are still minimal

The current project has routing and basic execution, but it is still light on production controls.

Gaps include:

- stronger timeout policy per provider / route
- retry policy tuning and clearer error classes
- rate limiting and concurrency protection
- circuit breaking / backpressure behavior
- quota switching and budget enforcement
- richer traffic and failure statistics

This is acceptable for the current stage, but it is a real risk if the project is used as a stable multi-tenant gateway too early.

---

## Medium priority risks

### 5. Observability is useful but not yet production-grade

The project already has `--dev-log` and trace output, which is good for local debugging.

But production operations still need stronger observability, such as:

- structured metrics
- clearer request / route / provider dimensions
- latency and error dashboards
- alert-friendly failure counters
- easier trace correlation across gateway and upstream calls

Without that, many issues will be diagnosable only by manual log inspection.

### 6. Misconfiguration risk is still meaningful

Configuration loading and validation already exist, but several classes of mistakes can still be expensive:

- wrong tokens or endpoints
- routing rules that are syntactically valid but operationally unsafe
- capability declarations that do not match real upstream behavior
- listener / inbound combinations that expose unintended protocols
- model mappings that appear valid but route to the wrong upstream behavior

At this stage, configuration is powerful, but still easy to misuse.

### 7. Release maturity is ahead of runtime maturity

The release path is already relatively complete:

- release workflow exists
- tag-driven release exists
- local pre-release checks exist
- installer path exists

But shipping binaries is easier than guaranteeing stable runtime behavior across heterogeneous upstreams.

The main risk is not the act of releasing, but releasing faster than compatibility and operational confidence grow.

---

## Lower priority but notable risks

### 8. Security hardening is still basic

Current work has focused on runnable architecture and protocol closure, not full hardening.

Areas that likely still need tightening over time:

- more defensive validation at trust boundaries
- better secret handling and masking guarantees
- stricter safeguards around trace/log content
- stronger operational recommendations for deployment

### 9. The project still depends on continued architecture discipline

The current package boundaries are one of the strongest parts of the codebase.

The risk is future drift:

- pushing provider details into gateway or router
- leaking protocol-specific fields into runtime
- adding abstractions too early
- weakening the current `gateway -> runtime -> router/execution -> provider` path

This is not a current bug, but it is a future maintenance risk.

---

## Recommended next steps

### Short term

1. Add more real-provider smoke tests for OpenAI-compatible and Anthropic-compatible upstreams.
2. Expand regression coverage for protocol edge cases, especially stream ordering and tool semantics.
3. Strengthen configuration validation for capability declarations and risky route combinations.
4. Add a concise compatibility matrix documenting what is fully supported, partially supported, or intentionally unsupported.

### Mid term

1. Add metrics and better production observability.
2. Add stronger timeout / retry / fallback / concurrency controls.
3. Add safer provider capability governance and clearer error taxonomy.
4. Add more explicit operational guidance for production deployment.

---

## Bottom line

Syrogo is already a solid 0→1 gateway skeleton with clear boundaries and a working multi-protocol request path.

Its biggest risks are no longer basic startup or routing, but protocol edge compatibility, heterogeneous provider behavior, and production-grade governance / observability maturity.
