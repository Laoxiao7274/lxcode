// sakura.ts —— 樱花frp 公网穿透（壳主进程领域模块）。
//
// 职责（docs/sakurafrp-integration.md §4 架构决策）：API 请求从壳发（渲染层
// 无 CORS/无密钥暴露面）、frpc 由壳 spawn 并挂在壳的退出路径上、访问密钥只
// 存本机 userData（0600，不进日志）、隧道打本地后端端口（Go 后端零改动——
// 它不知道自己在被穿透）。API 形状依据 natfrp 官方 OpenAPI v4（仅调用，不分发）。
//
// 状态机：主进程持有唯一真相（SakuraState），每次变更经回调推给渲染层；
// 渲染层只发意图（login/create/start/...），不自己拼 API。
// 测试注入口：LXCODE_SAKURA_API 覆盖 API 基址（e2e 指向本地 mock）。
//
// 节点选择（2026-12）：列表 + 实测延迟（TCP connect，见 sakura-nodes.ts 头注）
// 经 sakura:listNodes 拉、延迟结果经 sakura:nodeLatency 异步推；创建隧道可带
// 显式 nodeId（校验在候选列表里且可用），不传则沿用自动选（负载最低）。
import { spawn, type ChildProcess } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, openSync, readFileSync, unlinkSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { app } from "electron";
import {
  AUTO_HTTPS_EXTRA,
  autoHttpsConfigured,
  candidateNodes,
  createLatencyProber,
  resolveNodeChoice,
  withAutoHttps,
  type NodeCandidate,
  type RawNode,
} from "./sakura-nodes";

const API_BASE = process.env.LXCODE_SAKURA_API ?? "https://api.natfrp.com/v4";
/** 隧道命名约定：靠它识别「我们的」隧道，复用不重建（文档 §4.3）。 */
export const TUNNEL_NAME = "lxcode-backend";
const POLL_MS = 60_000; // API 限频：轮询不低于 60s（文档 §7）
const CONNECT_TIMEOUT_MS = 10_000;

// ---- 对渲染层的状态视图 ----

export interface SakuraAccountView {
  name: string;
  avatar: string | null;
  /** [本日消耗字节, 总剩余字节]。 */
  traffic: [number, number];
  speed: string;
  /** 用户组名称（与渲染层 SakuraAccount.group 同形）。 */
  group: string;
}

export interface SakuraTunnelView {
  id: number;
  name: string;
  nodeName: string;
  /** 公网连接地址 host:port。 */
  addr: string;
  /** 节点端确认在线（/tunnels 的 x.online——服务端真实在线态，不与 running 合并）。 */
  online: boolean;
  /** 本机 frpc 正在服务这条隧道（runningId===id）。 */
  running: boolean;
  /**
   * 本机 frpc 在跑但节点端还没确认在线（starting = running && !online，渲染层
   * 可自行推导；壳里直接给出省得两端各推一遍）。此窗口内公网地址扫了连不上
   * ——二维码入口只认 online。
   */
  starting?: boolean;
  /** 节点负载百分比（/node/stats 拉不到时为 null——UI 显示「—」，不是 0%）。 */
  load: number | null;
  /** 隧道累计用量（字节，双向计费）。 */
  used: number | null;
  /** 自动 HTTPS 已配置（extra.auto_https 非空）——未配置的隧道 WS 流量会被内地节点 501。 */
  autoHttps: boolean;
}

/** 节点选择列表项（渲染层投影：候选节点 + 已测出的延迟）。 */
export interface SakuraNodeView {
  id: number;
  name: string;
  host: string;
  /** 负载百分比（null = 未知）。 */
  load: number | null;
  /** 能否用来创建隧道。 */
  usable: boolean;
  /** 不可用原因（可用时 null）：需更高等级 / 离线 / 不可创建隧道 / 节点地址缺失。 */
  reason: string | null;
  /** 已测延迟（ms；null = 还没测出/测不出）。 */
  latency: number | null;
}

