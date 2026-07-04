const tokenInput = document.querySelector("#admin-token");
const toast = document.querySelector("#toast");
const savedToken = localStorage.getItem("syrogo_admin_token") || "";
let loadedConfigPath = "";
let loadedConfigRaw = "";
let loadedConfigRedacted = "";
tokenInput.value = savedToken;

function adminToken() {
  const token = tokenInput.value.trim();
  localStorage.setItem("syrogo_admin_token", token);
  return token;
}

function showToast(message) {
  toast.textContent = message;
  toast.classList.add("show");
  window.setTimeout(() => toast.classList.remove("show"), 3600);
}

async function fetchJSON(path, options = {}) {
  const headers = new Headers(options.headers || {});
  const token = adminToken();
  if (token) {
    headers.set("Authorization", `Bearer ${token}`);
  }
  const response = await fetch(path, { ...options, headers });
  const text = await response.text();
  let body = text;
  try {
    body = text ? JSON.parse(text) : null;
  } catch (_) {
    body = text;
  }
  if (!response.ok) {
    const detail = typeof body === "string" ? body : JSON.stringify(body);
    throw new Error(`${response.status} ${response.statusText}: ${detail}`);
  }
  return body;
}

function pretty(value) {
  return JSON.stringify(value, null, 2);
}

function setJSON(selector, value) {
  document.querySelector(selector).textContent = typeof value === "string" ? value : pretty(value);
}

function metric(label, value) {
  return `<div class="metric"><span>${escapeHTML(label)}</span><strong>${escapeHTML(value ?? 0)}</strong></div>`;
}

function emptyState(message) {
  return `<div class="card"><p class="muted">${escapeHTML(message)}</p></div>`;
}

async function refreshOverview() {
  const healthStatus = document.querySelector("#health-status");
  try {
    const health = await fetchJSON("/healthz");
    healthStatus.textContent = health.status || "ok";
    healthStatus.className = "status ok";
  } catch (error) {
    healthStatus.textContent = "error";
    healthStatus.className = "status error";
    showToast(error.message);
  }

  try {
    const overview = await fetchJSON("/admin/overview");
    renderOverviewSummary(overview);
    setJSON("#governance-json", overview);
  } catch (error) {
    document.querySelector("#overview-summary").innerHTML = `<div class="muted">${escapeHTML(error.message)}</div>`;
    setJSON("#governance-json", error.message);
  }
}

function renderOverviewSummary(overview) {
  const usage = overview.usage || {};
  const latency = overview.latency || {};
  const quota = overview.quota || {};
  const health = overview.health || {};
  const events = overview.recent_events || {};
  const admin = overview.admin || {};
  document.querySelector("#overview-summary").innerHTML = [
    metric("requests", usage.request_count),
    metric("errors", usage.error_count),
    metric("fallbacks", usage.fallback_count),
    metric("p95 ms", latency.p95_ms),
    metric("p99 ms", latency.p99_ms),
    metric("quota entries", quota.configured_quota_items),
    metric("degraded providers", health.degraded_count),
    metric("recent events", events.count),
    metric("config path", admin.config_path_set ? "set" : "missing"),
    metric("logs", admin.logs_enabled ? "enabled" : "disabled"),
  ].join("");
}

async function refreshUsage() {
  const target = document.querySelector("#usage-table");
  const params = usageParams();
  const groupBy = params.get("group_by");
  const windowValue = params.get("window");
  const bucket = params.get("bucket");
  if (windowValue !== "total" && !bucket) {
    target.innerHTML = errorBlock(`${windowValue} usage requires a bucket.`);
    showToast("Usage bucket is required.");
    return;
  }
  try {
    const response = await fetchJSON(`/admin/usage?${params.toString()}`);
    const items = response.items || [];
    const filter = [`group_by=${groupBy}`, `window=${windowValue}`];
    if (bucket) {
      filter.push(`bucket=${bucket}`);
    }
    target.innerHTML = [
      `<div class="table-heading">${escapeHTML(filter.join(", "))}</div>`,
      items.length === 0 ? emptyState("No usage records.") : renderObjectTable(items),
    ].join("");
  } catch (error) {
    target.innerHTML = errorBlock(error.message);
  }
}

