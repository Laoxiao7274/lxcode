// main.ts —— Electron 壳主进程：只做窗口/单实例/sidecar 生命周期，不放业务
// （AGENTS.md §2.1 Electron 侧工程纪律）。渲染层复用 frontend/ 同一套
// React 应用，经 WS 直连 127.0.0.1:7789，与浏览器/CLI 客户端同权。
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import { app, BrowserWindow, dialog, ipcMain, nativeTheme, net, protocol } from "electron";
import { ensureBackend, shutdownBackend } from "./sidecar";
import { initSakura, sakuraHandlers, shutdownSakura } from "./sakura";
import { initTailscale, tailscaleHandlers } from "./tailscale";
import { cachedState, cachedToken, disable as remoteDisable, enable as remoteEnable, initBackendRemote, refresh as remoteRefresh, rotate as remoteRotate } from "./backend-remote";
import { updateHandlers } from "./updater";
import { createTray } from "./tray";

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

// 开发形态开 CDP 调试口（temp/cdp-*.mjs 验证脚本连它驱动真实窗口；打包形态不开）。
// 端口可经 LXCODE_CDP_PORT 覆盖——多实例并存时（如 e2e 用临时 userData 再拉一个壳）
// 避免与常驻 dev 壳的 9229 撞端口。
if (!app.isPackaged) {
  app.commandLine.appendSwitch("remote-debugging-port", process.env.LXCODE_CDP_PORT ?? "9229");
}

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
    // 远程访问状态（token 等）在窗口加载前取好——渲染层创建 WSAgent 时要
    // 同步取 token 拼 WS URL（sendSync 只读缓存，不阻塞在网络上）
    await initBackendRemote();

    win = new BrowserWindow({
      width: 1440,
      height: 900,
      minWidth: 1024,
      minHeight: 700,
      autoHideMenuBar: true,
      title: "Lxcode",
      // 启动瞬间的窗口底色（页面加载前）：按当前系统亮暗取对应 token 基色，
      // 避免与首帧主题相反的闪块（页面自身的防闪由 index.html 内联脚本负责）。
      backgroundColor: nativeTheme.shouldUseDarkColors ? "#18181c" : "#ffffff",
      show: false, // 先就绪再显示，避免白窗
      frame: false, // 无系统标题栏——顶部栏由渲染层 Topbar 自绘（拖拽区 + 窗口控制按钮，经 preload 桥 __LX__）
      roundedCorners: true, // Win11 圆角（默认即 true，显式记录）
      // 窗口图标：dev 用源文件（shell/build/icon.png）；产线 exe 自带内嵌图标，无需指定
      ...(app.isPackaged ? {} : { icon: join(__dirname, "..", "build", "icon.png") }),
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

    // 鼠标侧键（Windows WM_APPCOMMAND）→ 页面浏览器历史。Electron 把侧键转成
    // app-command 事件后**不做默认导航**；且它的 navigationHistory/goBack **不包含
    // pushState 的同文档条目**（electron#24899：webContents 栈与页面栈是两套，
    // 实测 canGoBack=false 而 CDP 的 Page.getNavigationHistory 满条目）——所以
    // 直接在页面里调 history.back()/forward()：同文档遍历触发 popstate，App 把
    // 工作区视图落回来（App.tsx 的 History API 接线）。页面里调用本身是安全的：
    // 退到首条是 no-op，无需 canGo 判定。
    win.on("app-command", (_e, cmd) => {
      if (cmd !== "browser-backward" && cmd !== "browser-forward") return;
      void win?.webContents
        .executeJavaScript(`history.${cmd === "browser-backward" ? "back" : "forward"}()`)
        .catch(() => { /* 页面正在跳转/销毁时注入失败无害 */ });
    });

    // 窗口控制 IPC（preload 的 __LX__ 桥 → Topbar 按钮）
    ipcMain.on("win:minimize", () => win?.minimize());
    ipcMain.on("win:toggleMaximize", () => {
      if (!win) return;
      win.isMaximized() ? win.unmaximize() : win.maximize();
    });
    ipcMain.on("win:close", () => win?.close());

    // 主题偏好（渲染层设置面板 → shared/theme.ts → 此处）：同步
    // nativeTheme.themeSource，壳侧原生控件/对话框跟随渲染层主题。
    // auto → "system"（Electron 的说法）；未知值回落 system 不抛错。
    ipcMain.on("theme:prefer", (_e, pref: unknown) => {
      nativeTheme.themeSource = pref === "dark" ? "dark" : pref === "light" ? "light" : "system";
    });

    // 目录选择器（添加项目用）：只开系统选择框，返回路径字符串——
    // 渲染层拿不到任何 fs 能力，只是「让用户自己选」的 UI 通道
    ipcMain.handle("dialog:selectDirectory", async () => {
      if (!win) return null;
      const r = await dialog.showOpenDialog(win, { properties: ["openDirectory"] });
      return r.canceled || r.filePaths.length === 0 ? null : r.filePaths[0];
    });

    // 樱花frp 公网穿透（docs/sakurafrp-integration.md）：API/fpc 全在主进程，
    // 渲染层经 invoke 发意图、经 sakura:state 推送收状态。handler 表逐个注册
    //（invoke 的 channel 须在主进程显式声明，批量展开会扩大攻击面）。
    for (const [channel, handler] of Object.entries(sakuraHandlers)) {
      ipcMain.handle(channel, handler as (e: Electron.IpcMainInvokeEvent, ...args: unknown[]) => unknown);
    }
    initSakura((s) => win?.webContents.send("sakura:state", s));

    // Tailscale 模式（远程访问的第二实现——与樱花frp 同构：主进程持状态，
    // 渲染层发意图收推送）
    for (const [channel, handler] of Object.entries(tailscaleHandlers)) {
      ipcMain.handle(channel, handler as (e: Electron.IpcMainInvokeEvent, ...args: unknown[]) => unknown);
    }
    initTailscale((s) => win?.webContents.send("tailscale:state", s));

    // 后端远程访问管理（token 同步取给渲染层拼 WS URL；开关/轮换走 invoke）
    ipcMain.on("backendRemote:tokenSync", (e) => { e.returnValue = cachedToken(); });
    ipcMain.handle("backendRemote:get", () => cachedState() ?? { enabled: false, token: "", lanAddr: "" });
    ipcMain.handle("backendRemote:enable", () => remoteEnable());
    ipcMain.handle("backendRemote:disable", () => remoteDisable());
    ipcMain.handle("backendRemote:rotate", () => remoteRotate());
    ipcMain.handle("backendRemote:refresh", () => remoteRefresh());

    // 自更新（manifest → 下载校验 → 后端热替换 → asar 退出冷替换，见 updater.ts）
    updateHandlers();

    // 系统托盘（关闭 = 隐藏到托盘，后端/隧道保持运行；托盘菜单退出才真退）
    createTray(() => win);

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

  // 托盘常驻：关窗（隐藏到托盘）不退出——真退出走托盘菜单/更新重启
  // （app.quit() 触发 will-quit 的统一收尾）。应用要常驻的后端/隧道因此保持。
  app.on("window-all-closed", () => { /* 不退出：托盘在 */ });
  app.on("will-quit", () => {
    shutdownBackend();
    shutdownSakura(); // frpc 与后端同一条退出路径：壳走，隧道进程跟着收
  });
}
