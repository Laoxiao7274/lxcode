// 抓用户正在运行的 lxcode 窗口真实像素（desktopCapturer），扫描标签条逐行墨迹。
// 结果写 JSON 文件（PowerShell 的 2> 重定向会把长行截断在 ~120 字符）。
const { app, desktopCapturer } = require("electron");
const fs = require("fs");
const log = (...a) => process.stderr.write(a.join(" ") + "\n");
const OUT = "C:\\Users\\xzy\\Desktop\\my\\lxcode\\temp\\uw-report.json";

function inkRows(img, x0, x1, y0, y1, threshold = 170) {
  const size = img.getSize();
  const buf = img.toBitmap();
  const rows = [];
  for (let y = y0; y < Math.min(y1, size.height); y++) {
    let ink = 0;
    for (let x = x0; x < Math.min(x1, size.width); x++) {
      const i = (y * size.width + x) * 4;
      const lum = 0.114 * buf[i] + 0.587 * buf[i + 1] + 0.299 * buf[i + 2];
      if (lum < threshold) ink++;
    }
    rows.push({ y, ink });
  }
  return { size, rows };
}

app.whenReady().then(async () => {
  try {
    const sources = await desktopCapturer.getSources({ types: ["window"], thumbnailSize: { width: 1600, height: 1000 } });
    const target = sources.find((s) => /lxcode/i.test(s.name));
    if (!target) { log("FAIL no lxcode window"); process.exit(1); }
    const img = target.thumbnail;
    const size = img.getSize();
    const scale = size.width / 1440; // 窗口 1440 宽 → 缩略图宽度的缩放比
    const report = {
      source: target.name,
      thumb: size,
      scale,
      windowCss: { w: 1440, h: 900 },
      bands: {
        topbar: inkRows(img, 0, 900, 0, Math.round(48 * scale)),
        tabbar: inkRows(img, 0, 900, Math.round(40 * scale), Math.round(86 * scale)),
        // 只扫第一个标签宽度，避免其它标签干扰
        firstTab: inkRows(img, 0, Math.round(200 * scale), Math.round(40 * scale), Math.round(86 * scale)),
      },
    };
    fs.writeFileSync(OUT, JSON.stringify(report, null, 1), "utf8");
    log("WROTE " + OUT);
    process.exit(0);
  } catch (e) {
    log("FAIL " + e.message);
    process.exit(1);
  }
});