function usageParams() {
  syncUsageBucketInput();
  const params = new URLSearchParams();
  const groupBy = document.querySelector("#usage-group-by").value;
  const windowValue = document.querySelector("#usage-window").value;
  const bucket = document.querySelector("#usage-bucket").value.trim();
  params.set("group_by", groupBy);
  params.set("window", windowValue);
  if (windowValue !== "total" && bucket) {
    params.set("bucket", bucket);
  }
  return params;
}

function syncUsageBucketInput() {
  const windowInput = document.querySelector("#usage-window");
  const bucketInput = document.querySelector("#usage-bucket");
  const windowValue = windowInput.value;
  if (windowValue === "total") {
    bucketInput.value = "";
    bucketInput.disabled = true;
    bucketInput.placeholder = "not required for total";
    return;
  }
  bucketInput.disabled = false;
  bucketInput.placeholder = usageBucketPlaceholder(windowValue);
  if (!bucketInput.value.trim()) {
    bucketInput.value = currentUsageBucket(windowValue);
  }
}

function usageBucketPlaceholder(windowValue) {
  if (windowValue === "day") {
    return "YYYY-MM-DD";
  }
  if (windowValue === "week") {
    return "YYYY-Www";
  }
  if (windowValue === "month") {
    return "YYYY-MM";
  }
  return "bucket";
}

function currentUsageBucket(windowValue) {
  const now = new Date();
  const year = now.getUTCFullYear();
  const month = String(now.getUTCMonth() + 1).padStart(2, "0");
  if (windowValue === "day") {
    return `${year}-${month}-${String(now.getUTCDate()).padStart(2, "0")}`;
  }
  if (windowValue === "month") {
    return `${year}-${month}`;
  }
  if (windowValue === "week") {
    return isoWeekBucket(now);
  }
  return "";
}

function isoWeekBucket(date) {
  const day = new Date(Date.UTC(date.getUTCFullYear(), date.getUTCMonth(), date.getUTCDate()));
  const weekday = day.getUTCDay() || 7;
  day.setUTCDate(day.getUTCDate() + 4 - weekday);
  const weekYear = day.getUTCFullYear();
  const yearStart = new Date(Date.UTC(weekYear, 0, 1));
  const week = Math.ceil((((day - yearStart) / 86400000) + 1) / 7);
  return `${weekYear}-W${String(week).padStart(2, "0")}`;
}

async function refreshQuota() {
  const target = document.querySelector("#quota-table");
  try {
    const response = await fetchJSON("/admin/quota");
    const outbound = response.outbound || [];
    const client = response.client || [];
    target.innerHTML = [
      `<div class="table-heading">Outbound quota</div>`,
      outbound.length === 0 ? emptyState("No outbound quota records.") : renderObjectTable(outbound),
      `<div class="table-heading">Client quota</div>`,
      client.length === 0 ? emptyState("No client quota records.") : renderObjectTable(client),
    ].join("");
  } catch (error) {
    target.innerHTML = errorBlock(error.message);
  }
}

async function refreshLatency() {
  const target = document.querySelector("#latency-table");
  try {
    const snapshot = await fetchJSON("/admin/latency");
    const items = snapshot.items || [];
    if (items.length === 0) {
      target.innerHTML = emptyState("No recent requests.");
      return;
    }
    target.innerHTML = `<table>
      <thead><tr><th>Time</th><th>Path</th><th>Inbound</th><th>Client</th><th>Status</th><th>Total</th><th>Spans</th></tr></thead>
      <tbody>${items.map(renderTraceRow).join("")}</tbody>
    </table>`;
  } catch (error) {
    target.innerHTML = errorBlock(error.message);
  }
}

