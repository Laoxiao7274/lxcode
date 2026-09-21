// 探针：任务清单相关元素的真实几何（PlanBar / 内联 todo 卡 / 输入区）
const { app, BrowserWindow } = require("electron");
const log = (...a) => process.stderr.write(a.join(" ") + "\n");
const url = process.env.LXCODE_PREVIEW_URL || "http://127.0.0.1:5190/?mode=demo";
app.whenReady().then(async () => {
  const win = new BrowserWindow({ width: 1440, height: 900, show: false });
  try {
    await win.loadURL(url);
    await win.webContents.executeJavaScript("window.__LX_TEST_MOTION_OFF__ = true");
    // 发消息触发 demo 编排
    await win.webContents.executeJavaScript(`(() => {
      const ta = document.querySelector(".piInput");
      const setter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, "value").set;
      setter.call(ta, "给工具循环加个超时保护");
      ta.dispatchEvent(new Event("input", { bubbles: true }));
      document.querySelector(".send-btn").click();
    })()`);
    // 等 todo 出现（demo ~5s）
    for (let i = 0; i < 40; i++) {
      await new Promise((r) => setTimeout(r, 300));
      const has = await win.webContents.executeJavaScript(`!!document.querySelector(".plan-bar")`);
      if (has) break;
    }
    const geo = await win.webContents.executeJavaScript(`(() => {
      const box = (s) => {
        const el = document.querySelector(s);
        if (!el) return null;
        const b = el.getBoundingClientRect();
        return { top: Math.round(b.top), bottom: Math.round(b.bottom), left: Math.round(b.left), w: Math.round(b.width), h: Math.round(b.height) };
      };
      const app = document.querySelector(".app");
      return {
        appH: app ? Math.round(app.getBoundingClientRect().height) : 0,
        tabbar: box(".tabbar"),
        threadScroll: box(".thread-scroll"),
        planBar: box(".plan-bar"),
        composer: box(".composer-zone"),
        pi: box(".pi"),
        planSummaryH: (() => { const el = document.querySelector(".plan-summary"); return el ? Math.round(el.getBoundingClientRect().height) : null; })(),
        planText: document.querySelector(".plan-summary")?.textContent,
        inlineTodoCards: document.querySelectorAll(".todo-list, .todo-card").length,
        composerChildren: [...(document.querySelector(".main")?.children ?? [])].map((c) => c.className),
      };
    })()`);
    log("GEO " + JSON.stringify(geo, null, 1));
    process.exit(0);
  } catch (e) {
    log("FAIL " + e.message);
    process.exit(1);
  }
});
