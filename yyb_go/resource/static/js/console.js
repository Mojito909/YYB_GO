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
  drawerOpenID: ""
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
  return state.accounts.filter(acc => {
    if (state.statusFilter !== "all" && statusClass(acc.status) !== state.statusFilter) return false;
    if (!kw) return true;
    const haystack = [acc.nickname, acc.alias, acc.openid, acc.uin].filter(Boolean).join(" ").toLowerCase();
    return haystack.includes(kw);
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

  $("legendAlive").textContent = String(alive);
  $("legendExpired").textContent = String(expired);
  $("legendUnknown").textContent = String(unknown);

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
  $("currentAccountText").textContent = label;
  document.querySelectorAll(".account-card").forEach(card => {
    card.classList.toggle("selected", card.dataset.openid === state.selectedOpenID);
  });
}

function renderAccounts() {
  const grid = $("accountGrid");
  const list = filteredAccounts();

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
      <span class="avatar">
        <img alt="" width="40" height="40" loading="lazy" src="/accounts/avatar?ref=${encodeURIComponent(account.openid)}">
        <span class="avatar-letter hidden">${escapeHTML(name.slice(0, 1).toUpperCase() || "Y")}</span>
      </span>
      <span class="account-main">
        <span class="account-name" title="${escapeHTML(name)}">${escapeHTML(name)}</span>
        <span class="badge ${cls}">${statusText(account.status)}</span>
        <span class="account-meta" title="uin ${escapeHTML(account.uin ?? "-")}">uin: ${escapeHTML(account.uin ?? "-")}</span>
        <span class="account-card-actions">
          <button class="btn sm secondary" type="button" data-op="detail">详情</button>
          <button class="btn sm danger" type="button" data-op="delete">删除</button>
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
}

async function callFeature() {
  const account = selectedAccount();
  if (!account) {
    Toast.warning("请先选择一个账号");
    setResult("请先选择一个账号。", "缺少账号", true);
    return;
  }

  const endpoint = currentFeatureName();
  const appID = $("appidInput").value.trim();
  if (!endpoint) { setResult("当前没有可调用能力。", "缺少能力", true); return; }
  if (!appID) { Toast.warning("请输入 APPID"); setResult("请输入 APPID。", "缺少 APPID", true); return; }

  const body = { ref: account.openid, app_id: appID };
  if (endpoint.toLowerCase() === "operatewxdata") {
    const raw = $("payloadInput").value.trim();
    if (!raw) { Toast.warning("operateWxData 需要请求 JSON"); return; }
    try {
      body.payload = JSON.parse(raw);
    } catch {
      setResult("请求 JSON 格式不正确。", "JSON 解析失败", true);
      Toast.error("请求 JSON 格式不正确");
      return;
    }
  }

  const button = $("callBtn");
  button.disabled = true;
  const originalCallLabel = button.innerHTML;
  button.innerHTML = '<span class="spin" aria-hidden="true"></span> 调用中';
  const startedAt = performance.now();
  setResult("调用中，首次调用可能需要建立会话…", "请求进行中", false);
  setActivity(`正在调用 ${endpoint}：${accountName(account)}`);

  try {
    const result = await api("POST", `/wxapp/${endpoint}`, body);
    const seconds = ((performance.now() - startedAt) / 1000).toFixed(1);
    setResult(result, `调用完成，用时 ${seconds}s`, false);
    Toast.success(`调用完成，用时 ${seconds}s`);
    await loadAccounts();
  } catch (error) {
    setResult(error.data || error.message, `调用失败 ${error.status || ""}`.trim(), true);
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
$("clearResultBtn").addEventListener("click", () => setResult("结果已清空。", "等待调用", false));
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
$("statusFilter").addEventListener("change", e => {
  state.statusFilter = e.target.value;
  renderAccounts();
});

setupSidebar();
Theme.bindButton($("themeBtn"));
setupUserMenu();
setupDrawer();
loadSession();
loadHealth();
renderFeatures();
loadAccounts();
setInterval(loadHealth, 15000);
