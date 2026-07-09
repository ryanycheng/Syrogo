const els = {
  loginView: document.querySelector("#login-view"),
  appView: document.querySelector("#app-view"),
  tokenInput: document.querySelector("#admin-token"),
  remember: document.querySelector("#remember-token"),
  toast: document.querySelector("#toast"),
  pageTitle: document.querySelector("#page-title"),
  localeSelect: document.querySelector("#locale-select"),
};

const TOKEN_KEY = "syrogo_admin_token";
const SESSION_TOKEN_KEY = "syrogo_admin_session_token";
const LOCALE_KEY = "syrogo_admin_locale";
let loadedConfigPath = "";
let loadedConfigRaw = "";
let loadedConfigRedacted = "";
let resourceOptions = { inbounds: [], outbounds: [], client_tags: [], outbound_tags: [] };
let providerMetrics = [];
let providerLiveChecks = {};
let providerRefreshTimer = 0;
let activeProvider = null;

const i18n = {
  en: {
    login_eyebrow: "Admin Console", login_title: "Sign in to Syrogo", login_hint: "Use the admin.token configured in your Syrogo config file.", admin_token: "Admin UI token", remember_browser: "Remember this browser", sign_in: "Sign in", console: "Console", dashboard: "Dashboard", providers: "Providers", clients: "Clients", routes_models: "Routes & Models", usage: "Usage", monitoring: "Monitoring", logs: "Logs", system_config: "System Config", debug: "Debug", apply_current_file: "Apply current file", logout: "Logout", refresh: "Refresh", admin_overview: "Admin overview", new_provider: "New provider", save_provider: "Save provider", delete_provider: "Delete", new_client: "New client", save_client: "Save client", delete_client: "Delete client", new_route: "New route", save_route: "Save route", delete_route: "Delete route", refresh_quota: "Refresh quota", refresh_latency: "Refresh latency"
  },
  "zh-CN": {
    login_eyebrow: "管理控制台", login_title: "登录 Syrogo", login_hint: "使用配置文件中的 admin.token。", admin_token: "Admin UI token", remember_browser: "记住此浏览器", sign_in: "登录", console: "控制台", dashboard: "仪表盘", providers: "Provider 配置", clients: "Clients", routes_models: "Routes & Models", usage: "Usage", monitoring: "监控", logs: "日志", system_config: "系统配置", debug: "Debug", apply_current_file: "应用当前配置", logout: "退出", refresh: "刷新", admin_overview: "管理概览", new_provider: "新增 Provider", save_provider: "保存 Provider", delete_provider: "删除", new_client: "新增 Client", save_client: "保存 Client", delete_client: "删除 Client", new_route: "新增路由", save_route: "保存路由", delete_route: "删除路由", refresh_quota: "刷新配额", refresh_latency: "刷新延迟"
  }
};

let locale = localStorage.getItem(LOCALE_KEY) || "en";
const savedToken = localStorage.getItem(TOKEN_KEY) || sessionStorage.getItem(SESSION_TOKEN_KEY) || "";
els.tokenInput.value = savedToken;
els.remember.checked = Boolean(localStorage.getItem(TOKEN_KEY));
els.localeSelect.value = locale;

function t(key) { return (i18n[locale] && i18n[locale][key]) || i18n.en[key] || key; }
function applyI18n() { document.querySelectorAll("[data-i18n]").forEach((el) => { el.textContent = t(el.dataset.i18n); }); updatePageTitle(); }
function setLocale(next) { locale = next; localStorage.setItem(LOCALE_KEY, locale); els.localeSelect.value = locale; applyI18n(); }
function currentToken() { return localStorage.getItem(TOKEN_KEY) || sessionStorage.getItem(SESSION_TOKEN_KEY) || els.tokenInput.value.trim(); }
function persistToken(token) { if (els.remember.checked) { localStorage.setItem(TOKEN_KEY, token); sessionStorage.removeItem(SESSION_TOKEN_KEY); } else { sessionStorage.setItem(SESSION_TOKEN_KEY, token); localStorage.removeItem(TOKEN_KEY); } }
function clearToken() { localStorage.removeItem(TOKEN_KEY); sessionStorage.removeItem(SESSION_TOKEN_KEY); els.tokenInput.value = ""; }

async function login() {
  const token = els.tokenInput.value.trim();
  if (!token) { showToast("Admin token is required."); return; }
  persistToken(token);
  try {
    await fetchJSON("/admin/session");
    showApp();
    await Promise.all([refreshOverview(), refreshResourceOptions()]);
    showToast("Signed in.");
  } catch (error) {
    clearToken();
    showToast(error.message);
  }
}

function showApp() { els.loginView.classList.add("hidden"); els.appView.classList.remove("hidden"); }
function showLogin() { els.appView.classList.add("hidden"); els.loginView.classList.remove("hidden"); }
function logout() { clearToken(); showLogin(); showToast("Logged out."); }
function showToast(message) { els.toast.textContent = message; els.toast.classList.add("show"); window.setTimeout(() => els.toast.classList.remove("show"), 3600); }

async function fetchJSON(path, options = {}) {
  const headers = new Headers(options.headers || {});
  const token = currentToken();
  if (token) headers.set("Authorization", `Bearer ${token}`);
  const response = await fetch(path, { ...options, headers });
  const text = await response.text();
  let body = text;
  try { body = text ? JSON.parse(text) : null; } catch (_) { body = text; }
  if (!response.ok) {
    const detail = typeof body === "string" ? body : JSON.stringify(body);
    throw new Error(`${response.status} ${response.statusText}: ${detail}`);
  }
  return body;
}

