/* 控制台逻辑：账号管理（搜索/筛选/详情/删除）+ 能力调用 + 会话管理 */
"use strict";

const state = {
  accounts: [],
  features: [
    { name: "getCode" },
    { name: "getPhoneNumber" },
    { name: "operateWxData" }
  ],
  selectedOpenID: "",
  lastResult: "",
  searchKeyword: "",
  statusFilter: "all",
  accountSort: "updated",
  drawerOpenID: "",
  logs: { items: [], total: 0, offset: 0, limit: 20, retentionDays: 7 },
  logRefreshTimer: null,
  dashboardPeriod: "day",
  dashboardTimer: null
};

const $ = id => document.getElementById(id);

function accountName(account) {
  return account.nickname || account.alias || account.openid || "未命名账号";
}

function statusText(status) {
  if (status === "alive") return "可用";
  if (status === "expired") return "需重扫";
  return "待确认";
}

function statusClass(status) {
  if (status === "alive") return "alive";
  if (status === "expired") return "expired";
  return "unknown";
}

function setActivity(message) {
  $("activityLine").textContent = message;
}

function setResult(value, meta, isError) {
  const text = typeof value === "string" ? value : JSON.stringify(value, null, 2);
  state.lastResult = text;
  const box = $("resultBox");
  box.textContent = text;
  box.classList.toggle("error", Boolean(isError));
  $("resultMeta").textContent = meta || "已更新";
}

function selectedAccount() {
  return state.accounts.find(item => item.openid === state.selectedOpenID) || null;
}

function drawerAccount() {
  return state.accounts.find(item => item.openid === state.drawerOpenID) || null;
}

/* ---------- 用户菜单与会话 ---------- */

async function loadSession() {
  try {
    const me = await api("GET", "/auth/me");
    const name = me.username || "admin";
    $("userName").textContent = name;
    $("menuUserName").textContent = name;
    $("userAvatar").textContent = (name.slice(0, 1) || "A").toUpperCase();
  } catch {
    /* api() 已处理 401 跳转 */
  }
}

function setupUserMenu() {
  const btn = $("userMenuBtn");
  const pop = $("userMenuPop");
  const close = () => { pop.classList.add("hidden"); btn.setAttribute("aria-expanded", "false"); };
  btn.addEventListener("click", e => {
    e.stopPropagation();
    const open = pop.classList.toggle("hidden");
    btn.setAttribute("aria-expanded", String(!open));
  });
  document.addEventListener("click", e => {
    if (!pop.contains(e.target) && e.target !== btn) close();
  });
  document.addEventListener("keydown", e => { if (e.key === "Escape") close(); });

  $("logoutBtn").addEventListener("click", async () => {
    close();
    try {
      await api("POST", "/auth/logout");
      location.href = "/login";
    } catch (error) {
      Toast.error("退出失败：" + error.message);
    }
  });

  $("changePwdBtn").addEventListener("click", () => {
    close();
    showChangePasswordModal();
  });
}

/**
 * 显示指定账号的 API 令牌管理弹窗。
 * 令牌与账号一对一绑定，只能调用该账号的接口。
 * @param {object} account 账号对象（需含 openid）
 */
function showAPITokenModal(account) {
  const name = accountName(account);
  const ref = account.openid;
  const overlay = document.createElement("div");
  overlay.className = "modal-overlay";
  overlay.innerHTML = `
    <div class="modal" role="dialog" aria-modal="true" aria-labelledby="tokenTitle" style="width:min(520px,100%)">
      <h3 id="tokenTitle">API 令牌 · ${escapeHTML(name)}</h3>
      <p class="modal-msg">令牌绑定当前账号，青龙等脚本用它只能操作该账号。重新生成会立即吊销旧令牌。</p>
      <dl class="kv-grid" id="tokenStatus"><dt>状态</dt><dd>加载中…</dd></dl>
      <div class="field hidden" id="tokenResultField">
        <label for="apiTokenValue">新令牌（仅显示这一次）</label>
        <input class="input" id="apiTokenValue" readonly spellcheck="false">
      </div>
      <div class="modal-actions">
        <button class="btn secondary" type="button" data-act="cancel">关闭</button>
        <button class="btn danger hidden" type="button" data-act="revoke">吊销令牌</button>
        <button class="btn primary" type="button" data-act="generate">生成令牌</button>
      </div>
    </div>
  `;
  const close = () => overlay.remove();
  const statusEl = overlay.querySelector("#tokenStatus");
  const revokeBtn = overlay.querySelector('[data-act="revoke"]');
  const generateBtn = overlay.querySelector('[data-act="generate"]');

  const renderStatus = info => {
    statusEl.innerHTML = "";
    const rows = info && info.active
      ? [["状态", "已生成"], ["绑定账号 ID", info.account_id], ["生成时间", formatTime(info.created_at)], ["最近使用", formatTime(info.last_used_at)]]
      : [["状态", "尚未生成"]];
    for (const [k, v] of rows) {
      const dt = document.createElement("dt");
      dt.textContent = k;
      const dd = document.createElement("dd");
      dd.textContent = String(v);
      statusEl.append(dt, dd);
    }
    revokeBtn.classList.toggle("hidden", !(info && info.active));
    generateBtn.textContent = info && info.active ? "重新生成" : "生成令牌";
  };

  const loadStatus = async () => {
    try {
      renderStatus(await api("GET", `/auth/token?ref=${encodeURIComponent(ref)}`));
    } catch (error) {
      statusEl.innerHTML = "";
      const dt = document.createElement("dt");
      dt.textContent = "状态";
      const dd = document.createElement("dd");
      dd.textContent = "查询失败：" + error.message;
      statusEl.append(dt, dd);
    }
  };

  overlay.querySelector('[data-act="cancel"]').addEventListener("click", close);

  generateBtn.addEventListener("click", async event => {
    const button = event.currentTarget;
    const label = button.textContent;
    button.disabled = true;
    button.innerHTML = '<span class="spin" aria-hidden="true"></span> 生成中';
    try {
      const result = await api("POST", "/auth/token", { ref });
      const input = overlay.querySelector("#apiTokenValue");
      input.value = result.token || "";
      overlay.querySelector("#tokenResultField").classList.remove("hidden");
      input.focus();
      input.select();
      Toast.success("令牌已生成，请立即复制保存");
      await loadStatus();
    } catch (error) {
      Toast.error("令牌生成失败：" + error.message);
      button.textContent = label;
    }
    button.disabled = false;
  });

  revokeBtn.addEventListener("click", async () => {
    const ok = await confirmModal({
      title: "吊销 API 令牌",
      message: `确定吊销账号「${name}」的令牌吗？使用该令牌的脚本会立即失去访问权限。`,
      confirmText: "吊销",
      danger: true
    });
    if (!ok) return;
    try {
      await api("DELETE", "/auth/token", { ref });
      overlay.querySelector("#tokenResultField").classList.add("hidden");
      Toast.success("令牌已吊销");
      await loadStatus();
    } catch (error) {
      Toast.error("吊销失败：" + error.message);
    }
  });

  document.body.appendChild(overlay);
  loadStatus();
}

