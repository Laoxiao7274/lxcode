// 探针：点标签不改顺序（浏览器语义）——记录切换前后的标签标题序列
const { app, BrowserWindow } = require("electron");
const fs = require("fs");
const log = (...a) => process.stderr.write(a.join(" ") + "\n");
const OUT = "C:\\Users\\xzy\\Desktop\\my\\lxcode\\temp\\tab-order-report.json";
const url = process.env.LXCODE_PREVIEW_URL || "http://127.0.0.1:5190/";

const readTabs = (win) => win.webContents.executeJavaScript(`(() => {
  const tabs = [...document.querySelectorAll(".tab")];
  return {
    titles: tabs.map((t) => t.querySelector(".tab-title")?.textContent),
    activeIdx: tabs.findIndex((t) => t.classList.contains("on")),
  };
})()`);

app.whenReady().then(async () => {
  const report = {};
  try {
    const win = new BrowserWindow({ width: 1440, height: 900, show: true });
    await win.loadURL(url);
    await win.webContents.executeJavaScript("window.__LX_TEST_MOTION_OFF__ = true");
    for (let i = 0; i < 60; i++) {
      await new Promise((r) => setTimeout(r, 400));
      const has = await win.webContents.executeJavaScript(`document.querySelectorAll(".tab").length >= 2`);
      if (has) break;
    }
    report.before = await readTabs(win);
    // 点中间那个标签（不是第一个、不是最后一个）
    const clicked = await win.webContents.executeJavaScript(`(() => {
      const tabs = [...document.querySelectorAll(".tab")];
      const idx = Math.max(0, Math.floor(tabs.length / 2));
      tabs[idx].querySelector(".tab-main").click();
      return { idx, title: tabs[idx].querySelector(".tab-title")?.textContent };
    })()`);
    report.clicked = clicked;
    await new Promise((r) => setTimeout(r, 1200));
    report.after = await readTabs(win);
    // 再点另一个（第一个）
    await win.webContents.executeJavaScript(`(() => { const t = document.querySelector(".tab"); if (t) t.querySelector(".tab-main").click(); })()`);
    await new Promise((r) => setTimeout(r, 1200));
    report.after2 = await readTabs(win);
    report.orderStable = JSON.stringify(report.before.titles) === JSON.stringify(report.after.titles) &&
      JSON.stringify(report.after.titles) === JSON.stringify(report.after2.titles);
    report.activeMoved = report.before.activeIdx !== report.after.activeIdx;
    fs.writeFileSync(OUT, JSON.stringify(report, null, 1), "utf8");
    log("WROTE " + OUT + " orderStable=" + report.orderStable + " activeMoved=" + report.activeMoved);
    process.exit(0);
  } catch (e) {
    fs.writeFileSync(OUT, JSON.stringify({ fail: e.message, report }, null, 1), "utf8");
    log("FAIL " + e.message);
    process.exit(1);
  }
});
