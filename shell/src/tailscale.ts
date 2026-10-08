// tailscale.ts —— Tailscale 远程访问模式（壳主进程领域模块，与 sakura.ts 同构）。
//
// 原理：tailscale CLI 的 serve 把 tailnet 的 443 反代到本地后端端口——后端
// 零改动（继续监听 127.0.0.1，Go 侧不知道自己在被穿透），tailnet 内设备经
// https://<机器名>.<tailnet>.ts.net 直连（serve 代理支持 WebSocket）。
// 预留多模式的第二实现：与 sakura 同一套状态机/推送/IPC 形状，前端页面按
// 模式卡片并列。
//
// CLI 语法目标 Tailscale ≥1.38（serve --bg <端口> / serve reset）；真实键名
// 或语法随版本漂移时，错误会带 CLI 原话冒到 UI。测试注入口：LXCODE_TS_CLI
// 覆盖 CLI 路径（e2e 指向假脚本）。
import { spawn } from "node:child_process";

const CLI_TIMEOUT_MS = 8_000;

export interface TailscaleState {
  installed: boolean;
  /** tailscaled 后端状态：Running / NeedsLogin / Stopped / ""（未安装）。 */
  backendState: string;
  /** 本机在 tailnet 里的 DNS 名（去尾点，如 machine.tail-scale.ts.net）。 */
  deviceName: string;
  /** tailnet IPv4（100.x.y.z）。 */
  ipv4: string;
  /** serve 是否已把后端端口发布到 tailnet。 */
  serveOn: boolean;
  /** serve 开启后的访问地址（https://deviceName）。 */
  serveUrl: string;
  busy: string | null;
  error: string | null;
  /** 未安装/未登录时的人话指引。 */
  hint: string | null;
}

let state: TailscaleState = emptyState();
let notify: ((s: TailscaleState) => void) | null = null;
let detecting = false;

function emptyState(): TailscaleState {
  return { installed: false, backendState: "", deviceName: "", ipv4: "", serveOn: false, serveUrl: "", busy: null, error: null, hint: null };
}

function setState(patch: Partial<TailscaleState>): void {
  state = { ...state, ...patch };
  notify?.(state);
}

/** 本地后端端口（与 sakura.ts 的隧道指向同一个：壳直连的后端）。 */
function localBackendPort(): number {
  const addr = process.env.LXCODE_ADDR ?? "127.0.0.1:7789";
  const port = Number(addr.split(":").pop());
  return Number.isFinite(port) && port > 0 ? port : 7789;
}

function cliPath(): string {
  // env 覆盖（e2e 假 CLI）→ Windows 默认安装路径 → PATH（CreateProcess 解析）
  if (process.env.LXCODE_TS_CLI) return process.env.LXCODE_TS_CLI;
  return process.platform === "win32" ? "C:\\Program Files\\Tailscale\\tailscale.exe" : "tailscale";
}

interface ExecResult {
  code: number;
  stdout: string;
  stderr: string;
}

function execCli(args: string[]): Promise<ExecResult> {
  // .cmd/.bat 不能被 spawn 直接执行（Node 18.20+ 直接 EINVAL）——包一层 cmd /c。
  // 这既是 e2e 假 CLI 的需要，也让用户把 LXCODE_TS_CLI 指到批处理包装成为可能。
  const p = cliPath();
  const isScript = /\.cmd$/i.test(p) || /\.bat$/i.test(p);
  const exe = isScript ? "cmd.exe" : p;
  const argv = isScript ? ["/d", "/s", "/c", p, ...args] : args;
  return new Promise((resolve, reject) => {
    const child = spawn(exe, argv, { windowsHide: true, stdio: ["ignore", "pipe", "pipe"] });
    let stdout = "";
    let stderr = "";
    const timer = setTimeout(() => {
      child.kill();
      reject(new Error("tailscale 命令超时"));
    }, CLI_TIMEOUT_MS);
    child.stdout?.on("data", (c) => { stdout += c; });
    child.stderr?.on("data", (c) => { stderr += c; });
    child.on("error", (e) => {
      clearTimeout(timer);
      reject(e);
    });
    child.on("exit", (code) => {
      clearTimeout(timer);
      resolve({ code: code ?? -1, stdout, stderr });
    });
  });
}

