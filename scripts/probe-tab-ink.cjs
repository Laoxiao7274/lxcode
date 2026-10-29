// 探针：真实后端下的标签条（有活动标签）逐行墨迹——活动/非活动都量
const { app, BrowserWindow } = require("electron");
const log = (...a) => process.stderr.write(a.join(" ") + "\n");
const url = process.env.LXCODE_PREVIEW_URL || "http://127.0.0.1:5190/";

function inkRows(img, threshold = 170) {
  const { width, height } = img.getSize();
  const buf = img.toBitmap();
  const out = [];
  for (let y = 0; y < height; y++) {
    let ink = 0;
    for (let x = 0; x < width; x++) {
      const i = (y * width + x) * 4;
      const lum = 0.114 * buf[i] + 0.587 * buf[i + 1] + 0.299 * buf[i + 2];
      if (lum < threshold) ink++;
    }
    out.push(ink);
  }
  return out;
}

app.whenReady().then(async () => {
  const win = new BrowserWindow({ width: 1440, height: 900, show: true });
  try {
    await win.loadURL(url);
    await win.webContents.executeJavaScript("window.__LX_TEST_MOTION_OFF__ = true");
    // 等标签条（连真实后端，session.list 回来才有）
    for (let i = 0; i < 60; i++) {
      await new Promise((r) => setTimeout(r, 400));
      const has = await win.webContents.executeJavaScript(`!!document.querySelector(".tab")`);
      if (has) break;
    }
    const info = await win.webContents.executeJavaScript(`(() => {
      const r = (e) => e ? { top: +e.getBoundingClientRect().top.toFixed(1), bottom: +e.getBoundingClientRect().bottom.toFixed(1), h: +e.getBoundingClientRect().height.toFixed(1), left: +e.getBoundingClientRect().left.toFixed(1), w: +e.getBoundingClientRect().width.toFixed(1) } : null;
      const bar = document.querySelector(".tabbar");
      const tabs = [...document.querySelectorAll(".tab")];
      const act = document.querySelector(".tab.on");
      return {
        bar: r(bar),
        count: tabs.length,
        active: act ? { box: r(act), title: act.querySelector(".tab-title")?.textContent, titleBox: r(act.querySelector(".tab-title")) } : null,
        first: tabs[0] ? { box: r(tabs[0]), title: tabs[0].querySelector(".tab-title")?.textContent, titleBox: r(tabs[0].querySelector(".tab-title")), cls: tabs[0].className } : null,
        last: tabs.length > 1 ? { box: r(tabs[tabs.length - 1]), title: tabs[tabs.length - 1].querySelector(".tab-title")?.textContent } : null,
      };
    })()`);
    log("BOXES " + JSON.stringify(info));
    const bar = await win.webContents.executeJavaScript(`(() => { const b = document.querySelector(".tabbar").getBoundingClientRect(); return { x: Math.round(b.left), y: Math.round(b.top), width: Math.round(b.width), height: Math.round(b.height) }; })()`);
    const img = await win.webContents.capturePage(bar);
    const rows = inkRows(img);
    log("BAR " + JSON.stringify(bar));
    log("INK-ROWS " + rows.map((n, i) => `${i}:${n}`).join(" "));
    process.exit(0);
  } catch (e) {
    log("FAIL " + e.message);
    process.exit(1);
  }
});