function showChangePasswordModal() {
  const overlay = document.createElement("div");
  overlay.className = "modal-overlay";
  overlay.innerHTML = `
    <div class="modal" role="dialog" aria-modal="true" style="width:min(400px,100%)">
      <h3>修改密码</h3>
      <div class="field">
        <label for="pwdOld">当前密码</label>
        <input class="input" id="pwdOld" type="password" autocomplete="current-password">
      </div>
      <div class="field">
        <label for="pwdNew">新密码（至少 6 位）</label>
        <input class="input" id="pwdNew" type="password" autocomplete="new-password">
      </div>
      <div class="field">
        <label for="pwdNew2">确认新密码</label>
        <input class="input" id="pwdNew2" type="password" autocomplete="new-password">
      </div>
      <div class="modal-actions">
        <button class="btn secondary" type="button" data-act="cancel">取消</button>
        <button class="btn primary" type="button" data-act="ok">确认修改</button>
      </div>
    </div>
  `;
  const close = () => overlay.remove();
  overlay.querySelector('[data-act="cancel"]').addEventListener("click", close);
  overlay.querySelector('[data-act="ok"]').addEventListener("click", async () => {
    const oldPwd = overlay.querySelector("#pwdOld").value;
    const newPwd = overlay.querySelector("#pwdNew").value;
    const newPwd2 = overlay.querySelector("#pwdNew2").value;
    if (!oldPwd || !newPwd) { Toast.warning("请填写完整"); return; }
    if (newPwd !== newPwd2) { Toast.warning("两次输入的新密码不一致"); return; }
    if (newPwd.length < 6) { Toast.warning("新密码至少 6 位"); return; }
    try {
      await api("POST", "/auth/password", { old_password: oldPwd, new_password: newPwd });
      close();
      Toast.success("密码已修改");
    } catch (error) {
      Toast.error(error.message);
    }
  });
  document.body.appendChild(overlay);
  overlay.querySelector("#pwdOld").focus();
}

/* ---------- 健康检查 ---------- */

async function loadHealth() {
  const pill = $("healthPill");
  try {
    await api("GET", "/health");
    pill.classList.add("ok");
    pill.classList.remove("bad");
    $("healthText").textContent = "服务正常";
  } catch {
    pill.classList.add("bad");
    pill.classList.remove("ok");
    $("healthText").textContent = "服务异常";
  }
}

/* ---------- 视图切换 ---------- */

const VIEWS = {
  overview: { title: "总览", subtitle: "账号状态与常用维护动作" },
  accounts: { title: "账号管理", subtitle: "搜索、查看详情与删除账号" },
  call: { title: "能力调用", subtitle: "选择账号并调用 wxapp 接口" },
  "api-logs": { title: "接口日志", subtitle: "实时查看调用 IP、小程序 ID、状态与耗时" },
  system: { title: "系统", subtitle: "文档入口与最近活动" }
};

function activeViewFromHash() {
  const name = location.hash.replace("#", "");
  return VIEWS[name] ? name : "overview";
}