export interface SakuraNodeList {
  nodes: SakuraNodeView[];
  /** 当前用户组等级（UI 标注「需更高等级」的依据）。 */
  groupLevel: number;
  /** 延迟是否会在随后异步补（列表先出、延迟逐个推）。 */
  probing: boolean;
}

/** 单节点延迟探测结果（IPC 推送：列表先出，延迟异步填）。 */
export interface SakuraLatencyEvent {
  host: string;
  latency: number | null;
}

export interface SakuraState {
  loggedIn: boolean;
  account: SakuraAccountView | null;
  /** 账户冻结（/user/info 冻结形态）——展示后应引导退出。 */
  ban: { title: string; reason: string } | null;
  tunnels: SakuraTunnelView[];
  /** frpc 正在服务的隧道 id（null = 没在跑）。 */
  runningId: number | null;
  /** frpc 二进制是否就绪（不可用时 frpcHint 说明原因）。 */
  frpcReady: boolean;
  frpcHint: string | null;
  /** 进行中的操作描述（前端据此禁按钮；null = 空闲）。 */
  busy: string | null;
  /** 最近一次用户触发操作的失败原因（后台轮询失败不写这里）。 */
  error: string | null;
}

let state: SakuraState = emptyState();
let token: string | null = null;
let groupLevel = 0; // 用户组等级（选节点过滤 VIP 专属用，不进渲染层）
let pollTimer: ReturnType<typeof setInterval> | null = null;
let frpc: ChildProcess | null = null;
let notify: ((s: SakuraState) => void) | null = null;
let notifyLatency: ((e: SakuraLatencyEvent) => void) | null = null;
/** 延迟探测（TCP connect 到 frps 控制端口，结果 5 分钟缓存）。 */
const prober = createLatencyProber();

function emptyState(): SakuraState {
  return { loggedIn: false, account: null, ban: null, tunnels: [], runningId: null, frpcReady: false, frpcHint: null, busy: null, error: null };
}

function snapshot(): SakuraState {
  return state;
}

function setState(patch: Partial<SakuraState>): void {
  state = { ...state, ...patch };
  notify?.(state);
}

// ---- API 客户端 ----

class SakuraError extends Error {}

/** 统一请求：Bearer 认证（POST 同时带 form body）；错误归一为 {code,msg}。 */
async function api<T>(path: string, opts: { token?: string | null; form?: Record<string, string>; raw?: boolean } = {}): Promise<T> {
  const headers: Record<string, string> = { Accept: opts.raw ? "text/plain, text/toml, application/json" : "application/json" };
  if (opts.token) headers.Authorization = `Bearer ${opts.token}`;
  const init: RequestInit = { headers, signal: AbortSignal.timeout(CONNECT_TIMEOUT_MS) };
  if (opts.form) {
    headers["Content-Type"] = "application/x-www-form-urlencoded";
    init.method = "POST";
    init.body = new URLSearchParams(opts.form).toString();
  }
  const res = await fetch(`${API_BASE}${path}`, init).catch((e) => {
    throw new SakuraError(`网络请求失败: ${e instanceof Error ? e.message : String(e)}`);
  });
  if (res.status === 429) throw new SakuraError("请求过于频繁（API 限频），稍后再试");
  if (opts.raw) {
    // 配置文件端点：文本直返；但错误仍是 JSON {code,msg}
    const ct = res.headers.get("content-type") ?? "";
    const text = await res.text();
    if (!res.ok || ct.includes("application/json")) {
      throw jsonError(text, res.status);
    }
    return text as unknown as T;
  }
  const body = (await res.json().catch(() => null)) as unknown;
  if (!res.ok) throw jsonError(JSON.stringify(body ?? ""), res.status);
  // 成功响应没有 code 字段；{code,msg} 形态即错误（ ErrorResponse 全局形状）
  const err = body as { code?: number; msg?: string } | null;
  if (err && typeof err.code === "number" && typeof err.msg === "string") throw new SakuraError(err.msg);
  return body as T;
}

function jsonError(text: string, status: number): SakuraError {
  try {
    const parsed = JSON.parse(text) as { code?: number; msg?: string };
    if (parsed?.msg) return new SakuraError(parsed.msg);
  } catch { /* 非 JSON，走状态码兜底 */ }
  return new SakuraError(`API 返回 HTTP ${status}`);
}

