/* 扫码页逻辑 */
"use strict";

(function () {
  const $ = id => document.getElementById(id);
  const QR_TTL = 110; // 秒，到点自动重新生成（早于微信真正过期）
  let sid = null, pollTimer = null, cdTimer = null, secsLeft = 0;
  let leaving = false, polling = false, epoch = 0;
  const inflight = new Set(); // 在飞的轮询请求，仅在离开页面时统一中止

  function setStatus(text, cls) {
    const s = $("status");
    s.textContent = text;
    s.className = "scan-status " + cls;
  }

  function stopTimers() {
    polling = false;
    // 递增轮次让在飞响应作废，而不是 abort（abort 本身也会被记为 net::ERR_ABORTED）
    epoch++;
    if (pollTimer) clearTimeout(pollTimer);
    if (cdTimer) clearInterval(cdTimer);
    pollTimer = cdTimer = null;
  }

  function startCountdown() {
    secsLeft = QR_TTL;
    $("countdown").textContent = `二维码 ${secsLeft}s 后自动刷新`;
    cdTimer = setInterval(() => {
      secsLeft--;
      $("countdown").textContent = secsLeft > 0 ? `二维码 ${secsLeft}s 后自动刷新` : "二维码已过期，正在刷新…";
      if (secsLeft <= 0) { stopTimers(); newQr(); }
    }, 1000);
  }

  async function newQr() {
    stopTimers();
    $("accBox").classList.add("hidden");
    $("refreshBtn").disabled = true;
    $("refreshBtn").innerHTML = '<span class="spin" aria-hidden="true"></span> 生成中';
    $("qrWrap").innerHTML = '<span class="qr-placeholder"><span class="spin" aria-hidden="true"></span><br>正在生成二维码</span>';
    setStatus("正在获取二维码…", "pending");
    $("countdown").textContent = "";
    try {
      const r = await api("POST", "/qr");
      sid = r.session_id;
      $("qrWrap").innerHTML = `<img alt="二维码" src="${r.image_url}?t=${Date.now()}">`;
      $("refreshBtn").disabled = false;
      $("refreshBtn").innerHTML = '<span class="btn-icon" aria-hidden="true">↻</span> 重新生成二维码';
      setStatus("等待扫码…", "pending");
      startCountdown();
      schedulePoll();
    } catch (error) {
      $("refreshBtn").disabled = false;
      $("refreshBtn").innerHTML = '<span class="btn-icon" aria-hidden="true">↻</span> 重新生成二维码';
      setStatus("获取二维码失败: " + error.message, "err");
      Toast.error("获取二维码失败：" + error.message);
    }
  }

  // 串行轮询：上一次响应回来后再排下一次，避免请求堆叠在飞行中
  function schedulePoll() {
    polling = true;
    pollTimer = setTimeout(runPoll, 1500);
  }

  async function runPoll() {
    pollTimer = null;
    await poll();
    if (polling && !leaving && !pollTimer) schedulePoll();
  }

  async function poll() {
    if (!sid || leaving) return;
    const mine = epoch;
    const controller = new AbortController();
    inflight.add(controller);
    let st;
    try {
      st = await api("GET", `/qr/${sid}/poll`, undefined, controller.signal);
    } catch {
      return; // 包含离开页面时的主动取消，静默丢弃
    } finally {
      inflight.delete(controller);
    }
    if (leaving || mine !== epoch) return; // 轮次已失效（已重新生成或已停止）
    if (st.status === "pending") setStatus("等待扫码…", "pending");
    else if (st.status === "scanned") setStatus("已扫码，请在手机上确认", "scanned");
    else if (st.status === "authorized" || st.status === "confirmed") { stopTimers(); await confirm(); }
    else if (st.status === "expired" || st.status === "unknown") {
      stopTimers();
      setStatus("二维码已失效，正在重新生成…", "err");
      newQr();
    } else if (st.status === "cancelled") {
      stopTimers();
      setStatus("已取消，请重新生成二维码", "err");
      $("countdown").textContent = "";
    }
  }

  async function confirm() {
    $("countdown").textContent = "";
    setStatus("授权成功，正在入库…", "authorized");
    try {
      const acc = await api("POST", `/qr/${sid}/confirm`);
      setStatus("添加成功", "ok");
      Toast.success("账号添加成功");
      const name = acc.nickname || acc.alias || acc.openid;
      const box = $("accBox");
      box.classList.remove("hidden");
      box.innerHTML = `
        <div>账号：<b>${escapeHTML(name)}</b></div>
        <div>openid：${escapeHTML(acc.openid)}</div>
        <div>状态：${escapeHTML(acc.status || "alive")}</div>
      `;
      $("qrWrap").innerHTML = '<span class="qr-ok">✓</span>';
    } catch (error) {
      setStatus("入库失败: " + error.message, "err");
      Toast.error("入库失败：" + error.message);
    }
  }

  $("refreshBtn").addEventListener("click", newQr);

  // 离开页面前主动收尾：停掉定时器并取消在飞的长轮询，
  // 避免请求被导航打断后仍继续处理，或多个请求同时被中止。
  function leave() {
    if (leaving) return;
    leaving = true;
    stopTimers();
    inflight.forEach(c => c.abort());
    inflight.clear();
  }
  // 点击站内链接时导航尚未开始，这是最早的可靠收尾时机
  document.addEventListener("click", event => {
    const a = event.target.closest ? event.target.closest("a[href]") : null;
    if (a && !a.target && a.getAttribute("href").charAt(0) !== "#") leave();
  }, true);
  window.addEventListener("beforeunload", leave);
  window.addEventListener("pagehide", leave);
  // 从浏览器前进/后退缓存恢复时重新生成二维码并恢复轮询
  window.addEventListener("pageshow", event => {
    if (!event.persisted) return;
    leaving = false;
    newQr();
  });

  newQr();
})();
