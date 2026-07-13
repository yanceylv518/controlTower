const state = {
  token: localStorage.getItem("ct.dashboard.token") || "local-dashboard-token",
  apiBase: localStorage.getItem("ct.api.base") || "",
  view: "overview",
  metrics: [],
  trendMetrics: [],
  channelSnapshots: [],
  selectedChannelID: "",
  selectedCustomerID: "",
  selectedModelID: "",
  alerts: [],
};

const titles = {
  overview: ["\u603b\u89c8", "\u5f53\u524d new-api \u7684\u5b9e\u65f6\u8d28\u91cf\u3001\u541e\u5410\u548c\u8d44\u6e90\u72b6\u6001"],
  alerts: ["\u544a\u8b66\u4e2d\u5fc3", "\u5f53\u524d new-api \u7684\u5f02\u5e38\u72b6\u6001\u548c\u89e6\u53d1\u89c4\u5219"],
  customers: ["\u5ba2\u6237\u76d1\u63a7", "\u6309\u7528\u6237\u7ef4\u5ea6\u67e5\u770b\u8bf7\u6c42\u91cf\u3001\u6210\u529f\u7387\u3001\u9519\u8bef\u7387\u548c\u8017\u65f6"],
  channels: ["\u6e20\u9053\u76d1\u63a7", "\u6309\u6e20\u9053\u7ef4\u5ea6\u67e5\u770b\u8f6c\u53d1\u8d28\u91cf\u548c\u541e\u5410\u8868\u73b0"],
  models: ["\u6a21\u578b\u76d1\u63a7", "\u6309\u6a21\u578b\u7ef4\u5ea6\u67e5\u770b\u8bf7\u6c42\u8d8b\u52bf\u3001\u9519\u8bef\u548c\u5ef6\u8fdf"],
  logs: ["\u6837\u672c\u5206\u6790", "\u67e5\u770b\u5f53\u524d new-api \u7684\u9519\u8bef\u548c\u6162\u8bf7\u6c42\u6837\u672c"],
  runtime: ["\u7cfb\u7edf\u72b6\u6001", "\u67e5\u770b\u670d\u52a1\u5668\u3001\u5065\u5eb7\u68c0\u67e5\u548c Docker \u8fd0\u884c\u72b6\u6001"],
  usage: ["\u7528\u91cf\u7edf\u8ba1", "\u6309\u5ba2\u6237\u3001\u6e20\u9053\u548c\u6a21\u578b\u67e5\u770b\u8bf7\u6c42\u3001Token \u548c Quota \u6392\u884c"],
  settings: ["\u8bbe\u7f6e", "\u672c\u5730\u4fdd\u5b58 Dashboard Token \u548c API Base URL"],
};

const $ = (selector) => document.querySelector(selector);
const $$ = (selector) => Array.from(document.querySelectorAll(selector));

function apiURL(path) {
  const base = state.apiBase.trim().replace(/\/$/, "");
  return `${base}${path}`;
}

async function requestJSON(path) {
  const response = await fetch(apiURL(path), { headers: { Authorization: `Bearer ${state.token}` } });
  if (!response.ok) throw new Error(`HTTP ${response.status}`);
  return response.json();
}

function setConnection(ok, text) {
  const dot = $("#connectionDot");
  if (!dot) return;
  dot.classList.toggle("is-ok", ok === true);
  dot.classList.toggle("is-bad", ok === false);
  $("#connectionText").textContent = text;
}

function setUpdated() {
  $("#lastUpdated").textContent = new Date().toLocaleTimeString();
}

function escapeHTML(value) {
  return String(value ?? "").replace(/[&<>"']/g, (char) => ({
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    '"': "&quot;",
    "'": "&#39;",
  }[char]));
}

function formatNumber(value) {
  if (value === null || value === undefined || Number.isNaN(Number(value))) return "--";
  return Number(value).toLocaleString();
}

function formatPercent(value) {
  if (value === null || value === undefined) return "--";
  return `${(Number(value) * 100).toFixed(1)}%`;
}

function formatMetricPercent(value) {
  if (value === null || value === undefined) return "--";
  return `${Number(value).toFixed(1)}%`;
}

function formatSeconds(value) {
  if (value === null || value === undefined) return "--";
  return `${Number(value).toFixed(2)}s`;
}

function formatTime(value) {
  if (!value) return "--";
  return new Date(value).toLocaleString();
}

function formatDuration(seconds) {
  const value = Number(seconds || 0);
  if (value < 60) return `${Math.max(0, Math.floor(value))}s`;
  if (value < 3600) return `${Math.floor(value / 60)}m ${Math.floor(value % 60)}s`;
  return `${Math.floor(value / 3600)}h ${Math.floor((value % 3600) / 60)}m`;
}

function badge(text, ok) {
  const cls = ok === true ? "ok" : ok === false ? "bad" : "";
  return `<span class="badge ${cls}">${escapeHTML(text)}</span>`;
}

function tableEmpty(colspan, text = "\u6682\u65e0\u6570\u636e") {
  return `<tr><td colspan="${colspan}" class="empty">${escapeHTML(text)}</td></tr>`;
}

function metricWindow() {
  return $("#metricWindow")?.value || "1m";
}

function metricFilterMatches(item) {
  const model = ($("#globalModel")?.value || "").trim().toLowerCase();
  if (!model) return true;
  const target = `${item.display_key || ""} ${item.dimension_key || ""}`.toLowerCase();
  return target.includes(model);
}

