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
let loadedConfigRevision = "";
let loadedConfigRedacted = "";
let configDraftDirty = false;
let resourceOptions = { inbounds: [], outbounds: [], client_tags: [], outbound_tags: [] };
let providerMetrics = [];
let providerLiveChecks = {};
let providerRefreshTimer = 0;
let liveRequestsTimer = 0;
let sessionsTimer = 0;
let sessionViewMode = localStorage.getItem("syrogo_sessions_view") || "cards";
let sessionItems = [];
let activeProvider = null;
let clientItems = [];
let clientMetrics = [];
let clientMetricsDays = 7;
let activeClient = null;
let activeClientDetail = null;
let clientHeatmapMetric = "requests";
let routeItems = [];
let routeOrderRevision = "";

const i18n = {
  en: {
    login_eyebrow: "Admin Console", login_title: "Sign in to Syrogo", login_hint: "Use the admin.token configured in your Syrogo config file.", admin_token: "Admin UI token", remember_browser: "Remember this browser", sign_in: "Sign in", console: "Console", dashboard: "Dashboard", sessions: "Sessions", providers: "Providers", clients: "Clients", routes_models: "Routes & Models", usage: "Usage", monitoring: "Monitoring", logs: "Logs", system_config: "System Config", debug: "Debug", apply_current_file: "Apply current file", logout: "Logout", refresh: "Refresh", admin_overview: "Admin overview", new_provider: "New provider", save_provider: "Save provider", delete_provider: "Delete", new_client: "New client", save_client: "Save client", delete_client: "Delete client", new_route: "New route", save_route: "Save route", delete_route: "Delete route", refresh_quota: "Refresh quota", refresh_latency: "Refresh latency"
  },
  "zh-CN": {
    login_eyebrow: "管理控制台", login_title: "登录 Syrogo", login_hint: "使用配置文件中的 admin.token。", admin_token: "Admin UI token", remember_browser: "记住此浏览器", sign_in: "登录", console: "控制台", dashboard: "仪表盘", sessions: "会话", providers: "Provider 配置", clients: "Clients", routes_models: "Routes & Models", usage: "Usage", monitoring: "监控", logs: "日志", system_config: "系统配置", debug: "Debug", apply_current_file: "应用当前配置", logout: "退出", refresh: "刷新", admin_overview: "管理概览", new_provider: "新增 Provider", save_provider: "保存 Provider", delete_provider: "删除", new_client: "新增 Client", save_client: "保存 Client", delete_client: "删除 Client", new_route: "新增路由", save_route: "保存路由", delete_route: "删除路由", refresh_quota: "刷新配额", refresh_latency: "刷新延迟"
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
    const error = new Error(`${response.status} ${response.statusText}: ${detail}`);
    error.status = response.status;
    error.body = body;
    throw error;
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

async function refreshSessions() {
  const params = new URLSearchParams();
  const filters = { client: value("#session-client-filter"), status: value("#session-status-filter"), host: value("#session-host-filter"), cwd: value("#session-cwd-filter") };
  Object.entries(filters).forEach(([key, filterValue]) => { if (filterValue) params.set(key, filterValue); });
  const suffix = params.toString() ? `?${params.toString()}` : "";
  const response = await fetchJSON(`/admin/sessions${suffix}`);
  sessionItems = response.items || [];
  renderSessions();
}

function renderSessions() {
  const target = document.querySelector("#sessions-table");
  target.classList.toggle("table-wrap", sessionViewMode === "table");
  target.classList.toggle("sessions-card-wrap", sessionViewMode === "cards");
  target.innerHTML = sessionItems.length === 0 ? emptyState("No Claude Code sessions yet. Start one with syrogo run claude inside tmux.") : sessionViewMode === "cards" ? renderSessionCards(sessionItems) : renderSessionsTable(sessionItems);
  document.querySelector("#sessions-view-cards").classList.toggle("active", sessionViewMode === "cards");
  document.querySelector("#sessions-view-table").classList.toggle("active", sessionViewMode === "table");
}

function renderSessionsTable(items) {
  return `<table><thead><tr><th>Status</th><th>Client</th><th>Tag</th><th>Location</th><th>Workspace</th><th>Last seen</th><th>Commands</th></tr></thead><tbody>${items.map(renderSessionRow).join("")}</tbody></table>`;
}

function renderSessionRow(item) {
  const tmux = item.tmux || {};
  const commands = item.commands || {};
  const status = item.status || "unknown";
  const location = tmux.present ? `${escapeHTML(tmux.session || "-")} / w${escapeHTML(tmux.window_index || "-")} / p${escapeHTML(tmux.pane_id || tmux.pane_index || "-")}` : "not in tmux";
  const command = commands.attach || commands.select_window || commands.select_pane;
  return `<tr><td>${badge(status, sessionStatusKind(status))}</td><td><strong>${escapeHTML(item.client_name || "-")}</strong><div class="muted">${escapeHTML(item.inbound_name || "-")}</div></td><td>${badge(item.tag || "-", item.tag ? "" : "muted")}</td><td>${location}</td><td><strong>${escapeHTML(item.cwd || "-")}</strong><div class="muted">${escapeHTML(item.host || "-")}</div></td><td>${escapeHTML(formatDateTime(item.last_seen_at || item.started_at))}</td><td class="session-commands">${command ? `<code>${escapeHTML(command)}</code>` : `<span class="muted">No tmux command</span>`}</td></tr>`;
}

function renderSessionCards(items) {
  return `<div class="session-card-grid">${items.map(renderSessionCard).join("")}</div>`;
}

function renderSessionCard(item) {
  const tmux = item.tmux || {};
  const commands = item.commands || {};
  const status = item.status || "unknown";
  const statusKind = sessionStatusKind(status) || "ok";
  const location = tmux.present ? [`${escapeHTML(tmux.session || "-")}`, `w${escapeHTML(tmux.window_index || "-")}${tmux.window_name ? ` · ${escapeHTML(tmux.window_name)}` : ""}`, `p${escapeHTML(tmux.pane_id || tmux.pane_index || "-")}`].join(" / ") : "not in tmux";
  const command = commands.attach || commands.select_window || commands.select_pane;
  const commandHTML = command ? `<code>${escapeHTML(command)}</code>` : `<span class="muted">No tmux command</span>`;
  return `<article class="session-card ${escapeHTML(statusKind)}"><div class="session-card-head"><div>${badge(status, statusKind)}<span>${escapeHTML(item.last_event || "no hook")}</span></div><strong>${escapeHTML(formatDateTime(item.last_seen_at || item.started_at))}</strong></div><div class="session-card-main"><h3>${escapeHTML(item.client_name || "-")}</h3><p>${escapeHTML(item.inbound_name || "-")} · tag ${escapeHTML(item.tag || "-")}</p></div><div class="session-card-meta"><span>tmux</span><strong>${location}</strong><span>cwd</span><strong>${escapeHTML(item.cwd || "-")}</strong><span>host</span><strong>${escapeHTML(item.host || "-")}</strong></div><div class="session-card-commands">${commandHTML}</div></article>`;
}

function setSessionsViewMode(mode) {
  sessionViewMode = mode;
  localStorage.setItem("syrogo_sessions_view", mode);
  renderSessions();
}

function startSessionsRefresh() {
  stopSessionsRefresh();
  refreshSessions().catch((error) => showToast(error.message));
  sessionsTimer = window.setInterval(() => refreshSessions().catch((error) => showToast(error.message)), 2000);
}

function stopSessionsRefresh() {
  if (sessionsTimer) {
    window.clearInterval(sessionsTimer);
    sessionsTimer = 0;
  }
}

function sessionStatusKind(status) {
  if (status === "waiting_permission") return "danger";
  if (status === "tool_running" || status === "compacting") return "warn";
  if (status === "idle" || status === "stopped" || status === "unknown") return "muted";
  return "";
}

async function refreshResourceOptions() {
  resourceOptions = await fetchJSON("/admin/config/options");
  const bindingInbound = document.querySelector("#binding-inbound");
  bindingInbound.innerHTML = (resourceOptions.inbounds || []).map((inbound) => `<option value="${escapeHTML(inbound.name)}">${escapeHTML(inbound.name)} (${escapeHTML(inbound.protocol)})</option>`).join("");
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
  return `<tr class="${provider.enabled === false ? "disabled-row" : ""}"><td><strong>${escapeHTML(provider.name || "")}</strong><div class="muted endpoint-text">${escapeHTML(provider.endpoint || "local mock")}</div></td><td><div class="info-stack"><span>${badge(provider.protocol || "unknown")}</span><span class="muted">tag: ${escapeHTML(provider.tag || "-")}</span></div></td><td>${providerUsageCell(usage)}</td><td>${providerStatusCell(item)}</td><td>${providerQuotaCell(item.quota, provider.quota)}</td><td>${providerTimeline(item.timeline || [])}</td><td>${providerEnabledSwitch(provider)}</td><td><div class="row-actions"><button class="small" data-provider-edit='${escapeAttr(JSON.stringify(provider))}'>Edit</button><button class="small ghost" data-provider-check="${escapeAttr(provider.name || "")}" data-provider-check-protocol="${escapeAttr(provider.protocol || "")}">Test</button></div></td></tr>`;
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
  const windows = (quota.windows || []).map((window) => {
    const requests = window.max_requests > 0 ? `${window.used_requests}/${window.max_requests} req` : "";
    const tokens = window.max_tokens > 0 ? `${window.used_tokens}/${window.max_tokens} tok` : "";
    const reset = [window.reset, window.fixed_period, window.reset_at ? `reset ${formatTime(window.reset_at)}` : ""].filter(Boolean).join(" · ");
    return escapeHTML(`${window.name}: ${[requests, tokens, reset].filter(Boolean).join(" / ")}`);
  }).join("<br>");
  const kind = quota.state === "available" ? "" : "warn";
  const metadata = [quota.cooldown_until ? `cooldown ${formatTime(quota.cooldown_until)}` : "", quota.next_probe_at ? `probe ${formatTime(quota.next_probe_at)}` : "", quota.last_quota_exceeded_at ? `last 429 ${formatTime(quota.last_quota_exceeded_at)}` : ""].filter(Boolean).join(" · ");
  return `${badge(quota.state || "available", kind)}<div class="muted">${windows || "no windows"}</div>${metadata ? `<div class="muted">${escapeHTML(metadata)}</div>` : ""}`;
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
  document.querySelector("#provider-test-model").value = "";
  document.querySelector("#provider-models-json").value = pretty(item.models || []);
  document.querySelector("#provider-quota-json").value = pretty(item.quota || { enabled: false, windows: [], cooldown: "10m", probe_interval: "1m" });
  document.querySelector("#provider-usage-estimation").checked = Boolean(item.capabilities?.usage_estimation);
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
  let models;
  let quota;
  try {
    models = JSON.parse(value("#provider-models-json") || "[]");
    setInvalid("#provider-models-json", false);
  } catch (_) {
    models = null;
  }
  try {
    quota = JSON.parse(value("#provider-quota-json") || "{}");
    setInvalid("#provider-quota-json", false);
  } catch (_) {
    quota = null;
  }
  const capabilities = { ...(activeProvider?.capabilities || {}) };
  capabilities.usage_estimation = document.querySelector("#provider-usage-estimation").checked;
  capabilities.usage_estimation_mode = capabilities.usage_estimation ? "heuristic" : "";
  return { name: value("#provider-name"), protocol: value("#provider-protocol"), tag: value("#provider-tag"), endpoint: value("#provider-endpoint"), auth_token: value("#provider-auth-token"), enabled: activeProvider?.enabled !== false, models, capabilities, quota, proxy: { url: value("#provider-proxy-url") } };
}

function validateProviderDraft() {
  const payload = providerPayload();
  const issues = [];
  setInvalid("#provider-name", !payload.name);
  setInvalid("#provider-protocol", !payload.protocol);
  const endpointInvalid = payload.protocol !== "mock" && !payload.endpoint;
  const modelsInvalid = !Array.isArray(payload.models) || payload.models.some((model) => !model || typeof model !== "object" || Array.isArray(model));
  const quotaInvalid = payload.quota === null || typeof payload.quota !== "object" || Array.isArray(payload.quota);
  setInvalid("#provider-endpoint", endpointInvalid);
  setInvalid("#provider-models-json", modelsInvalid);
  setInvalid("#provider-quota-json", quotaInvalid);
  if (!payload.name) issues.push("name is required");
  if (!payload.protocol) issues.push("protocol is required");
  if (endpointInvalid) issues.push("endpoint is required for non-mock providers");
  if (modelsInvalid) issues.push("models must be a valid JSON array; Core validates canonical names and aliases");
  if (quotaInvalid) issues.push("quota must be valid JSON object; Core validates its schema");
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

async function checkProviderLive(name, model, draft = null) {
  if (!name && !draft?.name) return;
  const checkName = name || draft.name;
  const testModel = String(model || "").trim();
  providerLiveChecks[checkName] = { ok: false, state: "checking", checked_at: new Date().toISOString(), latency_ms: 0 };
  renderFilteredProviders();
  try {
    const payload = draft ? { name: checkName, model: testModel, provider: draft } : { name: checkName, model: testModel };
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
  const draft = providerPayload();
  const testModel = value("#provider-test-model");
  const target = document.querySelector("#provider-validate-result");
  const modelRequired = draft.protocol !== "mock" && !testModel;
  setInvalid("#provider-test-model", modelRequired);
  if (modelRequired) {
    target.className = "inline-status error";
    target.textContent = "test model is required for non-mock providers";
    return;
  }
  target.className = "inline-status loading";
  target.textContent = "Testing provider connection...";
  const result = await checkProviderLive("", testModel, draft);
  if (!result) return;
  target.className = result.ok ? "inline-status" : "inline-status error";
  target.textContent = result.ok ? `Connection OK · ${result.latency_ms || 0}ms` : `Connection failed: ${result.error || "unknown error"}`;
}

async function testProviderFromList(name, protocol) {
  const model = window.prompt(protocol === "mock" ? "Test model (optional for mock)" : "Test model", "");
  if (model === null) return;
  const testModel = model.trim();
  if (protocol !== "mock" && !testModel) {
    providerLiveChecks[name] = { ok: false, state: "unavailable", checked_at: new Date().toISOString(), latency_ms: 0, error: "test model is required for non-mock providers" };
    renderFilteredProviders();
    return;
  }
  await checkProviderLive(name, testModel);
}

async function setProviderEnabled(name, enabled) {
  if (!name) return;
  await mutateResource("/admin/config/provider/enabled", { name, enabled }, refreshProviders, `${name} ${enabled ? "enabled" : "disabled"} and applied.`);
}

async function saveProvider() {
  if (!validateProviderDraft()) return;
  await mutateResource("/admin/config/provider/upsert", providerPayload(), async () => { closeProviderModal(); await refreshProviders(); }, "Provider saved and applied.");
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
  await mutateResource("/admin/config/provider/delete", { name }, async () => { closeProviderModal(); await refreshProviders(); }, "Provider deleted and applied.");
}

async function refreshClients() {
  await refreshResourceOptions();
  const warning = document.querySelector("#clients-warning");
  warning.classList.add("hidden");
  const [configResult, metricsResult] = await Promise.allSettled([
    fetchJSON("/admin/config/clients"),
    fetchJSON(`/admin/config/clients/metrics?days=${encodeURIComponent(clientMetricsDays)}`),
  ]);
  if (configResult.status === "rejected") {
    document.querySelector("#clients-table").innerHTML = errorBlock(configResult.reason.message);
    throw configResult.reason;
  }
  clientItems = configResult.value.items || [];
  clientMetrics = metricsResult.status === "fulfilled" ? metricsResult.value.items || [] : [];
  if (metricsResult.status === "rejected") {
    warning.textContent = `Metrics unavailable: ${metricsResult.reason.message}. Client editing remains available.`;
    warning.classList.remove("hidden");
  }
  renderClients();
}

function renderClients() {
  const metricsByClient = new Map(clientMetrics.map((item) => [item.client?.name, item]));
  document.querySelector("#clients-table").innerHTML = clientItems.length === 0 ? emptyState("No clients.") : renderClientTable(clientItems, metricsByClient);
  document.querySelectorAll("[data-client-days]").forEach((button) => button.classList.toggle("active", Number(button.dataset.clientDays) === clientMetricsDays));
  if (activeClientDetail) {
    const current = clientItems.find((item) => item.name === activeClientDetail.client?.name);
    if (current) renderClientDetail(activeClientDetail);
    else closeClientDetail();
  }
}

function renderClientTable(items, metricsByClient) {
  return `<table class="client-table"><thead><tr><th>Client</th><th>Bindings</th><th>Usage (all-time)</th><th>Frequency (${clientMetricsDays}d)</th><th>Quota</th><th>Actions</th></tr></thead><tbody>${items.map((item) => renderClientRow(item, metricsByClient.get(item.name))).join("")}</tbody></table>`;
}

function renderClientBindings(bindings) {
  if (!bindings?.length) return `<span class="muted">Not bound to an inbound</span>`;
  return `<div class="binding-chips">${bindings.map((binding) => `<span class="binding-chip"><strong>${escapeHTML(binding.inbound || "-")}</strong><span>${escapeHTML(binding.inbound_protocol || "unknown")} · tag ${escapeHTML(binding.tag || "-")}</span></span>`).join("")}</div>`;
}

function renderClientRow(item, metrics) {
  const usage = metrics?.all_time;
  const frequency = metrics?.frequency;
  const quota = metrics?.quota;
  return `<tr data-client-detail-name="${escapeAttr(item.name || "")}" data-row><td><strong>${escapeHTML(item.name || "")}</strong></td><td>${renderClientBindings(item.bindings || [])}</td><td>${usage ? `<div class="compact-metrics"><span>${formatNumber(usage.request_count)} req</span><span>${formatNumber(usage.total_tokens)} tok</span><span>${formatCost(usage.cost_usd)}</span></div>` : badge("unavailable", "warn")}</td><td>${frequency ? `<div class="compact-metrics"><span>${formatNumber(frequency.requests)} req</span><span>${formatNumber(frequency.active_days)}/${formatNumber(frequency.calendar_days)} active days</span><span>${formatDecimal(frequency.requests_per_day)} / day</span></div>` : badge("unavailable", "warn")}</td><td>${clientQuotaCell(quota, item.quota)}</td><td><button class="small" data-client-edit='${escapeAttr(JSON.stringify(item))}'>Edit</button></td></tr>`;
}

function clientQuotaCell(quota, quotaConfig) {
  if (!quota && !quotaConfig?.enabled) return badge("off", "muted");
  if (!quota) return badge("configured");
  const windows = (quota.windows || []).map((window) => {
    const type = window.type || (window.max_requests > 0 ? "requests" : "requests");
    let usage;
    if (type === "tokens") usage = `${formatNumber(window.used_tokens)}/${formatNumber(window.max_tokens)} tokens`;
    else if (type === "cost") usage = `${formatCost(window.used_cost_usd)}/${formatCost(window.max_cost_usd)} cost`;
    else usage = `${formatNumber(window.used_requests)}/${formatNumber(window.max_requests)} requests`;
    const warning = window.warning || (Number(window.unpriced_count || 0) > 0 ? `unpriced usage: ${formatNumber(window.unpriced_count)} terminal result(s) counted as $0` : "");
    return `<div class="quota-window"><strong>${escapeHTML(window.name || type)}</strong> ${badge(type, "muted")}<span>${escapeHTML(usage)}</span>${warning ? `<span class="quota-warning">Warning: ${escapeHTML(warning)}</span>` : ""}</div>`;
  }).join("");
  const limited = quota.state && quota.state !== "available";
  return `${badge(quota.state || "available", limited ? "danger" : "")}<div class="client-quota-windows">${windows || `<span class="muted">no windows</span>`}</div>`;
}

function fillClientForm(item = {}) {
  activeClient = item.name ? item : null;
  document.querySelector("#client-modal-title").textContent = item.name ? "Edit Client" : "New Client";
  document.querySelector("#client-name").value = item.name || "";
  document.querySelector("#client-name").disabled = Boolean(item.name);
  document.querySelector("#client-token").value = item.name ? "" : (item.token || "");
  document.querySelector("#client-quota-json").value = pretty(item.quota || { enabled: false, windows: [] });
  document.querySelector("#client-delete-confirm").classList.add("hidden");
  document.querySelector("#client-delete-name").value = "";
  document.querySelector("#delete-client").textContent = "Delete client";
  document.querySelector("#delete-client").classList.toggle("hidden", !item.name);
  document.querySelector("#client-bindings-section").classList.toggle("hidden", !item.name);
  clearClientBindingError();
  document.querySelector("#binding-tag").value = "";
  renderClientBindingEditor();
  document.querySelector("#client-modal").classList.remove("hidden");
}
function closeClientModal() { document.querySelector("#client-modal").classList.add("hidden"); activeClient = null; }
function clientPayload() {
  const quotaText = value("#client-quota-json");
  if (!quotaText) throw new Error("Quota JSON is required to preserve the complete quota configuration.");
  let quota;
  try { quota = JSON.parse(quotaText); } catch (_) { throw new Error("Quota must be a valid JSON object."); }
  if (!quota || typeof quota !== "object" || Array.isArray(quota)) throw new Error("Quota must be a valid JSON object.");
  return { name: value("#client-name"), token: value("#client-token"), quota };
}
async function saveClient() {
  let payload;
  try { payload = clientPayload(); setInvalid("#client-quota-json", false); } catch (error) { setInvalid("#client-quota-json", true); showToast(error.message); return; }
  await mutateAppliedClient("/admin/config/client/upsert", payload, async () => { closeClientModal(); await refreshClients(); }, "Client saved and applied.");
}
async function deleteClient() {
  const name = value("#client-name");
  if (!name) return;
  const bindings = activeClient?.bindings || [];
  if (bindings.length) { showToast(`Remove all ${bindings.length} binding(s) before deleting this Client.`); return; }
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
  await mutateAppliedClient("/admin/config/client/delete", { name }, async () => { closeClientModal(); closeClientDetail(); await refreshClients(); }, "Client deleted and applied.");
}

function renderClientBindingEditor() {
  const target = document.querySelector("#client-bindings-list");
  if (!target) return;
  const bindings = activeClient?.bindings || [];
  target.innerHTML = bindings.length ? bindings.map((binding) => `<div class="client-binding-row"><div><strong>${escapeHTML(binding.inbound || "-")}</strong><span>${escapeHTML(binding.inbound_protocol || "unknown")} · ${escapeHTML(binding.inbound_path || "-")} · tag ${escapeHTML(binding.tag || "-")}</span></div><div class="row-actions"><button class="small ghost" type="button" data-binding-edit='${escapeAttr(JSON.stringify(binding))}'>Edit</button><button class="small danger" type="button" data-binding-delete-inbound="${escapeAttr(binding.inbound || "")}" data-binding-delete-ref="${escapeAttr(binding.ref || activeClient?.name || "")}">Unbind</button></div></div>`).join("") : `<p class="muted">No bindings. This Client cannot authenticate on any Inbound yet.</p>`;
}

function clearClientBindingError() {
  const target = document.querySelector("#client-binding-error");
  if (!target) return;
  target.classList.add("hidden");
  target.innerHTML = "";
}

function showClientBindingError(error) {
  const target = document.querySelector("#client-binding-error");
  const body = error?.body;
  if (!target || !body || typeof body !== "object" || body.error_code !== "binding_tag_last_source") return false;
  const details = body.details || {};
  const routes = Array.isArray(details.route_names) ? details.route_names : [];
  target.innerHTML = `<strong>Binding change blocked: tag ${escapeHTML(details.tag || "-")} is the last source for route${routes.length === 1 ? "" : "s"} ${escapeHTML(routes.join(", ") || "-")}.</strong><span>Client ${escapeHTML(details.client || activeClient?.name || "-")} on Inbound ${escapeHTML(details.inbound || "-")} cannot be ${escapeHTML(details.operation === "delete" ? "unbound" : "retagged")} yet.</span><ol><li>Add or update another Client binding to provide tag <code>${escapeHTML(details.tag || "-")}</code>, then retry this binding change.</li><li>Remove tag <code>${escapeHTML(details.tag || "-")}</code> from the listed route <code>from_tags</code> (or delete/change those routes), then retry.</li></ol>`;
  target.classList.remove("hidden");
  target.scrollIntoView({ block: "nearest" });
  return true;
}

async function mutateClientBinding(path, payload, refresh, successMessage) {
  clearClientBindingError();
  try {
    const response = await fetchJSON(path, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(payload) });
    if (!response?.saved || !response?.applied) throw new Error(response?.reason || "Binding config was not both saved and applied.");
    showToast(successMessage);
    await refresh();
  } catch (error) {
    if (!showClientBindingError(error)) showToast(error.message);
  }
}

async function saveClientBinding() {
  if (!activeClient?.name) return;
  const payload = { inbound: value("#binding-inbound"), ref: activeClient.name, tag: value("#binding-tag") };
  if (!payload.inbound || !payload.tag) { showToast("Inbound and tag are required for a binding."); return; }
  await mutateClientBinding("/admin/config/client-binding/upsert", payload, async () => { await refreshClients(); activeClient = clientItems.find((item) => item.name === payload.ref) || activeClient; renderClientBindingEditor(); }, "Binding saved and applied.");
}

async function deleteClientBinding(inbound, ref) {
  await mutateClientBinding("/admin/config/client-binding/delete", { inbound, ref }, async () => { await refreshClients(); activeClient = clientItems.find((item) => item.name === ref) || activeClient; renderClientBindingEditor(); }, "Binding removed and applied.");
}

async function mutateAppliedClient(path, payload, refresh, successMessage) {
  try {
    const response = await fetchJSON(path, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(payload) });
    if (!response?.saved || !response?.applied) throw new Error(response?.reason || "Client config was not both saved and applied.");
    showToast(successMessage);
    await refresh();
  } catch (error) { showToast(error.message); }
}

async function openClientDetail(name) {
  if (!name) return;
  const target = document.querySelector("#client-detail");
  target.classList.remove("hidden");
  target.innerHTML = inlineStatus("Loading client usage...", "loading");
  try {
    activeClientDetail = await fetchJSON(`/admin/config/client/usage?name=${encodeURIComponent(name)}`);
    renderClientDetail(activeClientDetail);
  } catch (error) {
    activeClientDetail = null;
    target.innerHTML = errorBlock(error.message);
  }
}

function closeClientDetail() {
  activeClientDetail = null;
  const target = document.querySelector("#client-detail");
  target.classList.add("hidden");
  target.innerHTML = "";
}

function renderClientDetail(detail) {
  const client = detail.client || {};
  const summary = detail.range_summary || {};
  const coverage = detail.coverage || {};
  const daily = detail.daily || [];
  const coverageText = coverage.known ? `Tracked since ${coverage.tracking_started_at || "unknown"}; raw retention ${coverage.raw_retention_days || 0} days; daily aggregates ${coverage.aggregates_persisted ? "persisted" : "memory-only"}.` : "Legacy coverage is unknown; zero-value historical days are not asserted as complete.";
  document.querySelector("#client-detail").innerHTML = `<article class="card full client-detail-card"><div class="section-title"><div><p class="eyebrow">Client detail</p><h2>${escapeHTML(client.name || "")}</h2><p class="hint">UTC ${escapeHTML(detail.start_date || "")} to ${escapeHTML(detail.end_date || "")} (end exclusive). The current day is partial. Usage and quota are global across all bindings.</p></div><button class="ghost small" data-client-detail-close>Close</button></div><div class="client-detail-bindings"><strong>Bindings</strong>${renderClientBindings(client.bindings || [])}</div><div class="summary-grid">${metric("Requests", summary.request_count)}${metric("Tokens", summary.total_tokens)}${metric("Cost", formatCost(summary.cost_usd))}${metric("Errors", summary.error_count)}</div><div class="heatmap-toolbar"><div class="segmented-control" aria-label="Heatmap metric">${["requests", "tokens", "cost", "errors"].map((name) => `<button type="button" class="small ${clientHeatmapMetric === name ? "active" : ""}" data-client-heatmap-metric="${name}">${name[0].toUpperCase() + name.slice(1)}</button>`).join("")}</div><span class="muted">${escapeHTML(coverageText)}</span></div>${renderClientHeatmap(daily, clientHeatmapMetric)}<div class="section-subtitle"><h3>Daily records</h3><span class="hint">Daily aggregates, not a per-request audit log.</span></div>${daily.length ? renderClientDailyTable(daily) : emptyState("No daily usage records.")}</article>`;
}

function renderClientHeatmap(daily, selectedMetric) {
  const values = daily.map((day) => clientDailyMetric(day, selectedMetric));
  const maxLog = Math.max(0, ...values.map((number) => Math.log1p(Math.max(0, number))));
  const cells = daily.map((day, index) => {
    const number = values[index];
    const level = maxLog > 0 && number > 0 ? Math.max(1, Math.min(5, Math.ceil((Math.log1p(number) / maxLog) * 5))) : 0;
    const status = day.status || "unknown";
    const tooltip = `${day.value || day.date} UTC · ${status} · ${formatNumber(day.request_count)} requests · ${formatNumber(day.total_tokens)} tokens · ${formatCost(day.cost_usd)} · ${formatNumber(day.error_count)} errors`;
    return `<span class="heatmap-cell level-${level} ${escapeAttr(status)}" tabindex="0" role="img" aria-label="${escapeAttr(tooltip)}" data-tooltip="${escapeAttr(tooltip)}"></span>`;
  }).join("");
  return `<div class="contribution-heatmap" aria-label="${escapeAttr(selectedMetric)} contribution heatmap">${cells}</div>`;
}

function clientDailyMetric(day, metricName) {
  if (metricName === "tokens") return Number(day.total_tokens || 0);
  if (metricName === "cost") return Number(day.cost_usd || 0);
  if (metricName === "errors") return Number(day.error_count || 0);
  return Number(day.request_count || 0);
}

function renderClientDailyTable(daily) {
  return `<div class="table-wrap daily-records"><table><thead><tr><th>Date (UTC)</th><th>Status</th><th>Requests</th><th>Tokens</th><th>Cost</th><th>Errors</th></tr></thead><tbody>${daily.slice().reverse().map((day) => `<tr><td>${escapeHTML(day.value || day.date || "")}</td><td>${badge(day.status || "unknown", day.status === "partial" ? "warn" : day.status === "unknown" ? "muted" : "")}</td><td>${formatNumber(day.request_count)}</td><td>${formatNumber(day.total_tokens)}</td><td>${formatCost(day.cost_usd)}</td><td>${formatNumber(day.error_count)}</td></tr>`).join("")}</tbody></table></div>`;
}

function formatNumber(number) { return new Intl.NumberFormat().format(Number(number || 0)); }
function formatDecimal(number) { return Number(number || 0).toFixed(2); }
function formatCost(number) { return `$${Number(number || 0).toFixed(6)}`; }

async function refreshRoutes() {
  await refreshResourceOptions();
  const response = await fetchJSON("/admin/config/routes");
  routeItems = response.items || [];
  routeOrderRevision = response.order_revision || "";
  document.querySelector("#routes-table").innerHTML = routeItems.length === 0 ? emptyState("No routes.") : renderRouteTable(routeItems);
}

function renderRouteTable(items) {
  return `<table class="route-table"><thead><tr><th>Priority</th><th>Name</th><th>Request models / fallback</th><th>From tags</th><th>To tags</th><th>Strategy</th><th>Routed model</th><th>Actions</th></tr></thead><tbody>${items.map((item, index) => renderRouteRow(item, index, items.length)).join("")}</tbody></table>`;
}

function renderRouteRow(item, index, count) {
  const patterns = item.match?.models || [];
  const requestModels = patterns.length ? `<div class="route-patterns">${patterns.map((pattern) => `<code>${escapeHTML(pattern)}</code>`).join("")}</div>` : `${badge("fallback", "muted")}<div class="muted">any original requested model</div>`;
  const routedModel = item.target_model ? `<code>${escapeHTML(item.target_model)}</code>` : Object.keys(item.model_map || {}).length ? `<pre>${escapeHTML(pretty(item.model_map))}</pre>` : `<span class="muted">original model unchanged</span>`;
  return `<tr data-route='${escapeAttr(JSON.stringify(item))}' data-row><td><strong>${index + 1}</strong><div class="route-order-actions"><button type="button" class="small ghost" data-route-move="up" data-route-index="${index}" ${index === 0 ? "disabled" : ""} aria-label="Move ${escapeAttr(item.name || "route")} up">↑</button><button type="button" class="small ghost" data-route-move="down" data-route-index="${index}" ${index === count - 1 ? "disabled" : ""} aria-label="Move ${escapeAttr(item.name || "route")} down">↓</button></div></td><td><strong>${escapeHTML(item.name || "")}</strong></td><td>${requestModels}</td><td>${escapeHTML((item.from_tags || []).join(", "))}</td><td>${escapeHTML((item.to_tags || []).join(", "))}</td><td>${badge(item.strategy || "unknown", "")}</td><td>${routedModel}</td><td><button class="small" data-route-edit='${escapeAttr(JSON.stringify(item))}'>Edit</button></td></tr>`;
}

function fillRouteForm(item = {}) {
  document.querySelector("#route-modal-title").textContent = item.name ? "编辑 Route" : "新增 Route";
  document.querySelector("#route-name").value = item.name || "";
  document.querySelector("#route-from-tags").value = (item.from_tags || []).join(", ");
  document.querySelector("#route-match-models").value = (item.match?.models || []).join("\n");
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
  const matchModels = value("#route-match-models").split("\n").map((pattern) => pattern.trim()).filter(Boolean);
  const payload = { name: value("#route-name"), from_tags: csv("#route-from-tags"), to_tags: csv("#route-to-tags"), strategy: value("#route-strategy"), target_model: value("#route-target-model"), model_map: modelMap, weights, match: matchModels.length ? { models: matchModels } : null };
  await mutateResource("/admin/config/route/upsert", payload, async () => { closeRouteModal(); await refreshRoutes(); }, "Route saved and applied.");
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
  await mutateResource("/admin/config/route/delete", { name }, async () => { closeRouteModal(); await refreshRoutes(); }, "Route deleted and applied.");
}

async function reorderRoute(fromIndex, toIndex) {
  if (fromIndex < 0 || toIndex < 0 || fromIndex >= routeItems.length || toIndex >= routeItems.length || fromIndex === toIndex) return;
  try {
    const response = await fetchJSON("/admin/config/routes/reorder", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ from_index: fromIndex, to_index: toIndex, expected_revision: routeOrderRevision }) });
    if (!response?.saved || !response?.applied) throw new Error(response?.reason || "Route order was not both saved and applied.");
    showToast("Route priority saved and applied.");
    await refreshRoutes();
  } catch (error) {
    if (error.status === 409) {
      showToast("Route order changed elsewhere. Refreshed the latest priorities; please retry.");
      await refreshRoutes();
      return;
    }
    showToast(error.message);
  }
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
  if (!params) { target.innerHTML = errorBlock("Select both UTC dates for a custom range."); showToast("Usage date range is incomplete."); return; }
  try { const response = await fetchJSON(`/admin/usage?${params.toString()}`); const items = response.items || []; target.innerHTML = items.length === 0 ? emptyState("No usage records.") : renderObjectTable(items); } catch (error) { target.innerHTML = errorBlock(error.message); }
}
function usageParams() { const range = usageDateRange(); if (!range) return null; const params = new URLSearchParams(); params.set("group_by", value("#usage-group-by")); params.set("start_date", range.start); params.set("end_date", shiftUTCDate(range.end, 1)); return params; }
function usageDateRange() { const preset = value("#usage-range"); if (preset === "custom") { const start = value("#usage-start-date"); const end = value("#usage-end-date"); return start && end ? { start, end } : null; } const today = utcDate(new Date()); if (preset === "month") return { start: `${today.slice(0, 8)}01`, end: today }; const days = preset === "30d" ? 30 : 7; return { start: shiftUTCDate(today, 1 - days), end: today }; }
function utcDate(date) { return date.toISOString().slice(0, 10); }
function shiftUTCDate(date, days) { const shifted = new Date(`${date}T00:00:00Z`); shifted.setUTCDate(shifted.getUTCDate() + days); return utcDate(shifted); }
function syncUsageRangeInputs() { const custom = value("#usage-range") === "custom"; const start = document.querySelector("#usage-start-date"); const end = document.querySelector("#usage-end-date"); start.hidden = !custom; end.hidden = !custom; if (!custom) { start.value = ""; end.value = ""; } }