async function refreshLogs() {
  const target = document.querySelector("#logs-content");
  const meta = document.querySelector("#logs-meta");
  const refreshButton = document.querySelector("#refresh-logs");
  const params = new URLSearchParams();
  const lines = document.querySelector("#log-lines").value.trim();
  const bytes = document.querySelector("#log-bytes").value.trim();
  if (lines) {
    params.set("lines", lines);
  }
  if (bytes) {
    params.set("bytes", bytes);
  }
  refreshButton.disabled = true;
  meta.innerHTML = inlineStatus("Loading logs...", "loading");
  try {
    const response = await fetchJSON(`/admin/logs?${params.toString()}`);
    target.textContent = response.content || "";
    meta.innerHTML = renderLogsMeta(response);
    showToast("Logs refreshed.");
  } catch (error) {
    target.textContent = `Failed to load logs.\n${error.message}`;
    meta.innerHTML = inlineStatus(error.message, "error");
    showToast("Refresh logs failed.");
  } finally {
    refreshButton.disabled = false;
  }
}

function renderLogsMeta(response) {
  const truncated = Boolean(response.truncated);
  const lineLimit = response.lines ? response.lines : "not applied";
  return `<div class="meta-item"><span>Path</span><strong>${escapeHTML(response.path || "")}</strong></div>
    <div class="meta-item"><span>Truncated</span><strong><span class="badge ${truncated ? "warn" : "ok"}">${truncated ? "yes" : "no"}</span></strong></div>
    <div class="meta-item"><span>Read limit</span><strong>${escapeHTML(formatBytes(response.max_bytes || 0))}</strong></div>
    <div class="meta-item"><span>Line limit</span><strong>${escapeHTML(lineLimit)}</strong></div>
    <div class="meta-item"><span>Refreshed</span><strong>${escapeHTML(new Date().toLocaleString())}</strong></div>`;
}

function inlineStatus(message, kind) {
  return `<div class="inline-status ${escapeHTML(kind)}">${escapeHTML(message)}</div>`;
}

function formatBytes(value) {
  const bytes = Number(value) || 0;
  if (bytes < 1024) {
    return `${bytes} B`;
  }
  if (bytes < 1024 * 1024) {
    return `${(bytes / 1024).toFixed(1)} KiB`;
  }
  return `${(bytes / 1024 / 1024).toFixed(1)} MiB`;
}

async function loadConfig() {
  const result = document.querySelector("#config-result");
  try {
    const response = await fetchJSON("/admin/config");
    loadedConfigPath = response.path || "";
    loadedConfigRaw = response.content || "";
    loadedConfigRedacted = response.redacted_content || loadedConfigRaw;
    document.querySelector("#config-yaml").value = loadedConfigRedacted;
    document.querySelector("#config-diff").textContent = "Loaded redacted current config. Paste a complete config before updating.";
    result.textContent = pretty({ ok: true, path: response.path, redacted: loadedConfigRedacted !== loadedConfigRaw });
    showToast("Loaded redacted current config.");
  } catch (error) {
    result.textContent = error.message;
    showToast("Load config failed.");
  }
}

async function submitConfig(path) {
  const body = document.querySelector("#config-yaml").value;
  const result = document.querySelector("#config-result");
  try {
    const response = await fetchJSON(path, {
      method: "POST",
      headers: { "Content-Type": "application/x-yaml" },
      body,
    });
    result.textContent = pretty(response);
    showToast(path.endsWith("update") ? "Config file updated. Click Apply current file to hot-reload safe changes." : "Config is valid.");
  } catch (error) {
    result.textContent = error.message;
    showToast("Config request failed.");
  }
}

function updateConfigWithConfirm() {
  const editor = document.querySelector("#config-yaml");
  const result = document.querySelector("#config-result");
  const nextConfig = editor.value;
  if (!nextConfig.trim()) {
    result.textContent = "Config body is empty.";
    showToast("Config body is empty.");
    return;
  }
  if (nextConfig.includes("<redacted>")) {
    result.textContent = "Config contains <redacted>. Paste a complete config before updating.";
    showToast("Cannot update redacted config.");
    return;
  }
  if (!loadedConfigRaw) {
    document.querySelector("#config-diff").textContent = "No loaded baseline. Click Load current first for a meaningful diff preview.";
    showToast("Load current config first for a useful diff.");
  } else {
    const diff = renderConfigDiff(loadedConfigRaw, nextConfig);
    document.querySelector("#config-diff").innerHTML = diff || "No changes from loaded config.";
  }
  const target = loadedConfigPath || "the startup config path";
  const confirmed = window.confirm([
    `Update ${target}?`,
    "",
    "A redacted diff preview has been generated below the editor.",
    "This will overwrite the startup config file after validation.",
    "After updating, click Apply current file to hot-reload safe runtime changes.",
    "Validate first if you have not already done so.",
  ].join("\n"));
  if (!confirmed) {
    result.textContent = "Config update cancelled.";
    showToast("Config update cancelled.");
    return;
  }
  submitConfig("/admin/config/update");
}

