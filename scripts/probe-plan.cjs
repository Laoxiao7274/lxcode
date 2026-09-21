// 探针：清单卡展开/折叠时线程区预留是否跟随（不遮挡对话）
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
    const measure = () => win.webContents.executeJavaScript(`(() => {
      const ts = document.querySelector(".thread-scroll");
      const cz = document.querySelector(".composer-zone");
      const card = document.querySelector(".composer-plan > div");
      const main = document.querySelector(".main");
      return {
        composerH: cz ? Math.round(cz.getBoundingClientRect().height) : null,
        cardH: card ? Math.round(card.getBoundingClientRect().height) : null,
        varValue: main ? main.style.getPropertyValue("--composer-h") : null,
        padBottom: ts ? getComputedStyle(ts).paddingBottom : null,
        threadInnerBottom: (() => {
          const inner = document.querySelector(".thread");
          return inner ? Math.round(inner.getBoundingClientRect().bottom) : null;
        })(),
        composerTop: cz ? Math.round(cz.getBoundingClientRect().top) : null,
      };
    })()`);
    log("EXPANDED " + JSON.stringify(await measure()));
    // 折叠清单卡（点头部）
    await win.webContents.executeJavaScript(`(() => {
      const head = document.querySelector(".composer-plan button");
      if (head) head.click();
    })()`);
    await new Promise((r) => setTimeout(r, 800));
    log("COLLAPSED " + JSON.stringify(await measure()));
    process.exit(0);
  } catch (e) {
    log("FAIL " + e.message);
    process.exit(1);
  }
});