function showView(name) {
  const view = VIEWS[name] ? name : "overview";
  document.querySelectorAll(".view").forEach(section => {
    section.classList.toggle("hidden", section.dataset.view !== view);
  });
  document.querySelectorAll(".nav-item").forEach(item => {
    const on = item.dataset.view === view;
    item.classList.toggle("active", on);
    if (on) item.setAttribute("aria-current", "page");
    else item.removeAttribute("aria-current");
  });
  $("viewTitle").textContent = VIEWS[view].title;
  $("viewSubtitle").textContent = VIEWS[view].subtitle;
  if (view === "api-logs") {
    loadAPILogs();
    if (!state.logRefreshTimer) state.logRefreshTimer = setInterval(() => {
      if (activeViewFromHash() === "api-logs") loadAPILogs(true);
    }, 10000);
  } else if (state.logRefreshTimer) {
    clearInterval(state.logRefreshTimer);
    state.logRefreshTimer = null;
  }
  if (view === "overview") {
    loadDashboardMetrics(true);
    if (!state.dashboardTimer) state.dashboardTimer = setInterval(() => {
      if (activeViewFromHash() === "overview") loadDashboardMetrics(true);
    }, 15000);
  } else if (state.dashboardTimer) {
    clearInterval(state.dashboardTimer);
    state.dashboardTimer = null;
  }
}

function formatDuration(seconds) {
  const value = Math.max(0, Number(seconds || 0));
  const days = Math.floor(value / 86400);
  const hours = Math.floor(value % 86400 / 3600);
  const minutes = Math.floor(value % 3600 / 60);
  if (days) return `${days} 天 ${hours} 小时`;
  if (hours) return `${hours} 小时 ${minutes} 分`;
  return `${minutes} 分钟`;
}

function renderTrendChart(metrics) {
  const buckets = metrics.buckets || [];
  const max = Math.max(1, ...buckets.map(item => Number(item.calls || 0)));
  const chart = $("trendChart");
  chart.innerHTML = buckets.map(item => {
    const calls = Number(item.calls || 0);
    const failed = Number(item.failed || 0);
    const height = calls ? Math.max(3, calls / max * 100) : 1;
    const failedHeight = calls ? failed / max * 100 : 0;
    const title = `${item.label}：${calls} 次，失败 ${failed} 次`;
    return `<div class="trend-column" title="${escapeHTML(title)}"><span class="trend-bar" style="height:${height}%"></span><span class="trend-failed" style="height:${failedHeight}%"></span></div>`;
  }).join("");
  const labels = buckets.length ? [buckets[0].label, buckets[Math.floor((buckets.length - 1) / 2)].label, buckets[buckets.length - 1].label] : ["—", "—", "—"];
  $("trendAxis").innerHTML = labels.map(label => `<span>${escapeHTML(label)}</span>`).join("");
  chart.setAttribute("aria-label", `${periodLabel(metrics.period)}调用趋势，共 ${metrics.calls || 0} 次，失败 ${metrics.failed || 0} 次`);
}

function periodLabel(period) {
  if (period === "week") return "最近 7 天";
  if (period === "month") return "最近 30 天";
  return "最近 24 小时";
}

function renderDashboardMetrics(data) {
  const metrics = data.metrics || {};
  const runtime = data.runtime || {};
  const label = periodLabel(metrics.period || state.dashboardPeriod);
  $("metricCalls").textContent = String(metrics.calls || 0);
  $("metricCallsHint").textContent = label;
  $("metricSuccessRate").textContent = metrics.calls ? `${Number(metrics.success_rate || 0).toFixed(1)}%` : "—";
  $("metricSuccessHint").textContent = metrics.calls ? `成功 ${metrics.success || 0} · 失败 ${metrics.failed || 0}` : "暂无请求";
  $("metricIPs").textContent = String(metrics.unique_ips || 0);
  $("metricAvg").textContent = metrics.calls ? `${Number(metrics.avg_duration_ms || 0).toFixed(0)} ms` : "—";
  $("metricP95").textContent = metrics.calls ? `P95 ${metrics.p95_duration_ms || 0} ms` : "P95 —";
  $("trendDescription").textContent = metrics.period === "day" ? "按小时统计最近 24 小时" : `按天统计${label}`;
  $("trendTotal").textContent = `${metrics.calls || 0} 次`;
  $("runtimeUptime").textContent = formatDuration(runtime.uptime_seconds);
  $("runtimeMemory").textContent = `${Number(runtime.heap_alloc_mb || 0).toFixed(1)} MB`;
  $("runtimeGoroutines").textContent = String(runtime.goroutines || 0);
  $("runtimeGC").textContent = String(runtime.num_gc || 0);
  $("runtimeStatus").classList.add("ok");
  $("runtimeStatus").classList.remove("bad");
  $("runtimeStatusText").textContent = "正常";
  $("trendChart").classList.remove("empty");
  renderTrendChart(metrics);
}

function renderDashboardUnavailable() {
  ["metricCalls", "metricSuccessRate", "metricIPs", "metricAvg", "runtimeUptime", "runtimeMemory", "runtimeGoroutines", "runtimeGC"].forEach(id => { $(id).textContent = "—"; });
  $("metricSuccessHint").textContent = "指标暂不可用";
  $("metricP95").textContent = "P95 —";
  $("trendTotal").textContent = "—";
  $("trendChart").classList.add("empty");
  $("trendChart").innerHTML = '<span class="trend-empty">等待后端指标接口</span>';
  $("trendAxis").innerHTML = "";
  $("runtimeStatus").classList.remove("ok");
  $("runtimeStatus").classList.add("bad");
  $("runtimeStatusText").textContent = "未连接";
}

