// 探针：标签条几何与交互（药丸形态 + 关闭钮 hover 显隐）
const { app, BrowserWindow } = require("electron");
const log = (...a) => process.stderr.write(a.join(" ") + "\n");
const url = process.env.LXCODE_PREVIEW_URL || "http://127.0.0.1:5190/?mode=demo";
app.whenReady().then(async () => {
  const win = new BrowserWindow({ width: 1440, height: 900, show: false });
  try {
    await win.loadURL(url);
    await win.webContents.executeJavaScript("window.__LX_TEST_MOTION_OFF__ = true");
    const geo = await win.webContents.executeJavaScript(`(() => {
      try {
        const bar = document.querySelector(".tabbar");
        const tab = document.querySelector(".tab");
        const on = document.querySelector(".tab.on");
        const closeBtn = document.querySelector(".tab .tab-close");
        if (!bar) return { error: "no-tabbar", sessions: document.querySelectorAll(".session-item").length };
        if (!tab) return { error: "no-tab", barH: bar.getBoundingClientRect().height };
        const r = (el) => { const b = el.getBoundingClientRect(); return { w: Math.round(b.width), h: Math.round(b.height) }; };
        return {
          bar: r(bar), tab: r(tab), on: on ? r(on) : null,
          closeOpacity: closeBtn ? getComputedStyle(closeBtn).opacity : "none",
          pillRadius: getComputedStyle(tab).borderRadius,
          tabs: document.querySelectorAll(".tab").length,
          newBtn: !!document.querySelector(".tab-new"),
        };
      } catch (e) { return { error: String(e) }; }
    })()`);
    log("tab-geo", JSON.stringify(geo));
    process.exit(0);
  } catch (e) {
    log("FAIL " + e.message);
    process.exit(1);
  }
});