async function applyConfigWithConfirm() {
  const result = document.querySelector("#config-result");
  const confirmed = window.confirm([
    "Apply the current startup config file now?",
    "",
    "Safe runtime changes will be hot-reloaded.",
    "Listener address/count/binding changes will report restart_required instead.",
  ].join("\n"));
  if (!confirmed) {
    result.textContent = "Config apply cancelled.";
    showToast("Config apply cancelled.");
    return;
  }
  try {
    const response = await fetchJSON("/admin/config/apply", { method: "POST" });
    result.textContent = pretty(response);
    if (response.restart_required) {
      showToast(`Restart required: ${response.reason || "listener changed"}`);
    } else if (response.applied) {
      loadedConfigRaw = "";
      showToast("Config applied.");
      loadConfigHistory();
    } else {
      showToast("Config was not applied.");
    }
  } catch (error) {
    result.textContent = error.message;
    showToast("Apply config failed.");
  }
}

async function loadConfigHistory() {
  const target = document.querySelector("#config-history");
  try {
    const response = await fetchJSON("/admin/config/history");
    const items = response.items || [];
    target.innerHTML = items.length === 0 ? emptyState("No config history yet.") : renderHistoryTable(items);
    showToast("Config history loaded.");
  } catch (error) {
    target.innerHTML = errorBlock(error.message);
    showToast("Load config history failed.");
  }
}

function renderHistoryTable(items) {
  return `<table>
    <thead><tr><th>ID</th><th>Created</th><th>Reason</th><th>Checksum</th><th>Action</th></tr></thead>
    <tbody>${items.map((item) => `<tr>
      <td><code>${escapeHTML(item.id || "")}</code></td>
      <td>${escapeHTML(item.created_at || "")}</td>
      <td>${escapeHTML(item.reason || "")}</td>
      <td><code>${escapeHTML((item.checksum || "").slice(0, 12))}</code></td>
      <td><button class="small" data-history-diff-id="${escapeHTML(item.id || "")}">Diff</button> <button class="small" data-history-id="${escapeHTML(item.id || "")}">Rollback</button></td>
    </tr>`).join("")}</tbody>
  </table>`;
}

async function rollbackConfig(id) {
  const result = document.querySelector("#config-result");
  if (!id) {
    showToast("Select a history item first.");
    return;
  }
  if (!window.confirm(`Rollback to history item ${id}?`)) {
    result.textContent = "Config rollback cancelled.";
    showToast("Config rollback cancelled.");
    return;
  }
  try {
    const response = await fetchJSON("/admin/config/rollback", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ id }),
    });
    result.textContent = pretty(response);
    showToast(response.restart_required ? "Rollback needs restart." : "Config rolled back and applied.");
    loadConfig();
    loadConfigHistory();
  } catch (error) {
    result.textContent = error.message;
    showToast("Rollback failed.");
  }
}

async function loadHistoryDiff(id) {
  const result = document.querySelector("#config-result");
  if (!id) {
    showToast("Select a history item first.");
    return;
  }
  try {
    const response = await fetchJSON(`/admin/config/history/diff?id=${encodeURIComponent(id)}`);
    document.querySelector("#config-diff").innerHTML = renderConfigDiff(response.history_content || "", response.current_content || "") || "No changes.";
    result.textContent = pretty({ ok: true, id: response.id, redacted: true });
    showToast("History diff loaded.");
  } catch (error) {
    result.textContent = error.message;
    showToast("Load history diff failed.");
  }
}