async function loadDashboardMetrics(silent = false) {
  try {
    renderDashboardMetrics(await api("GET", `/dashboard/metrics?period=${encodeURIComponent(state.dashboardPeriod)}`));
  } catch (error) {
    renderDashboardUnavailable();
    if (!silent) {
      const detail = error.status === 404 ? "总览接口未加载，请重启服务后刷新页面" : error.message;
      Toast.error("加载总览指标失败：" + detail);
    }
  }
}

function setupDashboard() {
  document.querySelectorAll("[data-period]").forEach(button => button.addEventListener("click", () => {
    state.dashboardPeriod = button.dataset.period;
    document.querySelectorAll("[data-period]").forEach(item => {
      const active = item === button;
      item.classList.toggle("active", active);
      item.setAttribute("aria-selected", String(active));
    });
    loadDashboardMetrics();
  }));
}

function formatLogTime(unix) {
  return unix ? new Date(unix * 1000).toLocaleString("zh-CN", { hour12: false }) : "—";
}

function logQuery() {
  const params = new URLSearchParams({ limit: String(state.logs.limit), offset: String(state.logs.offset) });
  const q = $("logSearch").value.trim();
  const ip = $("logIPFilter").value.trim();
  const status = $("logStatusFilter").value;
  if (q) params.set("q", q);
  if (ip) params.set("ip", ip);
  if (status !== "all") params.set("status", status);
  return params.toString();
}

function renderAPILogs() {
  const { items, total, offset, limit } = state.logs;
  const body = $("logTableBody");
  const successCount = items.filter(item => item.success).length;
  const ips = new Set(items.map(item => item.client_ip).filter(Boolean));
  $("logTotal").textContent = String(total);
  $("logSuccessRate").textContent = items.length ? `${Math.round(successCount / items.length * 100)}%` : "—";
  $("logIPCount").textContent = String(ips.size);
  $("logRetentionLabel").textContent = `${state.logs.retentionDays} 天`;
  $("logRetentionSelect").value = String(state.logs.retentionDays);
  $("logSummary").textContent = total ? `显示 ${offset + 1}–${Math.min(offset + items.length, total)} / 共 ${total} 条` : "暂无记录";
  $("logPrevBtn").disabled = offset <= 0;
  $("logNextBtn").disabled = offset + items.length >= total;
  if (!items.length) { body.innerHTML = `<tr><td colspan="8" class="log-empty">暂无接口日志</td></tr>`; return; }
  body.innerHTML = items.map(item => `<tr>
    <td class="tnum log-time">${escapeHTML(formatLogTime(item.created_at))}</td>
    <td><code>${escapeHTML(item.method)} ${escapeHTML(item.endpoint)}</code></td>
    <td>${escapeHTML(item.account_name || item.account_ref || "—")}</td>
    <td class="tnum">${escapeHTML(item.client_ip || "—")}</td>
    <td class="log-program" title="${escapeHTML(item.app_id || "未知")}">${escapeHTML(item.app_id || "未知")}</td>
    <td><span class="log-status ${item.success ? "ok" : "bad"}">${item.status_code} ${item.success ? "成功" : "失败"}</span></td>
    <td class="tnum">${item.duration_ms} ms</td>
    <td><button class="btn ghost sm" type="button" data-delete-log="${item.id}">删除</button></td>
  </tr>`).join("");
  body.querySelectorAll("[data-delete-log]").forEach(button => button.addEventListener("click", async () => {
    if (!window.confirm("确定删除这条接口日志吗？")) return;
    try { await api("DELETE", `/api-logs/${button.dataset.deleteLog}`); Toast.success("记录已删除"); await loadAPILogs(true); }
    catch (error) { Toast.error("删除失败：" + error.message); }
  }));
}

async function loadAPILogs(silent = false) {
  try {
    const result = await api("GET", `/api-logs?${logQuery()}`);
    state.logs.items = result.items || [];
    state.logs.total = Number(result.total || 0);
    state.logs.retentionDays = Number(result.retention_days || 7);
    renderAPILogs();
  } catch (error) { if (!silent) Toast.error("加载接口日志失败：" + error.message); }
}

function setupAPILogs() {
  ["logSearch", "logIPFilter", "logStatusFilter"].forEach(id => $(id).addEventListener("input", () => { state.logs.offset = 0; loadAPILogs(); }));
  $("reloadLogsBtn").addEventListener("click", () => loadAPILogs());
  $("logPageSize").addEventListener("change", event => { state.logs.limit = Number(event.target.value); state.logs.offset = 0; loadAPILogs(); });
  $("logPrevBtn").addEventListener("click", () => { state.logs.offset = Math.max(0, state.logs.offset - state.logs.limit); loadAPILogs(); });
  $("logNextBtn").addEventListener("click", () => { state.logs.offset += state.logs.limit; loadAPILogs(); });
  $("logRetentionSelect").addEventListener("change", async event => {
    try { await api("PATCH", "/api-logs/settings", { retention_days: Number(event.target.value) }); state.logs.offset = 0; await loadAPILogs(); Toast.success("保留周期已更新"); }
    catch (error) { Toast.error("更新保留周期失败：" + error.message); }
  });
  $("clearLogsBtn").addEventListener("click", async () => {
    if (!window.confirm("确定清空全部接口日志吗？此操作不可恢复。")) return;
    try { await api("DELETE", "/api-logs"); state.logs.offset = 0; await loadAPILogs(); Toast.success("接口日志已清空"); }
    catch (error) { Toast.error("清空失败：" + error.message); }
  });
}