function latestByKey(items) {
  const latest = new Map();
  for (const item of items) {
    const key = `${item.dimension_type}:${item.dimension_key}`;
    const current = latest.get(key);
    if (!current || new Date(item.bucket_time) > new Date(current.bucket_time)) latest.set(key, item);
  }
  return Array.from(latest.values()).sort((a, b) => (b.request_count || 0) - (a.request_count || 0));
}

function dimensionItems(type) {
  return latestByKey(state.metrics.filter((item) => item.dimension_type === type && metricFilterMatches(item)));
}

async function loadMetrics() {
  const response = await requestJSON(`/api/dashboard/metrics?window=${encodeURIComponent(metricWindow())}&latest=true`);
  state.metrics = response.items || [];
  const trendWindow = $("#trendWindow");
  if (trendWindow) trendWindow.textContent = metricWindow();
  return state.metrics;
}

async function loadTrendMetrics() {
  const instance = state.metrics.find((item) => item.dimension_type === "instance");
  if (!instance) { state.trendMetrics = []; return; }
  const response = await requestJSON(`/api/dashboard/metric-history?dimension_type=instance&dimension_key=${encodeURIComponent(instance.dimension_key)}&window=${encodeURIComponent(metricWindow())}&hours=1`);
  state.trendMetrics = response.items || [];
}

async function loadChannelSnapshots() {
  const response = await requestJSON("/api/dashboard/channel-snapshots?latest_only=true&limit=200");
  state.channelSnapshots = response.items || [];
  return state.channelSnapshots;
}

function channelSnapshotMap() {
  const items = new Map();
  for (const item of state.channelSnapshots || []) items.set(String(item.channel_id), item);
  return items;
}

function channelIDFromMetric(item) {
  const parts = String(item.dimension_key || "").split(":");
  return parts.length >= 3 ? parts[2] : "";
}

function channelSnapshotForMetric(item) {
  return channelSnapshotMap().get(String(channelIDFromMetric(item)));
}

function channelDisplayName(item) {
  const id = channelIDFromMetric(item);
  const snapshot = channelSnapshotForMetric(item);
  if (snapshot?.channel_name) return `${snapshot.channel_name} (#${id})`;
  return item.display_key || (id ? `\u6e20\u9053 ${id}` : "--");
}

function channelStatusBadge(status) {
  const value = status || "unknown";
  const ok = value === "enabled" ? true : value === "disabled" || value === "auto_disabled" ? false : undefined;
  const text = ({ enabled: "\u542f\u7528", disabled: "\u505c\u7528", auto_disabled: "\u81ea\u52a8\u505c\u7528", unknown: "\u672a\u77e5" })[value] || value;
  return badge(text, ok);
}

function modelList(modelsText) {
  return String(modelsText || "").split(",").map((item) => item.trim()).filter(Boolean);
}

function modelChips(modelsText) {
  const models = modelList(modelsText);
  if (!models.length) return `<span class="muted-text">--</span>`;
  const chips = models.slice(0, 4).map((model) => `<span class="model-chip">${escapeHTML(model)}</span>`);
  if (models.length > 4) chips.push(`<span class="model-chip more">+${models.length - 4}</span>`);
  return `<div class="model-chip-row">${chips.join("")}</div>`;
}

function miniMetric(label, value, subtext, tone = "") {
  return `<div class="mini-metric ${tone}"><span>${escapeHTML(label)}</span><strong>${escapeHTML(value)}</strong>${subtext ? `<small>${escapeHTML(subtext)}</small>` : ""}</div>`;
}

function rateBar(value, kind) {
  const numeric = Math.max(0, Math.min(1, Number(value || 0)));
  return `<div class="rate-bar ${kind}"><span style="width:${(numeric * 100).toFixed(1)}%"></span></div>`;
}

function channelHealthClass(item, snapshot) {
  if (snapshot?.status === "disabled" || snapshot?.status === "auto_disabled") return "is-disabled";
  if ((item.error_rate || 0) >= 0.1) return "is-risk";
  if ((item.success_rate || 0) >= 0.98) return "is-healthy";
  return "";
}

async function loadOverview() {
  const [overview] = await Promise.all([
    requestJSON("/api/dashboard/overview"),
    loadMetrics(),
    loadChannelSnapshots(),
    loadAlerts(),
  ]);
  await loadTrendMetrics();
  renderKPIs(overview.recent_1m || {});
  renderRuntimeSummary(overview.runtime || {});
  renderAlertLists();
  renderTrend();
  renderOverviewDimensions();
}

function renderKPIs(summary) {
  $("#successRate").textContent = formatPercent(summary.success_rate);
  $("#requestCount").textContent = `${formatNumber(summary.request_count || 0)} \u8bf7\u6c42`;
  $("#tpm").textContent = formatNumber(summary.tpm || 0);
  $("#errorRate").textContent = formatPercent(summary.error_rate);
  $("#errorCount").textContent = `${formatNumber(summary.error_count || 0)} \u9519\u8bef`;
  $("#avgUseTime").textContent = formatSeconds(summary.avg_use_time);
  $("#p95UseTime").textContent = `P95 ${formatSeconds(summary.p95_use_time)}`;
}