/** 拉一次状态：installed / backendState / 设备名 / IP / serve 发布态。 */
export async function detectTailscale(): Promise<void> {
  if (detecting) return;
  detecting = true;
  try {
    let res: ExecResult;
    try {
      res = await execCli(["status", "--json"]);
    } catch {
      setState({ installed: false, backendState: "", deviceName: "", ipv4: "", serveOn: false, serveUrl: "", hint: "未检测到 Tailscale——从 tailscale.com/download 安装并登录后刷新" });
      return;
    }
    let parsed: {
      BackendState?: string;
      Self?: { DNSName?: string; TailscaleIPs?: string[] };
    } | null = null;
    try {
      parsed = JSON.parse(res.stdout) as { BackendState?: string; Self?: { DNSName?: string; TailscaleIPs?: string[] } } | null;
    } catch { /* 版本差异导致非 JSON：按未登录态处理 */ }
    const backendState = parsed?.BackendState ?? "";
    const dns = (parsed?.Self?.DNSName ?? "").replace(/\.$/, "");
    const ipv4 = parsed?.Self?.TailscaleIPs?.[0] ?? "";
    const running = backendState === "Running";
    let serveOn = false;
    if (running) {
      try {
        const serve = await execCli(["serve", "status"]);
        // 版本无关的判定：状态输出里出现我们代理的端口即视为已发布
        serveOn = serve.stdout.includes(String(localBackendPort()));
      } catch { /* serve 子命令不存在（极老版本）→ 视为未开启 */ }
    }
    setState({
      installed: true,
      backendState,
      deviceName: dns,
      ipv4,
      serveOn,
      serveUrl: serveOn && dns ? `https://${dns}` : "",
      hint: running ? null : "Tailscale 已安装但未连接——从系统托盘登录（或命令行 tailscale up）后刷新",
      error: null,
    });
  } finally {
    detecting = false;
  }
}

/** 发布后端到 tailnet（serve --bg）。CLI 语义错误（无权限/版本不认）原话上抛。 */
export async function serveOn(): Promise<void> {
  setState({ busy: "正在发布到 tailnet" });
  try {
    const res = await execCli(["serve", "--bg", String(localBackendPort())]);
    if (res.code !== 0) throw new Error(res.stderr.trim() || `tailscale serve 退出码 ${res.code}`);
    await detectTailscale();
  } catch (e) {
    setState({ error: e instanceof Error ? e.message : String(e) });
    throw e;
  } finally {
    setState({ busy: null });
  }
}

/** 撤销发布。优先 serve reset（清空全部 serve 规则——本应用只用 443 这一条）。 */
export async function serveOff(): Promise<void> {
  setState({ busy: "正在关闭发布" });
  try {
    let res = await execCli(["serve", "reset"]);
    if (res.code !== 0) res = await execCli(["serve", "--https=443", "off"]);
    if (res.code !== 0) throw new Error(res.stderr.trim() || `tailscale serve 退出码 ${res.code}`);
    await detectTailscale();
  } catch (e) {
    setState({ error: e instanceof Error ? e.message : String(e) });
    throw e;
  } finally {
    setState({ busy: null });
  }
}

export function initTailscale(onState: (s: TailscaleState) => void): void {
  notify = onState;
  void detectTailscale();
}

export const tailscaleHandlers = {
  "tailscale:getState": (): TailscaleState => state,
  "tailscale:serveOn": (): Promise<TailscaleState> => serveOn().then(() => state),
  "tailscale:serveOff": (): Promise<TailscaleState> => serveOff().then(() => state),
  "tailscale:refresh": (): Promise<TailscaleState> => detectTailscale().then(() => state),
};