async function refreshTraces() {
  const target = document.querySelector("#debug-traces-table");
  try {
    const snapshot = await fetchJSON("/admin/debug/traces");
    const items = snapshot.items || [];
    target.innerHTML = items.length === 0 ? emptyState("No recent debug traces.") : `<table>
      <thead><tr><th>Time</th><th>Path</th><th>Client</th><th>Rule</th><th>Strategy</th><th>Fallbacks</th><th>Steps</th><th>Spans</th></tr></thead>
      <tbody>${items.map(renderDebugTraceRow).join("")}</tbody>
    </table>`;
  } catch (error) {
    target.innerHTML = errorBlock(error.message);
  }
}

async function runRouteDryRun() {
  const payload = {
    inbound: document.querySelector("#dry-run-inbound").value.trim(),
    client: document.querySelector("#dry-run-client").value.trim(),
    model: document.querySelector("#dry-run-model").value.trim(),
    stream: document.querySelector("#dry-run-stream").checked,
  };
  const target = document.querySelector("#dry-run-result");
  try {
    const response = await fetchJSON("/admin/debug/route-dry-run", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
    target.textContent = pretty(response);
    showToast("Route dry-run completed.");
  } catch (error) {
    target.textContent = error.message;
    showToast("Route dry-run failed.");
  }
}

async function refreshProviderDebug() {
  try {
    const response = await fetchJSON("/admin/debug/providers");
    setJSON("#provider-debug-json", response);
    showToast("Provider debug refreshed.");
  } catch (error) {
    setJSON("#provider-debug-json", error.message);
    showToast("Provider debug failed.");
  }
}

function renderConfigDiff(previous, next) {
  const previousLines = redactConfigForPreview(previous).split("\n");
  const nextLines = redactConfigForPreview(next).split("\n");
  const rows = diffLines(previousLines, nextLines);
  return rows.map((row) => `<div class="diff-line ${row.kind}">${escapeHTML(row.prefix + row.text)}</div>`).join("");
}

function diffLines(previousLines, nextLines) {
  const dp = Array.from({ length: previousLines.length + 1 }, () => Array(nextLines.length + 1).fill(0));
  for (let i = previousLines.length - 1; i >= 0; i -= 1) {
    for (let j = nextLines.length - 1; j >= 0; j -= 1) {
      dp[i][j] = previousLines[i] === nextLines[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1]);
    }
  }
  const rows = [];
  let i = 0;
  let j = 0;
  while (i < previousLines.length && j < nextLines.length) {
    if (previousLines[i] === nextLines[j]) {
      rows.push({ kind: "same", prefix: "  ", text: previousLines[i] });
      i += 1;
      j += 1;
    } else if (dp[i + 1][j] >= dp[i][j + 1]) {
      rows.push({ kind: "removed", prefix: "- ", text: previousLines[i] });
      i += 1;
    } else {
      rows.push({ kind: "added", prefix: "+ ", text: nextLines[j] });
      j += 1;
    }
  }
  while (i < previousLines.length) {
    rows.push({ kind: "removed", prefix: "- ", text: previousLines[i] });
    i += 1;
  }
  while (j < nextLines.length) {
    rows.push({ kind: "added", prefix: "+ ", text: nextLines[j] });
    j += 1;
  }
  return rows;
}

function redactConfigForPreview(content) {
  return content.split("\n").map((line) => line.replace(/^(\s*(?:token|auth_token|admin_token|api[_-]?key|secret)\s*:\s*).+$/i, '$1"<redacted>"')).join("\n");
}

function renderTraceRow(item) {
  const spans = (item.spans || []).map((span) => `${span.name}: ${span.duration_ms}ms`).join("\n");
  return `<tr>
    <td>${escapeHTML(item.started_at || "")}</td>
    <td>${escapeHTML(item.path || "")}</td>
    <td>${escapeHTML(item.inbound || "")}</td>
    <td>${escapeHTML(item.client_name || "")}</td>
    <td>${escapeHTML(item.status || 0)}</td>
    <td>${escapeHTML(item.duration_ms || 0)}ms</td>
    <td><pre>${escapeHTML(spans)}</pre></td>
  </tr>`;
}