function setupSidebar() {
  const sidebar = $("sidebar");
  const scrim = $("sidebarScrim");
  const openBtn = $("sidebarOpenBtn");

  const setOpen = open => {
    sidebar.classList.toggle("open", open);
    scrim.classList.toggle("hidden", !open);
    openBtn.setAttribute("aria-expanded", String(open));
  };

  openBtn.addEventListener("click", () => setOpen(true));
  $("sidebarCloseBtn").addEventListener("click", () => setOpen(false));
  scrim.addEventListener("click", () => setOpen(false));
  document.addEventListener("keydown", e => {
    if (e.key === "Escape" && sidebar.classList.contains("open")) setOpen(false);
  });

  $("sidebarNav").addEventListener("click", e => {
    const item = e.target.closest(".nav-item");
    if (item) setOpen(false);
  });

  window.addEventListener("hashchange", () => showView(activeViewFromHash()));
  showView(activeViewFromHash());
}


/* ---------- 账号列表 ---------- */

function filteredAccounts() {
  const kw = state.searchKeyword.trim().toLowerCase();
  const list = state.accounts.filter(acc => {
    if (state.statusFilter !== "all" && statusClass(acc.status) !== state.statusFilter) return false;
    if (!kw) return true;
    const haystack = [acc.nickname, acc.alias, acc.openid, acc.uin].filter(Boolean).join(" ").toLowerCase();
    return haystack.includes(kw);
  });
  const statusRank = { alive: 0, expired: 1, unknown: 2 };
  return list.sort((a, b) => {
    if (state.accountSort === "name") return accountName(a).localeCompare(accountName(b), "zh-CN");
    if (state.accountSort === "status") return statusRank[statusClass(a.status)] - statusRank[statusClass(b.status)] || accountName(a).localeCompare(accountName(b), "zh-CN");
    return Number(b.updated_at || b.last_checked_at || 0) - Number(a.updated_at || a.last_checked_at || 0);
  });
}

function renderStats() {
  const total = state.accounts.length;
  const alive = state.accounts.filter(item => statusClass(item.status) === "alive").length;
  const expired = state.accounts.filter(item => statusClass(item.status) === "expired").length;
  const unknown = total - alive - expired;

  $("accountCount").textContent = String(total);
  $("aliveCount").textContent = String(alive);
  $("featureCount").textContent = String(state.features.length);
  $("navAccountCount").textContent = String(total);

  [
    ["filterCountAll", total],
    ["filterCountAlive", alive],
    ["filterCountExpired", expired],
    ["filterCountUnknown", unknown]
  ].forEach(([id, value]) => { if ($(id)) $(id).textContent = String(value); });

  $("legendAlive").textContent = String(alive);
  $("legendExpired").textContent = String(expired);
  $("legendUnknown").textContent = String(unknown);

  const pctText = n => (total ? `${Math.round(n / total * 100)}%` : "0%");
  $("legendAlivePct").textContent = pctText(alive);
  $("legendExpiredPct").textContent = pctText(expired);
  $("legendUnknownPct").textContent = pctText(unknown);
  const health = total ? Math.round(alive / total * 100) : 0;
  $("accountHealthScore").textContent = total ? `${health}%` : "—";
  $("accountHealthHint").textContent = total ? (health >= 80 ? "账号整体状态良好" : health >= 50 ? "部分账号需要关注" : "建议尽快检查账号") : "等待账号数据";

  const pct = n => (total ? `${(n / total) * 100}%` : "0%");
  $("distAlive").style.width = pct(alive);
  $("distExpired").style.width = pct(expired);
  $("distUnknown").style.width = total ? pct(unknown) : "100%";
  $("distBar").setAttribute(
    "aria-label",
    `账号状态分布：可用 ${alive}，需重扫 ${expired}，待确认 ${unknown}`
  );

  syncSelectedAccount();
}

function syncSelectedAccount() {
  const selected = selectedAccount();
  const label = selected ? accountName(selected) : "未选择";
  $("selectedAccountName").textContent = label;
  $("selectedAccountHint").textContent = selected ? `${statusText(selected.status)} · ${selected.openid}` : "未绑定调用账号";
  $("currentAccountText").textContent = label;
  document.querySelectorAll(".account-card").forEach(card => {
    const selectedCard = card.dataset.openid === state.selectedOpenID;
    card.classList.toggle("selected", selectedCard);
    card.setAttribute("aria-pressed", String(selectedCard));
    const currentLabel = card.querySelector(".account-current-label");
    if (currentLabel) currentLabel.innerHTML = selectedCard
      ? '<span class="status-dot" aria-hidden="true"></span> 当前调用账号'
      : "可点击卡片选择";
  });
}

