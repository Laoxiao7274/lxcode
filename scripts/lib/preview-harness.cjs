// 预览布局检查的骨架：Electron 引导、窗口生命周期、量测脚本与收尾。
// 与场景分开的理由：骨架只回答「怎么把窗口跑起来、怎么量、怎么退」，场景
//（check-preview-layout.cjs）只回答「量什么、断言什么」——混在一个文件里时，
// 任何一次场景改动都要同时读懂两层。
//
// 实测注意（踩过的坑，别再踩）：
// - Windows 上 Electron 主进程的 stdout 在管道下可能丢失（只丢 stdout 不丢
//   stderr），所以所有输出走 stderr；app.exit 前留冲刷时间。
// - 同实例「销毁窗口再建新窗口」会让第二次 loadURL 网络层 ERR_FAILED——
//   因此全程复用同一个窗口，逐场景改尺寸/UA 后重载。
// - 隐藏窗口（show:false）里 rAF 不触发，测量不等待；getBoundingClientRect
//   本就强制同步排布。
const { app, BrowserWindow } = require("electron");
const assert = require("node:assert/strict");

const url = process.env.LXCODE_PREVIEW_URL || "http://127.0.0.1:5190/?mode=demo";
const log = (...a) => process.stderr.write(a.join(" ") + "\n");

// 全局兜底超时（含 dispatch 全链路冒烟 ~40s——纯结构检查时代是 30s）
const TIMEOUT_MS = 120000;

// measureScript：隐藏窗口里的布局量测。返回各关键区域的盒模型与少量诊断值。
const measureScript = `(() => {
  const box = (selector) => {
    const el = document.querySelector(selector);
    if (!el) return null;
    const r = el.getBoundingClientRect();
    return { x: r.x, y: r.y, width: r.width, height: r.height, right: r.right, bottom: r.bottom, display: getComputedStyle(el).display };
  };
  return {
    outer: box(".app-window"),
    stage: box(".window-stage"),
    app: box(".app"),
    topbar: box(".topbar"),
    sidebar: box(".sidebar"),
    main: box(".main"),
    workspaceTabs: box(".workspace-tab-strip"),
    sessionTabs: box(".session-tab-strip"),
    input: box("textarea"),
  };
})()`;

/** withPreview：跑起一个隐藏窗口，把骨架交给 run(ctx)，并负责收尾（含控制台错误兜底）。 */
function withPreview(run) {
  const timeout = setTimeout(() => { log("预览布局检查超时"); app.exit(1); }, TIMEOUT_MS);
  const finish = (code) => { clearTimeout(timeout); setTimeout(() => app.exit(code), 150); };
  app.whenReady().then(async () => {
    const errors = [];
    const win = new BrowserWindow({
      width: 1200, height: 800, useContentSize: true, show: false,
      webPreferences: { contextIsolation: true, nodeIntegration: false },
    });
    const electronUA = win.webContents.getUserAgent();
    const browserUA = electronUA.replace(/Electron\/\S+/g, "");
    // 控制台错误：**立即打出来**再收进 errors。
    // 只收集不打的话，后面任何一个断言失败都会先抛，控制台里那条真错误永远读不到——
    // 而"Script failed to execute"这种话本身不说明任何原因（实测踩过）。
    win.webContents.on("console-message", (ev) => {
      if (ev.level !== 3) return;
      errors.push(ev.message);
      log("[console:error]", String(ev.message).slice(0, 800));
    });
    const measure = () => win.webContents.executeJavaScript(measureScript);
    try {
      await run({ win, log, measure, errors, url, electronUA, browserUA });
      assert.deepEqual(errors, [], "页面不应出现控制台错误");
      log("PASS: desktop / mobile / shell layout");
      finish(0);
    } catch (error) {
      log(String((error && error.stack) || error));
      finish(1);
    }
  });
}

module.exports = { withPreview };