function pretty(value) { return JSON.stringify(value, null, 2); }
function setJSON(selector, value) { document.querySelector(selector).textContent = typeof value === "string" ? value : pretty(value); }
function metric(label, value) { return `<div class="metric"><span>${escapeHTML(label)}</span><strong>${escapeHTML(value ?? 0)}</strong></div>`; }
function emptyState(message) { return `<div class="card"><p class="muted">${escapeHTML(message)}</p></div>`; }

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
  document.querySelector("#overview-summary").innerHTML = [metric("requests", usage.request_count), metric("errors", usage.error_count), metric("fallbacks", usage.fallback_count), metric("p95 ms", latency.p95_ms), metric("p99 ms", latency.p99_ms), metric("quota entries", quota.configured_quota_items), metric("degraded providers", health.degraded_count), metric("recent events", events.count), metric("config path", admin.config_path_set ? "set" : "missing"), metric("logs", admin.logs_enabled ? "enabled" : "disabled")].join("");
}

async function refreshResourceOptions() {
  resourceOptions = await fetchJSON("/admin/config/options");
  const clientInbound = document.querySelector("#client-inbound");
  clientInbound.innerHTML = (resourceOptions.inbounds || []).map((inbound) => `<option value="${escapeHTML(inbound.name)}">${escapeHTML(inbound.name)} (${escapeHTML(inbound.protocol)})</option>`).join("");
}

async function refreshProviderMetricsOnly() {
  const hours = value("#provider-hours") || "6";
  const response = await fetchJSON(`/admin/config/providers/metrics?hours=${encodeURIComponent(hours)}`);
  providerMetrics = response.items || [];
  renderProviderSummary(providerMetrics);
  renderProviderProtocolFilter(providerMetrics);
  renderFilteredProviders();
}

async function refreshProviders() {
  await refreshResourceOptions();
  await refreshProviderMetricsOnly();
}

function renderProviderSummary(items) {
  const totalRequests = items.reduce((sum, item) => sum + Number(item.usage?.request_count || 0), 0);
  const totalErrors = items.reduce((sum, item) => sum + Number(item.usage?.error_count || 0), 0);
  const limited = items.filter((item) => providerState(item).includes("limited") || providerState(item).includes("cooldown")).length;
  const degraded = items.filter((item) => providerState(item).includes("degraded") || providerState(item).includes("probing")).length;
  document.querySelector("#provider-summary").innerHTML = [metric("providers", items.length), metric("requests", totalRequests), metric("errors", totalErrors), metric("limited", limited), metric("degraded", degraded)].join("");
}

function renderProviderProtocolFilter(items) {
  const select = document.querySelector("#provider-protocol-filter");
  const current = select.value;
  const protocols = Array.from(new Set(items.map((item) => item.provider?.protocol).filter(Boolean))).sort();
  select.innerHTML = `<option value="">All protocols</option>${protocols.map((protocol) => `<option value="${escapeAttr(protocol)}">${escapeHTML(protocol)}</option>`).join("")}`;
  select.value = protocols.includes(current) ? current : "";
}

function renderFilteredProviders() {
  const query = value("#provider-search").toLowerCase();
  const protocol = value("#provider-protocol-filter");
  const state = value("#provider-state-filter");
  const items = providerMetrics.filter((item) => {
    const provider = item.provider || {};
    const haystack = [provider.name, provider.tag, provider.endpoint, provider.protocol].join(" ").toLowerCase();
    if (query && !haystack.includes(query)) return false;
    if (protocol && provider.protocol !== protocol) return false;
    if (state && !providerState(item).includes(state)) return false;
    return true;
  });
  document.querySelector("#providers-table").innerHTML = items.length === 0 ? emptyState("No providers match the current filters.") : renderProviderTable(items);
}

function renderProviderTable(items) {
  return `<table class="provider-table"><thead><tr><th>Provider</th><th>Protocol / Tag</th><th>Recent calls</th><th>Availability</th><th>Limits</th><th>Recent status</th><th>Enabled</th><th>Actions</th></tr></thead><tbody>${items.map(renderProviderRow).join("")}</tbody></table>`;
}

function renderProviderRow(item) {
  const provider = item.provider || {};
  const usage = item.usage || {};
  const state = providerState(item);
  return `<tr class="${provider.enabled === false ? "disabled-row" : ""}"><td><strong>${escapeHTML(provider.name || "")}</strong><div class="muted endpoint-text">${escapeHTML(provider.endpoint || "local mock")}</div></td><td><div class="info-stack"><span>${badge(provider.protocol || "unknown")}</span><span class="muted">tag: ${escapeHTML(provider.tag || "-")}</span></div></td><td>${providerUsageCell(usage)}</td><td>${providerStatusCell(item)}</td><td>${providerQuotaCell(item.quota, provider.quota)}</td><td>${providerTimeline(item.timeline || [])}</td><td>${providerEnabledSwitch(provider)}</td><td><div class="row-actions"><button class="small" data-provider-edit='${escapeAttr(JSON.stringify(provider))}'>Edit</button><button class="small ghost" data-provider-check="${escapeAttr(provider.name || "")}">Test</button></div></td></tr>`;
}

function providerUsageCell(usage) {
  return `<div class="compact-metrics"><span>${escapeHTML(usage.request_count || 0)} req</span><span>${escapeHTML(usage.success_count || 0)} ok</span><span>${escapeHTML(usage.error_count || 0)} err</span></div>`;
}

function providerEnabledSwitch(provider) {
  return `<label class="switch" title="Enable or disable this provider"><input type="checkbox" data-provider-enabled-name="${escapeAttr(provider.name || "")}" ${provider.enabled === false ? "" : "checked"}><span></span></label>`;
}

function providerStatusCell(item) {
  const health = item.health;
  const live = providerLiveChecks[item.provider?.name || ""];
  const rows = [];
  if (health) rows.push(`${badge(health.state || "available", (health.state || "available") === "available" ? "" : "warn")}<div class="muted">runtime failures: ${escapeHTML(health.consecutive_failures || 0)}</div>`);
  else rows.push(`${badge("not tracked", "warn")}<div class="muted">no runtime health yet</div>`);
  if (live) {
    const kind = live.ok ? "" : "danger";
    rows.push(`${badge(live.ok ? "live" : "dead", kind)}<div class="muted">${escapeHTML(live.latency_ms || 0)}ms ${escapeHTML(formatTime(live.checked_at))}${live.error ? ` · ${escapeHTML(live.error)}` : ""}</div>`);
  }
  return `<div class="provider-status-cell">${rows.join("")}</div>`;
}