// ---- 领域读取 ----

interface RawUserInfo {
  name?: string;
  avatar?: string;
  speed?: string;
  token?: string;
  traffic?: number[];
  group?: { name?: string; level?: number };
  ban?: { title?: string; reason?: string };
}

interface RawTunnel {
  id?: number;
  name?: string;
  node?: number;
  online?: boolean;
  remote?: string;
  /** ini 风格键值串（auto_https 等）；缺失 = 未配置任何扩展项。 */
  extra?: string;
}

async function fetchAccount(t: string): Promise<{ account: SakuraAccountView | null; ban: SakuraState["ban"] }> {
  const u = await api<RawUserInfo>("/user/info", { token: t });
  if (u.ban) {
    return { account: null, ban: { title: u.ban.title ?? "账户已被冻结", reason: u.ban.reason ?? "" } };
  }
  groupLevel = u.group?.level ?? 0;
  return {
    account: {
      name: u.name ?? "未知用户",
      avatar: u.avatar ?? null,
      traffic: [u.traffic?.[0] ?? 0, u.traffic?.[1] ?? 0],
      speed: u.speed ?? "未知限速",
      group: u.group?.name ?? "普通用户",
    },
    ban: null,
  };
}

/** 节点字典（id → 名称/地址）+ 负载表。 */
async function fetchNodes(t: string): Promise<{ nodes: Map<number, RawNode & { load: number | null }> }> {
  const list = await api<Record<string, RawNode>>("/nodes", { token: t });
  let loads = new Map<number, number>();
  try {
    const stats = await api<{ nodes?: { id?: number; load?: number }[] }>("/node/stats", { token: t });
    loads = new Map((stats.nodes ?? []).filter((n) => typeof n.id === "number").map((n) => [n.id as number, n.load ?? 0]));
  } catch { /* 负载表拉不到不挡隧道列表 */ }
  const nodes = new Map<number, RawNode & { load: number | null }>();
  for (const [id, n] of Object.entries(list)) {
    nodes.set(Number(id), { ...n, load: loads.get(Number(id)) ?? null });
  }
  return { nodes };
}

// ---- 本地隧道缓存：lxcode 建过/管理过的隧道 id ----
// 用户拍板：不按名字过滤（服务端会把名字规范化，面板自建的也会混进来），
// 而是**创建时拿回显缓存 id**；首次迁移按 lxcode 前缀模糊播种一轮（兼容
// 历史创建的 lxcode-backend / lxcode_backend）。缓存文件缺失 = 首次运行；
// 文件存在但空 = 用户确实全删了（展示空列表，不再播种）。

function managedPath(): string {
  return join(app.getPath("userData"), "sakurafrp-tunnels.json");
}

let managedLoaded = false;
let managedIds: number[] = [];

function loadManaged(): void {
  if (managedLoaded) return;
  try {
    const raw = JSON.parse(readFileSync(managedPath(), "utf-8")) as { ids?: unknown };
    managedIds = Array.isArray(raw.ids) ? raw.ids.filter((n): n is number => typeof n === "number") : [];
    managedLoaded = true;
  } catch {
    managedLoaded = false; // 文件缺失/坏 = 尚未播种，交给 fetchTunnels 按名播种
  }
}

function saveManaged(): void {
  try {
    mkdirSync(app.getPath("userData"), { recursive: true });
    writeFileSync(managedPath(), JSON.stringify({ ids: managedIds }), { encoding: "utf-8", mode: 0o600 });
  } catch (e) {
    console.log(`[sakura] 隧道缓存写入失败（不影响本次展示）: ${errMsg(e)}`);
  }
}

