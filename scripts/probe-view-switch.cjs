// 探针：agents 视图 → 点会话行 → 应回 chat 视图
const { app, BrowserWindow } = require("electron");
const assert = require("node:assert/strict");
const url = process.env.LXCODE_PREVIEW_URL || "http://127.0.0.1:5190/?mode=demo";
const log = (...a) => process.stderr.write(a.join(" ") + "\n");

app.whenReady().then(async () => {
  const win = new BrowserWindow({ width: 1440, height: 900, show: false });
  try {
    await win.loadURL(url);
    await win.webContents.executeJavaScript("window.__LX_TEST_MOTION_OFF__ = true");
    // 1. 进 agents 视图
    await win.webContents.executeJavaScript(`document.querySelector('[data-nav="agents"]').click()`);
    let state = await win.webContents.executeJavaScript(`(() => ({
      agentsPage: !!document.querySelector(".ag-page"),
      chatThread: !!document.querySelector(".thread-scroll"),
    }))()`);
    log("after-agents-nav", JSON.stringify(state));
    assert.ok(state.agentsPage && !state.chatThread, "应在 agents 视图");
    // 2. 点击第一个会话行
    const clicked = await win.webContents.executeJavaScript(`(() => {
      const row = document.querySelector(".session-item");
      if (!row) return "no-row";
      row.click();
      return "clicked";
    })()`);
    log("session-row-click", clicked);
    // 3. 检查视图 + 标签条
    await new Promise((r) => setTimeout(r, 300));
    state = await win.webContents.executeJavaScript(`(() => ({
      agentsPage: !!document.querySelector(".ag-page"),
      chatThread: !!document.querySelector(".thread-scroll"),
      tabs: document.querySelectorAll(".tab").length,
      tabOn: !!document.querySelector(".tab.on"),
    }))()`);
    log("after-session-click", JSON.stringify(state));
    if (state.agentsPage) {
      log("BUG 复现：点击会话行后仍在 agents 视图");
      process.exit(1);
    }
    // 项目行点击也应回 chat（过滤 + 切视图）
    await win.webContents.executeJavaScript(`document.querySelector('[data-nav="agents"]').click()`);
    await new Promise((r) => setTimeout(r, 200));
    const projRow = await win.webContents.executeJavaScript(`(() => {
      const row = document.querySelector(".proj-row");
      if (!row) return false;
      row.click();
      return true;
    })()`);
    await new Promise((r) => setTimeout(r, 300));
    const afterProj = await win.webContents.executeJavaScript(`(() => ({
      agentsPage: !!document.querySelector(".ag-page"),
      chatThread: !!document.querySelector(".thread-scroll"),
    }))()`);
    log("after-proj-row-click", JSON.stringify({ clicked: projRow, ...afterProj }));
    if (afterProj.agentsPage) {
      log("BUG 复现：项目行点击后仍在 agents 视图");
      process.exit(1);
    }
    log("PASS: 会话行 + 项目行点击都回到 chat 视图");
    process.exit(0);
  } catch (e) {
    log("FAIL: " + e.message);
    process.exit(1);
  }
});
