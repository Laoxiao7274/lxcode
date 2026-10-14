// WsConnection：WS JSON-RPC 传输层（与 Go internal/protocol 对齐）——重连、
// 心跳、请求配对、超时、握手初始化链与事件分发。对外暴露「发请求（call）+
// 订阅事件（subscribe）」的最小面；管理域请求编排在各域对象（model-admin 等），
// 它们经 call 发请求、经 onEventSideEffect 注册事件副作用（重拉事实源/写缓存）。
//
// 错误纪律：生成类失败（chat.error）走 error 事件（会话状态由 reducer 收敛）；
// 请求类失败（拒绝/断连/超时）走 operationError 事件——UI 只提示，不动
// blocks/pending。
import type { AgentEvent, HistorySnapshot } from "../../shared/types";
import { setAttachmentOrigin, setAttachmentToken } from "../../shared/attachments";
import { normalizeApproval } from "../../shared/approval";
import { DeltaCoalescer } from "./coalesce";
import { mapEvent } from "./events";
import { snapshotFromWire } from "./history-snapshot";

/** WS JSON-RPC 帧结构（与 Go internal/protocol 对齐）。 */
interface WsRequest {
  jsonrpc: string;
  id?: number;
  method: string;
  params?: unknown;
}

interface WsResponse {
  jsonrpc: string;
  id?: number;
  result?: unknown;
  error?: { code: number; message: string };
  method?: string; // 事件帧（无 id）
  params?: unknown;
}

export type Listener = (ev: AgentEvent) => void;

interface PendingCall {
  resolve: (v: unknown) => void;
  reject: (e: Error) => void;
  timeout: ReturnType<typeof setTimeout>;
}

/** 由地址形态构造附件端点的 HTTP 源（与 wsURL 同一套形态自适应）：
 *  http(s):// 原样；ws(s):// 换 http(s)；裸 host:port → http://。 */