/** 展示 = 本地缓存的隧道集合 ∩ 账户实际存在的隧道。 */
async function fetchTunnels(t: string, nodes: Map<number, RawNode & { load: number | null }>): Promise<SakuraTunnelView[]> {
  const list = await api<RawTunnel[]>("/tunnels", { token: t });
  loadManaged();
  if (!managedLoaded) {
    // 首次播种：lxcode 前缀模糊匹配（覆盖服务端把连字符规范成下划线的情况）
    managedIds = list
      .filter((x) => typeof x.id === "number" && /^lxcode/i.test(x.name ?? ""))
      .map((x) => x.id as number);
    managedLoaded = true;
    if (managedIds.length > 0) saveManaged();
  }
  const ours = list.filter((x) => typeof x.id === "number" && managedIds.includes(x.id as number));
  const views: SakuraTunnelView[] = [];
  for (const x of ours) {
    const node = nodes.get(x.node ?? -1);
    const port = parseInt(x.remote ?? "", 10);
    const running = state.runningId === x.id;
    const online = Boolean(x.online);
    views.push({
      id: x.id as number,
      name: x.name ?? TUNNEL_NAME,
      nodeName: node?.name ?? `节点 ${x.node ?? "?"}`,
      addr: node?.host && Number.isFinite(port) ? `${node.host}:${port}` : (x.remote || "待分配"),
      // 三态拆分（2026-12 用户拍板）：online = 节点端确认在线（服务端真实态），
      // running = 本机 frpc 在跑。旧实现把 runningId===id 合并进 online，导致
      // 「在线」失真（frpc 拉配置+注册节点要几秒，用户看到在线其实还没连上）。
      online,
      running,
      starting: running && !online,
      load: node?.load ?? null,
      used: null,
      autoHttps: autoHttpsConfigured(x.extra),
    });
  }
  // 隧道流量：每条一次请求，条数少时直取；失败不挡列表（used 留 null）
  if (views.length > 0 && views.length <= 5) {
    await Promise.all(views.map(async (v) => {
      try {
        const hist = await api<Record<string, number>>(`/tunnel/traffic?id=${v.id}`, { token: t });
        v.used = Object.values(hist).reduce((a, b) => a + b, 0);
      } catch { /* 用量缺省 */ }
    }));
  }
  return views;
}

/** 全量刷新：账户 + 节点 + 隧道。后台轮询与用户刷新共用（quiet 不覆盖用户操作错误）。 */
async function refresh(quiet: boolean): Promise<void> {
  if (!token) return;
  try {
    const { account, ban } = await fetchAccount(token);
    if (ban) {
      setState({ loggedIn: true, account: null, ban, tunnels: [] });
      return;
    }
    const { nodes } = await fetchNodes(token);
    const tunnels = await fetchTunnels(token, nodes);
    setState({ loggedIn: true, account, ban: null, tunnels, error: quiet ? state.error : null });
  } catch (e) {
    if (!quiet) setState({ error: errMsg(e) });
    else console.log(`[sakura] 后台刷新失败（保留上次数据）: ${errMsg(e)}`);
  }
}

// ---- 节点列表与延迟探测 ----

/**
 * 候选节点列表（含已测延迟，未知为 null）+ 当前用户组等级。
 *
 * 首屏只带**缓存里已知**的延迟（点开面板立即有数），随后按 hosts 异步探测并
 * 逐个推 sakura:nodeLatency——列表不被探测阻塞（70 个节点全测完要十几秒）。
 * 探测本身用 5 分钟缓存 + 并发 6，面板反复开关不会刷屏式 connect。
 */
async function listNodes(): Promise<SakuraNodeList> {
  const t = requireToken();
  const { nodes } = await fetchNodes(t);
  const cands = candidateNodes(nodes.entries(), groupLevel);
  const views: SakuraNodeView[] = cands.map((c) => {
    const cached = c.host ? prober.peek(c.host) : { known: false, ms: null };
    return {
      id: c.id,
      name: c.name,
      host: c.host,
      load: c.load,
      usable: c.usable,
      reason: c.reason,
      latency: cached.known ? cached.ms : null,
    };
  });
  // 只探可用且未缓存的节点：不可用的选了也建不了，别浪费连接
  const hosts = cands.filter((c) => c.usable).map((c) => c.host);
  void prober
    .probeAll(hosts, (host, ms) => notifyLatency?.({ host, latency: ms }))
    .catch((e) => console.log(`[sakura] 延迟探测中断: ${errMsg(e)}`));
  return { nodes: views, groupLevel, probing: true };
}

