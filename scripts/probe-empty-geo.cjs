// 探针：空态几何（tabbar / thread-scroll / composer / empty-state 的位置关系）
const { app, BrowserWindow } = require("electron");
const fs = require("fs");
const log = (...a) => process.stderr.write(a.join(" ") + "\n");
const OUT = "C:\\Users\\xzy\\Desktop\\my\\lxcode\\temp\\empty-geo.json";
const url = process.env.LXCODE_PREVIEW_URL || "http://127.0.0.1:5190/?mode=demo";
app.whenReady().then(async () => {
  const win = new BrowserWindow({ width: 1440, height: 900, show: true });
  try {
    await win.loadURL(url);
    await win.webContents.executeJavaScript("window.__LX_TEST_MOTION_OFF__ = true");
    await new Promise((r) => setTimeout(r, 1200));
    const geo = await win.webContents.executeJavaScript(`(() => {
      const box = (s) => { const el = document.querySelector(s); if (!el) return null; const b = el.getBoundingClientRect(); return { top: Math.round(b.top), bottom: Math.round(b.bottom), h: Math.round(b.height) }; };
      const ts = document.querySelector(".thread-scroll");
      const main = document.querySelector(".main");
      return {
        tabbar: box(".tabbar"),
        main: box(".main"),
        threadScroll: box(".thread-scroll"),
        empty: box(".empty-state"),
        composer: box(".composer-zone"),
        tsPadTop: ts ? getComputedStyle(ts).paddingTop : null,
        tsPadBottom: ts ? getComputedStyle(ts).paddingBottom : null,
        composerVar: main ? main.style.getPropertyValue("--composer-h") : null,
        tsDisplay: ts ? getComputedStyle(ts).display : null,
        tabs: document.querySelectorAll(".tab").length,
      };
    })()`);
    fs.writeFileSync(OUT, JSON.stringify(geo, null, 1), "utf8");
    log("WROTE " + OUT);
    process.exit(0);
  } catch (e) {
    log("FAIL " + e.message);
    process.exit(1);
  }
});