function providerHealthCell(health, state) {
  if (!health) return badge("not tracked", "warn");
  return `${badge(health.state || state, state.includes("available") ? "" : "warn")}<div class="muted">failures: ${escapeHTML(health.consecutive_failures || 0)}</div>`;
}

function providerQuotaCell(quota, quotaConfig) {
  if (!quota && !quotaConfig?.enabled) return badge("off", "warn");
  if (!quota) return badge("configured");
  const windows = (quota.windows || []).map((window) => `${window.name}: ${window.used}/${window.limit}`).join(" · ");
  const kind = quota.state === "available" ? "" : "warn";
  return `${badge(quota.state || "available", kind)}<div class="muted">${escapeHTML(windows || "no windows")}</div>`;
}

function providerTimeline(timeline) {
  const visible = timeline.slice(-24);
  const midpoint = Math.ceil(visible.length / 2);
  const rows = [visible.slice(0, midpoint), visible.slice(midpoint)];
  return `<div class="timeline timeline-compact">${rows.map((row) => `<div class="timeline-row">${row.map((bucket) => `<span class="timeline-dot ${escapeAttr(bucket.state || "empty")}" data-tooltip="${escapeAttr(`${formatTime(bucket.start)}-${formatTime(bucket.end)} · ${bucket.request_count || 0} req · ${bucket.success_count || 0} ok · ${bucket.error_count || 0} failed`)}"></span>`).join("")}</div>`).join("")}</div>`;
}

function providerState(item) {
  const states = [];
  if (item.health?.state) states.push(item.health.state);
  if (item.quota?.state) states.push(item.quota.state);
  if (states.length === 0) states.push("available");
  return states.join(" ");
}

function badge(text, kind = "") {
  return `<span class="badge ${escapeAttr(kind)}">${escapeHTML(text)}</span>`;
}

function fillProviderForm(item = {}) {
  activeProvider = item.name ? item : null;
  document.querySelector("#provider-modal-title").textContent = item.name ? "编辑 Provider" : "新增 Provider";
  document.querySelector("#provider-name").value = item.name || "";
  document.querySelector("#provider-protocol").value = item.protocol || "mock";
  document.querySelector("#provider-tag").value = item.tag || "";
  document.querySelector("#provider-endpoint").value = item.endpoint || "";
  document.querySelector("#provider-auth-token").value = item.auth_token || "";
  document.querySelector("#provider-proxy-url").value = item.proxy?.url || "";
  document.querySelector("#provider-validate-result").className = "inline-status loading hidden";
  document.querySelector("#provider-validate-result").textContent = "";
  document.querySelector("#provider-delete-confirm").classList.add("hidden");
  document.querySelector("#provider-delete-name").value = "";
  document.querySelector("#delete-provider").textContent = "Delete";
  document.querySelector("#provider-modal").classList.remove("hidden");
}

function closeProviderModal() {
  document.querySelector("#provider-modal").classList.add("hidden");
}

function providerPayload() {
  return { name: value("#provider-name"), protocol: value("#provider-protocol"), tag: value("#provider-tag"), endpoint: value("#provider-endpoint"), auth_token: value("#provider-auth-token"), enabled: activeProvider?.enabled !== false, capabilities: activeProvider?.capabilities || {}, quota: activeProvider?.quota || {}, proxy: { url: value("#provider-proxy-url") } };
}

function validateProviderDraft() {
  const payload = providerPayload();
  const issues = [];
  setInvalid("#provider-name", !payload.name);
  setInvalid("#provider-protocol", !payload.protocol);
  const endpointInvalid = payload.protocol !== "mock" && !payload.endpoint;
  setInvalid("#provider-endpoint", endpointInvalid);
  if (!payload.name) issues.push("name is required");
  if (!payload.protocol) issues.push("protocol is required");
  if (endpointInvalid) issues.push("endpoint is required for non-mock providers");
  const target = document.querySelector("#provider-validate-result");
  if (issues.length > 0) {
    target.className = "inline-status error";
    target.textContent = issues.join("; ");
    return false;
  }
  target.className = "inline-status hidden";
  target.textContent = "";
  return true;
}

function setInvalid(selector, invalid) {
  document.querySelector(selector).classList.toggle("invalid", invalid);
}

async function checkProviderLive(name, draft = null) {
  if (!name && !draft?.name) return;
  const checkName = name || draft.name;
  providerLiveChecks[checkName] = { ok: false, state: "checking", checked_at: new Date().toISOString(), latency_ms: 0 };
  renderFilteredProviders();
  try {
    const payload = draft ? { name: checkName, provider: draft } : { name: checkName };
    const result = await fetchJSON("/admin/config/provider/check", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(payload) });
    providerLiveChecks[checkName] = result;
    showToast(result.ok ? `${checkName} is live.` : `${checkName} is not reachable.`);
    return result;
  } catch (error) {
    providerLiveChecks[checkName] = { ok: false, state: "unavailable", checked_at: new Date().toISOString(), latency_ms: 0, error: error.message };
    showToast(error.message);
    return providerLiveChecks[checkName];
  } finally {
    renderFilteredProviders();
  }
}

async function testProviderForm() {
  if (!validateProviderDraft()) return;
  const target = document.querySelector("#provider-validate-result");
  target.className = "inline-status loading";
  target.textContent = "Testing provider connection...";
  const result = await checkProviderLive("", providerPayload());
  if (!result) return;
  target.className = result.ok ? "inline-status" : "inline-status error";
  target.textContent = result.ok ? `Connection OK · ${result.latency_ms || 0}ms` : `Connection failed: ${result.error || "unknown error"}`;
}

