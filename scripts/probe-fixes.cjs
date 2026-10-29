// 探针：三项前端修复的实测
// 1) 任务清单不进对话流（thread 内无 todo 卡；plan-bar 在输入框上方）
// 2) dispatch 期间无残留闪烁光标（streaming assistant 已定格）
// 3) 确认放行后审批卡就地变工具行
const { app, BrowserWindow } = require("electron");
const log = (...a) => process.stderr.write(a.join(" ") + "\n");
const url = process.env.LXCODE_PREVIEW_URL || "http://127.0.0.1:5190/?mode=demo";
app.whenReady().then(async () => {
  const win = new BrowserWindow({ width: 1440, height: 900, show: false });
  try {
    await win.loadURL(url);
    await win.webContents.executeJavaScript("window.__LX_TEST_MOTION_OFF__ = true");
    await win.webContents.executeJavaScript(`(() => {
      const ta = document.querySelector(".piInput");
      const setter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, "value").set;
      setter.call(ta, "给工具循环加个超时保护");
      ta.dispatchEvent(new Event("input", { bubbles: true }));
      document.querySelector(".send-btn").click();
    })()`);
    // 等 dispatch 卡（demo ~6s）
    let sawDispatch = false;
    for (let i = 0; i < 40 && !sawDispatch; i++) {
      await new Promise((r) => setTimeout(r, 300));
      sawDispatch = await win.webContents.executeJavaScript(`!!document.querySelector(".dispatch-card")`);
    }
    // 检查 1 + 2：thread 内 todo 卡数、streaming 残留
    const check12 = await win.webContents.executeJavaScript(`(() => {
      const thread = document.querySelector(".thread-scroll");
      const streaming = document.querySelectorAll(".thread-scroll .cursor, .thread-scroll .blink, .thread-scroll [data-streaming]");
      // assistant 块的 streaming 态：找 .answer-body.streaming 或类似
      const streamingBlocks = document.querySelectorAll(".thread-scroll .streaming");
      return {
        dispatchCard: !!document.querySelector(".dispatch-card"),
        todoInThread: thread ? thread.querySelectorAll(".todo-list").length : -1,
        planBar: !!document.querySelector(".plan-bar"),
        planAboveInput: (() => {
          const p = document.querySelector(".plan-bar"), i = document.querySelector(".pi");
          if (!p || !i) return null;
          return p.getBoundingClientRect().bottom <= i.getBoundingClientRect().top + 2;
        })(),
        streamingInThread: streamingBlocks.length,
        streamingEls: [...streamingBlocks].map((e) => e.className).slice(0, 3),
      };
    })()`);
    log("CHECK-1-2 " + JSON.stringify(check12));
    // 等确认卡（子 Agent 的 bash）
    let confirm = false;
    for (let i = 0; i < 60 && !confirm; i++) {
      await new Promise((r) => setTimeout(r, 300));
      confirm = await win.webContents.executeJavaScript(`!!document.querySelector('[data-variant="command"]')`);
    }
    const inCard = await win.webContents.executeJavaScript(`(() => {
      const c = document.querySelector('[data-variant="command"]');
      return c ? !!c.closest(".dispatch-card") : null;
    })()`);
    log("CHECK-4 confirmInDispatchCard " + inCard);
    // 点允许
    await win.webContents.executeJavaScript(`(() => {
      const btn = document.querySelector('[data-variant="command"]:not([data-resolved]) button:nth-of-type(2)');
      if (btn) btn.click();
    })()`);
    await new Promise((r) => setTimeout(r, 1200));
    // 检查 3：审批卡应消失 → 变成工具行（trow）
    const check3 = await win.webContents.executeJavaScript(`(() => ({
      approvalCards: document.querySelectorAll('[data-variant="command"]').length,
      toolRows: document.querySelectorAll(".trow").length,
      toolRowNames: [...document.querySelectorAll(".trow")].map((e) => e.textContent?.slice(0, 30)),
    }))()`);
    log("CHECK-3 " + JSON.stringify(check3));
    process.exit(0);
  } catch (e) {
    log("FAIL " + e.message);
    process.exit(1);
  }
});