function renderAccounts() {
  const grid = $("accountGrid");
  const list = filteredAccounts();

  $("accountResultCount").textContent = state.searchKeyword.trim() || state.statusFilter !== "all"
    ? `显示 ${list.length} / 共 ${state.accounts.length} 个`
    : `${list.length} 个结果`;
  $("accountFilterHint").textContent = state.searchKeyword.trim()
    ? `正在搜索“${state.searchKeyword.trim()}”`
    : state.statusFilter === "all" ? "显示全部账号" : `仅显示${statusText(state.statusFilter)}账号`;

  if (!state.accounts.length) {
    grid.innerHTML = `<div class="empty-state">暂无账号，点击「添加账号」开始扫码。</div>`;
    state.selectedOpenID = "";
    renderStats();
    return;
  }
  if (!list.length) {
    grid.innerHTML = `<div class="empty-state">没有符合当前搜索 / 筛选条件的账号。</div>`;
    renderStats();
    return;
  }

  if (!state.accounts.some(item => item.openid === state.selectedOpenID)) {
    state.selectedOpenID = state.accounts[0].openid;
  }

  grid.innerHTML = "";
  for (const account of list) {
    const name = accountName(account);
    const cls = statusClass(account.status);
    const card = document.createElement("div");
    card.className = "account-card";
    card.dataset.openid = account.openid;
    card.tabIndex = 0;
    card.setAttribute("role", "button");
    card.setAttribute("aria-label", `选择账号：${name}`);
    card.innerHTML = `
      <span class="avatar account-avatar">
        <img alt="" width="40" height="40" loading="lazy" src="/accounts/avatar?ref=${encodeURIComponent(account.openid)}">
        <span class="avatar-letter hidden">${escapeHTML(name.slice(0, 1).toUpperCase() || "Y")}</span>
      </span>
      <span class="account-main">
        <span class="account-card-top">
          <span class="account-name" title="${escapeHTML(name)}">${escapeHTML(name)}</span>
          <span class="badge ${cls}">${statusText(account.status)}</span>
        </span>
        <span class="account-subline">${escapeHTML(account.alias || "未设置别名")}</span>
        <span class="account-detail-row"><span>OpenID</span><strong title="${escapeHTML(account.openid || "-")}">${escapeHTML(account.openid || "-")}</strong></span>
        <span class="account-detail-row"><span>UIN</span><strong>${escapeHTML(account.uin ?? "-")}</strong></span>
        <span class="account-detail-row"><span>最近检测</span><strong>${escapeHTML(formatTime(account.last_checked_at))}</strong></span>
        <span class="account-card-footer">
          <span class="account-current-label">${state.selectedOpenID === account.openid ? '<span class="status-dot" aria-hidden="true"></span> 当前调用账号' : "可点击卡片选择"}</span>
          <span class="account-card-actions">
            <button class="btn sm secondary" type="button" data-op="detail">详情</button>
            <button class="btn sm secondary" type="button" data-op="token">令牌</button>
            <button class="btn sm danger" type="button" data-op="delete">删除</button>
          </span>
        </span>
      </span>
    `;
    card.querySelector("img").addEventListener("error", e => {
      e.currentTarget.classList.add("hidden");
      card.querySelector(".avatar-letter").classList.remove("hidden");
    });
    const selectCard = () => {
      state.selectedOpenID = account.openid;
      syncSelectedAccount();
      setActivity(`已选择账号：${name}`);
    };
    card.addEventListener("click", e => {
      const op = e.target.closest("[data-op]")?.dataset.op;
      if (op === "detail") { openDrawer(account.openid); return; }
      if (op === "token") { showAPITokenModal(account); return; }
      if (op === "delete") { confirmDelete(account); return; }
      selectCard();
    });
    card.addEventListener("keydown", e => {
      if (e.target !== card || (e.key !== "Enter" && e.key !== " ")) return;
      e.preventDefault();
      selectCard();
    });
    grid.appendChild(card);
  }
  renderStats();
}

async function loadAccounts() {
  $("accountGrid").innerHTML = `<div class="empty-state">账号加载中…</div>`;
  try {
    state.accounts = await api("GET", "/accounts");
    renderAccounts();
    setActivity("账号列表已加载");
  } catch (error) {
    $("accountGrid").innerHTML = `<div class="empty-state">账号加载失败：${escapeHTML(error.message)}</div>`;
    setActivity("账号列表加载失败");
  }
}

async function confirmDelete(account) {
  const name = accountName(account);
  const ok = await confirmModal({
    title: "删除账号",
    message: `确定删除账号「${name}」吗？该操作会移除其登录凭证与会话数据，不可恢复。`,
    confirmText: "删除",
    danger: true
  });
  if (!ok) return;
  try {
    await api("DELETE", `/accounts?ref=${encodeURIComponent(account.openid)}`);
    Toast.success(`账号「${name}」已删除`);
    if (state.drawerOpenID === account.openid) closeDrawer();
    await loadAccounts();
  } catch (error) {
    Toast.error("删除失败：" + error.message);
  }
}

/* ---------- 详情抽屉 ---------- */

function openDrawer(openid) {
  const account = state.accounts.find(item => item.openid === openid);
  if (!account) return;
  state.drawerOpenID = openid;
  renderDrawer(account);
  $("detailOverlay").classList.remove("hidden");
  $("detailDrawer").classList.remove("hidden");
}