async function setProviderEnabled(name, enabled) {
  if (!name) return;
  await mutateResource("/admin/config/provider/enabled", { name, enabled }, refreshProviders, `${name} ${enabled ? "enabled" : "disabled"}. Click Apply current file when ready.`);
}

async function saveProvider() {
  if (!validateProviderDraft()) return;
  await mutateResource("/admin/config/provider/upsert", providerPayload(), async () => { closeProviderModal(); await refreshProviders(); }, "Provider saved. Click Apply current file when ready.");
}
async function deleteProvider() {
  const name = value("#provider-name");
  if (!name) return;
  const confirmBox = document.querySelector("#provider-delete-confirm");
  if (confirmBox.classList.contains("hidden")) {
    confirmBox.classList.remove("hidden");
    document.querySelector("#provider-delete-name").focus();
    document.querySelector("#delete-provider").textContent = "Confirm delete";
    return;
  }
  const typedName = value("#provider-delete-name");
  const input = document.querySelector("#provider-delete-name");
  const invalid = typedName !== name;
  input.classList.toggle("invalid", invalid);
  if (invalid) { showToast("Type the provider name to confirm deletion."); return; }
  await mutateResource("/admin/config/provider/delete", { name }, async () => { closeProviderModal(); await refreshProviders(); }, "Provider deleted. Click Apply current file when ready.");
}

async function refreshClients() {
  await refreshResourceOptions();
  const response = await fetchJSON("/admin/config/clients");
  const items = response.items || [];
  document.querySelector("#clients-table").innerHTML = items.length === 0 ? emptyState("No clients.") : renderClientTable(items);
}

function renderClientTable(items) {
  return `<table><thead><tr><th>Inbound</th><th>Name</th><th>Tag</th><th>Token</th><th>Protocol</th><th>Actions</th></tr></thead><tbody>${items.map((item) => `<tr data-client='${escapeAttr(JSON.stringify(item))}' data-row><td>${escapeHTML(item.inbound || "")}</td><td><strong>${escapeHTML(item.name || "")}</strong></td><td>${escapeHTML(item.tag || "")}</td><td>${escapeHTML(item.token || "")}</td><td>${badge(item.inbound_protocol || "unknown", "")}</td><td><button class="small" data-client-edit='${escapeAttr(JSON.stringify(item))}'>Edit</button></td></tr>`).join("")}</tbody></table>`;
}

function fillClientForm(item = {}) {
  document.querySelector("#client-modal-title").textContent = item.name ? "编辑 Client" : "新增 Client";
  document.querySelector("#client-inbound").value = item.inbound || document.querySelector("#client-inbound").value;
  document.querySelector("#client-name").value = item.name || "";
  document.querySelector("#client-token").value = item.token || "";
  document.querySelector("#client-tag").value = item.tag || "";
  document.querySelector("#client-delete-confirm").classList.add("hidden");
  document.querySelector("#client-delete-name").value = "";
  document.querySelector("#delete-client").textContent = "Delete client";
  document.querySelector("#client-modal").classList.remove("hidden");
}
function closeClientModal() { document.querySelector("#client-modal").classList.add("hidden"); }
async function saveClient() { await mutateResource("/admin/config/client/upsert", { inbound: value("#client-inbound"), name: value("#client-name"), token: value("#client-token"), tag: value("#client-tag") }, async () => { closeClientModal(); await refreshClients(); }, "Client saved. Click Apply current file when ready."); }
async function deleteClient() {
  const inbound = value("#client-inbound");
  const name = value("#client-name");
  if (!name) return;
  const confirmBox = document.querySelector("#client-delete-confirm");
  if (confirmBox.classList.contains("hidden")) {
    confirmBox.classList.remove("hidden");
    document.querySelector("#client-delete-name").focus();
    document.querySelector("#delete-client").textContent = "Confirm delete";
    return;
  }
  const typedName = value("#client-delete-name");
  const input = document.querySelector("#client-delete-name");
  const invalid = typedName !== name;
  input.classList.toggle("invalid", invalid);
  if (invalid) { showToast("Type the client name to confirm deletion."); return; }
  await mutateResource("/admin/config/client/delete", { inbound, name }, async () => { closeClientModal(); await refreshClients(); }, "Client deleted. Click Apply current file when ready.");
}

async function refreshRoutes() {
  await refreshResourceOptions();
  const response = await fetchJSON("/admin/config/routes");
  const items = response.items || [];
  document.querySelector("#routes-table").innerHTML = items.length === 0 ? emptyState("No routes.") : renderRouteTable(items);
}

function renderRouteTable(items) {
  return `<table><thead><tr><th>Name</th><th>From tags</th><th>To tags</th><th>Strategy</th><th>Target model</th><th>Model map</th><th>Actions</th></tr></thead><tbody>${items.map((item) => `<tr data-route='${escapeAttr(JSON.stringify(item))}' data-row><td><strong>${escapeHTML(item.name || "")}</strong></td><td>${escapeHTML((item.from_tags || []).join(", "))}</td><td>${escapeHTML((item.to_tags || []).join(", "))}</td><td>${badge(item.strategy || "unknown", "")}</td><td>${escapeHTML(item.target_model || "")}</td><td><pre>${escapeHTML(pretty(item.model_map || {}))}</pre></td><td><button class="small" data-route-edit='${escapeAttr(JSON.stringify(item))}'>Edit</button></td></tr>`).join("")}</tbody></table>`;
}