function renderRuntimeSummary(runtime) {
  const health = runtime.health || {};
  const docker = runtime.docker || {};
  $("#healthCounts").textContent = `${health.up_count || 0}/${(health.up_count || 0) + (health.down_count || 0)}`;
  $("#dockerCounts").textContent = `${docker.running_count || 0}/${(docker.running_count || 0) + (docker.stopped_count || 0)}`;
  const metric = (runtime.latest_server_metrics || [])[0];
  $("#serverMetricTime").textContent = formatTime(metric?.collected_at);
  if (!metric) {
    $("#serverSummary").innerHTML = `<div class="empty">\u6682\u65e0\u670d\u52a1\u5668\u6307\u6807</div>`;
    return;
  }
  $("#serverSummary").innerHTML = [
    ["CPU", formatMetricPercent(metric.cpu_percent)],
    ["\u5185\u5b58", formatMetricPercent(metric.memory_used_percent)],
    ["\u78c1\u76d8", formatMetricPercent(metric.disk_used_percent)],
    ["Load 1m", Number(metric.load_1m || 0).toFixed(2)],
    ["\u7f51\u7edc", `${formatNumber(metric.network_rx_bytes_per_second || 0)} RX / ${formatNumber(metric.network_tx_bytes_per_second || 0)} TX`],
  ].map(([label, value]) => `<div class="server-row"><span>${label}</span><strong>${escapeHTML(value)}</strong></div>`).join("");
}

function renderTrend() {
  const canvas = $("#trafficChart");
  if (!canvas) return;
  const ctx = canvas.getContext("2d");
  const rect = canvas.getBoundingClientRect();
  const dpr = window.devicePixelRatio || 1;
  canvas.width = Math.max(320, Math.floor(rect.width * dpr));
  canvas.height = Math.floor(Number(canvas.getAttribute("height")) * dpr);
  ctx.scale(dpr, dpr);
  const width = canvas.width / dpr;
  const height = canvas.height / dpr;
  ctx.clearRect(0, 0, width, height);
  ctx.fillStyle = "#ffffff";
  ctx.fillRect(0, 0, width, height);
  const points = state.trendMetrics;
  const padding = { left: 38, right: 14, top: 34, bottom: 28 };
  ctx.strokeStyle = "#d7dee9";
  ctx.lineWidth = 1;
  for (let i = 0; i < 4; i += 1) {
    const y = padding.top + ((height - padding.top - padding.bottom) / 3) * i;
    ctx.beginPath(); ctx.moveTo(padding.left, y); ctx.lineTo(width - padding.right, y); ctx.stroke();
  }
  if (!points.length) {
    ctx.fillStyle = "#66748a"; ctx.font = "13px sans-serif"; ctx.fillText("\u6682\u65e0\u8d8b\u52bf\u6570\u636e", padding.left, height / 2); return;
  }
  const values = points.flatMap((item) => [item.request_count || 0, item.error_count || 0]);
  const max = Math.max(1, ...values);
  const plotWidth = width - padding.left - padding.right;
  const plotHeight = height - padding.top - padding.bottom;
  const drawLine = (field, color) => {
    ctx.strokeStyle = color; ctx.lineWidth = 2; ctx.beginPath();
    points.forEach((item, index) => { const x = padding.left + (points.length === 1 ? plotWidth / 2 : (plotWidth / (points.length - 1)) * index); const y = padding.top + plotHeight - ((item[field] || 0) / max) * plotHeight; if (index === 0) ctx.moveTo(x, y); else ctx.lineTo(x, y); });
    ctx.stroke();
  };
  drawLine("request_count", "#1769e0"); drawLine("error_count", "#d64545");
  ctx.font = "12px sans-serif"; ctx.fillStyle = "#1769e0"; ctx.fillRect(padding.left, 10, 10, 3); ctx.fillStyle = "#334155"; ctx.fillText("\u8bf7\u6c42", padding.left + 15, 15); ctx.fillStyle = "#d64545"; ctx.fillRect(padding.left + 60, 10, 10, 3); ctx.fillStyle = "#334155"; ctx.fillText("\u9519\u8bef", padding.left + 75, 15);
  ctx.fillStyle = "#66748a"; ctx.textAlign = "center";
  [0, Math.floor((points.length - 1) / 2), points.length - 1].forEach((index) => { const x = padding.left + (points.length === 1 ? plotWidth / 2 : (plotWidth / (points.length - 1)) * index); ctx.fillText(new Date(points[index].bucket_time).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }), x, height - 8); });
  ctx.textAlign = "start"; ctx.fillText(`max ${max}`, 6, padding.top + 4);
}

function renderOverviewDimensions() {
  renderModelRows($("#overviewModelBody"), dimensionItems("instance_model").slice(0, 5), true);
  renderChannelRows($("#overviewChannelBody"), dimensionItems("instance_channel").slice(0, 5), true);
  renderCustomerRows($("#overviewCustomerBody"), dimensionItems("instance_user").slice(0, 5), true);
}

function alertQueryString() {
  const params = new URLSearchParams();
  const status = $("#alertStatusFilter")?.value || "";
  const severity = $("#alertSeverityFilter")?.value || "";
  const active = $("#alertActiveFilter")?.value || "";
  if (status) params.set("status", status);
  if (severity) params.set("severity", severity);
  if (active) params.set("active_only", active);
  params.set("limit", "100");
  const query = params.toString();
  return query ? `?${query}` : "";
}

async function loadAlerts() {
  const response = await requestJSON(`/api/dashboard/alerts${alertQueryString()}`);
  state.alerts = response.items || [];
  return state.alerts;
}

function activeAlerts() {
  return state.alerts.filter((item) => item.status !== "resolved");
}

function renderAlertLists() {
  renderAlertList($("#overviewAlerts"), activeAlerts().slice(0, 3), true);
  renderAlertList($("#alertsBody"), state.alerts, false);
  const counter = $("#alertCount");
  if (counter) counter.textContent = `${activeAlerts().length} \u6d3b\u8dc3 / ${state.alerts.length} \u603b\u8ba1`;
}

