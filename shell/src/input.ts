// input.ts —— 投屏控制（第二阶段：可控制）的**注入执行段**（Windows）。
//
// 角色划分（internal/protocol/screen.go）：后端跑在 SCM 服务（session 0）注不了
// 用户桌面输入——SendInput 必须在用户会话内的进程里调，即本模块（壳）。
// 通道：常驻 goroutine 语义的长轮询循环（startInputPolling）：
//   GET /screen/input?wait=1 → 后端 hold ≤900ms（有事件立即回）→ 本地 SendInput
//   → 立刻发下一个请求。事件从手机 WS 到后端入队 → 长轮询取走 → SendInput，
//   全链路延迟 ≈ 一次回环往返（<1ms）+ 手机侧发帧间隔；投屏画面本身 30fps
//   （33ms/帧），输入通道不成为瓶颈。
//
// FFI 选型（2026-12 拍板）：**koffi**（npm 依赖，预编译二进制随包分发，纯 JS
// 安装零编译；npmRebuild:false 的 electron-builder 配置不用动——koffi 不需要
// 对 Electron 头文件 rebuild，它加载的是 OS 系统库 user32.dll）。本机实测：
//   - 结构体布局正确（MOUSEINPUT=32B / KEYBDINPUT=24B / INPUT=40B，与 Win64
//     ABI 一致——koffi 的 long=32bit、unsigned long=32bit 正是 Windows 语义）；
//   - SendInput(1, {MOUSEEVENTF_MOVE|RELATIVE dx=0}, …) 返回 1，真实例化通过。
// 降级方案（koffi 不可用时）不实现：模块加载失败 → startInputPolling 直接
// 返回 false 并日志说明「控制不可用」——手机端 screen.input 仍会被后端入队但
// 无人取走（≤64 条队列满丢最旧），安全语义不变（不能注入 ≠ 泄漏）。
//
// 坐标换算：手机端发的是**视频画面归一化坐标**（0-1，相对被投显示器）。壳侧
// 用 Electron screen API 拿显示器几何（与 enumerateMonitors 同一份来源，序号
// 一致），绝对坐标 = display.workArea? 不用 workArea——投屏捕的是全屏
//（ddagrab 抓整个 display），换算基准用 display.bounds。换算成 SendInput
// ABSOLUTE|VIRTUALDESK 的 0-65535 整数（基准 = 全虚拟桌面，多显示器天然正确）。
// 之所以用 VIRTUALDESK 而不是按 display.bounds 算像素再 SetCursorPos：
// SetCursorPos 的 DPI 虚拟化在高 DPI 混合 DPI 环境下行为不可控；SendInput
// 的 0-65535 归一化是系统级、与 DPI 无关。
import { screen } from "electron";
import { BACKEND, screenToken as screenTokenRaw } from "./screen";

/** 输入事件（与 internal/protocol.ScreenInputParams 逐字段一致）。 */
export type ScreenInputEvent = {
  type: string;
  x?: number;
  y?: number;
  button?: string;
  dx?: number;
  dy?: number;
  key?: string;
  modifiers?: string[];
  text?: string;
  monitor?: number;
};

/** koffi 的最小类型面（node_modules 里有完整 index.d.ts，但这里只用到
 *  load/func/struct/union/sizeof 五个入口——收窄声明，升级 koffi 不破壳编译）。 */
interface koffiLib {
  load(name: string): unknown;
  struct(name: string, fields: Record<string, string>): unknown;
  union(name: string, fields: Record<string, unknown>): unknown;
  sizeof(t: unknown): number;
}


/** 壳↔后端屏端点 token（screen.ts 的缓存 getter；包一层避免命名冲突）。 */
const token = (): string => screenTokenRaw() ?? "";

// ---- Win32 常量（winuser.h）----

const INPUT_MOUSE = 0;
const INPUT_KEYBOARD = 1;
const MOUSEEVENTF_MOVE = 0x0001;
const MOUSEEVENTF_LEFTDOWN = 0x0002;
const MOUSEEVENTF_LEFTUP = 0x0004;
const MOUSEEVENTF_RIGHTDOWN = 0x0008;
const MOUSEEVENTF_RIGHTUP = 0x0010;
const MOUSEEVENTF_MIDDLEDOWN = 0x0020;
const MOUSEEVENTF_MIDDLEUP = 0x0040;
const MOUSEEVENTF_WHEEL = 0x0800;
const MOUSEEVENTF_HWHEEL = 0x1000;
const MOUSEEVENTF_ABSOLUTE = 0x8000;
const MOUSEEVENTF_VIRTUALDESK = 0x4000;
const KEYEVENTF_KEYUP = 0x0002;
const KEYEVENTF_UNICODE = 0x0004;

// ---- 虚拟键码表（命名键 → VK；单字符走 Unicode 注入，不查布局）----