function fillRouteForm(item = {}) {
  document.querySelector("#route-modal-title").textContent = item.name ? "编辑 Route" : "新增 Route";
  document.querySelector("#route-name").value = item.name || "";
  document.querySelector("#route-from-tags").value = (item.from_tags || []).join(", ");
  document.querySelector("#route-to-tags").value = (item.to_tags || []).join(", ");
  document.querySelector("#route-strategy").value = item.strategy || "failover";
  document.querySelector("#route-target-model").value = item.target_model || "";
  document.querySelector("#route-model-map").value = item.model_map ? pretty(item.model_map) : "";
  document.querySelector("#route-weights").value = item.weights ? pretty(item.weights) : "";
  document.querySelector("#route-delete-confirm").classList.add("hidden");
  document.querySelector("#route-delete-name").value = "";
  document.querySelector("#delete-route").textContent = "Delete route";
  document.querySelector("#route-modal").classList.remove("hidden");
}
function closeRouteModal() { document.querySelector("#route-modal").classList.add("hidden"); }

async function saveRoute() {
  let modelMap = {}; let weights = {};
  try { modelMap = parseOptionalJSON("#route-model-map"); weights = parseOptionalJSON("#route-weights"); } catch (error) { showToast(error.message); return; }
  const payload = { name: value("#route-name"), from_tags: csv("#route-from-tags"), to_tags: csv("#route-to-tags"), strategy: value("#route-strategy"), target_model: value("#route-target-model"), model_map: modelMap, weights };
  await mutateResource("/admin/config/route/upsert", payload, async () => { closeRouteModal(); await refreshRoutes(); }, "Route saved. Click Apply current file when ready.");
}
async function deleteRoute() {
  const name = value("#route-name");
  if (!name) return;
  const confirmBox = document.querySelector("#route-delete-confirm");
  if (confirmBox.classList.contains("hidden")) {
    confirmBox.classList.remove("hidden");
    document.querySelector("#route-delete-name").focus();
    document.querySelector("#delete-route").textContent = "Confirm delete";
    return;
  }
  const typedName = value("#route-delete-name");
  const input = document.querySelector("#route-delete-name");
  const invalid = typedName !== name;
  input.classList.toggle("invalid", invalid);
  if (invalid) { showToast("Type the route name to confirm deletion."); return; }
  await mutateResource("/admin/config/route/delete", { name }, async () => { closeRouteModal(); await refreshRoutes(); }, "Route deleted. Click Apply current file when ready.");
}

async function mutateResource(path, payload, refresh, successMessage) {
  try {
    await fetchJSON(path, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(payload) });
    showToast(successMessage);
    await refresh();
  } catch (error) { showToast(error.message); }
}

function value(selector) { return document.querySelector(selector).value.trim(); }
function csv(selector) { return value(selector).split(",").map((item) => item.trim()).filter(Boolean); }
function parseOptionalJSON(selector) { const raw = value(selector); return raw ? JSON.parse(raw) : {}; }

async function refreshUsage() {
  const target = document.querySelector("#usage-table");
  const params = usageParams();
  const windowValue = params.get("window");
  const bucket = params.get("bucket");
  if (windowValue !== "total" && !bucket) { target.innerHTML = errorBlock(`${windowValue} usage requires a bucket.`); showToast("Usage bucket is required."); return; }
  try { const response = await fetchJSON(`/admin/usage?${params.toString()}`); const items = response.items || []; target.innerHTML = items.length === 0 ? emptyState("No usage records.") : renderObjectTable(items); } catch (error) { target.innerHTML = errorBlock(error.message); }
}
function usageParams() { syncUsageBucketInput(); const params = new URLSearchParams(); const groupBy = value("#usage-group-by"); const windowValue = value("#usage-window"); const bucket = value("#usage-bucket"); params.set("group_by", groupBy); params.set("window", windowValue); if (windowValue !== "total" && bucket) params.set("bucket", bucket); return params; }
function syncUsageBucketInput() { const windowInput = document.querySelector("#usage-window"); const bucketInput = document.querySelector("#usage-bucket"); const windowValue = windowInput.value; if (windowValue === "total") { bucketInput.value = ""; bucketInput.disabled = true; bucketInput.placeholder = "not required for total"; return; } bucketInput.disabled = false; bucketInput.placeholder = usageBucketPlaceholder(windowValue); if (!bucketInput.value.trim()) bucketInput.value = currentUsageBucket(windowValue); }
function usageBucketPlaceholder(windowValue) { if (windowValue === "day") return "YYYY-MM-DD"; if (windowValue === "week") return "YYYY-Www"; if (windowValue === "month") return "YYYY-MM"; return "bucket"; }
function currentUsageBucket(windowValue) { const now = new Date(); const year = now.getUTCFullYear(); const month = String(now.getUTCMonth() + 1).padStart(2, "0"); if (windowValue === "day") return `${year}-${month}-${String(now.getUTCDate()).padStart(2, "0")}`; if (windowValue === "month") return `${year}-${month}`; if (windowValue === "week") return isoWeekBucket(now); return ""; }
function isoWeekBucket(date) { const day = new Date(Date.UTC(date.getUTCFullYear(), date.getUTCMonth(), date.getUTCDate())); const weekday = day.getUTCDay() || 7; day.setUTCDate(day.getUTCDate() + 4 - weekday); const weekYear = day.getUTCFullYear(); const yearStart = new Date(Date.UTC(weekYear, 0, 1)); const week = Math.ceil((((day - yearStart) / 86400000) + 1) / 7); return `${weekYear}-W${String(week).padStart(2, "0")}`; }

async function refreshQuota() { const target = document.querySelector("#quota-table"); try { const response = await fetchJSON("/admin/quota"); target.innerHTML = [`<div class="table-heading">Outbound quota</div>`, (response.outbound || []).length === 0 ? emptyState("No outbound quota records.") : renderObjectTable(response.outbound || []), `<div class="table-heading">Client quota</div>`, (response.client || []).length === 0 ? emptyState("No client quota records.") : renderObjectTable(response.client || [])].join(""); } catch (error) { target.innerHTML = errorBlock(error.message); } }
async function refreshLatency() { const target = document.querySelector("#latency-table"); try { const snapshot = await fetchJSON("/admin/latency"); const items = snapshot.items || []; target.innerHTML = items.length === 0 ? emptyState("No recent requests.") : `<table><thead><tr><th>Time</th><th>Path</th><th>Inbound</th><th>Client</th><th>Status</th><th>Total</th><th>Spans</th></tr></thead><tbody>${items.map(renderTraceRow).join("")}</tbody></table>`; } catch (error) { target.innerHTML = errorBlock(error.message); } }