function alertStatusText(status) {
  return ({ firing: "\u89e6\u53d1\u4e2d", acknowledged: "\u5df2\u786e\u8ba4", silenced: "\u9759\u9ed8\u4e2d", resolved: "\u5df2\u6062\u590d" })[status] || status || "--";
}

function alertExtraTime(item) {
  if (item.status === "resolved" && item.resolved_at) return ` \u00b7 \u6062\u590d ${formatTime(item.resolved_at)}`;
  if (item.status === "silenced" && item.silence_until) return ` \u00b7 \u9759\u9ed8\u5230 ${formatTime(item.silence_until)}`;
  return "";
}

function alertActions(item) {
  if (item.status === "resolved") return "";
  return `<div class="alert-actions"><button type="button" data-alert-action="acknowledge" data-alert-id="${escapeHTML(item.id)}">\u786e\u8ba4</button><button type="button" data-alert-action="silence" data-alert-id="${escapeHTML(item.id)}">\u9759\u9ed8 1 \u5c0f\u65f6</button></div>`;
}

function renderAlertList(element, alerts, compact) {
  if (!element) return;
  if (!alerts.length) { element.innerHTML = `<div class="empty">\u5f53\u524d\u6ca1\u6709\u544a\u8b66</div>`; return; }
  element.innerHTML = alerts.map((item) => `<div class="alert-item ${escapeHTML(item.severity)}"><div class="alert-title"><span>${escapeHTML(item.title)}</span><span>${badge(item.severity, item.severity !== "critical")} ${badge(alertStatusText(item.status), item.status !== "firing")}</span></div><div class="alert-summary">${escapeHTML(item.summary)}</div>${compact ? "" : `<div class="alert-meta">${escapeHTML(item.instance_id)} \u00b7 ${escapeHTML(item.rule_key)} \u00b7 \u9996\u6b21 ${formatTime(item.first_seen_at)} \u00b7 \u6700\u8fd1 ${formatTime(item.last_seen_at)}${alertExtraTime(item)}</div>${alertActions(item)}`}</div>`).join("");
}

function renderModelRows(body, rows, compact = false) {
  if (!rows.length) { body.innerHTML = tableEmpty(compact ? 5 : 6); return; }
  body.innerHTML = rows.map((item) => compact
    ? `<tr><td>${escapeHTML(item.display_key)}</td><td>${formatNumber(item.request_count)}</td><td>${formatPercent(item.success_rate)}</td><td>${formatNumber(item.tpm)}</td><td>${formatSeconds(item.p95_use_time)}</td></tr>`
    : `<tr><td>${escapeHTML(item.display_key)}</td><td>${formatNumber(item.request_count)}</td><td>${formatPercent(item.success_rate)}</td><td>${formatPercent(item.error_rate)}</td><td>${formatNumber(item.tpm)}</td><td>${formatSeconds(item.p95_use_time)}</td></tr>`).join("");
}

function renderChannelRows(body, rows, compact = false) {
  if (!compact) { renderChannelWorkspace(rows); return; }
  if (!rows.length) { body.innerHTML = tableEmpty(5); return; }
  body.innerHTML = rows.map((item) => {
    const snapshot = channelSnapshotForMetric(item);
    const name = channelDisplayName(item);
    const healthClass = channelHealthClass(item, snapshot);
    return `<tr class="channel-row ${healthClass}"><td><div class="channel-name"><strong>${escapeHTML(name)}</strong><span>${channelStatusBadge(snapshot?.status)} ${snapshot ? `<span class="weight-pill">W ${formatNumber(snapshot.weight)}</span>` : ""}</span></div></td><td>${formatNumber(item.request_count)}</td><td>${formatPercent(item.error_rate)}${rateBar(item.error_rate, "bad")}</td><td>${formatNumber(item.tpm)}</td><td>${formatSeconds(item.p95_use_time)}</td></tr>`;
  }).join("");
}

function renderChannelWorkspace(rows) {
  const list = $("#channelList");
  const detail = $("#channelDetail");
  const count = $("#channelListCount");
  if (!list || !detail) return;
  if (count) count.textContent = `${rows.length} \u4e2a`;
  if (!rows.length) {
    list.innerHTML = `<div class="empty">\u6682\u65e0\u6e20\u9053\u6307\u6807</div>`;
    detail.innerHTML = `<div class="empty">\u9009\u62e9\u5de6\u4fa7\u6e20\u9053\u67e5\u770b\u8be6\u60c5</div>`;
    return;
  }
  const firstID = channelIDFromMetric(rows[0]);
  if (!state.selectedChannelID || !rows.some((item) => channelIDFromMetric(item) === state.selectedChannelID)) {
    state.selectedChannelID = firstID;
  }
  list.innerHTML = rows.map((item) => renderChannelListItem(item)).join("");
  const selected = rows.find((item) => channelIDFromMetric(item) === state.selectedChannelID) || rows[0];
  detail.innerHTML = renderChannelDetail(selected);
}

function renderChannelListItem(item) {
  const id = channelIDFromMetric(item);
  const snapshot = channelSnapshotForMetric(item);
  const name = channelDisplayName(item);
  const selected = id === state.selectedChannelID ? "is-selected" : "";
  const healthClass = channelHealthClass(item, snapshot);
  return `<button type="button" class="channel-list-item ${selected} ${healthClass}" data-channel-select="${escapeHTML(id)}">
    <span class="channel-list-top"><strong>${escapeHTML(name)}</strong>${channelStatusBadge(snapshot?.status)}</span>
    <span class="channel-list-meta">ID ${escapeHTML(id || "--")} \u00b7 W ${formatNumber(snapshot?.weight)} \u00b7 ${formatNumber(item.request_count)} \u8bf7\u6c42</span>
    <span class="channel-list-bottom"><span>${formatPercent(item.success_rate)} \u6210\u529f</span><span>${formatSeconds(item.p95_use_time)} P95</span></span>
    ${rateBar(item.error_rate, "bad")}
  </button>`;
}