const VK_MAP: Record<string, number> = {
  backspace: 0x08, tab: 0x09, enter: 0x0d, return: 0x0d, esc: 0x1b, escape: 0x1b,
  space: 0x20, pgup: 0x21, pgdn: 0x22, end: 0x23, home: 0x24,
  left: 0x25, up: 0x26, right: 0x27, down: 0x28, insert: 0x2d, delete: 0x2e,
  win: 0x5b, lwin: 0x5b, rwin: 0x5c, ctrl: 0x11, control: 0x11, alt: 0x12, shift: 0x10,
  volumeup: 0xaf, volumedown: 0xae, volumemute: 0xad,
  medianext: 0xb0, mediaprev: 0xb1, mediaplay: 0xb3,
};
for (let i = 1; i <= 24; i++) VK_MAP[`f${i}`] = 0x6f + i; // F1=0x70

/** 修饰键名 → VK（key 事件的 modifiers 组合）。 */
const VK_MOD: Record<string, number> = { ctrl: 0x11, control: 0x11, alt: 0x12, shift: 0x10, win: 0x5b };

// ---- koffi 绑定（惰性初始化：加载失败 = 控制不可用，安全降级为不注入）----

let user32: unknown | null = null;
let sendInput: ((n: number, inputs: unknown[], size: number) => number) | null = null;
let getSystemMetrics: ((idx: number) => number) | null = null;
let inputSize = 40;
let initError = "";

const SM_XVIRTUALSCREEN = 76;
const SM_YVIRTUALSCREEN = 77;
const SM_CXVIRTUALSCREEN = 78;
const SM_CYVIRTUALSCREEN = 79;

/** 惰性绑定 user32（首次注入时）。返回 false = 不可用（initError 已填）。 */
function ensureBound(): boolean {
  if (sendInput) return true;
  if (initError) return false;
  try {
    const koffi = require("koffi") as koffiLib;
    user32 = koffi.load("user32.dll");
    const lib = user32 as { func(sig: string): (...args: any[]) => any };
    // DPI 感知：Electron 主进程已是 per-monitor DPI aware，但显式钉一次（幂等，
    // 失败无害）——GetSystemMetrics 的虚拟桌面尺寸必须按物理像素解释，
    // SendInput 的 0-65535 归一化才有确定语义（本机实测：钉 V2 后虚拟桌面
    // = 1920×1080 物理像素，中心点命中 960,540 精确无偏）。
    try {
      const setCtx = lib.func("intptr_t __stdcall SetProcessDpiAwarenessContext(intptr_t)");
      setCtx(BigInt.asIntN(64, -4n)); // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2
    } catch { /* 旧系统无此 API：SendInput 归一化仍按系统当前 DPI 上下文工作 */ }
    const MOUSEINPUT = koffi.struct("LX_MOUSEINPUT", {
      dx: "long", dy: "long", mouseData: "long",
      dwFlags: "unsigned long", time: "unsigned long", dwExtraInfo: "uintptr_t",
    });
    const KEYBDINPUT = koffi.struct("LX_KEYBDINPUT", {
      wVk: "unsigned short", wScan: "unsigned short", dwFlags: "unsigned long",
      time: "unsigned long", dwExtraInfo: "uintptr_t",
    });
    koffi.union("LX_INPUT_U", { mi: MOUSEINPUT, ki: KEYBDINPUT });
    const INPUT = koffi.struct("LX_INPUT", { type: "unsigned long", u: "LX_INPUT_U" });
    inputSize = koffi.sizeof(INPUT);
    const send = lib.func(
      "unsigned int __stdcall SendInput(unsigned int, LX_INPUT*, int)",
    );
    const gsm = lib.func("int __stdcall GetSystemMetrics(int)");
    // koffi 的 func 签名重载按第一处调用形态推断；这里显式包一层收窄成两个
    // 独立函数（TS 收窄声明见文件头注释）。
    sendInput = (n, inputs, size) => {
      // SendInput 的第二参数是 LX_INPUT 数组——koffi 接受对象数组按声明结构体
      // 逐个编组。
      const arr = inputs as never;
      return send(n, arr, size);
    };
    getSystemMetrics = (idx) => gsm(idx);
    return true;
  } catch (e) {
    initError = e instanceof Error ? e.message : String(e);
    console.log(`[input] koffi 绑定失败——控制注入不可用: ${initError}`);
    return false;
  }
}

/** 单条 INPUT 的构造（koffi 按声明的结构体形状校验字段；union 必须是
 *  「单属性」对象——{mi:…} 或 {ki:…}，两个键都给会报 ambiguous）。 */
