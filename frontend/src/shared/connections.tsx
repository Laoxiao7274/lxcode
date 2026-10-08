// 连接域：壳 ↔ 后端的连接管理（前后端完全隔离的落点）。
// 本地（127.0.0.1:7789 内置回落默认）+ 远程连接（Linux 开发服务器等，
// 地址 + token）；本机也能被别人连——开启远程访问后展示地址与 token
// （凭证由服务端生成与校验，壳只展示/复制）。
// 公网穿透（樱花frp）：登录（访问密钥）→ 自动建隧道 → 公网连接地址 +
// 流量/隧道信息展示。真实链路经 sakuraBridge（壳主进程持状态与 frpc 进程，
// 本域只发意图收推送——App 按 SettingsProvider source 的同款注入模式传入）；
// 局域网远程访问的真数据（token/开关/局域网地址）经 remoteControlBridge。
// 浏览器模式无桥 → 演示回落。实现文档 docs/sakurafrp-integration.md。
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import type { SakuraBridge, SakuraStateView } from "../agent/sakura";
import type { RemoteControlBridge, RemoteControlState } from "../agent/host";

/** 一条远程后端连接（地址 + token 凭证）。 */
export interface RemoteConn {
  id: string;
  /** 显示名（如「公司开发机」）。 */
  name: string;
  /** 后端地址（host:port 或 https://host）。 */
  addr: string;
  /** 连接凭证（服务端生成）。 */
  token: string;
}

/** 当前活动连接：本机 或 某条远程。 */
export type ActiveConn = "local" | string;

/** 樱花frp 账户（登录态——本地只存访问密钥，凭证哲学同上）。 */
export interface SakuraAccount {
  name: string;
  /** [本日消耗字节, 总剩余字节]。 */
  traffic: [number, number];
  /** 限速描述（如 "10 Mbps"）。 */
  speed: string;
  /** 用户组（VIP 展示）。 */
  group: string;
}

/** 一条公网隧道（tcp——本地 7789 → 樱花frp 节点；命名约定 lxcode-backend）。 */
export interface SakuraTunnel {
  id: number;
  name: string;
  /** 节点名（展示用）。 */
  nodeName: string;
  /** 公网连接地址（节点 host + 远程端口）。 */
  addr: string;
  /** 在线（frpc 在跑且节点可达）。 */
  online: boolean;
  /** 节点负载百分比（选节点时的参考）。 */
  load: number;
  /** 隧道累计用量（字节——双向计费）。 */
  used: number;
}

/** 字节数人性化（流量展示）。 */
export function humanBytes(bytes: number): string {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let v = bytes;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  return `${v >= 100 ? Math.round(v) : v.toFixed(1)} ${units[i]}`;
}

/** 遮罩 token：保头尾各 4 位，中间打点。 */
export function maskToken(token: string): string {
  if (token.length <= 8) return token;
  return `${token.slice(0, 4)}••••${token.slice(-4)}`;
}

/** 生成演示 token（后端化时由服务端生成——壳不造凭证）。 */
function demoToken(): string {
  const seg = () => Math.random().toString(16).slice(2, 10);
  return `${seg()}-${seg()}-${seg()}`;
}

interface ConnectionsValue {
  /** 远程连接列表（本机不在内——内置回落默认）。 */
  remotes: RemoteConn[];
  addRemote: (conn: RemoteConn) => void;
  updateRemote: (conn: RemoteConn) => void;
  removeRemote: (id: string) => void;
  /** 当前活动连接（local = 本机）。 */
  active: ActiveConn;
  setActive: (id: ActiveConn) => void;
  /** 本机远程访问（被连）：开启后所有连接须携带 token（后端强制校验）。 */
  remoteAccess: boolean;
  setRemoteAccess: (on: boolean) => void;
  /** 远程访问开关/token 操作进行中。 */
  remoteBusy: boolean;
  /** 对外地址（局域网 IP:端口——壳上报；无桥时演示值）。 */
  remoteAddr: string;
  /** 访问 token（后端生成与校验）。 */
  remoteToken: string;
  regenerateRemoteToken: () => void;
  /** 樱花frp 登录态（null = 未登录；冻结/失败态见 sakuraError）。 */
  sakura: SakuraAccount | null;
  loginSakura: (key: string) => void;
  logoutSakura: () => void;
  /** 樱花frp 进行中的操作描述（null = 空闲，UI 据此禁按钮）。 */
  sakuraBusy: string | null;
  /** 樱花frp 最近一次操作失败原因 / 账户冻结提示。 */
  sakuraError: string | null;
  /** 手动刷新（账户流量 + 隧道列表——面板上新建的隧道也会出现）。 */
  refreshSakura: () => void;
  /** 公网隧道（登录后可建——lxcode-backend 命名约定）。 */
  tunnels: SakuraTunnel[];
  createTunnel: () => void;
  toggleTunnel: (id: number) => void;
  removeTunnel: (id: number) => void;
}