function closeDrawer() {
  state.drawerOpenID = "";
  $("detailOverlay").classList.add("hidden");
  $("detailDrawer").classList.add("hidden");
}

function renderDrawer(account) {
  const name = accountName(account);
  $("drawerTitle").textContent = name;
  $("drawerNickname").textContent = name;

  const avatar = $("drawerAvatar");
  avatar.innerHTML = `<img alt="" width="56" height="56" src="/accounts/avatar?ref=${encodeURIComponent(account.openid)}">
    <span class="avatar-letter hidden">${escapeHTML(name.slice(0, 1).toUpperCase() || "Y")}</span>`;
  avatar.querySelector("img").addEventListener("error", e => {
    e.currentTarget.classList.add("hidden");
    avatar.querySelector(".avatar-letter").classList.remove("hidden");
  });

  const badge = $("drawerStatusBadge");
  badge.className = `badge ${statusClass(account.status)}`;
  badge.textContent = statusText(account.status);

  const kv = $("drawerBaseInfo");
  kv.innerHTML = "";
  const rows = [
    ["ID", account.id],
    ["OpenID", account.openid],
    ["UIN", account.uin ?? "—"],
    ["别名", account.alias || "—"],
    ["最近检测", formatTime(account.last_checked_at)],
    ["创建时间", formatTime(account.created_at)],
    ["更新时间", formatTime(account.updated_at)]
  ];
  for (const [k, v] of rows) {
    const dt = document.createElement("dt");
    dt.textContent = k;
    const dd = document.createElement("dd");
    dd.textContent = String(v);
    kv.append(dt, dd);
  }

  $("drawerUserInfo").textContent = "该账号暂无 user_info 数据（AccountPublic 接口不包含，可先执行「同步资料」）。";
}

function setupDrawer() {
  $("drawerCloseBtn").addEventListener("click", closeDrawer);
  $("detailOverlay").addEventListener("click", closeDrawer);
  document.addEventListener("keydown", e => { if (e.key === "Escape" && state.drawerOpenID) closeDrawer(); });

  $("drawerRefreshBtn").addEventListener("click", async () => {
    const account = drawerAccount();
    if (!account) return;
    try {
      const result = await api("POST", "/accounts/refresh", { ref: account.openid });
      setResult(result, "存活刷新完成", false);
      Toast.success(`「${accountName(account)}」状态：${statusText(result.status) || result.status}`);
      await loadAccounts();
      const fresh = state.accounts.find(item => item.openid === account.openid);
      if (fresh) renderDrawer(fresh);
    } catch (error) {
      Toast.error("刷新失败：" + error.message);
    }
  });

  $("drawerResyncBtn").addEventListener("click", async () => {
    const account = drawerAccount();
    if (!account) return;
    try {
      const result = await api("POST", "/accounts/resync", { ref: account.openid });
      setResult(result, "资料同步完成", false);
      Toast.success("资料已同步");
      await loadAccounts();
      const fresh = state.accounts.find(item => item.openid === account.openid);
      if (fresh) renderDrawer(fresh);
    } catch (error) {
      Toast.error("同步失败：" + error.message);
    }
  });

  $("drawerTokenBtn").addEventListener("click", () => {
    const account = drawerAccount();
    if (account) showAPITokenModal(account);
  });

  $("drawerDeleteBtn").addEventListener("click", () => {
    const account = drawerAccount();
    if (account) confirmDelete(account);
  });
}

/* ---------- 批量维护 ---------- */

async function refreshAll() {
  const button = $("refreshAllBtn");
  button.disabled = true;
  button.innerHTML = '<span class="spin" aria-hidden="true"></span> 刷新中';
  try {
    const result = await api("POST", "/accounts/refresh");
    setResult(result, "存活刷新完成", false);
    const expired = (Array.isArray(result) ? result : []).filter(item => item.status === "expired").length;
    Toast.success(`存活刷新完成${expired ? `，${expired} 个账号需重扫` : ""}`);
    await loadAccounts();
  } catch (error) {
    setResult(error.data || error.message, "存活刷新失败", true);
    Toast.error("存活刷新失败：" + error.message);
  } finally {
    button.disabled = false;
    button.innerHTML = '<span class="btn-icon" aria-hidden="true">↻</span> 刷新存活';
  }
}

async function resyncAll() {
  const button = $("resyncAllBtn");
  button.disabled = true;
  button.innerHTML = '<span class="spin" aria-hidden="true"></span> 同步中';
  try {
    const result = await api("POST", "/accounts/resync");
    setResult(result, "资料同步完成", false);
    Toast.success("资料同步完成");
    await loadAccounts();
  } catch (error) {
    setResult(error.data || error.message, "资料同步失败", true);
    Toast.error("资料同步失败：" + error.message);
  } finally {
    button.disabled = false;
    button.innerHTML = '<span class="btn-icon" aria-hidden="true">⇄</span> 同步资料';
  }
}

/* ---------- 能力调用 ---------- */

function renderFeatures() {
  const select = $("featureSel");
  select.innerHTML = state.features.map(feature =>
    `<option value="${escapeHTML(feature.name)}" data-name="${escapeHTML(feature.name)}">${escapeHTML(feature.name)}</option>`
  ).join("");
  togglePayload();
}