function renderChannelDetail(item) {
  const id = channelIDFromMetric(item);
  const snapshot = channelSnapshotForMetric(item);
  const name = channelDisplayName(item);
  const tokenTotal = (item.prompt_tokens || 0) + (item.completion_tokens || 0);
  const healthClass = channelHealthClass(item, snapshot);
  return `<div class="channel-detail-head ${healthClass}">
    <div><span class="detail-kicker">\u6e20\u9053 #${escapeHTML(id || "--")}</span><h3>${escapeHTML(name)}</h3><p>${formatTime(snapshot?.captured_at)} \u5feb\u7167</p></div>
    <div class="detail-badges">${channelStatusBadge(snapshot?.status)}<span class="weight-pill">W ${formatNumber(snapshot?.weight)}</span></div>
  </div>
  <div class="channel-detail-metrics">
    ${miniMetric("\u8bf7\u6c42", formatNumber(item.request_count), `${formatNumber(item.tpm)} TPM`)}
    ${miniMetric("\u6210\u529f\u7387", formatPercent(item.success_rate), "", "ok")}
    ${miniMetric("\u9519\u8bef\u7387", formatPercent(item.error_rate), "", (item.error_rate || 0) > 0 ? "bad" : "")}
    ${miniMetric("P95", formatSeconds(item.p95_use_time), `P50 ${formatSeconds(item.p50_use_time)} \u00b7 P99 ${formatSeconds(item.p99_use_time)}`)}
    ${miniMetric("Token", formatNumber(tokenTotal), `${formatNumber(item.quota || 0)} quota`)}
  </div>
  <div class="channel-detail-section"><div class="section-heading compact"><h3>\u6a21\u578b\u8986\u76d6</h3><span>${modelList(snapshot?.models_text).length} models</span></div>${modelChips(snapshot?.models_text)}</div>
  <div class="channel-detail-section"><div class="section-heading compact"><h3>\u8d28\u91cf\u4fe1\u53f7</h3></div><div class="signal-grid"><div><span>\u6210\u529f\u7387</span>${rateBar(item.success_rate, "ok")}</div><div><span>\u9519\u8bef\u7387</span>${rateBar(item.error_rate, "bad")}</div><div><span>\u6d41\u5f0f\u5360\u6bd4</span>${rateBar(item.stream_rate, "ok")}</div><div><span>Cache token</span>${rateBar(item.cache_token_rate, "ok")}</div></div></div>`;
}

function dimensionEntityID(view, item) {
  const parts = String(item.dimension_key || "").split(":");
  if (view === "customers") return parts.length >= 3 ? parts[2] : item.display_key || "";
  if (view === "models") return parts.length >= 3 ? parts.slice(2).join(":") : item.display_key || "";
  return "";
}

function selectedDimensionID(view) {
  return view === "customers" ? state.selectedCustomerID : state.selectedModelID;
}

function setSelectedDimensionID(view, value) {
  if (view === "customers") state.selectedCustomerID = value;
  if (view === "models") state.selectedModelID = value;
}

function dimensionElements(view) {
  const prefix = view === "customers" ? "customer" : "model";
  return { list: $("#" + prefix + "List"), detail: $("#" + prefix + "Detail"), count: $("#" + prefix + "ListCount") };
}

function renderDimensionWorkspace(view, rows) {
  const elements = dimensionElements(view);
  if (!elements.list || !elements.detail) return;
  if (elements.count) elements.count.textContent = rows.length + " \u4e2a";
  if (!rows.length) {
    elements.list.innerHTML = '<div class="empty">\u6682\u65e0\u6307\u6807</div>';
    elements.detail.innerHTML = '<div class="empty">\u9009\u62e9\u5de6\u4fa7\u9879\u76ee\u67e5\u770b\u8be6\u60c5</div>';
    return;
  }
  let selectedID = selectedDimensionID(view);
  if (!selectedID || !rows.some((item) => dimensionEntityID(view, item) === selectedID)) {
    selectedID = dimensionEntityID(view, rows[0]);
    setSelectedDimensionID(view, selectedID);
  }
  elements.list.innerHTML = rows.map((item) => renderDimensionListItem(view, item, selectedID)).join("");
  const selected = rows.find((item) => dimensionEntityID(view, item) === selectedID) || rows[0];
  elements.detail.innerHTML = renderDimensionDetail(view, selected);
}

function renderDimensionListItem(view, item, selectedID) {
  const id = dimensionEntityID(view, item);
  const label = item.display_key || id || "--";
  const selected = id === selectedID ? "is-selected" : "";
  const risk = (item.error_rate || 0) >= 0.1 ? "is-risk" : (item.success_rate || 0) >= 0.98 ? "is-healthy" : "";
  return '<button type="button" class="dimension-list-item ' + selected + " " + risk + '" data-dimension-view="' + escapeHTML(view) + '" data-dimension-select="' + escapeHTML(id) + '">' +
    '<span class="dimension-list-top"><strong>' + escapeHTML(label) + "</strong>" + badge(formatPercent(item.success_rate), (item.success_rate || 0) >= 0.98) + "</span>" +
    '<span class="dimension-list-meta">' + formatNumber(item.request_count) + " \u8bf7\u6c42 \u00b7 " + formatNumber(item.tpm) + " TPM</span>" +
    '<span class="dimension-list-bottom"><span>' + formatPercent(item.error_rate) + " \u9519\u8bef</span><span>" + formatSeconds(item.p95_use_time) + " P95</span></span>" +
    rateBar(item.error_rate, "bad") + "</button>";
}

