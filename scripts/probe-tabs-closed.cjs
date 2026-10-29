// 探针：关掉全部标签后布局是否稳定（标签条位置保留、主区/输入区不上跳）
const { app, BrowserWindow } = require("electron");
const fs = require("fs");
const log = (...a) => process.stderr.write(a.join(" ") + "\n");
const OUT = "C:\\Users\\xzy\\Desktop\\my\\lxcode\\temp\\tabs-closed-report.json";
const url = process.env.LXCODE_PREVIEW_URL || "http://127.0.0.1:5190/?mode=demo";

const geo = (win) => win.webContents.executeJavaScript(`(() => {
  const box = (s) => { const el = document.querySelector(s); if (!el) return null; const b = el.getBoundingClientRect(); return { top: Math.round(b.top), bottom: Math.round(b.bottom), h: Math.round(b.height) }; };
  return {
    bar: box(".tabbar"),
    barVisible: !!document.querySelector(".tabbar"),
    tabs: document.querySelectorAll(".tab").length,
    newBtn: !!document.querySelector(".tab-new"),
    main: box(".main"),
    thread: box(".thread-scroll"),
    composer: box(".composer-zone"),
    pi: box(".pi"),
    empty: box(".empty-state"),
  };
})()`);

app.whenReady().then(async () => {
  const report = {};
  const win = new BrowserWindow({ width: 1440, height: 900, show: true });
  try {
    await win.loadURL(url);
    await win.webContents.executeJavaScript("window.__LX_TEST_MOTION_OFF__ = true");
    await new Promise((r) => setTimeout(r, 1500));
    report.before = await geo(win);
    // 反复点第一个标签的 ×，直到没有标签可点（关当前会切新对话，故循环上限保护）
    for (let round = 0; round < 12; round++) {
      const closed = await win.webContents.executeJavaScript(`(() => {
        const btn = document.querySelector(".tab .tab-close");
        if (!btn) return false;
        btn.click();
        return true;
      })()`);
      if (!closed) break;
      await new Promise((r) => setTimeout(r, 700));
    }
    report.after = await geo(win);
    // 判定：标签条仍在（占位保留），主区/输入区位置不因标签消失而上跳
    report.barStays = !!report.after.bar && report.after.bar.h === 36;
    report.mainStable = report.before.main.top === report.after.main.top && report.before.main.h === report.after.main.h;
    report.composerStable = report.before.composer.top === report.after.composer.top;
    fs.writeFileSync(OUT, JSON.stringify(report, null, 1), "utf8");
    log(`WROTE barStays=${report.barStays} mainStable=${report.mainStable} composerStable=${report.composerStable}`);
    process.exit(0);
  } catch (e) {
    fs.writeFileSync(OUT, JSON.stringify({ fail: e.message, report }, null, 1), "utf8");
    log("FAIL " + e.message);
    process.exit(1);
  }
});