const Ctx = createContext<ConnectionsValue | null>(null);

/** 演示名单（无持久化数据时的回落——用户删掉后不再出现，localStorage 生效）。 */
const DEMO_REMOTE: RemoteConn[] = [
  { id: "dev-server", name: "公司开发机", addr: "10.0.0.8:7789", token: "a3f8b2c1-9d4e-4f2a-8c6b-7e1d5a9f0b3c" },
];

/** 远程连接列表的本地持久化键（Electron 渲染层 localStorage 可用）。 */
const LS_REMOTES = "lx.remoteConns";
const LS_ACTIVE = "lx.activeConn";

function loadRemotes(): RemoteConn[] {
  try {
    const raw = localStorage.getItem(LS_REMOTES);
    if (raw) {
      const parsed = JSON.parse(raw) as unknown;
      if (Array.isArray(parsed)) return parsed.filter((c): c is RemoteConn =>
        typeof c === "object" && c !== null && typeof (c as RemoteConn).id === "string" && typeof (c as RemoteConn).addr === "string");
    }
  } catch { /* 坏数据回落演示名单 */ }
  return structuredClone(DEMO_REMOTE);
}

function loadActive(): ActiveConn {
  try {
    const v = localStorage.getItem(LS_ACTIVE);
    return v ?? "local";
  } catch {
    return "local";
  }
}

/** 演示账号（登录后展示——无宿主桥时的回落假数据）。 */
const DEMO_SAKURA: SakuraAccount = {
  name: "DemoUser",
  traffic: [1_293_000_000, 9_400_000_000],
  speed: "10 Mbps",
  group: "普通用户",
};

/** 演示节点池（新建隧道时的下拉——负载参考）。 */
const DEMO_NODES = [
  { name: "中国 · 台州", host: "idea-leaper-1.natfrp.io", load: 19 },
  { name: "中国 · 上海", host: "cn-sh-1.natfrp.io", load: 42 },
  { name: "香港 · HK", host: "hk-1.natfrp.io", load: 27 },
];