function currentFeatureName() {
  const option = $("featureSel").selectedOptions[0];
  return option ? option.dataset.name || "" : "";
}

function togglePayload() {
  const needsPayload = currentFeatureName().toLowerCase() === "operatewxdata";
  $("payloadGroup").classList.toggle("hidden", !needsPayload);
  const hints = {
    getCode: "获取小程序登录 code",
    getPhoneNumber: "解密并获取手机号信息",
    operateWxData: "代理调用通用云函数数据接口"
  };
  $("featureHint").textContent = hints[currentFeatureName()] || "接口参数";
}

function setCallStatus(type, label, summary, duration) {
  const status = $("resultStatus");
  status.className = `result-status ${type || "waiting"}`;
  $("resultMeta").textContent = label;
  $("resultSummary").firstElementChild.textContent = summary;
  $("resultDuration").textContent = duration || "—";
}

async function callFeature() {
  const account = selectedAccount();
  if (!account) {
    Toast.warning("请先选择一个账号");
    setResult("请先选择一个账号。", "缺少账号", true);
    setCallStatus("error", "需要账号", "无法发送请求", "—");
    return;
  }

  const endpoint = currentFeatureName();
  const appID = $("appidInput").value.trim();
  if (!endpoint) { setResult("当前没有可调用能力。", "缺少能力", true); setCallStatus("error", "需要能力", "无法发送请求", "—"); return; }
  if (!appID) { Toast.warning("请输入 APPID"); setResult("请输入 APPID。", "缺少 APPID", true); setCallStatus("error", "需要 APPID", "无法发送请求", "—"); return; }

  const body = { ref: account.openid, app_id: appID };
  if (endpoint.toLowerCase() === "operatewxdata") {
    const raw = $("payloadInput").value.trim();
    if (!raw) { Toast.warning("operateWxData 需要请求 JSON"); return; }
    try {
      body.payload = JSON.parse(raw);
    } catch {
      setResult("请求 JSON 格式不正确。", "JSON 解析失败", true);
      setCallStatus("error", "JSON 无效", "无法发送请求", "—");
      Toast.error("请求 JSON 格式不正确");
      return;
    }
  }

  const button = $("callBtn");
  button.disabled = true;
  const originalCallLabel = button.innerHTML;
  button.innerHTML = '<span class="spin" aria-hidden="true"></span> 请求中';
  const startedAt = performance.now();
  setResult("请求发送中，首次调用可能需要建立会话…", "请求进行中", false);
  setCallStatus("loading", "请求中", `正在调用 ${endpoint}`, "—");
  setActivity(`正在调用 ${endpoint}：${accountName(account)}`);

  try {
    const result = await api("POST", `/wxapp/${endpoint}`, body);
    const seconds = ((performance.now() - startedAt) / 1000).toFixed(1);
    setResult(result, `调用完成，用时 ${seconds}s`, false);
    setCallStatus("success", "请求成功", `已返回 ${endpoint} 响应`, `${seconds}s`);
    Toast.success(`调用完成，用时 ${seconds}s`);
    await loadAccounts();
  } catch (error) {
    setResult(error.data || error.message, `调用失败 ${error.status || ""}`.trim(), true);
    setCallStatus("error", "请求失败", error.message, `${((performance.now() - startedAt) / 1000).toFixed(1)}s`);
    Toast.error(`调用失败：${error.message}`);
  } finally {
    button.disabled = false;
    button.innerHTML = originalCallLabel;
  }
}

/* ---------- 初始化 ---------- */

$("reloadAccountsBtn").addEventListener("click", loadAccounts);
$("refreshAllBtn").addEventListener("click", refreshAll);
$("resyncAllBtn").addEventListener("click", resyncAll);
$("featureSel").addEventListener("change", togglePayload);
$("callBtn").addEventListener("click", callFeature);
$("clearResultBtn").addEventListener("click", () => {
  setResult("暂无响应数据。", "等待请求", false);
  setCallStatus("waiting", "等待请求", "准备就绪", "—");
});
$("copyResultBtn").addEventListener("click", async () => {
  try {
    await navigator.clipboard.writeText(state.lastResult || $("resultBox").textContent);
    Toast.success("结果已复制");
  } catch {
    Toast.warning("复制失败，请手动选择结果内容");
  }
});

$("accountSearch").addEventListener("input", e => {
  state.searchKeyword = e.target.value;
  renderAccounts();
});
document.querySelectorAll("#statusFilter [data-status]").forEach(button => button.addEventListener("click", () => {
  state.statusFilter = button.dataset.status;
  document.querySelectorAll("#statusFilter [data-status]").forEach(item => {
    const active = item === button;
    item.classList.toggle("active", active);
    item.setAttribute("aria-selected", String(active));
  });
  renderAccounts();
}));
$("accountSort").addEventListener("change", e => {
  state.accountSort = e.target.value;
  renderAccounts();
});

setupSidebar();
setupAPILogs();
setupDashboard();
Theme.bindButton($("themeBtn"));
setupUserMenu();
setupDrawer();
loadSession();
loadHealth();
renderFeatures();
loadAccounts();
setInterval(loadHealth, 15000);