function mouseInput(dx: number, dy: number, flags: number, mouseData: number): unknown {
  return {
    type: INPUT_MOUSE,
    u: { mi: { dx, dy, mouseData, dwFlags: flags, time: 0, dwExtraInfo: 0 } },
  };
}
function keyInput(vk: number, scan: number, flags: number): unknown {
  return {
    type: INPUT_KEYBOARD,
    u: { ki: { wVk: vk, wScan: scan, dwFlags: flags, time: 0, dwExtraInfo: 0 } },
  };
}

// ---- 坐标换算（归一化 → VIRTUALDESK 0-65535）----

/** 归一化（相对某个显示器的画面）→ 全虚拟桌面的 0-65535。monitor 与
 *  enumerateMonitors 的 id 同源（Electron screen API 顺序）。 */
function toVirtualDesktop(nx: number, ny: number, monitor: number): { ax: number; ay: number } | null {
  const displays = screen.getAllDisplays();
  const d = displays[monitor] ?? displays[0];
  if (!d) return null;
  const vx = getSystemMetrics?.(SM_XVIRTUALSCREEN) ?? 0;
  const vy = getSystemMetrics?.(SM_YVIRTUALSCREEN) ?? 0;
  const vw = getSystemMetrics?.(SM_CXVIRTUALSCREEN) ?? 0;
  const vh = getSystemMetrics?.(SM_CYVIRTUALSCREEN) ?? 0;
  if (vw <= 0 || vh <= 0) return null;
  const px = d.bounds.x + nx * d.bounds.width;
  const py = d.bounds.y + ny * d.bounds.height;
  // VIRTUALDESK 归一化：x = (px - vx) * 65536 / vw，钳 0..65535（65536 会溢出）
  const ax = Math.min(65535, Math.max(0, Math.round((px - vx) * 65536 / vw)));
  const ay = Math.min(65535, Math.max(0, Math.round((py - vy) * 65536 / vh)));
  return { ax, ay };
}

/** button 名 → down/up flags。 */
function buttonFlags(button: string, down: boolean): number {
  switch (button) {
    case "right": return down ? MOUSEEVENTF_RIGHTDOWN : MOUSEEVENTF_RIGHTUP;
    case "middle": return down ? MOUSEEVENTF_MIDDLEDOWN : MOUSEEVENTF_MIDDLEUP;
    default: return down ? MOUSEEVENTF_LEFTDOWN : MOUSEEVENTF_LEFTUP;
  }
}

/** 注入一条事件（send 一批 INPUT；返回是否全部被系统接受）。 */
export function injectEvent(ev: ScreenInputEvent): boolean {
  if (!ensureBound() || !sendInput) return false;
  // 可选字段的判空收窄（switch 分支内 TS 不做跨 case 收窄）
  const x = ev.x ?? 0;
  const y = ev.y ?? 0;
  const dx = ev.dx ?? 0;
  const dy = ev.dy ?? 0;
  const monitor = ev.monitor ?? 0;
  try {
    switch (ev.type) {
      case "move": {
        const p = toVirtualDesktop(x, y, monitor);
        if (!p) return false;
        return sendInput(1, [mouseInput(p.ax, p.ay, MOUSEEVENTF_MOVE | MOUSEEVENTF_ABSOLUTE | MOUSEEVENTF_VIRTUALDESK, 0)], inputSize) === 1;
      }
      case "click":
      case "dblclick":
      case "down":
      case "up": {
        const p = toVirtualDesktop(x, y, monitor);
        if (!p) return false;
        const btn = ev.button ?? "left";
        const inputs: unknown[] = [mouseInput(p.ax, p.ay, MOUSEEVENTF_MOVE | MOUSEEVENTF_ABSOLUTE | MOUSEEVENTF_VIRTUALDESK, 0)];
        if (ev.type === "down" || ev.type === "up") {
          inputs.push(mouseInput(0, 0, buttonFlags(btn, ev.type === "down"), 0));
        } else {
          const n = ev.type === "dblclick" ? 2 : 1;
          for (let i = 0; i < n; i++) {
            inputs.push(mouseInput(0, 0, buttonFlags(btn, true), 0));
            inputs.push(mouseInput(0, 0, buttonFlags(btn, false), 0));
          }
        }
        return sendInput(inputs.length, inputs, inputSize) === inputs.length;
      }
      case "scroll": {
        const p = toVirtualDesktop(x, y, monitor);
        const inputs: unknown[] = [];
        if (p) inputs.push(mouseInput(p.ax, p.ay, MOUSEEVENTF_MOVE | MOUSEEVENTF_ABSOLUTE | MOUSEEVENTF_VIRTUALDESK, 0));
        // 一刻度 = WHEEL_DELTA(120)；dy 正 = 上滚
        if (dy) inputs.push(mouseInput(0, 0, MOUSEEVENTF_WHEEL, Math.round(dy * 120)));
        if (dx) inputs.push(mouseInput(0, 0, MOUSEEVENTF_HWHEEL, Math.round(dx * 120)));
        if (inputs.length === 0) return false;
        return sendInput(inputs.length, inputs, inputSize) === inputs.length;
      }
      case "key": {
        const name = (ev.key ?? "").toLowerCase();
        const mods = ev.modifiers ?? [];
        const inputs: unknown[] = [];
        for (const m of mods) {
          const vk = VK_MOD[m];
          if (vk) inputs.push(keyInput(vk, 0, 0));
        }
        let vk = VK_MAP[name];
        let scan = 0;
        if (vk === undefined && name.length === 1) {
          // 单字符命名键（"a"-"z"/"0"-"9"）：按 Unicode 注入（不依赖布局）
          inputs.push(...unicodeInputs(name));
          vk = -1;
        }
        if (vk !== -1) {
          if (vk === undefined) return false;
          const up = false; // key 事件按「按下并抬起」注入（安卓端 tap 键条语义）
          if (!up) inputs.push(keyInput(vk, scan, 0));
          inputs.push(keyInput(vk, scan, KEYEVENTF_KEYUP));
        }
        for (let i = mods.length - 1; i >= 0; i--) {
          const vkM = VK_MOD[mods[i]];
          if (vkM) inputs.push(keyInput(vkM, 0, KEYEVENTF_KEYUP));
        }
        if (inputs.length === 0) return false;
        return sendInput(inputs.length, inputs, inputSize) === inputs.length;
      }
      case "char": {
        const inputs: unknown[] = [];
        for (const ch of ev.text ?? "") inputs.push(...unicodeInputs(ch));
        if (inputs.length === 0) return false;
        return sendInput(inputs.length, inputs, inputSize) === inputs.length;
      }
      default:
        return false;
    }
  } catch (e) {
    console.log(`[input] 注入失败 type=${ev.type}: ${e instanceof Error ? e.message : String(e)}`);
    return false;
  }
}