async function refreshQuota() { const target = document.querySelector("#quota-table"); try { const response = await fetchJSON("/admin/quota"); target.innerHTML = [`<div class="table-heading">Outbound quota</div>`, (response.outbound || []).length === 0 ? emptyState("No outbound quota records.") : renderObjectTable(response.outbound || []), `<div class="table-heading">Client quota</div>`, (response.client || []).length === 0 ? emptyState("No client quota records.") : renderObjectTable(response.client || [])].join(""); } catch (error) { target.innerHTML = errorBlock(error.message); } }
async function refreshLatency() { const target = document.querySelector("#latency-table"); try { const snapshot = await fetchJSON("/admin/latency"); const items = snapshot.items || []; target.innerHTML = items.length === 0 ? emptyState("No recent requests.") : `<table><thead><tr><th>Time</th><th>Path</th><th>Inbound</th><th>Client</th><th>Status</th><th>Total</th><th>Spans</th></tr></thead><tbody>${items.map(renderTraceRow).join("")}</tbody></table>`; } catch (error) { target.innerHTML = errorBlock(error.message); } }

async function refreshLogs() { const target = document.querySelector("#logs-content"); const meta = document.querySelector("#logs-meta"); const params = new URLSearchParams(); const lines = value("#log-lines"); const bytes = value("#log-bytes"); if (lines) params.set("lines", lines); if (bytes) params.set("bytes", bytes); meta.innerHTML = inlineStatus("Loading logs...", "loading"); try { const response = await fetchJSON(`/admin/logs?${params.toString()}`); target.textContent = response.content || ""; meta.innerHTML = renderLogsMeta(response); showToast("Logs refreshed."); } catch (error) { target.textContent = `Failed to load logs.\n${error.message}`; meta.innerHTML = inlineStatus(error.message, "error"); showToast("Refresh logs failed."); } }
function renderLogsMeta(response) { const truncated = Boolean(response.truncated); const lineLimit = response.lines ? response.lines : "not applied"; return `<div class="meta-item"><span>Path</span><strong>${escapeHTML(response.path || "")}</strong></div><div class="meta-item"><span>Truncated</span><strong><span class="badge ${truncated ? "warn" : "ok"}">${truncated ? "yes" : "no"}</span></strong></div><div class="meta-item"><span>Read limit</span><strong>${escapeHTML(formatBytes(response.max_bytes || 0))}</strong></div><div class="meta-item"><span>Line limit</span><strong>${escapeHTML(lineLimit)}</strong></div><div class="meta-item"><span>Refreshed</span><strong>${escapeHTML(new Date().toLocaleString())}</strong></div>`; }
function inlineStatus(message, kind) { return `<div class="inline-status ${escapeHTML(kind)}">${escapeHTML(message)}</div>`; }
function formatBytes(value) { const bytes = Number(value) || 0; if (bytes < 1024) return `${bytes} B`; if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`; return `${(bytes / 1024 / 1024).toFixed(1)} MiB`; }

async function loadConfig() { const result = document.querySelector("#config-result"); try { const response = await fetchJSON("/admin/config"); loadedConfigRevision = response.revision || ""; loadedConfigRedacted = response.redacted_content || ""; document.querySelector("#config-inspect").textContent = response.config_ready ? loadedConfigRedacted : "Config path is not configured."; result.textContent = pretty({ config_ready: response.config_ready, revision: response.revision, checksum: response.checksum }); showToast(response.config_ready ? "Loaded redacted current config." : "Config path is not configured."); } catch (error) { result.textContent = error.message; showToast("Load config failed."); } }
async function submitConfig(path) { const editor = document.querySelector("#config-yaml"); const body = editor.value; const result = document.querySelector("#config-result"); const headers = { "Content-Type": "application/x-yaml" }; if (path.endsWith("update")) headers["If-Match"] = loadedConfigRevision; try { const response = await fetchJSON(path, { method: "POST", headers, body }); result.textContent = pretty(response); if (path.endsWith("update")) { loadedConfigRevision = response.revision || loadedConfigRevision; configDraftDirty = false; editor.value = ""; showToast("Config file updated. Click Apply current file to hot-reload safe changes."); loadConfig(); } else showToast("Config is valid."); } catch (error) { result.textContent = error.message; if (error.status === 409) showToast("Config changed on disk. Draft preserved; inspect current config and retry or force explicitly."); else showToast("Config request failed."); } }
function updateConfigWithConfirm(force = false) { const editor = document.querySelector("#config-yaml"); const result = document.querySelector("#config-result"); const nextConfig = editor.value; if (!nextConfig.trim()) { result.textContent = "Config body is empty."; showToast("Config body is empty."); return; } if (nextConfig.includes("<redacted>")) { result.textContent = "Config contains <redacted>. Paste a complete config before updating."; showToast("Cannot update redacted config."); return; } document.querySelector("#config-diff").innerHTML = renderConfigDiff(loadedConfigRedacted, nextConfig) || "No visible changes from the redacted current config."; if (!window.confirm("Update the startup config file?\n\nThis overwrites it after validation and does not apply it.")) { result.textContent = "Config update cancelled."; return; } if (force) loadedConfigRevision = "*"; submitConfig("/admin/config/update"); }
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

async function refreshLiveRequests() {
  const response = await fetchJSON("/admin/latency/active");
  const items = response.items || [];
  document.querySelector("#live-requests-table").innerHTML = items.length === 0 ? `<div class="live-empty">No requests are currently running.</div>` : renderLiveRequestsTable(items);
}

function renderLiveRequestsTable(items) {
  return `<table class="live-requests-table"><thead><tr><th>State</th><th>Client / model</th><th>Provider</th><th>Elapsed</th><th>First token</th><th>Stream</th><th>Request</th></tr></thead><tbody>${items.map(renderLiveRequestRow).join("")}</tbody></table>`;
}

function renderLiveRequestRow(item) {
  const state = item.stream_state || "routing";
  const stateKind = state === "waiting_first_token" ? "warn" : state === "error" ? "danger" : "";
  const firstToken = item.first_token_at ? `${formatDuration(item.ttft_ms)} TTFT` : state === "waiting_first_token" ? `${formatDuration(item.waiting_first_token_ms)} waiting` : "not received";
  const stream = state === "streaming" ? `${formatDuration(item.stream_idle_ms)} idle · ${escapeHTML(item.stream_event_count || 0)} events` : `${escapeHTML(item.stream_event_count || 0)} events`;
  return `<tr><td>${badge(state, stateKind)}</td><td><strong>${escapeHTML(item.client_name || "-")}</strong><div class="muted">${escapeHTML(item.inbound || "-")} · ${escapeHTML(item.planned_steps?.[0]?.model || "-")}</div></td><td><strong>${escapeHTML(item.outbound_name || "selecting")}</strong><div class="muted">${escapeHTML(item.outbound_protocol || "-")}</div></td><td><strong>${formatDuration(item.elapsed_ms)}</strong><div class="muted">fallbacks ${escapeHTML(item.fallback_count || 0)}</div></td><td>${escapeHTML(firstToken)}</td><td>${escapeHTML(stream)}</td><td><code>${escapeHTML(item.request_id || "-")}</code><div class="muted">${escapeHTML(formatDateTime(item.started_at))}</div></td></tr>`;
}

function formatDuration(value) {
  const milliseconds = Number(value || 0);
  if (milliseconds < 1000) return `${milliseconds}ms`;
  return `${(milliseconds / 1000).toFixed(milliseconds < 10000 ? 1 : 0)}s`;
}

function startLiveRequestsRefresh() {
  stopLiveRequestsRefresh();
  refreshLiveRequests().catch((error) => showToast(error.message));
  liveRequestsTimer = window.setInterval(() => refreshLiveRequests().catch((error) => showToast(error.message)), 2000);
}

function stopLiveRequestsRefresh() {
  if (liveRequestsTimer) {
    window.clearInterval(liveRequestsTimer);
    liveRequestsTimer = 0;
  }
}

function formatTime(value) {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}
function formatDateTime(value) {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString([], { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
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
const pageTitles = { dashboard: "Dashboard", sessions: "Sessions", providers: "Providers", clients: "Clients", routes: "Routes & Models", usage: "Usage", monitoring: "Monitoring", logs: "Logs", config: "System Config", debug: "Debug" };
function switchPanel(target) {
  document.querySelectorAll(".nav-item").forEach((item) => item.classList.toggle("active", item.dataset.target === target));
  document.querySelectorAll(".panel").forEach((item) => item.classList.toggle("active", item.id === target));
  updatePageTitle();
  if (target === "providers") {
    refreshProviders();
    startProviderAutoRefresh();
  } else {
    stopProviderAutoRefresh();
  }
  if (target === "sessions") {
    startSessionsRefresh();
  } else {
    stopSessionsRefresh();
  }
  if (target === "clients") refreshClients();
  if (target === "routes") refreshRoutes();
  if (target === "usage") refreshUsage();
  if (target === "monitoring") {
    refreshQuota();
    refreshLatency();
    startLiveRequestsRefresh();
  } else {
    stopLiveRequestsRefresh();
  }
  if (target === "logs") refreshLogs();
  if (target === "debug") {
    refreshTraces();
    refreshProviderDebug();
  }
}
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
  document.querySelector("#refresh-sessions").addEventListener("click", refreshSessions);
  document.querySelector("#sessions-view-cards").addEventListener("click", () => setSessionsViewMode("cards"));
  document.querySelector("#sessions-view-table").addEventListener("click", () => setSessionsViewMode("table"));
  ["#session-client-filter", "#session-status-filter", "#session-host-filter", "#session-cwd-filter"].forEach((selector) => document.querySelector(selector).addEventListener("input", refreshSessions));
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
    if (checkButton) { testProviderFromList(checkButton.dataset.providerCheck, checkButton.dataset.providerCheckProtocol); }
  });
  document.querySelector("#refresh-clients").addEventListener("click", refreshClients);
  document.querySelector("#client-days").addEventListener("click", (event) => { const button = event.target.closest("button[data-client-days]"); if (!button) return; clientMetricsDays = Number(button.dataset.clientDays); refreshClients(); });
  document.querySelector("#new-client").addEventListener("click", () => fillClientForm());
  document.querySelector("#close-client-modal").addEventListener("click", closeClientModal);
  document.querySelector("#save-client").addEventListener("click", saveClient);
  document.querySelector("#delete-client").addEventListener("click", deleteClient);
  document.querySelector("#save-client-binding").addEventListener("click", saveClientBinding);
  document.querySelector("#client-bindings-list").addEventListener("click", (event) => {
    const edit = event.target.closest("button[data-binding-edit]");
    if (edit) {
      const binding = JSON.parse(edit.dataset.bindingEdit);
      document.querySelector("#binding-inbound").value = binding.inbound || "";
      document.querySelector("#binding-tag").value = binding.tag || "";
      return;
    }
    const remove = event.target.closest("button[data-binding-delete-inbound]");
    if (remove) deleteClientBinding(remove.dataset.bindingDeleteInbound, remove.dataset.bindingDeleteRef);
  });
  document.querySelector("#clients-table").addEventListener("click", (event) => { const button = event.target.closest("button[data-client-edit]"); if (button) { event.stopPropagation(); fillClientForm(JSON.parse(button.dataset.clientEdit)); return; } const row = event.target.closest("tr[data-client-detail-name]"); if (row) openClientDetail(row.dataset.clientDetailName); });
  document.querySelector("#client-detail").addEventListener("click", (event) => { if (event.target.closest("[data-client-detail-close]")) { closeClientDetail(); return; } const button = event.target.closest("button[data-client-heatmap-metric]"); if (button && activeClientDetail) { clientHeatmapMetric = button.dataset.clientHeatmapMetric; renderClientDetail(activeClientDetail); } });
  document.querySelector("#refresh-routes").addEventListener("click", refreshRoutes);
  document.querySelector("#new-route").addEventListener("click", () => fillRouteForm());
  document.querySelector("#close-route-modal").addEventListener("click", closeRouteModal);
  document.querySelector("#save-route").addEventListener("click", saveRoute);
  document.querySelector("#delete-route").addEventListener("click", deleteRoute);
  document.querySelector("#routes-table").addEventListener("click", (event) => { const moveButton = event.target.closest("button[data-route-move]"); if (moveButton) { event.stopPropagation(); const fromIndex = Number(moveButton.dataset.routeIndex); reorderRoute(fromIndex, fromIndex + (moveButton.dataset.routeMove === "up" ? -1 : 1)); return; } const button = event.target.closest("button[data-route-edit]"); if (button) { event.stopPropagation(); fillRouteForm(JSON.parse(button.dataset.routeEdit)); return; } const row = event.target.closest("tr[data-route]"); if (row) fillRouteForm(JSON.parse(row.dataset.route)); });
  document.querySelector("#refresh-usage").addEventListener("click", refreshUsage);
  document.querySelector("#usage-range").addEventListener("change", syncUsageRangeInputs);
  document.querySelector("#refresh-live-requests").addEventListener("click", refreshLiveRequests);
  document.querySelector("#refresh-quota").addEventListener("click", refreshQuota);
  document.querySelector("#refresh-latency").addEventListener("click", refreshLatency);
  document.querySelector("#refresh-logs").addEventListener("click", refreshLogs);
  document.querySelector("#config-yaml").addEventListener("input", () => { configDraftDirty = Boolean(document.querySelector("#config-yaml").value); });
  window.addEventListener("beforeunload", (event) => { if (!configDraftDirty) return; event.preventDefault(); event.returnValue = ""; });
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
syncUsageRangeInputs();
if (currentToken()) login();
