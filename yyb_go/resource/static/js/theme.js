/* 主题管理：system / light / dark 三态循环，localStorage 记忆，同步 theme-color */
"use strict";

const Theme = (() => {
  const KEY = "yyb-theme";
  const ORDER = ["system", "light", "dark"];
  const GLYPH = { system: "◐", light: "☀", dark: "☾" };
  const LABEL = { system: "跟随系统", light: "浅色", dark: "深色" };
  const BAR = { light: "#f4f6fa", dark: "#0b1220" };

  const media = window.matchMedia("(prefers-color-scheme: dark)");

  function read() {
    const value = localStorage.getItem(KEY);
    return ORDER.includes(value) ? value : "system";
  }

  function resolve(pref) {
    if (pref === "system") return media.matches ? "dark" : "light";
    return pref;
  }

  function apply(pref) {
    const resolved = resolve(pref);
    const root = document.documentElement;
    root.dataset.theme = pref;
    root.dataset.themeResolved = resolved;
    let meta = document.querySelector('meta[name="theme-color"]');
    if (!meta) {
      meta = document.createElement("meta");
      meta.name = "theme-color";
      document.head.appendChild(meta);
    }
    meta.content = BAR[resolved];
    document.dispatchEvent(new CustomEvent("themechange", { detail: { pref, resolved } }));
  }

  function set(pref) {
    if (pref === "system") localStorage.removeItem(KEY);
    else localStorage.setItem(KEY, pref);
    apply(pref);
    return pref;
  }

  function cycle() {
    const next = ORDER[(ORDER.indexOf(read()) + 1) % ORDER.length];
    return set(next);
  }

  /**
   * 绑定主题切换按钮：点击循环三态，并同步 aria-label 与图标。
   * @param {HTMLElement} button 按钮元素
   */
  function bindButton(button) {
    if (!button) return;
    const glyph = button.querySelector(".theme-glyph");
    const sync = () => {
      const pref = read();
      if (glyph) glyph.textContent = GLYPH[pref];
      button.setAttribute("aria-label", `主题：${LABEL[pref]}，点击切换`);
      button.title = `主题：${LABEL[pref]}`;
    };
    button.addEventListener("click", () => { cycle(); sync(); });
    document.addEventListener("themechange", sync);
    sync();
  }

  // 首帧即应用，避免主题闪烁
  apply(read());
  media.addEventListener("change", () => { if (read() === "system") apply("system"); });

  return { read, resolve, set, cycle, bindButton, label: pref => LABEL[pref] };
})();