/** Unicode 字符 → KEYEVENTF_UNICODE down+up 两条 INPUT（VK 0，scan = 码位）。 */
function unicodeInputs(ch: string): unknown[] {
  const cp = ch.codePointAt(0) ?? 0;
  if (cp <= 0) return [];
  return [keyInput(0, cp, KEYEVENTF_UNICODE), keyInput(0, cp, KEYEVENTF_UNICODE | KEYEVENTF_KEYUP)];
}

// ---- 长轮询消费循环 ----

let polling = false;
let lastInjectAt = 0;
/** 控制状态变化通知（main.ts 装配：注入开始/停止 → 刷新托盘 tooltip）。 */
let onControllingChange: ((controlled: boolean) => void) | null = null;
let controlling = false;

/** main.ts 装配回调：控制态翻转时通知（托盘 tooltip 合流）。 */
export function setControllingCallback(cb: (controlled: boolean) => void): void {
  onControllingChange = cb;
}

/** 最近一次注入时间（诊断 + 「正在被控制」托盘提示判定用）。 */
export function lastInputInjectedAt(): number {
  return lastInjectAt;
}

/**
 * 启动输入长轮询循环（壳在线期间常驻；与 screen.ts 的命令轮询独立——命令是
 * 秒级电平，输入是低延迟事件流，两套节奏）。
 * 返回是否成功进入循环（koffi 不可用 = false，调用方记日志即可）。
 */
export function startInputPolling(): boolean {
  if (!ensureBound()) return false;
  if (polling) return true;
  polling = true;
  void inputLoop();
  return true;
}

export function stopInputPolling(): void {
  polling = false;
}

async function inputLoop(): Promise<void> {
  console.log("[input] 输入长轮询循环已启动");
  while (polling) {
    try {
      const res = await fetch(
        `http://${BACKEND}/screen/input?wait=1${token() ? `&token=${encodeURIComponent(token())}` : ""}`,
        { signal: AbortSignal.timeout(3000) },
      );
      if (res.ok) {
        const body = (await res.json()) as { events: ScreenInputEvent[] };
        const had = (body.events ?? []).length > 0;
        for (const ev of body.events ?? []) {
          if (injectEvent(ev)) lastInjectAt = Date.now();
        }
        // 控制态翻转（停手 3s 回「仅投屏」）→ 通知托盘刷新 tooltip
        const nowControlled = had && Date.now() - lastInjectAt < 3000;
        if (nowControlled !== controlling) {
          controlling = nowControlled;
          try { onControllingChange?.(controlling); } catch { /* 通知失败不影响注入 */ }
        }
      } else {
        await sleep(1000); // 门关了/后端重启：退避，不空转
      }
    } catch {
      await sleep(1000); // 后端不可达：退避
    }
  }
  console.log("[input] 输入长轮询循环已退出");
}

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}