function renderDimensionDetail(view, item) {
  const id = dimensionEntityID(view, item);
  const label = item.display_key || id || "--";
  const kind = view === "customers" ? "\u5ba2\u6237" : "\u6a21\u578b";
  const tokenTotal = (item.prompt_tokens || 0) + (item.completion_tokens || 0);
  const risk = (item.error_rate || 0) >= 0.1 ? "is-risk" : (item.success_rate || 0) >= 0.98 ? "is-healthy" : "";
  return '<div class="dimension-detail-head ' + risk + '"><div><span class="detail-kicker">' + kind + "</span><h3>" + escapeHTML(label) + "</h3><p>" + formatTime(item.bucket_time) + " \u6307\u6807\u6876</p></div><div class=\"detail-badges\">" + badge(formatPercent(item.success_rate), (item.success_rate || 0) >= 0.98) + "</div></div>" +
    '<div class="dimension-detail-metrics">' +
    miniMetric("\u8bf7\u6c42", formatNumber(item.request_count), formatNumber(item.tpm) + " TPM") +
    miniMetric("\u6210\u529f\u7387", formatPercent(item.success_rate), "", "ok") +
    miniMetric("\u9519\u8bef\u7387", formatPercent(item.error_rate), formatNumber(item.error_count) + " \u9519\u8bef", (item.error_rate || 0) > 0 ? "bad" : "") +
    miniMetric("P95", formatSeconds(item.p95_use_time), "P50 " + formatSeconds(item.p50_use_time) + " \u00b7 P99 " + formatSeconds(item.p99_use_time)) +
    miniMetric("Token", formatNumber(tokenTotal), formatNumber(item.prompt_tokens) + " in / " + formatNumber(item.completion_tokens) + " out") + "</div>" +
    '<div class="dimension-detail-section"><div class="section-heading compact"><h3>\u8d28\u91cf\u4fe1\u53f7</h3></div><div class="signal-grid"><div><span>\u6210\u529f\u7387</span>' + rateBar(item.success_rate, "ok") + "</div><div><span>\u9519\u8bef\u7387</span>" + rateBar(item.error_rate, "bad") + "</div><div><span>\u6d41\u5f0f\u5360\u6bd4</span>" + rateBar(item.stream_rate, "ok") + "</div><div><span>Cache token</span>" + rateBar(item.cache_token_rate, "ok") + "</div></div></div>" +
    '<div class="dimension-detail-section"><div class="section-heading compact"><h3>\u7528\u91cf</h3></div><div class="detail-usage-row"><span>Quota</span><strong>' + formatNumber(item.quota || 0) + "</strong><span>Dimension ID</span><strong>" + escapeHTML(id) + "</strong></div></div>";
}

function renderCustomerRows(body, rows, compact = false) {
  if (!rows.length) { body.innerHTML = tableEmpty(compact ? 5 : 6); return; }
  body.innerHTML = rows.map((item) => compact
    ? `<tr><td>${escapeHTML(item.display_key)}</td><td>${formatNumber(item.request_count)}</td><td>${formatPercent(item.success_rate)}</td><td>${formatNumber((item.prompt_tokens || 0) + (item.completion_tokens || 0))}</td><td>${formatSeconds(item.avg_use_time)}</td></tr>`
    : `<tr><td>${escapeHTML(item.display_key)}</td><td>${formatNumber(item.request_count)}</td><td>${formatPercent(item.success_rate)}</td><td>${formatPercent(item.error_rate)}</td><td>${formatNumber(item.tpm)}</td><td>${formatSeconds(item.avg_use_time)}</td></tr>`).join("");
}

async function loadDimensionView(view) {
  if (!state.metrics.length) await loadMetrics();
  if (view === "customers") renderDimensionWorkspace(view, dimensionItems("instance_user").slice(0, 50));
  if (view === "channels") { await loadChannelSnapshots(); renderChannelWorkspace(dimensionItems("instance_channel").slice(0, 50)); }
  if (view === "models") renderDimensionWorkspace(view, dimensionItems("instance_model").slice(0, 50));
}

async function loadLogs() {
  const form = $("#logFilterForm");
  const params = new URLSearchParams(new FormData(form));
  const model = ($("#globalModel")?.value || "").trim();
  const status = ($("#globalStatus")?.value || "").trim();
  if (model && !params.get("model_name")) params.set("model_name", model);
  if (status) params.set("log_type", status);
  params.set("limit", "100");
  const data = await requestJSON("/api/dashboard/log-samples?" + params.toString());
  const rows = data.items || [];
  $("#logsBody").innerHTML = rows.length ? rows.map((item) => '<tr><td>' + formatTime(item.created_at) + "</td><td>" + badge(item.sample_kind === "slow" ? "\u6162\u8bf7\u6c42" : "\u9519\u8bef", item.sample_kind === "slow" ? undefined : false) + "</td><td>" + badge(item.log_type || "--", item.log_type === "consume" ? true : item.log_type === "error" ? false : undefined) + "</td><td>" + escapeHTML(item.model_name || "--") + "</td><td>" + escapeHTML(item.username || item.user_id || "--") + "</td><td>" + formatNumber(item.total_tokens || 0) + "</td><td>" + formatSeconds(item.use_time) + "</td><td><strong>" + escapeHTML(item.request_id || "--") + '</strong><div class="muted-text">' + escapeHTML(item.error_summary || item.upstream_request_id || "--") + "</div></td></tr>").join("") : tableEmpty(8);
}
async function sendAlertAction(id, action) {
  const response = await fetch(apiURL("/api/dashboard/alerts/action"), { method: "POST", headers: { Authorization: `Bearer ${state.token}`, "Content-Type": "application/json" }, body: JSON.stringify({ id, action, silence_minutes: 60 }) });
  if (!response.ok) throw new Error(`HTTP ${response.status}`);
  await response.json();
  await loadAlerts(); renderAlertLists(); setUpdated();
}

