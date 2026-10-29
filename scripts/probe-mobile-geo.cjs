// 探针：mobile 几何（tabbar 后的布局）
const { app, BrowserWindow } = require("electron");
const url = process.env.LXCODE_PREVIEW_URL || "http://127.0.0.1:5190/?mode=demo";
const log = (...a) => process.stderr.write(a.join(" ") + "\n");
app.whenReady().then(async () => {
  const win = new BrowserWindow({ width: 600, height: 800, show: false });
  try {
    await win.loadURL(url);
    await win.webContents.executeJavaScript("window.__LX_TEST_MOTION_OFF__ = true");
    const r = await win.webContents.executeJavaScript(`(() => {
      const g = (s) => { const el = document.querySelector(s); if (!el) return null; const b = el.getBoundingClientRect(); return { x: Math.round(b.x), y: Math.round(b.y), w: Math.round(b.width), h: Math.round(b.height), right: Math.round(b.right) }; };
      return {
        app: g(".app"), tabbar: g(".tabbar"), strip: g(".tab-strip"),
        sidebar: g(".sidebar"), main: g(".main"), topbar: g(".topbar"),
        scrollW: document.documentElement.scrollWidth,
      };
    })()`);
    log(JSON.stringify(r, null, 1));
    process.exit(0);
  } catch (e) {
    log("FAIL " + e.message);
    process.exit(1);
  }
});
