/* 登录页逻辑 */
"use strict";

(function () {
  const form = document.getElementById("loginForm");
  const errBox = document.getElementById("loginError");
  const submitBtn = document.getElementById("submitBtn");

  // 已登录则直接进入控制台
  ensureSession().then(ok => { if (ok) location.href = "/"; });

  form.addEventListener("submit", async event => {
    event.preventDefault();
    errBox.classList.remove("show");

    const username = document.getElementById("username").value.trim();
    const password = document.getElementById("password").value;
    if (!username || !password) {
      errBox.textContent = "请输入用户名和密码";
      errBox.classList.add("show");
      return;
    }

    submitBtn.disabled = true;
    submitBtn.textContent = "登录中…";
    try {
      await api("POST", "/auth/login", { username, password });
      location.href = "/";
    } catch (error) {
      errBox.textContent = error.message || "登录失败";
      errBox.classList.add("show");
    } finally {
      submitBtn.disabled = false;
      submitBtn.textContent = "登 录";
    }
  });
})();