// ---- 隧道操作 ----

/**
 * 选节点：候选列表由 candidateNodes 给出（与 sakura:listNodes 同一份判定），
 * nodeId 传了就校验它当前可用，不传就自动选（负载最低）。
 */
async function chooseNode(t: string, nodeId: number | null | undefined): Promise<NodeCandidate> {
  const { nodes } = await fetchNodes(t);
  const cands = candidateNodes(nodes.entries(), groupLevel);
  const choice = resolveNodeChoice(cands, nodeId);
  if (!choice.ok) throw new SakuraError(choice.error);
  return choice.node;
}

async function createTunnel(nodeId?: number | null): Promise<void> {
  const t = requireToken();
  setState({ busy: "正在创建隧道" });
  try {
    const node = await chooseNode(t, nodeId);
    // extra 带 auto_https = auto（ini 键值串，官方示例同款）：内地节点对 TCP
    // 隧道上的 HTTP（WS 升级）流量回 501（官方 FAQ 建站条件表：内地节点 +
    // TCP + HTTP 流量 = 需配置自动 HTTPS）。auto 模式下 frpc 不碰流量、节点
    // 按 SNI 自动签证书并在节点侧终结 TLS。passthrough 模式（更接近纯 TCP）
    // 官方未承诺同样免除 501 判定——待真机验证后再考虑。
    const created = await api<{ id?: number; name?: string; remote?: string }>(`/tunnels`, {
      token: t,
      form: {
        name: TUNNEL_NAME, type: "tcp", node: String(node.id),
        local_ip: "127.0.0.1", local_port: String(localBackendPort()), remote: "",
        extra: AUTO_HTTPS_EXTRA,
      },
    });
    // 回显 id 入缓存：这就是「这条是 lxcode 建的」的事实源（不依赖名字）
    if (typeof created?.id === "number") {
      loadManaged();
      if (!managedIds.includes(created.id)) {
        managedIds.push(created.id);
        saveManaged();
      }
    }
    await refresh(true);
  } catch (e) {
    setState({ error: errMsg(e) });
    throw e;
  } finally {
    setState({ busy: null });
  }
}

async function removeTunnel(id: number): Promise<void> {
  const t = requireToken();
  setState({ busy: "正在删除隧道" });
  try {
    if (state.runningId === id) stopFrpc();
    await api(`/tunnel/delete`, { token: t, form: { ids: String(id) } });
    // 删除成功 → 出缓存（服务端列表已无它，留在集合里也无害，但保持干净）
    loadManaged();
    if (managedIds.includes(id)) {
      managedIds = managedIds.filter((x) => x !== id);
      saveManaged();
    }
    await refresh(true);
  } catch (e) {
    setState({ error: errMsg(e) });
    throw e;
  } finally {
    setState({ busy: null });
  }
}

// ---- auto_https 自愈 ----
//
// 存量隧道（extra 没带 auto_https）在启动前补一次 edit：缺失/不对 →
// POST /tunnel/edit（保留原有其他键，写回 auto_https = auto）→ 已配置则
// 跳过（幂等）。改完 extra 后 frpc 重启即生效——frpc 每次启动都从服务端拉
// 配置，所以放在 spawn 之前检查即可，无需 /tunnel/config。

/**
 * 确保隧道的 auto_https 已配置；已配置（幂等）返回 false，做了 edit 返回 true。
 * 失败**不阻断启动**（edit 挂了隧道照常能跑，只是内地节点可能 501）——错误
 * 记日志并回 false，由调用方决定要不要提示。
 */
async function ensureAutoHttps(t: string, id: number, currentExtra: unknown): Promise<boolean> {
  if (autoHttpsConfigured(currentExtra)) return false;
  const extra = withAutoHttps(currentExtra);
  await api(`/tunnel/edit`, { token: t, form: { id: String(id), extra } });
  console.log(`[sakura] 隧道 ${id} 已补 auto_https = auto（/tunnel/edit）`);
  return true;
}

