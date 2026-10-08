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
import { spawn, type ChildProcess } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, openSync, readFileSync, unlinkSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { app } from "electron";

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
  online: boolean;
  /** 节点负载百分比。 */
  load: number | null;
  /** 隧道累计用量（字节，双向计费）。 */
  used: number | null;
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

interface RawNode {
  name?: string;
  host?: string;
  vip?: number;
  flag?: number;
}

interface RawTunnel {
  id?: number;
  name?: string;
  node?: number;
  online?: boolean;
  remote?: string;
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
    views.push({
      id: x.id as number,
      name: x.name ?? TUNNEL_NAME,
      nodeName: node?.name ?? `节点 ${x.node ?? "?"}`,
      addr: node?.host && Number.isFinite(port) ? `${node.host}:${port}` : (x.remote || "待分配"),
      // 在线 = 节点端确认在线，或 frpc 正在跑（spawn 后节点端状态有延迟——
      // 不合并的话行内毫无反馈，用户会连点启动）
      online: Boolean(x.online) || state.runningId === x.id,
      load: node?.load ?? null,
      used: null,
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

// ---- 隧道操作 ----

/** 自动选节点：可创建（flag bit2）+ 在线（bit9 清零）+ 非 VIP 专属，负载最低者优先。 */
async function pickNode(t: string, groupLevel: number): Promise<{ id: number; name: string }> {
  const { nodes } = await fetchNodes(t);
  const candidates = [...nodes.entries()]
    .filter(([, n]) => typeof n.flag === "number" && (n.flag & (1 << 2)) !== 0 && (n.flag & (1 << 9)) === 0)
    .filter(([, n]) => (n.vip ?? 0) <= groupLevel)
    .sort((a, b) => (a[1].load ?? 100) - (b[1].load ?? 100));
  if (candidates.length === 0) throw new SakuraError("没有可用的节点（全部满载/离线或需要更高用户等级）");
  return { id: candidates[0][0], name: candidates[0][1].name ?? String(candidates[0][0]) };
}

async function createTunnel(): Promise<void> {
  const t = requireToken();
  setState({ busy: "正在创建隧道" });
  try {
    const node = await pickNode(t, groupLevel);
    const created = await api<{ id?: number; name?: string; remote?: string }>(`/tunnels`, {
      token: t,
      form: { name: TUNNEL_NAME, type: "tcp", node: String(node.id), local_ip: "127.0.0.1", local_port: String(localBackendPort()), remote: "" },
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
export function initSakura(onState: (s: SakuraState) => void): void {
  notify = onState;
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
  "sakura:createTunnel": (): Promise<SakuraState> => createTunnel().then(snapshot),
  "sakura:removeTunnel": (_e: unknown, id: number): Promise<SakuraState> => removeTunnel(Number(id)).then(snapshot),
  "sakura:startTunnel": (_e: unknown, id: number): Promise<SakuraState> => startTunnel(Number(id)).then(snapshot),
  "sakura:stopTunnel": (): Promise<SakuraState> => stopTunnel().then(snapshot),
};
