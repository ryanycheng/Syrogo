const tokenInput = document.querySelector("#admin-token");
const toast = document.querySelector("#toast");
const savedToken = localStorage.getItem("syrogo_admin_token") || "";
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
    const summary = await fetchJSON("/admin/latency/summary");
    const total = summary.total || {};
    document.querySelector("#latency-summary").innerHTML = [
      metric("requests", summary.count),
      metric("avg ms", total.avg_ms),
      metric("p50 ms", total.p50_ms),
      metric("p95 ms", total.p95_ms),
      metric("p99 ms", total.p99_ms),
      metric("max ms", total.max_ms),
    ].join("");
  } catch (error) {
    document.querySelector("#latency-summary").innerHTML = `<div class="muted">${escapeHTML(error.message)}</div>`;
  }

  try {
    const [usage, quota] = await Promise.all([
      fetchJSON("/admin/usage?group_by=key"),
      fetchJSON("/admin/quota"),
    ]);
    setJSON("#governance-json", { usage, quota });
  } catch (error) {
    setJSON("#governance-json", error.message);
  }
}

async function refreshUsage() {
  const target = document.querySelector("#usage-table");
  const groupBy = document.querySelector("#usage-group-by").value;
  try {
    const response = await fetchJSON(`/admin/usage?group_by=${encodeURIComponent(groupBy)}`);
    const items = response.items || [];
    target.innerHTML = items.length === 0 ? emptyState("No usage records.") : renderObjectTable(items);
  } catch (error) {
    target.innerHTML = errorBlock(error.message);
  }
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
  const lines = document.querySelector("#log-lines").value || "200";
  try {
    const response = await fetchJSON(`/admin/logs?lines=${encodeURIComponent(lines)}`);
    target.textContent = [
      `path: ${response.path || ""}`,
      `truncated: ${Boolean(response.truncated)}`,
      "",
      response.content || "",
    ].join("\n");
  } catch (error) {
    target.textContent = error.message;
  }
}

async function loadConfig() {
  const result = document.querySelector("#config-result");
  try {
    const response = await fetchJSON("/admin/config");
    document.querySelector("#config-yaml").value = response.content || "";
    result.textContent = pretty({ ok: true, path: response.path });
    showToast("Loaded current config.");
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
    showToast(path.endsWith("update") ? "Config file updated. Restart Syrogo to apply it." : "Config is valid.");
  } catch (error) {
    result.textContent = error.message;
    showToast("Config request failed.");
  }
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
document.querySelector("#refresh-quota").addEventListener("click", refreshQuota);
document.querySelector("#refresh-latency").addEventListener("click", refreshLatency);
document.querySelector("#refresh-logs").addEventListener("click", refreshLogs);
document.querySelector("#load-config").addEventListener("click", loadConfig);
document.querySelector("#validate-config").addEventListener("click", () => submitConfig("/admin/config/validate"));
document.querySelector("#update-config").addEventListener("click", () => submitConfig("/admin/config/update"));

refreshOverview();