/** 手动触发口（IPC sakura:repairTunnel）：按当前列表补一遍 auto_https。 */
async function repairTunnels(): Promise<SakuraState> {
  const t = requireToken();
  let repaired = 0;
  try {
    const raw = await api<RawTunnel[]>("/tunnels", { token: t });
    for (const x of raw) {
      if (typeof x.id !== "number") continue;
      try {
        if (await ensureAutoHttps(t, x.id, x.extra)) repaired++;
      } catch (e) {
        console.log(`[sakura] 隧道 ${x.id} auto_https 修复失败: ${errMsg(e)}`);
      }
    }
    await refresh(true);
  } catch (e) {
    setState({ error: errMsg(e) });
    throw e;
  }
  console.log(`[sakura] auto_https 修复完成：${repaired} 条`);
  return snapshot();
}

// ---- frpc 生命周期 ----

function frpcDir(): string {
  return join(app.getPath("userData"), "frpc");
}

function frpcExeName(): string {
  return process.platform === "win32" ? "frpc.exe" : "frpc";
}

/** 本地后端端口（隧道指向它；与壳直连的同一个后端，LXCODE_ADDR 可覆盖）。 */
function localBackendPort(): number {
  const addr = process.env.LXCODE_ADDR ?? "127.0.0.1:7789";
  const port = Number(addr.split(":").pop());
  return Number.isFinite(port) && port > 0 ? port : 7789;
}

/** frpc 可执行文件：env 覆盖 → userData（曾下载/手动放置）→ 打包 extraResources → 自动下载。 */
async function ensureFrpc(): Promise<string> {
  const exeName = frpcExeName();
  const envPath = process.env.LXCODE_FRPC_PATH;
  if (envPath && existsSync(envPath)) return envPath;
  const userPath = join(frpcDir(), exeName);
  if (existsSync(userPath)) return userPath;
  if (app.isPackaged) {
    const bundled = join(process.resourcesPath, "frpc", exeName);
    if (existsSync(bundled)) return bundled;
  }
  return downloadFrpc(userPath);
}

/** 从 /system/clients 取下载信息（无鉴权）并校验哈希落盘。失败抛人话错误。 */
async function downloadFrpc(dest: string): Promise<string> {
  const clients = await api<Record<string, { ver?: string; archs?: Record<string, { url?: string; hash?: string; size?: number }> }>>("/system/clients");
  const categoryKey = Object.keys(clients).find((k) => /frpc/i.test(k));
  if (!categoryKey) throw new SakuraError("无法获取 frpc 下载信息（/system/clients 无 frpc 分类）");
  const archs = clients[categoryKey].archs ?? {};
  const archKey = Object.keys(archs).find((k) => /win/i.test(k) && /amd64|x64|64/i.test(k));
  if (!archKey) throw new SakuraError("无法获取 frpc 的 Windows 下载项");
  const { url, hash } = archs[archKey];
  if (!url) throw new SakuraError("frpc 下载链接缺失");
  const res = await fetch(url, { signal: AbortSignal.timeout(120_000) });
  if (!res.ok) throw new SakuraError(`frpc 下载失败: HTTP ${res.status}`);
  const buf = Buffer.from(await res.arrayBuffer());
  if (hash) {
    const algo = hash.length === 64 ? "sha256" : "md5"; // 官方示例为 32 位十六进制（MD5）
    const actual = createHash(algo).update(buf).digest("hex");
    if (actual.toLowerCase() !== hash.toLowerCase()) throw new SakuraError(`frpc 下载校验失败（${algo} 不匹配）——可能被篡改，已丢弃`);
  }
  mkdirSync(frpcDir(), { recursive: true });
  writeFileSync(dest, buf);
  return dest;
}

