// main.ts —— Electron 壳主进程：只做窗口/单实例/sidecar 生命周期，不放业务
// （AGENTS.md §2.1 Electron 侧工程纪律）。渲染层复用 frontend/ 同一套
// React 应用，经 WS 直连 127.0.0.1:7789，与浏览器/CLI 客户端同权。
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import { app, BrowserWindow, ipcMain, net, protocol } from "electron";
import { ensureBackend, shutdownBackend } from "./sidecar";

// userData 目录名与应用身份（单实例锁、任务栏、通知都吃这个）
app.setName("lxcode");

// 产线渲染层经 app:// 特权协议加载（必须 ready 前注册）：vite 产物是
// <script type="module" crossorigin>，file:// 是不透明源（null），模块脚本
// 的同源检查会失败——React 根本不挂载、页面空白且无报错（did-fail-load
// 只管主帧导航，资源级失败静默）。app:// 注册为 standard+secure 后，
// 渲染层从 asar 正常加载，模块脚本是同源请求。
protocol.registerSchemesAsPrivileged([
  { scheme: "app", privileges: { standard: true, secure: true } },
]);

// 关 Chromium 子进程沙箱（--no-sandbox）：本应用从部分上下文（如被
// Windows Job Object 包住的宿主进程，实测 DSH 后台 job）拉起时，Chromium
// 沙箱与外层 Job Object 冲突，GPU 子进程 STATUS_BREAKPOINT 崩溃循环直至
// 整个应用 FATAL。安全面可接受：单用户本机应用，渲染层零远程内容
// （asar 本地文件 / localhost dev server），不面向互联网。若未来引入
// 远程内容渲染，必须重新评估此开关。
app.commandLine.appendSwitch("no-sandbox");

let win: BrowserWindow | null = null;

// 单实例：第二个实例只聚焦已有窗口（也避免双壳竞态 spawn 后端）
if (!app.requestSingleInstanceLock()) {
  app.quit();
} else {
  app.on("second-instance", () => win?.focus());

  app.whenReady().then(async () => {
    app.setAppUserModelId("com.moyunteng.lxcode");

    // app:// → asar 内渲染层（app://lxcode/index.html → renderer/index.html）
    protocol.handle("app", (req) => {
      const { pathname } = new URL(req.url);
      const fp = join(__dirname, "..", "renderer", decodeURIComponent(pathname));
      return net.fetch(pathToFileURL(fp).toString());
    });

    // 后端先行（探测→直连或拉起），窗口加载时渲染层 WSAgent 即可连上
    await ensureBackend();

    win = new BrowserWindow({
      width: 1440,
      height: 900,
      minWidth: 1024,
      minHeight: 700,
      autoHideMenuBar: true,
      title: "Lxcode",
      backgroundColor: "#101014", // 对齐前端暗色主题，避免白闪
      show: false, // 先就绪再显示，避免白窗
      frame: false, // 无系统标题栏——顶部栏由渲染层 Topbar 自绘（拖拽区 + 窗口控制按钮，经 preload 桥 __LX__）
      roundedCorners: true, // Win11 圆角（默认即 true，显式记录）
      icon: join(__dirname, "..", "build", "icon.png"), // 开发态窗口图标；产线用 exe 内嵌图标
      webPreferences: {
        preload: join(__dirname, "preload.js"),
        // contextIsolation 开、node 集成关：渲染层无 Node 能力，只走 WS。
        // （sandbox 名义上 true，但被上面的 --no-sandbox 命令行开关整体
        // 关闭——见文件头注释的理由与安全权衡。）
        contextIsolation: true,
        nodeIntegration: false,
        sandbox: true,
      },
    });
    win.once("ready-to-show", () => win?.show());

    // 窗口控制 IPC（preload 的 __LX__ 桥 → Topbar 按钮）
    ipcMain.on("win:minimize", () => win?.minimize());
    ipcMain.on("win:toggleMaximize", () => {
      if (!win) return;
      win.isMaximized() ? win.unmaximize() : win.maximize();
    });
    ipcMain.on("win:close", () => win?.close());

    // 产线诊断通道：渲染层控制台与加载失败转发到主进程 stdout
    // （打包后无 DevTools 场景排查渲染层问题全靠它）
    win.webContents.on("console-message", (_e, _lvl, msg) => console.log(`[renderer] ${msg}`));
    win.webContents.on("did-fail-load", (_e, code, desc, url) =>
      console.log(`[shell] 加载失败 ${url}: ${code} ${desc}`));

    if (process.env.LXCODE_DEV_URL) {
      // 壳开发模式：加载 vite dev server（scripts/dev.mjs 注入）
      await win.loadURL(process.env.LXCODE_DEV_URL);
      win.webContents.openDevTools({ mode: "detach" });
    } else {
      // 产线：app:// 协议加载 asar 内渲染层（见文件头注释）
      await win.loadURL("app://lxcode/index.html");
    }
    console.log("[shell] 窗口已加载");
  });

  app.on("window-all-closed", () => app.quit()); // Windows 惯例：关窗即退出
  app.on("will-quit", () => shutdownBackend());
}
