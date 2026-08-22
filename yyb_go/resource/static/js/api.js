/* YYB Go 前端公共层：API 封装 + Toast 通知 + 模态框 */
"use strict";

/* ---------- API 封装 ---------- */

/**
 * 统一请求入口：解析 {code,msg,data} 信封，401 时跳转登录页。
 * @param {string} method HTTP 方法
 * @param {string} url 请求地址
 * @param {object} [body] 请求体（自动 JSON 序列化）
 * @returns {Promise<any>} data 载荷
 */
async function api(method, url, body) {
  const options = { method, headers: {} };
  if (body !== undefined) {
    options.headers["Content-Type"] = "application/json";
    options.body = JSON.stringify(body);
  }
  const response = await fetch(url, options);
  if (response.status === 401) {
    // 已在登录页时不再跳转，避免中止进行中的请求（ERR_ABORTED）
    if (location.pathname !== "/login") location.href = "/login";
    throw new Error("未登录或会话已过期");
  }
  const text = await response.text();
  let data = null;
  try { data = text ? JSON.parse(text) : null; } catch { data = text; }
  const isEnvelope = data && typeof data === "object" && !Array.isArray(data)
    && Object.prototype.hasOwnProperty.call(data, "code")
    && Object.prototype.hasOwnProperty.call(data, "msg")
    && Object.prototype.hasOwnProperty.call(data, "data");
  if (!response.ok || (isEnvelope && data.code !== 0)) {
    const error = new Error(isEnvelope ? data.msg : "HTTP " + response.status);
    error.status = response.status;
    error.code = isEnvelope ? data.code : response.status;
    error.data = isEnvelope ? data.data : data;
    error.payload = data;
    throw error;
  }
  return isEnvelope ? data.data : data;
}

/* ---------- Toast 通知 ---------- */

const Toast = (() => {
  let region = null;

  function ensureRegion() {
    if (!region) {
      region = document.createElement("div");
      region.className = "toast-region";
      document.body.appendChild(region);
    }
    return region;
  }

  function show(message, type = "info", duration = 3800) {
    const icons = { success: "✓", error: "✕", info: "ℹ", warning: "⚠" };
    const el = document.createElement("div");
    el.className = `toast ${type}`;
    el.setAttribute("role", type === "error" ? "alert" : "status");
    el.innerHTML = `
      <span class="toast-icon">${icons[type] || icons.info}</span>
      <span class="toast-msg"></span>
      <button class="toast-close" type="button" aria-label="关闭">×</button>
    `;
    el.querySelector(".toast-msg").textContent = message;
    const dismiss = () => {
      if (!el.parentNode) return;
      el.classList.add("out");
      el.addEventListener("animationend", () => el.remove(), { once: true });
    };
    el.querySelector(".toast-close").addEventListener("click", dismiss);
    ensureRegion().appendChild(el);
    if (duration > 0) setTimeout(dismiss, duration);
    return dismiss;
  }

  return {
    success: (msg, ms) => show(msg, "success", ms),
    error: (msg, ms) => show(msg, "error", ms ?? 5200),
    info: (msg, ms) => show(msg, "info", ms),
    warning: (msg, ms) => show(msg, "warning", ms),
  };
})();

/* ---------- 模态框（确认对话框） ---------- */

/**
 * 显示确认模态框。
 * @param {{title:string, message:string, confirmText?:string, danger?:boolean}} opts
 * @returns {Promise<boolean>} 用户是否确认
 */
function confirmModal(opts) {
  return new Promise(resolve => {
    const overlay = document.createElement("div");
    overlay.className = "modal-overlay";
    overlay.innerHTML = `
      <div class="modal" role="dialog" aria-modal="true">
        <h3></h3>
        <p class="modal-msg"></p>
        <div class="modal-actions">
          <button class="btn secondary" type="button" data-act="cancel"></button>
          <button class="btn primary" type="button" data-act="ok"></button>
        </div>
      </div>
    `;
    overlay.querySelector("h3").textContent = opts.title;
    overlay.querySelector(".modal-msg").textContent = opts.message;
    const okBtn = overlay.querySelector('[data-act="ok"]');
    okBtn.textContent = opts.confirmText || "确认";
    if (opts.danger) {
      okBtn.classList.remove("primary");
      okBtn.classList.add("danger");
    }
    const close = result => {
      overlay.classList.add("out");
      overlay.addEventListener("animationend", () => overlay.remove(), { once: true });
      document.removeEventListener("keydown", onKey);
      resolve(result);
    };
    const onKey = e => { if (e.key === "Escape") close(false); };
    overlay.querySelector('[data-act="cancel"]').textContent = "取消";
    overlay.querySelector('[data-act="cancel"]').addEventListener("click", () => close(false));
    okBtn.addEventListener("click", () => close(true));
    overlay.addEventListener("mousedown", e => { if (e.target === overlay) close(false); });
    document.addEventListener("keydown", onKey);
    document.body.appendChild(overlay);
    okBtn.focus();
  });
}

/* ---------- 通用工具 ---------- */

function escapeHTML(value) {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

const timeFormatter = new Intl.DateTimeFormat(undefined, {
  year: "numeric",
  month: "2-digit",
  day: "2-digit",
  hour: "2-digit",
  minute: "2-digit",
  hour12: false
});

function formatTime(ts) {
  if (!ts) return "—";
  return timeFormatter.format(new Date(ts * 1000));
}

/** 为 fetch 之外的场景兜底：检查会话是否有效 */
async function ensureSession() {
  try {
    await api("GET", "/auth/me");
    return true;
  } catch {
    return false;
  }
}