/** 启动隧道 = spawn natfrp frpc（官方姿势，frpc 用户手册 /frpc/manual）。
 *
 *  启动参数走**环境变量**而不是命令行：NATFRP_TOKEN + NATFRP_TARGET
 *  （v0.39.1-sakura-1.1 起支持）——访问密钥不进进程列表；`-n` 跳过更新检查
 *  （无人值守 spawn，别让 frpc 自己拉新版本）。frpc 自己从服务器拉配置，
 *  不需要 /tunnel/config。
 *  防重入：同一条隧道已在跑就直接回读状态（用户看不到反馈会连点——
 *  实测连点 5 次产生 5 个 frpc 实例互相打架）。 */
async function startTunnel(id: number): Promise<void> {
  const t = requireToken();
  if (state.runningId === id && frpc !== null) {
    // 已在跑：不重复 spawn，回读一次让行内状态立即反映
    await refresh(true);
    return;
  }
  setState({ busy: "正在启动隧道" });
  try {
    // 启动前自愈：存量隧道 extra 缺 auto_https → 先 /tunnel/edit 补上再 spawn
    // （frpc 每次启动从服务端拉配置，改完重启即生效）。失败不阻断启动，
    // 只记日志——内地节点 501 的修复尝试不该让隧道整个起不来。
    const raw = await api<RawTunnel[]>("/tunnels", { token: t });
    const current = raw.find((x) => x.id === id);
    await ensureAutoHttps(t, id, current?.extra).catch((e) => {
      console.log(`[sakura] 隧道 ${id} auto_https 修复失败（继续启动）: ${errMsg(e)}`);
    });
    const exe = await ensureFrpc();
    if (state.runningId !== null && state.runningId !== id) stopFrpc();
    mkdirSync(frpcDir(), { recursive: true });
    // frpc 日志落文件：真机排障全靠它（连接失败/端口冲突都在这里）
    const logPath = join(frpcDir(), `frpc-${id}.log`);
    const logFd = openSync(logPath, "a");
    const child = spawn(exe, ["-n"], {
      cwd: frpcDir(), // 工作目录约定：frpc 的更新检查/配置缓存都在这里
      env: { ...process.env, NATFRP_TOKEN: t, NATFRP_TARGET: String(id) },
      windowsHide: true,
      stdio: ["ignore", logFd, logFd],
    });
    frpc = child;
    setState({ runningId: id, frpcReady: true, frpcHint: null });
    child.on("exit", (code) => {
      if (frpc === child) {
        frpc = null;
        setState({ runningId: null });
        console.log(`[sakura] frpc 退出（码 ${code}），日志见 frpc-${id}.log`);
        // 非零退出 = 连接失败之类：把日志尾巴带到 UI（frpc 原话比状态码有用）
        if (code !== 0) {
          try {
            const tail = readFileSync(logPath, "utf-8").split(/\r?\n/).filter(Boolean).slice(-3).join(" | ");
            setState({ frpcHint: tail ? `frpc 退出（码 ${code}）：${tail}` : `frpc 退出（码 ${code}）` });
          } catch {
            setState({ frpcHint: `frpc 退出（码 ${code}）` });
          }
        }
        void refresh(true); // 节点端下线状态要回读
      }
    });
    // spawn 成功 ≠ 节点端立刻在线：稍候回读，让行内「在线」标签尽快翻转
    setTimeout(() => { if (frpc === child) void refresh(true); }, 4_000);
    setTimeout(() => { if (frpc === child) void refresh(true); }, 10_000);
  } catch (e) {
    setState({ error: errMsg(e), frpcHint: "可手动放置 frpc（0.51.0-sakura-14+）到 userData/frpc/ 后重试" });
    throw e;
  } finally {
    setState({ busy: null });
  }
}

function stopFrpc(): void {
  if (frpc) {
    frpc.kill();
    frpc = null;
    setState({ runningId: null });
  }
}

async function stopTunnel(): Promise<void> {
  if (frpc) {
    stopFrpc();
    await refresh(true);
  }
}

// ---- 登录态 ----

function tokenPath(): string {
  return join(app.getPath("userData"), "sakurafrp.json");
}

function saveToken(t: string): void {
  mkdirSync(app.getPath("userData"), { recursive: true });
  // 0600：密钥文件只归当前用户（凭证哲学——本机保管，不进日志不进渲染层）
  writeFileSync(tokenPath(), JSON.stringify({ token: t }), { encoding: "utf-8", mode: 0o600 });
}