async function loadRuntime() {
  const [agents, metrics, health, docker] = await Promise.all([
    requestJSON("/api/dashboard/agents?limit=20"),
    requestJSON("/api/dashboard/server-metrics?limit=30"),
    requestJSON("/api/dashboard/health-checks?limit=30"),
    requestJSON("/api/dashboard/docker-statuses?limit=30"),
  ]);
  renderRuntimeTables(agents.items || [], metrics.items || [], health.items || [], docker.items || []);
}

function renderRuntimeTables(agents, metrics, health, docker) {
  $("#agentsBody").innerHTML = agents.length ? agents.map((item) => `<tr><td><strong>${escapeHTML(item.id || "--")}</strong><div class="muted-text">${escapeHTML(item.version || "--")}</div></td><td>${escapeHTML(item.instance_id || "--")}</td><td>${badge(item.online ? "online" : "offline", item.online)} ${badge(item.status || "--", item.status === "ok" ? true : undefined)}</td><td>${formatTime(item.last_seen_at)}<div class="muted-text">${formatDuration(item.seconds_since_seen)} ago</div></td><td>${formatNumber(item.last_log_id || 0)}<div class="muted-text">source ${formatNumber(item.source_latest_log_id || 0)} · seq ${formatNumber(item.last_sequence || 0)}</div></td><td>${badge(formatNumber(item.backlog_estimate || 0), (item.backlog_estimate || 0) < 3000 ? true : false)}</td><td>${formatNumber(item.report_delay_ms || 0)}ms</td></tr>`).join("") : tableEmpty(7);
  $("#metricsBody").innerHTML = metrics.length ? metrics.map((item) => `<tr><td>${formatTime(item.collected_at)}</td><td>${formatMetricPercent(item.cpu_percent)}</td><td>${formatMetricPercent(item.memory_used_percent)}</td><td>${formatMetricPercent(item.disk_used_percent)}</td><td>${Number(item.load_1m || 0).toFixed(2)}</td><td>${formatNumber(item.network_rx_bytes_per_second)} / ${formatNumber(item.network_tx_bytes_per_second)}</td></tr>`).join("") : tableEmpty(6);
  $("#healthBody").innerHTML = health.length ? health.map((item) => `<tr><td>${formatTime(item.checked_at)}</td><td>${escapeHTML(item.target || "--")}</td><td>${badge(item.status || "--", item.status === "up" ? true : item.status === "down" ? false : undefined)}</td><td>${item.http_status_code || "--"}</td><td>${formatNumber(item.latency_ms || 0)}ms</td></tr>`).join("") : tableEmpty(5);
  $("#dockerBody").innerHTML = docker.length ? docker.map((item) => `<tr><td>${formatTime(item.collected_at)}</td><td>${escapeHTML(item.container_name || "--")}</td><td>${badge(item.running ? "running" : "stopped", item.running)}</td><td>${escapeHTML(item.status || "--")}</td></tr>`).join("") : tableEmpty(4);
}

async function loadUsage() {
  const hours = $("#usageHours")?.value || "24";
  const response = await requestJSON(`/api/dashboard/usage?hours=${encodeURIComponent(hours)}`);
  const rows = response.items || [];
  renderUsageTable($("#usageCustomerBody"), rows.filter((item) => item.dimension_type === "instance_user").slice(0, 20));
  renderUsageTable($("#usageChannelBody"), rows.filter((item) => item.dimension_type === "instance_channel").slice(0, 20));
  renderUsageTable($("#usageModelBody"), rows.filter((item) => item.dimension_type === "instance_model").slice(0, 20));
}

function renderUsageTable(body, rows) {
  if (!body) return;
  body.innerHTML = rows.length ? rows.map((item) => `<tr><td>${escapeHTML(item.display_key)}</td><td>${formatNumber(item.request_count)}</td><td>${formatNumber(item.prompt_tokens)} / ${formatNumber(item.completion_tokens)}</td><td>${formatNumber(item.quota)}</td></tr>`).join("") : tableEmpty(4);
}

function showSettings() {
  $("#tokenInput").value = state.token;
  $("#apiBaseInput").value = state.apiBase;
}

async function loadNotificationSettings() {
  showSettings();
  const [channels, deliveries] = await Promise.all([
    requestJSON("/api/dashboard/notification-channels"),
    requestJSON("/api/dashboard/notification-deliveries?limit=20"),
  ]);
  renderNotificationChannels(channels.items || []);
  renderNotificationDeliveries(deliveries.items || []);
}

function renderNotificationChannels(rows) {
  const body = $("#notificationChannelBody");
  if (!body) return;
  body.innerHTML = rows.length ? rows.map((item) => `<tr><td>${escapeHTML(item.name)}</td><td>${escapeHTML(item.channel_type)}</td><td>${escapeHTML(item.webhook_url_masked || "--")}</td><td>${badge(item.enabled ? "\u542f\u7528" : "\u505c\u7528", item.enabled)}</td><td>${formatTime(item.updated_at)}</td></tr>`).join("") : tableEmpty(5);
}