async function refreshLogs() { const target = document.querySelector("#logs-content"); const meta = document.querySelector("#logs-meta"); const params = new URLSearchParams(); const lines = value("#log-lines"); const bytes = value("#log-bytes"); if (lines) params.set("lines", lines); if (bytes) params.set("bytes", bytes); meta.innerHTML = inlineStatus("Loading logs...", "loading"); try { const response = await fetchJSON(`/admin/logs?${params.toString()}`); target.textContent = response.content || ""; meta.innerHTML = renderLogsMeta(response); showToast("Logs refreshed."); } catch (error) { target.textContent = `Failed to load logs.\n${error.message}`; meta.innerHTML = inlineStatus(error.message, "error"); showToast("Refresh logs failed."); } }
function renderLogsMeta(response) { const truncated = Boolean(response.truncated); const lineLimit = response.lines ? response.lines : "not applied"; return `<div class="meta-item"><span>Path</span><strong>${escapeHTML(response.path || "")}</strong></div><div class="meta-item"><span>Truncated</span><strong><span class="badge ${truncated ? "warn" : "ok"}">${truncated ? "yes" : "no"}</span></strong></div><div class="meta-item"><span>Read limit</span><strong>${escapeHTML(formatBytes(response.max_bytes || 0))}</strong></div><div class="meta-item"><span>Line limit</span><strong>${escapeHTML(lineLimit)}</strong></div><div class="meta-item"><span>Refreshed</span><strong>${escapeHTML(new Date().toLocaleString())}</strong></div>`; }
function inlineStatus(message, kind) { return `<div class="inline-status ${escapeHTML(kind)}">${escapeHTML(message)}</div>`; }
function formatBytes(value) { const bytes = Number(value) || 0; if (bytes < 1024) return `${bytes} B`; if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`; return `${(bytes / 1024 / 1024).toFixed(1)} MiB`; }

async function loadConfig() { const result = document.querySelector("#config-result"); try { const response = await fetchJSON("/admin/config"); loadedConfigPath = response.path || ""; loadedConfigRaw = response.content || ""; loadedConfigRedacted = response.redacted_content || loadedConfigRaw; document.querySelector("#config-yaml").value = loadedConfigRedacted; document.querySelector("#config-diff").textContent = "Loaded redacted current config. Paste a complete config before updating."; result.textContent = pretty({ ok: true, path: response.path, redacted: loadedConfigRedacted !== loadedConfigRaw }); showToast("Loaded redacted current config."); } catch (error) { result.textContent = error.message; showToast("Load config failed."); } }
async function submitConfig(path) { const body = document.querySelector("#config-yaml").value; const result = document.querySelector("#config-result"); try { const response = await fetchJSON(path, { method: "POST", headers: { "Content-Type": "application/x-yaml" }, body }); result.textContent = pretty(response); showToast(path.endsWith("update") ? "Config file updated. Click Apply current file to hot-reload safe changes." : "Config is valid."); } catch (error) { result.textContent = error.message; showToast("Config request failed."); } }
function updateConfigWithConfirm() { const editor = document.querySelector("#config-yaml"); const result = document.querySelector("#config-result"); const nextConfig = editor.value; if (!nextConfig.trim()) { result.textContent = "Config body is empty."; showToast("Config body is empty."); return; } if (nextConfig.includes("<redacted>")) { result.textContent = "Config contains <redacted>. Paste a complete config before updating."; showToast("Cannot update redacted config."); return; } if (!loadedConfigRaw) { document.querySelector("#config-diff").textContent = "No loaded baseline. Click Load current first for a meaningful diff preview."; } else { const diff = renderConfigDiff(loadedConfigRaw, nextConfig); document.querySelector("#config-diff").innerHTML = diff || "No changes from loaded config."; } const target = loadedConfigPath || "the startup config path"; if (!window.confirm(`Update ${target}?\n\nThis overwrites the startup config file after validation.`)) { result.textContent = "Config update cancelled."; return; } submitConfig("/admin/config/update"); }
async function applyConfigWithConfirm() { const result = document.querySelector("#config-result"); if (!window.confirm("Apply the current startup config file now?")) { if (result) result.textContent = "Config apply cancelled."; return; } try { const response = await fetchJSON("/admin/config/apply", { method: "POST" }); if (result) result.textContent = pretty(response); showToast(response.restart_required ? `Restart required: ${response.reason || "listener changed"}` : "Config applied."); loadConfigHistory(); } catch (error) { if (result) result.textContent = error.message; showToast("Apply config failed."); } }
async function loadConfigHistory() { const target = document.querySelector("#config-history"); try { const response = await fetchJSON("/admin/config/history"); const items = response.items || []; target.innerHTML = items.length === 0 ? emptyState("No config history yet.") : renderHistoryTable(items); showToast("Config history loaded."); } catch (error) { target.innerHTML = errorBlock(error.message); showToast("Load config history failed."); } }
function renderHistoryTable(items) { return `<table><thead><tr><th>ID</th><th>Created</th><th>Reason</th><th>Checksum</th><th>Action</th></tr></thead><tbody>${items.map((item) => `<tr><td><code>${escapeHTML(item.id || "")}</code></td><td>${escapeHTML(item.created_at || "")}</td><td>${escapeHTML(item.reason || "")}</td><td><code>${escapeHTML((item.checksum || "").slice(0, 12))}</code></td><td><button class="small" data-history-diff-id="${escapeHTML(item.id || "")}">Diff</button> <button class="small" data-history-id="${escapeHTML(item.id || "")}">Rollback</button></td></tr>`).join("")}</tbody></table>`; }
async function rollbackConfig(id) { const result = document.querySelector("#config-result"); if (!id || !window.confirm(`Rollback to history item ${id}?`)) return; try { const response = await fetchJSON("/admin/config/rollback", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ id }) }); result.textContent = pretty(response); showToast(response.restart_required ? "Rollback needs restart." : "Config rolled back and applied."); loadConfig(); loadConfigHistory(); } catch (error) { result.textContent = error.message; showToast("Rollback failed."); } }
async function loadHistoryDiff(id) { const result = document.querySelector("#config-result"); if (!id) return; try { const response = await fetchJSON(`/admin/config/history/diff?id=${encodeURIComponent(id)}`); document.querySelector("#config-diff").innerHTML = renderConfigDiff(response.history_content || "", response.current_content || "") || "No changes."; result.textContent = pretty({ ok: true, id: response.id, redacted: true }); showToast("History diff loaded."); } catch (error) { result.textContent = error.message; showToast("Load history diff failed."); } }

async function refreshTraces() { const target = document.querySelector("#debug-traces-table"); try { const snapshot = await fetchJSON("/admin/debug/traces"); const items = snapshot.items || []; target.innerHTML = items.length === 0 ? emptyState("No recent debug traces.") : `<table><thead><tr><th>Time</th><th>Path</th><th>Client</th><th>Rule</th><th>Strategy</th><th>Fallbacks</th><th>Steps</th><th>Spans</th></tr></thead><tbody>${items.map(renderDebugTraceRow).join("")}</tbody></table>`; } catch (error) { target.innerHTML = errorBlock(error.message); } }
async function runRouteDryRun() { const payload = { inbound: value("#dry-run-inbound"), client: value("#dry-run-client"), model: value("#dry-run-model"), stream: document.querySelector("#dry-run-stream").checked }; const target = document.querySelector("#dry-run-result"); try { const response = await fetchJSON("/admin/debug/route-dry-run", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(payload) }); target.textContent = pretty(response); showToast("Route dry-run completed."); } catch (error) { target.textContent = error.message; showToast("Route dry-run failed."); } }
async function refreshProviderDebug() { try { const response = await fetchJSON("/admin/debug/providers"); setJSON("#provider-debug-json", response); showToast("Provider debug refreshed."); } catch (error) { setJSON("#provider-debug-json", error.message); showToast("Provider debug failed."); } }

function renderConfigDiff(previous, next) { const previousLines = redactConfigForPreview(previous).split("\n"); const nextLines = redactConfigForPreview(next).split("\n"); return diffLines(previousLines, nextLines).map((row) => `<div class="diff-line ${row.kind}">${escapeHTML(row.prefix + row.text)}</div>`).join(""); }
function diffLines(previousLines, nextLines) { const dp = Array.from({ length: previousLines.length + 1 }, () => Array(nextLines.length + 1).fill(0)); for (let i = previousLines.length - 1; i >= 0; i -= 1) { for (let j = nextLines.length - 1; j >= 0; j -= 1) dp[i][j] = previousLines[i] === nextLines[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1]); } const rows = []; let i = 0; let j = 0; while (i < previousLines.length && j < nextLines.length) { if (previousLines[i] === nextLines[j]) { rows.push({ kind: "same", prefix: "  ", text: previousLines[i++] }); j += 1; } else if (dp[i + 1][j] >= dp[i][j + 1]) rows.push({ kind: "removed", prefix: "- ", text: previousLines[i++] }); else rows.push({ kind: "added", prefix: "+ ", text: nextLines[j++] }); } while (i < previousLines.length) rows.push({ kind: "removed", prefix: "- ", text: previousLines[i++] }); while (j < nextLines.length) rows.push({ kind: "added", prefix: "+ ", text: nextLines[j++] }); return rows; }
function redactConfigForPreview(content) { return content.split("\n").map((line) => line.replace(/^(\s*(?:token|auth_token|admin_token|api[_-]?key|secret)\s*:\s*).+$/i, '$1"<redacted>"')).join("\n"); }

function formatTime(value) {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}
function renderTraceRow(item) { const spans = (item.spans || []).map((span) => `${span.name}: ${span.duration_ms}ms`).join("\n"); return `<tr><td>${escapeHTML(item.started_at || "")}</td><td>${escapeHTML(item.path || "")}</td><td>${escapeHTML(item.inbound || "")}</td><td>${escapeHTML(item.client_name || "")}</td><td>${escapeHTML(item.status || 0)}</td><td>${escapeHTML(item.duration_ms || 0)}ms</td><td><pre>${escapeHTML(spans)}</pre></td></tr>`; }
function renderDebugTraceRow(item) { const steps = (item.planned_steps || []).map((step) => `${step.outbound_name} ${step.model || ""}`).join("\n"); const spans = (item.spans || []).map((span) => `${span.name}: ${span.duration_ms}ms`).join("\n"); return `<tr><td>${escapeHTML(item.started_at || "")}</td><td>${escapeHTML(item.path || "")}</td><td>${escapeHTML(item.client_name || "")}</td><td>${escapeHTML(item.matched_rule || "")}</td><td>${escapeHTML(item.strategy || "")}</td><td>${escapeHTML(item.fallback_count || 0)}</td><td><pre>${escapeHTML(steps)}</pre></td><td><pre>${escapeHTML(spans)}</pre></td></tr>`; }
function renderObjectTable(items) { const headers = Array.from(items.reduce((set, item) => { Object.keys(item || {}).forEach((key) => set.add(key)); return set; }, new Set())); return `<table><thead><tr>${headers.map((header) => `<th>${escapeHTML(header)}</th>`).join("")}</tr></thead><tbody>${items.map((item) => `<tr>${headers.map((header) => `<td>${formatCell(item?.[header])}</td>`).join("")}</tr>`).join("")}</tbody></table>`; }
function formatCell(value) { if (value === null || value === undefined) return ""; if (typeof value === "object") return `<pre>${escapeHTML(pretty(value))}</pre>`; return escapeHTML(value); }
function errorBlock(message) { return `<div class="card"><pre class="json-block">${escapeHTML(message)}</pre></div>`; }
function escapeHTML(value) { return String(value).replace(/[&<>"]/g, (char) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[char])); }
function escapeAttr(value) { return escapeHTML(value).replace(/'/g, "&#39;"); }

function startProviderAutoRefresh() {
  stopProviderAutoRefresh();
  providerRefreshTimer = window.setInterval(() => {
    refreshProviderMetricsOnly().catch((error) => showToast(error.message));
  }, 5000);
}
function stopProviderAutoRefresh() {
  if (providerRefreshTimer) {
    window.clearInterval(providerRefreshTimer);
    providerRefreshTimer = 0;
  }
}
const pageTitles = { dashboard: "Dashboard", providers: "Providers", clients: "Clients", routes: "Routes & Models", usage: "Usage", monitoring: "Monitoring", logs: "Logs", config: "System Config", debug: "Debug" };
function switchPanel(target) { document.querySelectorAll(".nav-item").forEach((item) => item.classList.toggle("active", item.dataset.target === target)); document.querySelectorAll(".panel").forEach((item) => item.classList.toggle("active", item.id === target)); updatePageTitle(); if (target === "providers") { refreshProviders(); startProviderAutoRefresh(); } else { stopProviderAutoRefresh(); } if (target === "clients") refreshClients(); if (target === "routes") refreshRoutes(); if (target === "usage") refreshUsage(); if (target === "monitoring") { refreshQuota(); refreshLatency(); } if (target === "logs") refreshLogs(); if (target === "debug") { refreshTraces(); refreshProviderDebug(); } }
function updatePageTitle() { const active = document.querySelector(".nav-item.active"); if (active) els.pageTitle.textContent = active.textContent || pageTitles[active.dataset.target] || "Syrogo"; }

function bindEvents() {
  document.querySelector("#login-button").addEventListener("click", login);
  els.tokenInput.addEventListener("keydown", (event) => { if (event.key === "Enter") login(); });
  document.querySelectorAll("[data-locale]").forEach((button) => button.addEventListener("click", () => setLocale(button.dataset.locale)));
  els.localeSelect.addEventListener("change", () => setLocale(els.localeSelect.value));
  document.querySelector("#logout-button").addEventListener("click", logout);
  document.querySelector("#apply-config-global").addEventListener("click", applyConfigWithConfirm);
  document.querySelectorAll(".nav-item").forEach((button) => button.addEventListener("click", () => switchPanel(button.dataset.target)));
  document.querySelector("#refresh-overview").addEventListener("click", refreshOverview);
  document.querySelector("#refresh-providers").addEventListener("click", refreshProviders);
  document.querySelector("#provider-hours").addEventListener("change", refreshProviders);
  document.querySelector("#provider-search").addEventListener("input", renderFilteredProviders);
  document.querySelector("#provider-protocol-filter").addEventListener("change", renderFilteredProviders);
  document.querySelector("#provider-state-filter").addEventListener("change", renderFilteredProviders);
  document.querySelector("#new-provider").addEventListener("click", () => fillProviderForm());
  document.querySelector("#close-provider-modal").addEventListener("click", closeProviderModal);
  document.querySelector("#test-provider-form").addEventListener("click", testProviderForm);
  document.querySelector("#save-provider").addEventListener("click", saveProvider);
  document.querySelector("#delete-provider").addEventListener("click", deleteProvider);
  document.querySelector("#providers-table").addEventListener("click", (event) => {
    const editButton = event.target.closest("button[data-provider-edit]");
    if (editButton) { fillProviderForm(JSON.parse(editButton.dataset.providerEdit)); return; }
    const enabledInput = event.target.closest("input[data-provider-enabled-name]");
    if (enabledInput) { setProviderEnabled(enabledInput.dataset.providerEnabledName, enabledInput.checked); return; }
    const checkButton = event.target.closest("button[data-provider-check]");
    if (checkButton) { checkProviderLive(checkButton.dataset.providerCheck); }
  });
  document.querySelector("#refresh-clients").addEventListener("click", refreshClients);
  document.querySelector("#new-client").addEventListener("click", () => fillClientForm());
  document.querySelector("#close-client-modal").addEventListener("click", closeClientModal);
  document.querySelector("#save-client").addEventListener("click", saveClient);
  document.querySelector("#delete-client").addEventListener("click", deleteClient);
  document.querySelector("#clients-table").addEventListener("click", (event) => { const button = event.target.closest("button[data-client-edit]"); if (button) { fillClientForm(JSON.parse(button.dataset.clientEdit)); return; } const row = event.target.closest("tr[data-client]"); if (row) fillClientForm(JSON.parse(row.dataset.client)); });
  document.querySelector("#refresh-routes").addEventListener("click", refreshRoutes);
  document.querySelector("#new-route").addEventListener("click", () => fillRouteForm());
  document.querySelector("#close-route-modal").addEventListener("click", closeRouteModal);
  document.querySelector("#save-route").addEventListener("click", saveRoute);
  document.querySelector("#delete-route").addEventListener("click", deleteRoute);
  document.querySelector("#routes-table").addEventListener("click", (event) => { const button = event.target.closest("button[data-route-edit]"); if (button) { fillRouteForm(JSON.parse(button.dataset.routeEdit)); return; } const row = event.target.closest("tr[data-route]"); if (row) fillRouteForm(JSON.parse(row.dataset.route)); });
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
  document.querySelector("#config-history").addEventListener("click", (event) => { const diffButton = event.target.closest("button[data-history-diff-id]"); if (diffButton) { loadHistoryDiff(diffButton.dataset.historyDiffId); return; } const button = event.target.closest("button[data-history-id]"); if (button) rollbackConfig(button.dataset.historyId); });
}

bindEvents();
applyI18n();
syncUsageBucketInput();
if (currentToken()) login();