function httpOrigin(addr: string): string {
  const trimmed = addr.trim();
  if (/^https:\/\//i.test(trimmed)) return trimmed.replace(/\/+$/, "");
  if (/^http:\/\//i.test(trimmed)) return trimmed.replace(/\/+$/, "");
  if (/^wss:\/\//i.test(trimmed)) return `https://${trimmed.slice(6)}`.replace(/\/+$/, "");
  if (/^ws:\/\//i.test(trimmed)) return `http://${trimmed.slice(5)}`.replace(/\/+$/, "");
  return `http://${trimmed}`;
}

const WS_READY_STATE_OPEN = 1;
export const PROTOCOL_VERSION = "2";

/** 由地址形态构造 WS URL（自适应）：
 *  - `https://` → `wss://`（远端 HTTPS 反代——WS 必须同协议升级，明文 ws 会被拒）
 *  - `http://`  → `ws://`
 *  - `wss://` / `ws://` 原样
 *  - 裸 `host:port` → `ws://`（局域网/本机直连，现状）
 *  路径统一 /rpc（地址自带路径则追加），token 挂查询串（与后端 checkRemoteToken
 *  的读取位置一致）。导出是为了测试钉住每条形态。 */
export function wsURL(addr: string, token: string | null): string {
  const trimmed = addr.trim();
  let base: string;
  if (/^wss:\/\//i.test(trimmed)) base = trimmed;
  else if (/^https:\/\//i.test(trimmed)) base = `wss://${trimmed.slice(8)}`;
  else if (/^http:\/\//i.test(trimmed)) base = `ws://${trimmed.slice(7)}`;
  else if (/^ws:\/\//i.test(trimmed)) base = trimmed;
  else base = `ws://${trimmed}`;
  const url = new URL(base);
  // 地址已带 /rpc（比如直接粘了完整升级路径）不重复追加
  url.pathname = `${url.pathname.replace(/\/rpc\/?$/i, "").replace(/\/+$/, "")}/rpc`;
  if (token) url.searchParams.set("token", token);
  return url.toString();
}

const RECONNECT_MS = 5000;
const REQUEST_TIMEOUT_MS = 10_000;
// 重方法的宽容阈值（2026-12 性能审计）：session.resume / chat.history 要全量加载
// 大会话历史（磁盘 IO + JSON 反序列化 + 统计折叠），chat.compact 要同步等一次
// 摘要调用，session.list 要扫全库算各会话条数——大会话/慢盘上 10s 会偶发命中
//（用户报的「恢复会话失败: 请求超时」）。放宽到 30s；其余请求保持 10s 快判。
const HEAVY_REQUEST_TIMEOUT_MS = 30_000;
const HEAVY_METHODS = new Set([
  "session.resume",
  "chat.history",
  "chat.compact",
  "chat.rewind",
  "session.list",
]);

// 心跳参数：连接存活期内每 15s 发一次 connection.ping，单次 3s 超时（独立于
// REQUEST_TIMEOUT_MS——心跳要快判，不能陪普通请求等 10s）；连续 2 次失败判定
// 断线，主动 close 触发现有 onclose → 重连。页面隐藏时暂停（后台节流下定时器
// 不可靠，恢复由 visibilitychange 钩子兜底）。
const HEARTBEAT_INTERVAL_MS = 15_000;
const HEARTBEAT_TIMEOUT_MS = 3_000;
const HEARTBEAT_MAX_FAILURES = 2;

/** 事件副作用的注册口：管理域把它「收到某后端事件时要做的 I/O」挂到这里
 *  （重拉事实源 / 写缓存）。连接层只负责分发时机（映射 + 副作用两步），
 *  不认识任何管理域的缓存。 */
export type EventSideEffect = (method: string, params: unknown, ev: AgentEvent | null) => void;

export class WsConnection {
  private addr: string;
  private ws: WebSocket | null = null;
  private listeners = new Set<Listener>();
  private nextId = 1;
  private pending = new Map<number, PendingCall>();
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  /** 心跳：存活期内每 15s 探测一次（参数见文件头 HEARTBEAT_* 常量）。
   *  没有它，最小化期间断的线要等窗口恢复那一刻才被 onclose/操作发现——
   *  Chromium 后台节流把重连 timer 推迟到了恢复时刻。 */
  private heartbeatTimer: ReturnType<typeof setTimeout> | null = null;
  private heartbeatFailures = 0;
  /** 恢复钩子防抖：窗口回到可见的处理进行中标志（防 visibilitychange 与
   *  手动操作/重连 timer 叠加出重复连接）。 */
  private restoring = false;
  /** visibilitychange 监听器（bind/unbind 用同一个引用，保证可解绑）。 */
  private readonly onVisibilityChange = () => {
    if (!this.isPageVisible()) {
      // 页面隐藏：暂停心跳（后台节流下定时器不可靠，恢复由钩子重置）
      this.stopHeartbeatTimer();
      return;
    }
    this.handleWindowRestored();
  };
  /** 订阅时惰性建连（构造不再触网——测试可先插桩再连接）。 */
  private started = false;
  /** 首次连接是否已为本连接建立焦点 Session；重连恢复原焦点，不清空运行态。 */
  private booted = false;
  private currentSessionId = "";
  /** 后端远程访问凭证提供者：**每次连接现取**（token 可能在两次连接之间被
   *  开启/轮换——构造时取死值会让重连 401 循环）。 */
  private tokenFn: (() => string | null) | null = null;
  /** 显式凭证覆盖（连接管理「连谁」切到远程后端时设置——该连接自己的 token，
   *  优先于 tokenFn）。null = 回落到 tokenFn（本机 = 宿主 token）。 */
  private tokenOverride: string | null = null;
  /** 后端切换待清账标志：setBackend 换地址/凭证后置位，**握手成功**那一刻清缓存
   *  与会话焦点（旧后端的会话 id 不能打到新后端上——session.resume/loadHistory
   *  会在新后端报「会话不存在」）。握手失败保留旧缓存并如实报错（opError）。 */
  private resetOnHello = false;
  /** delta 合帧器（行为逐字节保留，见 coalesce.ts）。 */
  private readonly coalescer = new DeltaCoalescer((ev) => this.emitRaw(ev));
  /** 事件副作用（各管理域经 onEventSideEffect 注册；握手刷新链由此拼出）。 */
  private sideEffects: EventSideEffect[] = [];

  constructor(addr = "127.0.0.1:7789", token?: string | (() => string | null)) {
    this.addr = addr;
    setAttachmentOrigin(httpOrigin(addr));
    this.tokenFn = typeof token === "function" ? token : token ? () => token : null;
  }

  /** 管理域注册事件副作用（返回注销——当前五个域恒注册一份，不注销）。 */
  onEventSideEffect(fn: EventSideEffect): () => void {
    this.sideEffects.push(fn);
    return () => {
      this.sideEffects = this.sideEffects.filter((f) => f !== fn);
    };
  }

  /** 连接焦点（会话 id）：setApproval 等「当前会话」语义的操作读它。 */
  get sessionFocus(): string {
    return this.currentSessionId;
  }

  /** 断线后 pending 的重连 timer（测试断言它被清/在；null = 无待发重连）。 */
  get pendingReconnect(): ReturnType<typeof setTimeout> | null {
    return this.reconnectTimer;
  }

  /** 切换后端（连接管理「连谁」）：换地址/凭证并立即断开重连。
   *  会话/模型/Agent 状态由新后端在握手后的初始化链里重放（connect 已有）。
   *  注意不 disposeSocket——那是卸载语义（started=false），切换后仍要保持
   *  订阅与自动重连。 */
  setBackend(addr: string, token?: string | null): void {
    const changed = this.addr !== addr || (this.tokenOverride ?? null) !== (token ?? null);
    this.addr = addr;
    setAttachmentOrigin(httpOrigin(addr));
    this.tokenOverride = token ?? null;
    if (changed) this.resetOnHello = true;
    if (changed && this.started) {
      if (this.reconnectTimer) {
        clearTimeout(this.reconnectTimer);
        this.reconnectTimer = null;
      }
      if (this.ws) {
        this.ws.onclose = null;
        this.rejectAllPending("切换后端连接");
        this.ws.close();
        this.ws = null;
      }
      this.connect();
    }
  }

  subscribe(listener: Listener): () => void {
    this.listeners.add(listener);
    if (!this.started) {
      this.started = true;
      this.bindWindowRestored();
      this.connect();
    }
    return () => {
      this.listeners.delete(listener);
      // 最后一个订阅者退订：停重连并断开（演示/测试挂载卸载不留后台连接）
      if (this.listeners.size === 0) this.disposeSocket();
    };
  }

  /** 连接 WS（自动重连）。 */
  connect() {
    if (this.ws && (this.ws.readyState === WS_READY_STATE_OPEN || this.ws.readyState === 0)) return;
    const token = this.tokenOverride ?? this.tokenFn?.() ?? null;
    const url = wsURL(this.addr, token);
    // 附件端点与升级门同宽（remote.json enabled 时无 token 一律 401）：每次连接
    // 现取的同一份 token 同步注入 URL 构造器（token 轮换随重连自动刷新）。
    setAttachmentToken(token ?? "");
    console.log("[ws] connect", url); // 连接轨迹（排查切换/重连问题的第一现场）
    const ws = new WebSocket(url);
    this.ws = ws;

    ws.onopen = () => {
      // 连接活了：连接态诚实上报（顶栏脉冲点/连接管理的数据源），心跳失败
      // 计数清零并起跳（页面隐藏时 startHeartbeat 会按住不发）
      this.emit({ type: "connectionChanged", connected: true });
      this.heartbeatFailures = 0;
      this.startHeartbeat();
      // 握手失败则不继续调用；旧连接的初始化链不得串入重连后的连接。
      this.call("connection.hello", { client: "lxcode-web", version: PROTOCOL_VERSION })
        .then(async (result) => {
          const serverVersion = (result as { version?: string } | null)?.version;
          if (serverVersion !== PROTOCOL_VERSION) {
            throw new Error(`协议版本不兼容（客户端 ${PROTOCOL_VERSION}，服务端 ${serverVersion ?? "未知"}）`);
          }
          // 握手成功才算「真的连上了新后端」：清旧后端的缓存与焦点（resetOnHello
          // 由 setBackend 置位）。握手失败走 catch——旧缓存保留、报错如实。
          if (this.resetOnHello && this.ws === ws) {
            this.resetOnHello = false;
            for (const fn of this.onHelloResetFns) fn();
            this.booted = false;
            this.currentSessionId = "";
          }
          const refresh = async (method: string, apply: (r: unknown) => void, params?: unknown) => {
            if (this.ws !== ws) return;
            try { const r = await this.call(method, params); if (this.ws === ws) apply(r); }
            catch (e) { if (this.ws === ws) this.opError(`${method}: ${String(e)}`); }
          };
          // 初始化链并行化：九个清单刷新互相独立（各自「发请求 + 事件回填」，
          // 无顺序依赖），原先逐个 await 的串行链在慢端点上一步挂就拖 10s 且
          // 中断后续——改为 allSettled 并行：单个失败报 operationError 但不
          // 中断其余，整链耗时从「九步之和」变成「最慢一步」。
          await Promise.allSettled([
            refresh("model.list", (r) => this.applyRefresh("model.list", r)),
            refresh("project.list", (r) => this.applyRefresh("project.list", r)),
            refresh("session.list", (r) => this.applyRefresh("session.list", r)),
            refresh("agent.list", (r) => this.applyRefresh("agent.list", r)),
            refresh("catalog.modules.list", (r) => this.applyRefresh("catalog.modules.list", r)),
            refresh("catalog.tools.list", (r) => this.applyRefresh("catalog.tools.list", r)),
            refresh("catalog.mcp.list", (r) => this.applyRefresh("catalog.mcp.list", r)),
            refresh("search.channels.list", (r) => this.applyRefresh("search.channels.list", r)),
            refresh("settings.todoPrompt.get", (r) => this.applyRefresh("settings.todoPrompt.get", r)),
            // 后台任务：重连/刷新后事件不会重放，必须主动拉一次清单
            //（在跑的任务在面板里不能因为重连而消失）
            refresh("job.list", (r) => this.applyRefresh("job.list", r)),
          ]);
          // 焦点恢复在并行组之后串行（session.resume 的语义依赖 session.list
          // 已刷新；chat.history 又依赖焦点落定——这两步保持原顺序）。
          // 首次连接开一个空会话；重连则恢复该连接原焦点。所有后续操作都显式带
          // session_id，因此连接焦点只为兼容旧客户端与首屏 UI 服务。
          if (!this.booted && this.ws === ws) {
            this.booted = true;
            try {
              const result = await this.call("session.new") as { session_id?: string };
              if (this.ws === ws && result?.session_id) this.focusSession(result.session_id);
            } catch (e) {
              if (this.ws === ws) {
                this.booted = false;
                this.opError(`新建会话失败: ${String(e)}`);
              }
            }
          } else if (this.ws === ws && this.currentSessionId) {
            await refresh("session.resume", () => {}, { id: this.currentSessionId });
          }
          if (this.ws === ws && this.currentSessionId) await this.loadHistory(this.currentSessionId);
        })
        .catch((e) => { if (this.ws === ws) this.opError(`初始化失败: ${e.message}`); });
    };

    ws.onmessage = (e) => {
      let msg: WsResponse;
      try {
        msg = JSON.parse(String(e.data));
      } catch {
        return; // 非 JSON 帧直接忽略（防坏数据炸监听器）
      }
      // 应答帧（有 id）
      if (msg.id !== undefined && msg.id !== null) {
        this.settlePending(Number(msg.id), msg);
        return;
      }
      // 事件帧（无 id）
      if (msg.method) {
        this.handleEvent(msg.method, msg.params);
      }
    };

    ws.onclose = () => {
      this.ws = null;
      // 连接没了：连接态诚实上报、心跳立即停（下次 onopen 重新起跳），失败计数清零
      this.emit({ type: "connectionChanged", connected: false });
      this.stopHeartbeatTimer();
      this.heartbeatFailures = 0;
      // 断连：所有挂起请求立刻失败（不等 10s 超时——后端已死，等是骗人）
      this.rejectAllPending("后端连接已断开");
      this.opError("后端连接断开，正在重连");
      // 5s 重连
      this.reconnectTimer = setTimeout(() => this.connect(), RECONNECT_MS);
    };

    ws.onerror = () => {
      // onclose 会跟着触发
    };
  }

  /** 断开并停止重连（测试/卸载用）。 */
  private disposeSocket() {
    this.started = false;
    this.unbindWindowRestored();
    this.stopHeartbeatTimer();
    this.heartbeatFailures = 0;
    if (this.ws) this.ws.onclose = null;
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    this.rejectAllPending("后端连接已断开");
    this.ws?.close();
    this.ws = null;
    // 合帧队列一并清账：dispose = 卸载语义（监听器已空/即将断开），挂起的
    // delta 没有投递对象了，留着只会在之后的一帧对空集 emit。
    this.coalescer.reset();
  }

  /** 握手成功清账钩子（管理域清各自的缓存；会话焦点/焦点会话 id 由连接层清）。 */
  onHelloReset(fn: () => void): () => void {
    this.onHelloResetFns.push(fn);
    return () => {
      this.onHelloResetFns = this.onHelloResetFns.filter((f) => f !== fn);
    };
  }
  private onHelloResetFns: (() => void)[] = [];

  // ---- 心跳与窗口恢复钩子（体验修复批次 3：消除「最小化一段时间后界面卡死」）----
  //
  // 背景：断线只靠被动 onclose 发现，而 Chromium 对隐藏页的定时器先节流到
  // ≥1 次/秒、超约 5 分钟进入 Intensive Throttling（最低 1 次/分钟）——最小化
  // 期间断的线，重连被推迟到恢复窗口那一刻。心跳负责「主动发现断线」，恢复
  // 钩子负责「回到可见的那一刻立即自检/重连」，两者互补。

  /** 页面是否可见（无 DOM 环境——如 node 测试——视为可见，便于插桩）。 */
  private isPageVisible(): boolean {
    return typeof document === "undefined" || document.visibilityState === "visible";
  }

  /** 起心跳计时：仅页面可见时跳（隐藏时暂停，恢复钩子负责重置节奏）。 */
  private startHeartbeat() {
    this.stopHeartbeatTimer();
    if (!this.isPageVisible()) return;
    this.heartbeatTimer = setTimeout(() => { void this.runHeartbeat(); }, HEARTBEAT_INTERVAL_MS);
  }

  private stopHeartbeatTimer() {
    if (this.heartbeatTimer) {
      clearTimeout(this.heartbeatTimer);
      this.heartbeatTimer = null;
    }
  }

  /** 心跳一跳：ping 成功清零失败计数；连续 2 次超时/失败判定断线，主动
   *  close 走既有 onclose → 重连流程（不另起一套重连逻辑）。 */
  private async runHeartbeat() {
    this.heartbeatTimer = null;
    const ws = this.ws;
    if (!ws || ws.readyState !== WS_READY_STATE_OPEN) return;
    if (await this.pingOnce()) {
      this.heartbeatFailures = 0;
    } else {
      this.heartbeatFailures++;
      if (this.heartbeatFailures >= HEARTBEAT_MAX_FAILURES) {
        this.heartbeatFailures = 0;
        this.stopHeartbeatTimer();
        // 只关当前这颗连接（await 期间可能已重连换新——不得误杀）
        if (this.ws === ws) ws.close();
        return;
      }
    }
    this.startHeartbeat();
  }

  /** 心跳单次探测：connection.ping 往返，3s 超时（独立于 REQUEST_TIMEOUT_MS）。
   *  静默纪律：失败只返回 false 交给调用方计数，绝不进 operationError——
   *  心跳是基础设施探测，不是用户操作，不该弹「请求失败」提示。 */
  private pingOnce(): Promise<boolean> {
    return new Promise((resolve) => {
      if (!this.ws || this.ws.readyState !== WS_READY_STATE_OPEN) {
        resolve(false);
        return;
      }
      const id = this.nextId++;
      const timeout = setTimeout(() => {
        this.pending.delete(id);
        resolve(false);
      }, HEARTBEAT_TIMEOUT_MS);
      this.pending.set(id, {
        resolve: () => { clearTimeout(timeout); resolve(true); },
        reject: () => { clearTimeout(timeout); resolve(false); },
        timeout,
      });
      try {
        this.ws.send(JSON.stringify({ jsonrpc: "2.0", id, method: "connection.ping" }));
      } catch {
        clearTimeout(timeout);
        this.pending.delete(id);
        resolve(false);
      }
    });
  }

  /** 窗口恢复钩子注册/解绑（subscribe 首次装配、disposeSocket 卸载——不泄漏）。
   *  无 DOM 环境不注册；测试用假 document 插桩驱动。 */
  private bindWindowRestored() {
    if (typeof document !== "undefined") {
      document.addEventListener("visibilitychange", this.onVisibilityChange);
    }
  }

  private unbindWindowRestored() {
    if (typeof document !== "undefined") {
      document.removeEventListener("visibilitychange", this.onVisibilityChange);
    }
  }

  /** 窗口回到可见那一刻的自检（防抖：restoring 标志保证恢复瞬间只处理一次，
   *  不与手动操作/重连 timer 叠加出重复连接；connect 自带「已在连接中就不
   *  重复」守卫兜第二层）。 */
  private handleWindowRestored() {
    if (this.restoring) return;
    this.restoring = true;
    const done = () => { this.restoring = false; };
    const ws = this.ws;
    if (!ws || ws.readyState !== WS_READY_STATE_OPEN) {
      // 已断线：清除 pending 的 5s 重连 timer，立即提前触发同一 connect 流程
      //（不动 onclose 的重连语义——这里只是把等待剪掉）
      if (this.reconnectTimer) {
        clearTimeout(this.reconnectTimer);
        this.reconnectTimer = null;
      }
      this.connect();
      done();
      this.startHeartbeat();
      return;
    }
    // 连接还在：立即发一次心跳健康检查（3s 超时），失败判定断线 → close
    // 触发既有 onclose → 重连。成功则重置心跳节奏与失败计数。
    void this.pingOnce().then((ok) => {
      done();
      if (ok) {
        this.heartbeatFailures = 0;
        this.startHeartbeat();
      } else {
        this.heartbeatFailures = 0;
        if (this.ws === ws) ws.close();
      }
    });
  }

  /** 应答帧落地：resolve/reject 并清掉超时计时器。 */
  private settlePending(id: number, msg: WsResponse) {
    const p = this.pending.get(id);
    if (!p) return;
    this.pending.delete(id);
    clearTimeout(p.timeout);
    if (msg.error) p.reject(new Error(msg.error.message));
    else p.resolve(msg.result);
  }

  /** 断连时统一拒绝全部挂起请求。 */
  private rejectAllPending(reason: string) {
    for (const [, p] of this.pending) {
      clearTimeout(p.timeout);
      p.reject(new Error(reason));
    }
    this.pending.clear();
  }

  /** 后端事件 → 前端事件（纯映射在 events.ts）+ 副作用（重拉事实源）。 */
  private handleEvent(method: string, params: unknown) {
    // ① 纯映射：绝大多数事件就是「载荷 → AgentEvent」
    const ev = mapEvent(method, params);
    if (ev) this.emit(ev);
    // ② 副作用：后端是事实源，元数据类事件要重拉列表 / 写缓存
    for (const fn of this.sideEffects) fn(method, params, ev);
  }

  /** 发 JSON-RPC 请求并等应答（10s 超时；超时/断连都会清理挂起表）。 */
  call(method: string, params?: unknown): Promise<unknown> {
    return new Promise((resolve, reject) => {
      if (!this.ws || this.ws.readyState !== WS_READY_STATE_OPEN) {
        reject(new Error("后端未连接"));
        return;
      }
      const id = this.nextId++;
      const timeout = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error("请求超时"));
      }, HEAVY_METHODS.has(method) ? HEAVY_REQUEST_TIMEOUT_MS : REQUEST_TIMEOUT_MS);
      this.pending.set(id, { resolve, reject, timeout });
      const req: WsRequest = { jsonrpc: "2.0", id, method };
      if (params !== undefined) req.params = params;
      try {
        this.ws.send(JSON.stringify(req));
      } catch (e) {
        clearTimeout(timeout);
        this.pending.delete(id);
        reject(e instanceof Error ? e : new Error(String(e)));
      }
    });
  }

  /** 事件出闸（经合帧器——delta 合帧、其余保序直发）。 */
  emit(ev: AgentEvent) {
    this.coalescer.emit(ev);
  }

  /** 合帧后的直发（DeltaCoalescer 的 raw emit 口）。 */
  private emitRaw(ev: AgentEvent) {
    this.listeners.forEach((l) => l(ev));
  }

  /** 请求级失败（不影响生成状态）→ operationError 事件。 */
  opError(message: string) {
    this.emit({ type: "operationError", message });
  }

  /** 管理域初始化刷新分发：握手并行组按方法名把结果交给注册了该方法的域。 */
  private applyRefresh(method: string, result: unknown) {
    const fn = this.refreshHandlers.get(method);
    if (fn) fn(result);
  }
  private refreshHandlers = new Map<string, (r: unknown) => void>();

  /** 域注册「握手并行组里 method 的结果怎么落缓存」。 */
  onInitList(method: string, apply: (r: unknown) => void): () => void {
    this.refreshHandlers.set(method, apply);
    return () => {
      this.refreshHandlers.delete(method);
    };
  }

  /** 拉当前会话的历史并重放视图（连接建立/切换会话后调）。 */
  loadHistory(sessionId: string): Promise<void> {
    return this.call("chat.history", { session_id: sessionId })
      .then((r) => {
        this.emit({ type: "historyLoaded", sessionId, history: snapshotFromWire(r) });
      }) as Promise<void>;
  }

  /** 读子会话的历史并发 historyLoaded（带子会话自己的 id——store 按 sessionId
   *  路由，重建的是子会话自己的 state）。child 标记：子会话历史里非通告的 user
   *  消息 = 主 Agent 派发的任务说明书，historyBlocks 据此渲染「任务」块而不是
   *  用户气泡（主会话的 loadHistory 不打这个标记）。失败原样抛——调用方必须把
   *  原因显示出来。 */
  async childHistory(sessionId: string): Promise<HistorySnapshot> {
    const r = await this.call("chat.history", { session_id: sessionId });
    const snapshot = snapshotFromWire(r);
    snapshot.child = true;
    // 用快照自己的 session_id（后端回带；缺席时回落请求参数——两者不一致时以后端为准）
    const id = snapshot.sessionId || sessionId;
    this.emit({ type: "historyLoaded", sessionId: id, history: snapshot });
    return snapshot;
  }

  /** 切换连接焦点会话（newSession / resumeSession 落焦点——发 sessionFocused）。 */
  focus(id: string) {
    this.focusSession(id);
  }

  private focusSession(id: string) {
    this.currentSessionId = id;
    this.emit({ type: "sessionFocused", id });
  }
}
