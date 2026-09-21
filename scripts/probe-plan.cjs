// 探针：任务清单卡（aicss TodoList）在输入框上方的几何与可见性
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
    // 等清单卡（demo ~5s）
    let has = false;
    for (let i = 0; i < 40 && !has; i++) {
      await new Promise((r) => setTimeout(r, 300));
      has = await win.webContents.executeJavaScript(`!!document.querySelector(".composer-plan")`);
    }
    const geo = await win.webContents.executeJavaScript(`(() => {
      const box = (s) => { const el = document.querySelector(s); if (!el) return null; const b = el.getBoundingClientRect(); return { top: Math.round(b.top), bottom: Math.round(b.bottom), left: Math.round(b.left), w: Math.round(b.width), h: Math.round(b.height) }; };
      const card = document.querySelector(".composer-plan > div");
      return {
        composerPlan: box(".composer-plan"),
        card: box(".composer-plan > div"),
        pi: box(".pi"),
        threadScroll: box(".thread-scroll"),
        cardBg: card ? getComputedStyle(card).backgroundColor : null,
        cardRadius: card ? getComputedStyle(card).borderRadius : null,
        cardText: card ? card.textContent.slice(0, 60) : null,
        cardAboveInput: (() => { const c = document.querySelector(".composer-plan > div"), i = document.querySelector(".pi"); return c && i ? c.getBoundingClientRect().bottom <= i.getBoundingClientRect().top + 2 : null; })(),
        todoInThread: document.querySelectorAll(".thread-scroll .todo").length,
      };
    })()`);
    log("GEO " + JSON.stringify(geo, null, 1));
    process.exit(0);
  } catch (e) {
    log("FAIL " + e.message);
    process.exit(1);
  }
});
