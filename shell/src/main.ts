// main.ts —— Electron 壳主进程：只做窗口/单实例/sidecar 生命周期，不放业务
// （AGENTS.md §2.1 Electron 侧工程纪律）。渲染层复用 frontend/ 同一套
// React 应用，经 WS 直连 127.0.0.1:7789，与浏览器/CLI 客户端同权。
import { appendFile, readFile, rename, stat } from "node:fs/promises";
import { existsSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import { app, BrowserWindow, crashReporter, dialog, ipcMain, nativeTheme, net, Notification, protocol } from "electron";
import { ensureBackend, shutdownBackend } from "./sidecar";
import { initSakura, sakuraHandlers, shutdownSakura } from "./sakura";
import { initTailscale, tailscaleHandlers } from "./tailscale";
import { cachedState, cachedToken, disable as remoteDisable, enable as remoteEnable, initBackendRemote, refresh as remoteRefresh, rotate as remoteRotate, setAllowControl } from "./backend-remote";
import { startUpdatePolling, stopUpdatePolling, updateHandlers, consumePendingUpdateFailure } from "./updater";
import { createTray } from "./tray";
import { shouldAllowSelfSigned } from "./cert-allow";
import { startScreenPolling, stopScreenPolling, warmUpEncoderProbe } from "./screen";
import { startInputPolling, stopInputPolling, setControllingCallback } from "./input";
import { setTrayHint } from "./tray";
import { consoleLevelName, crashRecovery, fatalMainLine, nextBootCount, rendererLogLine, rendererLogPaths, shouldRotate } from "./renderer-log";

// userData 目录名与应用身份（单实例锁、任务栏、通知都吃这个）
app.setName("lxcode");

// dev 形态判定（scripts/dev.mjs --electron 注入 LXCODE_DEV_USER_DATA）。
// 导出给 tray/screen 拼 dev 后缀 tooltip 用——同一份判定，不各判一遍。
export const devUserData = process.env.LXCODE_DEV_USER_DATA;
export const IS_DEV_SHELL = !!devUserData;
if (devUserData) {
  // 必须同步执行、且在 requestSingleInstanceLock 之前：userData 决定单实例锁
  // 的锁文件位置，重定向后 dev/生产壳的锁互不相干，两壳可并存。独立 userData
  // 同时隔离 models.json/sessions/frpc/更新暂存等一切落盘状态（与
  // dev.mjs 的 devSessionsDir 同一条纪律）。
  app.setPath("userData", devUserData);
}

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

// ===== 主进程死亡取证（2026-12：0.3.60 两次整壳静默死亡，转储零落盘）=====
// 三层覆盖（各有盲区，缺一不可）：
//  * crashReporter → native 层崩溃（段错误/GPU/Chromium FATAL）的 minidump；
//    必须在 app.ready 之前 start（晚了渲染进程不受监控）。落点
//    app.getPath("crashDumps")：Windows 默认 <userData>/Crashpad
//    （打包壳 = %APPDATA%\lxcode\Crashpad，app.setName 已定身份），
//    uploadToServer:false 纯本地留证。boot 行会记下实际路径。
//  * process 兜底 → JS 层 uncaughtException/unhandledRejection（Electron
//    默认只弹框/静默，转储与日志都没有；crashReporter 只截 native 崩溃，
//    JS 异常没触发 native 崩溃就只能靠这里）。走 fatalExit：**先落盘 FATAL
//    行（JS 异常的唯一证据）再 app.exit(1)**——不 rethrow（默认错误框对
//    常驻壳没意义），让用户/外部机制接管重启。
//  * will-quit 的 `graceful quit` 行 → 区分「走了优雅退出」vs「静默死亡」。
// 三类标记（boot #N / graceful quit / FATAL main:）与 minidump 落点记在
// renderer-log.ts 头注（目录地图注释）。
crashReporter.start({ uploadToServer: false, compress: true });

process.on("uncaughtException", (err) => {
  console.error("[shell] 主进程未捕获异常，退出：", err);
  void fatalExit("uncaughtException", err);
});
process.on("unhandledRejection", (reason) => {
  console.error("[shell] 主进程未处理的 Promise 拒绝，退出：", reason);
  void fatalExit("unhandledRejection", reason);
});

// wss 配对连接的自签证书放行（樱花frp 隧道，定向）。
// 背景：隧道 extra 是 auto_https=auto，节点对外 TLS 终结用 frpc **自签**证书；
// 渲染层 WsConnection 的 WebSocket 走 Chromium 网络栈，自签证书默认直接拒绝
// 且无任何可跳过的 UI——同一张配对二维码在桌面端扫（或手填 wss:// 地址）必然
// 连接失败，故按事件定向放行而不是全局 ignore-certificate-errors。
// 放行条件（三条取交集，尽量窄）：
//  * URL 是 wss:// 或 https://（隧道入口的 WS / 附件 origin 两种形态——
//    wsURL 会把 wss 地址用于 WS，httpOrigin 会把它换成 https 用于附件下载）；
//  * host 非回环（回环 = 本机后端，永远是 ws:// 明文，不该走到这里）；
//  * 错误属于自签特征（信任链无效 / 主机名不匹配——frpc 自签证书两者都会中）。
//    Electron 40 实测：事件给的 error 带 `net::` 前缀（wss 握手错误与导航同源
//    报 net::ERR_*），比对前必须剥掉——2026-11 用户「一直重连」的根因就是
//    白名单里存裸名、`Set.has()` 恒 false，放行条件从未命中。
// 其余证书错误（app:// 主帧、公网导航、过期证书等）照常拒绝，行为不变。
// 判定抽在 ./cert-allow.ts（纯函数——main.ts 顶部 import electron，测试没法
// 整体引入本文件；用例见 tests/cert-allow.test.mjs）。
app.on("certificate-error", (event, _webContents, url, error, _certificate, callback) => {
  const allow = shouldAllowSelfSigned(url, error);
  if (allow) event.preventDefault();
  callback(allow);
});

let win: BrowserWindow | null = null;

// ===== 渲染层可观测性 + 崩溃自动恢复（2026-12「跑一段时间全白屏」止血）=====
// 白屏的两条根因（渲染进程崩溃/OOM、React 渲染期未捕获异常卸掉整树）原先都
// 无恢复、无日志：打包壳的 stdout 无处落盘，渲染层 console 全部蒸发。这里把
// 渲染层日志写进 <userData>/renderer.log（判定逻辑在 ./renderer-log.ts，可单测）。
const LOG_PATHS = rendererLogPaths(app.getPath("userData"));
let crashStreak = 0; // 连崩计数（did-finish-load 成功清零；超过上限停止自动 reload）

// 启动计数（闪退循环取证）：boot 序号从 renderer.log / .log.old 两代里的
// `boot #N` 最大值 +1 推出（判定逻辑 nextBootCount 在 ./renderer-log.ts，可单测）。
// 连续多条 boot 而中间无 `graceful quit`，就是闪退循环；boot 行同时记下
// crashDumps 实际路径——崩溃后照着它找 minidump。
void (async () => {
  const lines: string[] = [];
  for (const p of [LOG_PATHS.old, LOG_PATHS.log]) {
    try {
      lines.push(...(await readFile(p, "utf8")).split("\n"));
    } catch { /* 文件不存在/占用——首启或暂时读不到，序号从 1 起即可 */ }
  }
  const n = nextBootCount(lines);
  const dumps = (() => { try { return app.getPath("crashDumps"); } catch { return "(crashDumps 目录未知)"; } })();
  appendRendererLog(rendererLogLine(new Date(), "info",
    `[main] boot #${n} — 主进程启动（minidump 目录：${dumps}）`));
})();

/** 异步追加一行渲染层日志；超 5MB 先两代轮转（.log → .log.old 覆盖）。
 *  常规路径 fire-and-forget 不 await；FATAL 取证路径（fatalExit）await 返回的
 *  Promise 确保落盘后再退出。内部全 catch 不 reject——写失败静默，观测通道
 *  自身不可信源。 */
function appendRendererLog(line: string): Promise<void> {
  return (async () => {
    try {
      if (existsSync(LOG_PATHS.log)) {
        const s = await stat(LOG_PATHS.log);
        if (shouldRotate(s.size)) {
          await rename(LOG_PATHS.log, LOG_PATHS.old).catch(() => { /* .old 被占用等——放弃旧代，直接续写 */ });
        }
      }
      await appendFile(LOG_PATHS.log, line);
    } catch { /* 日志失败静默——观测通道自身不可信源 */ }
  })();
}

/** FATAL 收尾（uncaughtException/unhandledRejection 共用）：先落盘 FATAL 行再
 *  app.exit(1)。**FATAL 行是 JS 异常的唯一证据**——JS 异常不产 minidump
 *  （crashReporter 只截 native 崩溃），这条丢了就零证据，所以必须等写完。
 *  appendRendererLog 内部全 catch 不 reject，这里仍兜 try/catch + 2s 超时：
 *  日志通道挂死（fs 卡住）也必须能退出，不能把进程卡死在崩溃路径上。 */
async function fatalExit(kind: "uncaughtException" | "unhandledRejection", err: unknown): Promise<void> {
  try {
    await Promise.race([
      appendRendererLog(rendererLogLine(new Date(), "error", fatalMainLine(err, kind))),
      new Promise<void>((resolve) => setTimeout(resolve, 2_000)),
    ]);
  } catch { /* 日志失败也要退出 */ }
  app.exit(1);
}

/** 渲染进程崩溃后的自动恢复时序：记日志 → 按连崩退避 reload → 超限停止。
 *  reload 前再查 isLoading：loadURL/did-fail-load 期间（页面根本没起来）不叠
 *  reload——加载在飞时再叠一次会打架；此时本次恢复放弃，不重排定时器（下一次
 *  崩溃会再走这里，用户手动重启兜底）。 */
function scheduleRendererRecovery(details: { reason: string; exitCode: number }): void {
  crashStreak += 1;
  const decision = crashRecovery(crashStreak);
  if (!decision.recover) {
    appendRendererLog(rendererLogLine(new Date(), "error",
      `render-process-gone reason=${details.reason} exitCode=${details.exitCode} —— 连崩 ${crashStreak} 次超过上限，停止自动恢复（请手动重启）`));
    console.log(`[shell] 渲染进程连崩 ${crashStreak} 次，停止自动恢复（reason=${details.reason}）`);
    return;
  }
  appendRendererLog(rendererLogLine(new Date(), "error",
    `render-process-gone reason=${details.reason} exitCode=${details.exitCode} —— ${decision.waitMs}ms 后自动 reload（连崩第 ${crashStreak} 次）`));
  console.log(`[shell] 渲染进程崩溃（reason=${details.reason} exitCode=${details.exitCode}），${decision.waitMs}ms 后自动恢复`);
  setTimeout(() => {
    const w = win;
    if (!w || w.isDestroyed() || w.webContents.isLoading()) return;
    w.webContents.reload();
  }, decision.waitMs);
}

/** 主进程 → 渲染层的统一推送出口：窗口/webContents 已销毁时静默跳过。
 *
 *  为什么必须有：退出路径（will-quit）里 stopUpdatePolling/shutdownSakura/
 *  shutdownBackend 都可能触发最后一次状态推送，而此时窗口已经销毁——直接
 *  `win?.webContents.send` 只挡了 null 挡不住已销毁，send 会抛
 *  「Object has been destroyed」变成未捕获异常弹窗（0.3.3 实测：退出时
 *  stopFrpc → setState → notify → send 崩）。任何退出路径都从这里过。 */
function pushToRenderer(channel: string, ...args: unknown[]): void {
  const w = win;
  if (!w || w.isDestroyed() || w.webContents.isDestroyed()) return;
  w.webContents.send(channel, ...args);
}

// 单实例：第二个实例只聚焦已有窗口（也避免双壳竞态 spawn 后端）
if (!app.requestSingleInstanceLock()) {
  app.quit();
} else {
  app.on("second-instance", () => win?.focus());

  app.whenReady().then(async () => {
    // 任务栏分组身份：dev 壳用独立 AUMID，与生产壳不并入同一任务栏组
    if (IS_DEV_SHELL) app.setAppUserModelId("com.moyunteng.lxcode.dev");
    else app.setAppUserModelId("com.moyunteng.lxcode");

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
      title: IS_DEV_SHELL ? "Lxcode dev" : "Lxcode",
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

    // 鼠标侧键（Windows WM_APPCOMMAND）→ 页面导航。Electron 把侧键转成
    // app-command 事件后**不做默认导航**；且它的 navigationHistory/goBack **不包含
    // pushState 的同文档条目**（electron#24899：webContents 栈与页面栈是两套，
    // 实测 canGoBack=false 而 CDP 的 Page.getNavigationHistory 满条目）——所以
    // 项目域在页面里调 history.back()/forward()：同文档遍历触发 popstate，App 把
    // 工作区视图落回来（use-workspace-nav 的 History API 接线）。
    // **探针优先（2026-12 侧键修复）**：助手域是纯 useState 导航、从不写历史，
    // history.back() 会消费项目模式遗留的旧条目、改到隐藏分支的 state。页面挂
    // __lxNavBack/__lxNavForward 探针（AssistantWorkspace 在助手模式可见时注册，
    // 卸载/切走注销）——探针存在优先调用（助手域接管），不存在回落 history
    // （项目域/首屏）。可选链在注入串里手写 `?` 不行——executeJavaScript 的
    // 目标页面是 JS，直接写即可。
    win.on("app-command", (_e, cmd) => {
      if (cmd !== "browser-backward" && cmd !== "browser-forward") return;
      const probe = cmd === "browser-backward" ? "__lxNavBack" : "__lxNavForward";
      void win?.webContents
        .executeJavaScript(`(window.${probe}?.() ?? history.${cmd === "browser-backward" ? "back" : "forward"}())`)
        .catch(() => { /* 页面正在跳转/销毁时注入失败无害 */ });
    });

    // 窗口控制 IPC（preload 的 __LX__ 桥 → Topbar 按钮）
    ipcMain.on("win:minimize", () => win?.minimize());
    ipcMain.on("win:toggleMaximize", () => {
      if (!win) return;
      win.isMaximized() ? win.unmaximize() : win.maximize();
    });
    ipcMain.on("win:close", () => win?.close());

    // 对话完成系统通知（preload 的 __LX__.notify.done）：渲染层已判定（主轮
    // done + 失焦 + 开关），这里只管弹。isSupported 闸——不支持通知的环境
    //（极旧系统/无通知中心）静默跳过；点击聚焦主窗口（复用 tray.ts showMain
    // 的 show+focus 模式）；失败静默 catch（通知弹不出不影响生成本身，
    // 对齐 tray.ts:86-88 纪律）。dev 壳 AUMID 无注册快捷方式时 Windows 可能
    // 弹不出——平台特性，验收以打包形态为准。
    ipcMain.on("notify:done", (_e, n: unknown) => {
      try {
        if (!Notification.isSupported()) return;
        const r = (n ?? {}) as { title?: unknown; body?: unknown };
        if (typeof r.title !== "string" || !r.title) return;
        const notification = new Notification({
          title: r.title,
          body: typeof r.body === "string" ? r.body : "",
        });
        notification.on("click", () => {
          win?.show();
          win?.focus();
        });
        notification.show();
      } catch {
        // 弹不出不影响生成流程本身（渲染层时间线照常展示结果）
      }
    });

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

    // 附件读入的磁盘回退通道（preload 的 __LX__.readFileBase64）：渲染层 blob
    // 快照失效时按 webUtils 解析出的真实路径重读。只读单个文件、限 20MB
    // （与前端 MAX_FILE_BYTES 同一套）；失败给带路径的人话错误（渲染层把它
    // 拼进附件提示，便于以后排查）。渲染层不因此获得任何通用 fs 能力。
    ipcMain.handle("file:readBase64", async (_e, p: unknown) => {
      if (typeof p !== "string" || p.trim() === "") {
        throw new Error("来源文件没有真实磁盘路径（虚拟文件），无法按路径重读");
      }
      let size = 0;
      try {
        size = (await stat(p)).size;
      } catch (err) {
        throw new Error(`无法读取 ${p}：文件不存在或无权访问（${err instanceof Error ? err.message : String(err)}）`);
      }
      if (size > 20 << 20) {
        throw new Error(`无法读取 ${p}：超过 20MB 上限（当前 ${size} 字节）`);
      }
      const buf = await readFile(p);
      return buf.toString("base64");
    });

    // 樱花frp 公网穿透（docs/sakurafrp-integration.md）：API/fpc 全在主进程，
    // 渲染层经 invoke 发意图、经 sakura:state 推送收状态。handler 表逐个注册
    //（invoke 的 channel 须在主进程显式声明，批量展开会扩大攻击面）。
    for (const [channel, handler] of Object.entries(sakuraHandlers)) {
      ipcMain.handle(channel, handler as (e: Electron.IpcMainInvokeEvent, ...args: unknown[]) => unknown);
    }
    initSakura(
      (s) => pushToRenderer("sakura:state", s),
      (e) => pushToRenderer("sakura:nodeLatency", e),
    );

    // Tailscale 模式（远程访问的第二实现——与樱花frp 同构：主进程持状态，
    // 渲染层发意图收推送）
    for (const [channel, handler] of Object.entries(tailscaleHandlers)) {
      ipcMain.handle(channel, handler as (e: Electron.IpcMainInvokeEvent, ...args: unknown[]) => unknown);
    }
    initTailscale((s) => pushToRenderer("tailscale:state", s));

    // 后端远程访问管理（token 同步取给渲染层拼 WS URL；开关/轮换走 invoke）
    ipcMain.on("backendRemote:tokenSync", (e) => { e.returnValue = cachedToken(); });
    ipcMain.handle("backendRemote:get", () => cachedState() ?? { enabled: false, token: "", allowControl: false, lanAddr: "" });
    ipcMain.handle("backendRemote:enable", () => remoteEnable());
    ipcMain.handle("backendRemote:disable", () => remoteDisable());
    ipcMain.handle("backendRemote:rotate", () => remoteRotate());
    ipcMain.handle("backendRemote:refresh", () => remoteRefresh());
    ipcMain.handle("backendRemote:allowControl", (_e, on: unknown) => setAllowControl(on === true));

    // 自更新（manifest → 下载校验 → 后端热替换 → asar 退出冷替换，见 updater.ts）
    updateHandlers();
    // 运行期周期检查（每 6h+抖动；发现新版本 → 渲染层 Toast/设置角标 + 托盘气泡，
    // 非侵入不打断——启动时的检查由渲染层负责，见 update-poll.ts 头注）
    startUpdatePolling();

    // 系统托盘（关闭 = 隐藏到托盘，后端/隧道保持运行；托盘菜单退出才真退）
    createTray(() => win);

    // 投屏（第一阶段只看，shell/src/screen.ts）：壳轮询后端 /screen/ctl 接命令、
    // 起 ffmpeg 捕屏推流。托盘实例经 tray.ts 的 getTray() 取（投屏的隐私指示
    // 改 tooltip + 弹气泡）；投屏运行状态变化由 screen 模块直接更新托盘。
    startScreenPolling();
    // 编码器探测预热（screen.ts probeEncoders 结果缓存）：把首次 start 的
    // 数秒探测空窗挪到壳启动后台，用户点「开始投屏」即起 ffmpeg
    warmUpEncoderProbe();
    // 投屏控制（第二阶段）：输入事件长轮询消费（shell/src/input.ts）——
    // 手机端 screen.input 经后端队列到这里被 SendInput 注入。koffi 不可用时
    // startInputPolling 返回 false（安全降级 = 不注入，控制事件无人取走）。
    if (!startInputPolling()) {
      console.log("[input] 控制注入不可用（koffi 加载失败）——投屏仍可观看，控制不可用");
    }
    // 控制态翻转 → 托盘 tooltip（「正在被控制」优先于「正在向手机投屏」）
    setControllingCallback((controlled) => setTrayHint(/* capturing */ true, controlled));

    // 产线诊断通道：渲染层控制台/加载失败/崩溃转发到 <userData>/renderer.log
    // （判定逻辑在 ./renderer-log.ts；打包后无 DevTools，渲染层问题全靠它）。
    // console-message 回调在主进程侧执行（event.level/message/lineNumber/sourceId），
    // error 级同时进 stdout 便于 dev 观察；frame 可能已销毁，读 frame 前先判。
    win.webContents.on("console-message", (e) => {
      const src = e.frame && !e.frame.url ? "" : e.frame ? ` ${e.frame.url}` : "";
      const line = rendererLogLine(new Date(), consoleLevelName(e.level), `${e.message} (${e.sourceId}:${e.lineNumber})${src}`);
      appendRendererLog(line);
      if (e.level === "error") console.log(`[renderer] ${e.message}`);
    });
    win.webContents.on("did-fail-load", (_e, code, desc, url) => {
      appendRendererLog(rendererLogLine(new Date(), "error", `did-fail-load ${url}: ${code} ${desc}`));
      console.log(`[shell] 加载失败 ${url}: ${code} ${desc}`);
    });
    // 渲染进程崩溃（白屏根因①）：记日志 + 连崩退避自动 reload（scheduleRendererRecovery）
    win.webContents.on("render-process-gone", (_e, details) => scheduleRendererRecovery(details));
    // 恢复成功（页面重新加载完成）→ 连崩计数清零：退避窗口只对「连着崩」累计
    win.webContents.on("did-finish-load", () => { crashStreak = 0; });
    // 页面无响应（白屏根因②的近亲：主线程卡死/长任务阻塞）——只记日志不动作
    win.webContents.on("unresponsive", () => {
      appendRendererLog(rendererLogLine(new Date(), "warning", "webContents unresponsive（渲染进程无响应）"));
      console.log("[shell] 渲染进程无响应（unresponsive）");
    });
    // GPU 等子进程崩溃：只记日志不重启（Chromium 自己会拉起新的 GPU 进程）
    app.on("child-process-gone", (_e, details) => {
      appendRendererLog(rendererLogLine(new Date(), "error",
        `child-process-gone type=${details.type} name=${details.name ?? "?"} reason=${details.reason} exitCode=${details.exitCode}`));
      console.log(`[shell] 子进程退出（${details.type}/${details.name ?? "?"} reason=${details.reason}）`);
    });

    // 上次更新失败（apply-update.bat 留下的失败标记）→ 开屏推给渲染层，
    // 统一更新弹窗展示可读原因 + 重试/手工安装指引。必须在窗口加载完之后发，
    // 否则渲染层订阅还没建立收不到。
    try {
      const failure = consumePendingUpdateFailure();
      if (failure) {
        console.log(`[shell] 上次更新失败：${failure}`);
        pushToRenderer("update:failed", { message: failure });
      }
    } catch (e) {
      console.log(`[shell] 消费更新失败标记出错：${e instanceof Error ? e.message : String(e)}`);
    }

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
  // macOS 专属：Dock 图标点击 → 重新显示主窗口（关闭=隐藏到托盘的语义在
  // mac 上没有 Dock 退出的习惯，Dock 是 mac 用户找回窗口的主入口；Windows
  // 无此事件，行为不变）。
  app.on("activate", () => {
    if (win && !win.isDestroyed()) {
      win.show();
      win.focus();
    }
  });
  app.on("will-quit", () => {
    // 正常退出取证：下次排障时用它区分「走了优雅退出」vs「静默死亡」
    //（0.3.60 两次死亡都没有这行——进程被强杀/原生崩溃）。
    appendRendererLog(rendererLogLine(new Date(), "info", "[main] graceful quit（will-quit 正常收尾）"));
    stopUpdatePolling();
    stopScreenPolling(); // 投屏：杀 ffmpeg + 断推流（shell/src/screen.ts）
    stopInputPolling(); // 投屏控制：停输入长轮询（shell/src/input.ts）
    shutdownBackend();
    shutdownSakura(); // frpc 与后端同一条退出路径：壳走，隧道进程跟着收
  });
}
