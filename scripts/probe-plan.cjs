// 探针：清单卡展开/收缩时对话内容是否跟随（贴底 → 内容整体上移/下移）
const { app, BrowserWindow } = require("electron");
const log = (...a) => process.stderr.write(a.join(" ") + "\n");
const url = process.env.LXCODE_PREVIEW_URL || "http://127.0.0.1:5190/?mode=demo";
app.whenReady().then(async () => {
  const win = new BrowserWindow({ width: 1440, height: 900, show: true });
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
    for (let i = 0; i < 40; i++) {
      await new Promise((r) => setTimeout(r, 300));
      const has = await win.webContents.executeJavaScript(`!!document.querySelector(".composer-plan")`);
      if (has) break;
    }
    // 等 demo 本轮跑完（内容稳定——流式追加期间测量会被内容增长污染）
    for (let i = 0; i < 120; i++) {
      const idle = await win.webContents.executeJavaScript(`!document.querySelector(".busy-row")`);
      if (idle) break;
      await new Promise((r) => setTimeout(r, 400));
    }
    await new Promise((r) => setTimeout(r, 600));
    const measure = () => win.webContents.executeJavaScript(`(() => {
      const ts = document.querySelector(".thread-scroll");
      const cz = document.querySelector(".composer-zone");
      const last = document.querySelector(".thread > *:last-child");
      const head = document.querySelector(".composer-plan button");
      return {
        cardExpanded: head ? head.getAttribute("aria-expanded") : null,
        composerH: cz ? Math.round(cz.getBoundingClientRect().height) : null,
        padBottom: ts ? Math.round(parseFloat(getComputedStyle(ts).paddingBottom)) : null,
        scrollTop: ts ? Math.round(ts.scrollTop) : null,
        maxScroll: ts ? Math.round(ts.scrollHeight - ts.clientHeight) : null,
        atBottom: ts ? ts.scrollHeight - ts.scrollTop - ts.clientHeight < 4 : null,
        lastBlockBottom: last ? Math.round(last.getBoundingClientRect().bottom) : null,
      };
    })()`);
    const expanded = await measure();
    log("S1 " + JSON.stringify(expanded));
    // 折叠（点卡片头部按钮）
    await win.webContents.executeJavaScript(`(() => { const h = document.querySelector(".composer-plan button"); if (h) h.click(); })()`);
    await new Promise((r) => setTimeout(r, 900));
    const collapsed = await measure();
    log("COLLAPSED " + JSON.stringify(collapsed));
    // 再展开
    await win.webContents.executeJavaScript(`(() => { const h = document.querySelector(".composer-plan button"); if (h) h.click(); })()`);
    await new Promise((r) => setTimeout(r, 900));
    const reexpanded = await measure();
    log("REEXPANDED " + JSON.stringify(reexpanded));
    // 判定：贴底始终成立，且内容块底边随输入区高度变化移动
    const pinnedOk = collapsed.atBottom && reexpanded.atBottom;
    const moved = collapsed.lastBlockBottom !== reexpanded.lastBlockBottom;
    log(`VERDICT pinnedOk=${pinnedOk} moved=${moved} (collapsed last=${collapsed.lastBlockBottom} reexpanded last=${reexpanded.lastBlockBottom})`);
    process.exit(0);
  } catch (e) {
    log("FAIL " + e.message);
    process.exit(1);
  }
});