function renderNotificationDeliveries(rows) {
  const body = $("#notificationDeliveryBody");
  if (!body) return;
  body.innerHTML = rows.length ? rows.map((item) => `<tr><td>${formatTime(item.attempted_at)}</td><td>${badge(item.status, item.status === "sent" ? true : item.status === "failed" ? false : undefined)}</td><td>${item.status_code || "--"}</td><td>${formatNumber(item.attempts || 0)}</td><td>${formatTime(item.next_attempt_at)}</td><td>${escapeHTML(item.alert_id || "--")}</td><td>${escapeHTML(item.error_summary || "--")}</td></tr>`).join("") : tableEmpty(7);
}

async function saveWebhookChannel(form) {
  const data = new FormData(form);
  const payload = { name: String(data.get("name") || "").trim(), channel_type: String(data.get("channel_type") || "webhook"), webhook_url: String(data.get("webhook_url") || "").trim(), enabled: String(data.get("enabled") || "true") === "true" };
  const response = await fetch(apiURL("/api/dashboard/notification-channels"), { method: "POST", headers: { Authorization: `Bearer ${state.token}`, "Content-Type": "application/json" }, body: JSON.stringify(payload) });
  if (!response.ok) throw new Error(`HTTP ${response.status}`);
  form.reset();
  await loadNotificationSettings();
}

async function refreshCurrentView() {
  try {
    setConnection(undefined, "\u8fde\u63a5\u4e2d");
    if (state.view === "overview") await loadOverview();
    if (state.view === "alerts") { await loadAlerts(); renderAlertLists(); }
    if (["customers", "channels", "models"].includes(state.view)) await loadDimensionView(state.view);
    if (state.view === "logs") await loadLogs();
    if (state.view === "runtime") await loadRuntime();
    if (state.view === "usage") await loadUsage();
    if (state.view === "settings") await loadNotificationSettings();
    setConnection(true, "\u5df2\u8fde\u63a5");
    setUpdated();
  } catch (error) {
    setConnection(false, error.message);
  }
}

function switchView(view) {
  state.view = view;
  $$(".nav-tab").forEach((button) => button.classList.toggle("is-active", button.dataset.view === view));
  $$(".view").forEach((section) => section.classList.toggle("is-active", section.id === `${view}View`));
  const [title, subtitle] = titles[view] || titles.overview;
  $("#viewTitle").textContent = title;
  $("#viewSubtitle").textContent = subtitle;
  refreshCurrentView();
}

function bindEvents() {
  $$(".nav-tab").forEach((button) => button.addEventListener("click", () => switchView(button.dataset.view)));
  $$("[data-view-link]").forEach((button) => button.addEventListener("click", () => switchView(button.dataset.viewLink)));
  $("#refreshButton").addEventListener("click", refreshCurrentView);
  $("#metricWindow").addEventListener("change", async () => { state.metrics = []; await refreshCurrentView(); });
  $("#usageHours").addEventListener("change", refreshCurrentView);
  $("#globalModel").addEventListener("input", () => { if (["overview", "customers", "channels", "models"].includes(state.view)) { renderOverviewDimensions(); if (state.view !== "overview") loadDimensionView(state.view); } });
  $("#globalStatus").addEventListener("change", () => { if (state.view === "logs") refreshCurrentView(); });
  $("#webhookForm").addEventListener("submit", (event) => { event.preventDefault(); saveWebhookChannel(event.currentTarget).then(() => { setConnection(true, "\u5df2\u8fde\u63a5"); setUpdated(); }).catch((error) => setConnection(false, error.message)); });
  $("#applyAlertFilterButton").addEventListener("click", () => { loadAlerts().then(() => { renderAlertLists(); setUpdated(); }).catch((error) => setConnection(false, error.message)); });
  $("#logFilterForm").addEventListener("submit", (event) => { event.preventDefault(); loadLogs().then(() => { setConnection(true, "\u5df2\u8fde\u63a5"); setUpdated(); }).catch((error) => setConnection(false, error.message)); });
  $("#saveSettingsButton").addEventListener("click", (event) => { event.preventDefault(); state.token = $("#tokenInput").value.trim(); state.apiBase = $("#apiBaseInput").value.trim(); localStorage.setItem("ct.dashboard.token", state.token); localStorage.setItem("ct.api.base", state.apiBase); refreshCurrentView(); });
  $("#clearSettingsButton").addEventListener("click", (event) => { event.preventDefault(); state.token = "local-dashboard-token"; state.apiBase = ""; localStorage.removeItem("ct.dashboard.token"); localStorage.removeItem("ct.api.base"); showSettings(); });
  document.addEventListener("click", (event) => { const dimensionButton = event.target.closest("[data-dimension-select]"); if (dimensionButton) { const view = dimensionButton.dataset.dimensionView; setSelectedDimensionID(view, dimensionButton.dataset.dimensionSelect || ""); loadDimensionView(view); return; } const channelButton = event.target.closest("[data-channel-select]"); if (channelButton) { state.selectedChannelID = channelButton.dataset.channelSelect || ""; renderChannelWorkspace(dimensionItems("instance_channel").slice(0, 50)); return; } const button = event.target.closest("[data-alert-action]"); if (!button) return; sendAlertAction(button.dataset.alertId, button.dataset.alertAction).catch((error) => setConnection(false, error.message)); });
  window.addEventListener("resize", () => { if (state.view === "overview") renderTrend(); });
  document.addEventListener("visibilitychange", () => { if (document.visibilityState === "visible") refreshCurrentView(); });
}

bindEvents();
refreshCurrentView();
setInterval(() => { if (document.visibilityState === "visible") refreshCurrentView(); }, 30000);