function loadToken(): string | null {
  try {
    const raw = JSON.parse(readFileSync(tokenPath(), "utf-8")) as { token?: string };
    return typeof raw.token === "string" && raw.token ? raw.token : null;
  } catch {
    return null;
  }
}

function requireToken(): string {
  if (!token) throw new SakuraError("未登录");
  return token;
}

async function login(key: string): Promise<void> {
  const t = key.trim();
  if (!t) throw new SakuraError("访问密钥不能为空");
  setState({ busy: "正在校验访问密钥" });
  try {
    const { account, ban } = await fetchAccount(t);
    if (ban) throw new SakuraError(`${ban.title}${ban.reason ? `：${ban.reason}` : ""}`);
    token = t;
    saveToken(t);
    setState({ loggedIn: true, account, ban: null, error: null });
    startPolling();
    await refresh(true);
    void refreshFrpcReadiness();
  } catch (e) {
    setState({ error: errMsg(e) });
    throw e;
  } finally {
    setState({ busy: null });
  }
}

function logout(): void {
  stopFrpc();
  stopPolling();
  token = null;
  try { unlinkSync(tokenPath()); } catch { /* 本来就没有 */ }
  state = emptyState();
  notify?.(state);
}

function startPolling(): void {
  stopPolling();
  pollTimer = setInterval(() => { void refresh(true); }, POLL_MS);
}

function stopPolling(): void {
  if (pollTimer) {
    clearInterval(pollTimer);
    pollTimer = null;
  }
}

function errMsg(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

// ---- 装配 ----

/** 初始化：读回密钥（有则自动恢复登录态）+ 注册状态推送。 */
export function initSakura(onState: (s: SakuraState) => void, onLatency?: (e: SakuraLatencyEvent) => void): void {
  notify = onState;
  notifyLatency = onLatency ?? null;
  const saved = loadToken();
  if (saved) {
    token = saved;
    setState({ loggedIn: true, busy: "正在恢复登录" });
    void refresh(true).finally(() => setState({ busy: null }));
    startPolling();
  }
  void refreshFrpcReadiness();
}

async function refreshFrpcReadiness(): Promise<void> {
  try {
    await ensureFrpc();
    setState({ frpcReady: true, frpcHint: null });
  } catch (e) {
    setState({ frpcReady: false, frpcHint: errMsg(e) });
  }
}

export function shutdownSakura(): void {
  stopFrpc();
  stopPolling();
}

/** IPC handler 表（main.ts 逐个注册——invoke 请求-应答语义）。 */
export const sakuraHandlers = {
  "sakura:getState": (): SakuraState => snapshot(),
  "sakura:login": (_e: unknown, key: string): Promise<SakuraState> => login(String(key ?? "")).then(snapshot),
  "sakura:logout": (): SakuraState => { logout(); return snapshot(); },
  "sakura:refresh": (): Promise<SakuraState> => refresh(false).then(snapshot),
  "sakura:listNodes": (): Promise<SakuraNodeList> => listNodes(),
  // nodeId 可选：不传（或非数字）= 自动选（负载最低），传了就校验候选与可用性
  "sakura:createTunnel": (_e: unknown, nodeId?: number | null): Promise<SakuraState> =>
    createTunnel(typeof nodeId === "number" && Number.isFinite(nodeId) ? nodeId : null).then(snapshot),
  "sakura:removeTunnel": (_e: unknown, id: number): Promise<SakuraState> => removeTunnel(Number(id)).then(snapshot),
  "sakura:startTunnel": (_e: unknown, id: number): Promise<SakuraState> => startTunnel(Number(id)).then(snapshot),
  "sakura:stopTunnel": (): Promise<SakuraState> => stopTunnel().then(snapshot),
  // 手动兜底口：对账户里 lxcode 管理的存量隧道补 auto_https（幂等；前端暂未接）。
  "sakura:repairTunnel": (): Promise<SakuraState> => repairTunnels(),
};
