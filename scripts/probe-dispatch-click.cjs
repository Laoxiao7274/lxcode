// 临时诊断（十三）：最小化探针——只发消息、等确认、批、等卡、点、看页。
const { withPreview } = require("./lib/preview-harness.cjs");

withPreview(async (preview) => {
  const { win, log, url } = preview;
  win.setContentSize(1912, 948);
  win.webContents.setUserAgent(preview.browserUA);
  await win.loadURL(url);
  await new Promise((r) => setTimeout(r, 1500));
  const step = async (label, script) => {
    try { log(label, JSON.stringify(await win.webContents.executeJavaScript(script))); }
    catch (e) { log(label + "-FAIL", String(e).slice(0, 200)); }
  };
  await step("send", `(() => {
    const ta = document.querySelector(".piInput");
    const setter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, "value").set;
    setter.call(ta, "给会话历史加个超时兜底");
    ta.dispatchEvent(new Event("input", { bubbles: true }));
    document.querySelector(".send-btn").click();
    return true;
  })()`);
  for (let i = 0; i < 120; i++) {
    await new Promise((r) => setTimeout(r, 250));
    if (await win.webContents.executeJavaScript(`!!document.querySelector('.dispatch-card [data-variant="command"]')`).catch(() => false)) break;
  }
  await step("approve", `(() => {
    const card = document.querySelector('.dispatch-card [data-variant="command"]');
    if (!card) return false;
    const btn = [...card.querySelectorAll("button")].find((b) => (b.textContent ?? "").trim() === "运行");
    if (!btn) return false;
    btn.click();
    return true;
  })()`);
  for (let i = 0; i < 120; i++) {
    await new Promise((r) => setTimeout(r, 250));
    if (await win.webContents.executeJavaScript(`!!document.querySelector('.dispatch-card[data-done="true"]')`).catch(() => false)) break;
  }
  await step("done", `!!document.querySelector('.dispatch-card[data-done="true"]')`);
  await win.webContents.executeJavaScript("window.__LX_TEST_MOTION_OFF__ = true").catch(() => {});
  await step("click", `(() => { document.querySelector('.dispatch-card[data-done="true"] .dispatch-head').click(); return true; })()`);
  await new Promise((r) => setTimeout(r, 1500));
  await step("page", `!!document.querySelector('.child-session-page')`);
  await step("pills", `document.querySelectorAll('.stats-pill').length`);
  await step("pillsInPage", `document.querySelectorAll('.child-session-page .stats-pill').length`);
  await step("ctx", `document.querySelectorAll('.child-session-page .ctx-chip').length`);
});
