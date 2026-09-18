// 连接域：壳 ↔ 后端的连接管理（前后端完全隔离的落点）。
// 本地（127.0.0.1:7789 内置回落默认）+ 远程连接（Linux 开发服务器等，
// 地址 + token）；本机也能被别人连——开启远程访问后展示地址与 token
// （凭证由服务端生成与校验，壳只展示/复制）。
// 公网穿透（樱花frp）：登录（访问密钥）→ 自动建隧道 → 公网连接地址 +
// 流量/隧道信息展示——实现文档见 docs/sakurafrp-integration.md。
// 原型：内存态 + 假数据；后端化时接 api.natfrp.com/v4（Bearer 密钥）。
import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from "react";

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
  /** 本机远程访问（被连）：开启后监听对外地址。 */
  remoteAccess: boolean;
  setRemoteAccess: (on: boolean) => void;
  /** 对外地址（后端上报——原型示例值）。 */
  remoteAddr: string;
  /** 访问 token（服务端生成——原型在开关开启时生成）。 */
  remoteToken: string;
  regenerateRemoteToken: () => void;
  /** 樱花frp 登录态（null = 未登录）。 */
  sakura: SakuraAccount | null;
  loginSakura: (key: string) => void;
  logoutSakura: () => void;
  /** 公网隧道（登录后可建——lxcode-backend 命名约定）。 */
  tunnels: SakuraTunnel[];
  createTunnel: () => void;
  toggleTunnel: (id: number) => void;
  removeTunnel: (id: number) => void;
}

const Ctx = createContext<ConnectionsValue | null>(null);

const DEMO_REMOTE: RemoteConn[] = [
  { id: "dev-server", name: "公司开发机", addr: "10.0.0.8:7789", token: "a3f8b2c1-9d4e-4f2a-8c6b-7e1d5a9f0b3c" },
];

/** 演示账号（登录后展示——原型假数据）。 */
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

export function ConnectionsProvider({ children }: { children: ReactNode }) {
  const [remotes, setRemotes] = useState<RemoteConn[]>(DEMO_REMOTE);
  const [active, setActive] = useState<ActiveConn>("local");
  const [remoteAccess, setRemoteAccessState] = useState(false);
  const [remoteToken, setRemoteToken] = useState(demoToken);
  const [sakura, setSakura] = useState<SakuraAccount | null>(null);
  const [tunnels, setTunnels] = useState<SakuraTunnel[]>([]);

  const addRemote = useCallback((conn: RemoteConn) => setRemotes((list) => [...list, conn]), []);
  const updateRemote = useCallback(
    (conn: RemoteConn) => setRemotes((list) => list.map((c) => (c.id === conn.id ? conn : c))),
    [],
  );
  const removeRemote = useCallback(
    (id: string) => {
      setRemotes((list) => list.filter((c) => c.id !== id));
      // 删的是当前活动连接 → 回落本机（不断线悬空）
      setActive((cur) => (cur === id ? "local" : cur));
    },
    [],
  );

  const setRemoteAccess = useCallback((on: boolean) => {
    setRemoteAccessState(on);
    // 重新开启换新凭证（旧的失效——服务端语义，原型同步生成）
    if (on) setRemoteToken(demoToken());
  }, []);
  const regenerateRemoteToken = useCallback(() => setRemoteToken(demoToken()), []);

  const loginSakura = useCallback((key: string) => {
    // 原型：密钥非空即登录成功；后端化 = GET /user/info 校验 Bearer
    if (key.trim() === "") return;
    setSakura(DEMO_SAKURA);
  }, []);
  const logoutSakura = useCallback(() => {
    setSakura(null);
    setTunnels([]);
  }, []);

  const createTunnel = useCallback(() => {
    // 原型：轮换演示节点 + 随机远程端口；后端化 = 选节点（负载/VIP）→
    // POST /tunnels（tcp, local 127.0.0.1:7789, remote 自动分配）→ 拉
    // frpc 配置 spawn
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
  }, []);
  const toggleTunnel = useCallback(
    (id: number) => setTunnels((list) => list.map((t) => (t.id === id ? { ...t, online: !t.online } : t))),
    [],
  );
  const removeTunnel = useCallback(
    (id: number) => setTunnels((list) => list.filter((t) => t.id !== id)),
    [],
  );

  const value = useMemo(
    () => ({
      remotes, addRemote, updateRemote, removeRemote,
      active, setActive,
      remoteAccess, setRemoteAccess, remoteAddr: "192.168.1.105:7789", remoteToken, regenerateRemoteToken,
      sakura, loginSakura, logoutSakura,
      tunnels, createTunnel, toggleTunnel, removeTunnel,
    }),
    [
      remotes, addRemote, updateRemote, removeRemote,
      active, setActive,
      remoteAccess, setRemoteAccess, remoteToken, regenerateRemoteToken,
      sakura, loginSakura, logoutSakura,
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