export function ConnectionsProvider({ children, sakuraBridge, remoteControlBridge, source }: {
  children: ReactNode;
  sakuraBridge?: SakuraBridge | null;
  remoteControlBridge?: RemoteControlBridge | null;
  /** AgentSource：连接切换要真的改传输层（WSAgent.setBackend）——原型期缺省
   *  也能用（UI 状态切换），但只有传了 source 才是真连接。 */
  source?: { setBackend?(addr: string, token?: string | null): void };
}) {
  const [remotes, setRemotes] = useState<RemoteConn[]>(loadRemotes);
  const [active, setActiveState] = useState<ActiveConn>(loadActive);
  const [remoteAccess, setRemoteAccessState] = useState(false);
  const [remoteToken, setRemoteToken] = useState(demoToken);
  const [remoteAddr, setRemoteAddr] = useState("192.168.1.105:7789");
  const [remoteBusy, setRemoteBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [sakura, setSakura] = useState<SakuraAccount | null>(null);
  const [tunnels, setTunnels] = useState<SakuraTunnel[]>([]);
  const [sakuraBusy, setSakuraBusy] = useState<string | null>(null);
  const [sakuraError, setSakuraError] = useState<string | null>(null);
  // 桥实例钉在挂载时的那一个（App useMemo 单例），不随重渲染换引用
  const bridgeRef = useRef<SakuraBridge | null>(sakuraBridge ?? null);
  const remoteCtlRef = useRef<RemoteControlBridge | null>(remoteControlBridge ?? null);
  const sourceRef = useRef(source);
  sourceRef.current = source;

  // 连接列表持久化（localStorage——刷新/重启不丢；删除演示条目也是永久的）
  useEffect(() => {
    try {
      localStorage.setItem(LS_REMOTES, JSON.stringify(remotes));
      localStorage.setItem(LS_ACTIVE, active);
    } catch { /* 存储满/禁用：仅丢持久化，不影响会话 */ }
  }, [remotes, active]);

  /** 切换连接 = UI 状态 + **真传输层切换**（WSAgent.setBackend 断开重连）。
   *  本机回落走宿主 token；远程连接用该连接自己的 token（后端校验）。 */
  const setActive = useCallback((id: ActiveConn) => {
    setActiveState(id);
    const s = sourceRef.current;
    if (!s?.setBackend) return; // 演示/缺省：仅 UI 状态
    if (id === "local") {
      s.setBackend("127.0.0.1:7789", null);
      return;
    }
    const conn = remotes.find((r) => r.id === id);
    if (conn) s.setBackend(conn.addr, conn.token || null);
  }, [remotes]);

  const addRemote = useCallback((conn: RemoteConn) => setRemotes((list) => [...list, conn]), []);
  const updateRemote = useCallback(
    (conn: RemoteConn) => setRemotes((list) => list.map((c) => (c.id === conn.id ? conn : c))),
    [],
  );
  const removeRemote = useCallback(
    (id: string) => {
      setRemotes((list) => list.filter((c) => c.id !== id));
      // 删的是当前活动连接 → 回落本机（不断线悬空）
      if (active === id) setActive("local");
    },
    [active, setActive],
  );

  // ---- 局域网远程访问：真数据（后端 remote.json）经 remoteControlBridge；无桥 → 演示 ----

  const remoteFail = useCallback((e: unknown) => {
    setError(e instanceof Error ? e.message : String(e));
  }, []);

  // 挂载拉真实状态（地址/token/开关）——无桥保持演示数据
  useEffect(() => {
    const b = remoteCtlRef.current;
    if (!b) return;
    let alive = true;
    const apply = (s: RemoteControlState) => {
      if (!alive) return;
      setRemoteAccessState(s.enabled);
      setRemoteToken(s.token);
      if (s.lanAddr) setRemoteAddr(s.lanAddr);
    };
    b.get().then(apply).catch(() => { /* 主进程未就绪时保持演示态 */ });
    return () => { alive = false; };
  }, [remoteControlBridge]);

  const setRemoteAccess = useCallback((on: boolean) => {
    const b = remoteCtlRef.current;
    if (!b) {
      setRemoteAccessState(on);
      // 重新开启换新凭证（旧的失效——服务端语义，原型同步生成）
      if (on) setRemoteToken(demoToken());
      return;
    }
    setRemoteBusy(true);
    (on ? b.enable() : b.disable())
      .then((s) => { setRemoteAccessState(s.enabled); setRemoteToken(s.token); })
      .catch(remoteFail)
      .finally(() => setRemoteBusy(false));
  }, [remoteFail]);

  const regenerateRemoteToken = useCallback(() => {
    const b = remoteCtlRef.current;
    if (!b) { setRemoteToken(demoToken()); return; }
    setRemoteBusy(true);
    b.rotate()
      .then((s) => { setRemoteToken(s.token); })
      .catch(remoteFail)
      .finally(() => setRemoteBusy(false));
  }, [remoteFail]);

  // ---- 樱花frp：桥推送 → React 状态（真链路）；无桥 → 演示回落 ----

  const applySakuraState = useCallback((s: SakuraStateView) => {
    setSakuraBusy(s.busy);
    if (s.error) setSakuraError(s.error);
    if (s.ban) {
      setSakura(null);
      setSakuraError(`${s.ban.title}${s.ban.reason ? `：${s.ban.reason}` : ""}`);
      return;
    }
    setSakura(s.loggedIn ? s.account : null);
    setTunnels(s.loggedIn ? s.tunnels : []);
    if (s.loggedIn && !s.error) setSakuraError(null);
  }, []);

  useEffect(() => {
    const b = bridgeRef.current;
    if (!b) return; // 浏览器/演示模式：下面各动作走演示分支
    let alive = true;
    const un = b.onState((s) => { if (alive) applySakuraState(s); });
    // 挂载即拉一次快照（壳可能已恢复登录态）
    b.getState().then((s) => { if (alive) applySakuraState(s); }).catch(() => { /* 主进程早于窗口就绪时由推送补 */ });
    return () => { alive = false; un(); };
  }, [applySakuraState]);

  const sakuraFail = useCallback((e: unknown) => {
    setSakuraError(e instanceof Error ? e.message : String(e));
  }, []);

  const refreshSakura = useCallback(() => {
    const b = bridgeRef.current;
    if (!b) return; // 演示模式没有可刷新的后端数据
    setSakuraError(null);
    b.refresh().then(applySakuraState).catch(sakuraFail);
  }, [applySakuraState, sakuraFail]);

  const loginSakura = useCallback((key: string) => {
    const b = bridgeRef.current;
    setSakuraError(null);
    if (!b) {
      // 演示：密钥非空即登录成功
      if (key.trim() === "") return;
      setSakura(DEMO_SAKURA);
      return;
    }
    b.login(key).then(applySakuraState).catch(sakuraFail);
  }, [applySakuraState, sakuraFail]);

  const logoutSakura = useCallback(() => {
    const b = bridgeRef.current;
    setSakuraError(null);
    if (!b) { setSakura(null); setTunnels([]); return; }
    b.logout().then(applySakuraState).catch(sakuraFail);
  }, [applySakuraState, sakuraFail]);

  const createTunnel = useCallback(() => {
    const b = bridgeRef.current;
    if (!b) {
      // 演示：轮换演示节点 + 随机远程端口
      setTunnels((list) => {
        const node = DEMO_NODES[list.length % DEMO_NODES.length];
        const port = 20000 + Math.floor(Math.random() * 30000);
        return [...list, {
          id: 10000 + list.length + 1,
          name: "lxcode-backend",
          nodeName: node.name,
          addr: `${node.host}:${port}`,
          online: true,
          load: node.load,
          used: 0,
        }];
      });
      return;
    }
    setSakuraError(null);
    b.createTunnel().then(applySakuraState).catch(sakuraFail);
  }, [applySakuraState, sakuraFail]);

  const toggleTunnel = useCallback((id: number) => {
    const b = bridgeRef.current;
    if (!b) {
      setTunnels((list) => list.map((t) => (t.id === id ? { ...t, online: !t.online } : t)));
      return;
    }
    setSakuraError(null);
    // 在线 = frpc 在跑：停掉；离线：启动（换隧道由主进程先停旧的）
    const running = tunnels.find((t) => t.id === id)?.online;
    const call = running ? b.stopTunnel() : b.startTunnel(id);
    call.then(applySakuraState).catch(sakuraFail);
  }, [applySakuraState, sakuraFail, tunnels]);

  const removeTunnel = useCallback((id: number) => {
    const b = bridgeRef.current;
    if (!b) { setTunnels((list) => list.filter((t) => t.id !== id)); return; }
    setSakuraError(null);
    b.removeTunnel(id).then(applySakuraState).catch(sakuraFail);
  }, [applySakuraState, sakuraFail]);

  const value = useMemo(
    () => ({
      remotes, addRemote, updateRemote, removeRemote,
      active, setActive,
      remoteAccess, setRemoteAccess, remoteBusy, remoteAddr, remoteToken, regenerateRemoteToken,
      sakura, loginSakura, logoutSakura, sakuraBusy, sakuraError, refreshSakura,
      tunnels, createTunnel, toggleTunnel, removeTunnel,
    }),
    [
      remotes, addRemote, updateRemote, removeRemote,
      active, setActive,
      remoteAccess, setRemoteAccess, remoteBusy, remoteAddr, remoteToken, regenerateRemoteToken,
      sakura, loginSakura, logoutSakura, sakuraBusy, sakuraError, refreshSakura,
      tunnels, createTunnel, toggleTunnel, removeTunnel,
    ],
  );
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useConnections(): ConnectionsValue {
  const v = useContext(Ctx);
  if (!v) throw new Error("useConnections 必须在 ConnectionsProvider 内使用");
  return v;
}