function renderDebugTraceRow(item) {
  const steps = (item.planned_steps || []).map((step) => `${step.outbound_name} ${step.model || ""}`).join("\n");
  const spans = (item.spans || []).map((span) => `${span.name}: ${span.duration_ms}ms`).join("\n");
  return `<tr>
    <td>${escapeHTML(item.started_at || "")}</td>
    <td>${escapeHTML(item.path || "")}</td>
    <td>${escapeHTML(item.client_name || "")}</td>
    <td>${escapeHTML(item.matched_rule || "")}</td>
    <td>${escapeHTML(item.strategy || "")}</td>
    <td>${escapeHTML(item.fallback_count || 0)}</td>
    <td><pre>${escapeHTML(steps)}</pre></td>
    <td><pre>${escapeHTML(spans)}</pre></td>
  </tr>`;
}

function renderObjectTable(items) {
  const headers = Array.from(items.reduce((set, item) => {
    Object.keys(item || {}).forEach((key) => set.add(key));
    return set;
  }, new Set()));
  return `<table>
    <thead><tr>${headers.map((header) => `<th>${escapeHTML(header)}</th>`).join("")}</tr></thead>
    <tbody>${items.map((item) => `<tr>${headers.map((header) => `<td>${formatCell(item?.[header])}</td>`).join("")}</tr>`).join("")}</tbody>
  </table>`;
}

function formatCell(value) {
  if (value === null || value === undefined) {
    return "";
  }
  if (typeof value === "object") {
    return `<pre>${escapeHTML(pretty(value))}</pre>`;
  }
  return escapeHTML(value);
}

function errorBlock(message) {
  return `<div class="card"><pre class="json-block">${escapeHTML(message)}</pre></div>`;
}

function escapeHTML(value) {
  return String(value).replace(/[&<>"]/g, (char) => ({
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    '"': "&quot;",
  }[char]));
}

document.querySelectorAll(".tab").forEach((button) => {
  button.addEventListener("click", () => {
    document.querySelectorAll(".tab").forEach((item) => item.classList.remove("active"));
    document.querySelectorAll(".panel").forEach((item) => item.classList.remove("active"));
    button.classList.add("active");
    document.querySelector(`#${button.dataset.target}`).classList.add("active");
  });
});

tokenInput.addEventListener("input", adminToken);
document.querySelector("#refresh-overview").addEventListener("click", refreshOverview);
document.querySelector("#refresh-usage").addEventListener("click", refreshUsage);
document.querySelector("#usage-window").addEventListener("change", syncUsageBucketInput);
document.querySelector("#refresh-quota").addEventListener("click", refreshQuota);
document.querySelector("#refresh-latency").addEventListener("click", refreshLatency);
document.querySelector("#refresh-logs").addEventListener("click", refreshLogs);
document.querySelector("#load-config").addEventListener("click", loadConfig);
document.querySelector("#validate-config").addEventListener("click", () => submitConfig("/admin/config/validate"));
document.querySelector("#update-config").addEventListener("click", updateConfigWithConfirm);
document.querySelector("#apply-config").addEventListener("click", applyConfigWithConfirm);
document.querySelector("#load-config-history").addEventListener("click", loadConfigHistory);
document.querySelector("#refresh-debug-traces").addEventListener("click", refreshTraces);
document.querySelector("#run-route-dry-run").addEventListener("click", runRouteDryRun);
document.querySelector("#refresh-provider-debug").addEventListener("click", refreshProviderDebug);
document.querySelector("#config-history").addEventListener("click", (event) => {
  const diffButton = event.target.closest("button[data-history-diff-id]");
  if (diffButton) {
    loadHistoryDiff(diffButton.dataset.historyDiffId);
    return;
  }
  const button = event.target.closest("button[data-history-id]");
  if (button) {
    rollbackConfig(button.dataset.historyId);
  }
});

syncUsageBucketInput();
refreshOverview();
