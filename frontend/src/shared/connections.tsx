// 连接域：壳 ↔ 后端的连接管理（前后端完全隔离的落点）。
// 本地（127.0.0.1:7789 内置回落默认）+ 远程连接（Linux 开发服务器等，
// 地址 + token）；本机也能被别人连——开启远程访问后展示地址与 token
// （凭证由服务端生成与校验，壳只展示/复制）。
// 原型：内存态 + 假 token；后端化时 WS 重连远端端点、token 随请求头。
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
}

const Ctx = createContext<ConnectionsValue | null>(null);

const DEMO_REMOTE: RemoteConn[] = [
  { id: "dev-server", name: "公司开发机", addr: "10.0.0.8:7789", token: "a3f8b2c1-9d4e-4f2a-8c6b-7e1d5a9f0b3c" },
];

export function ConnectionsProvider({ children }: { children: ReactNode }) {
  const [remotes, setRemotes] = useState<RemoteConn[]>(DEMO_REMOTE);
  const [active, setActive] = useState<ActiveConn>("local");
  const [remoteAccess, setRemoteAccessState] = useState(false);
  const [remoteToken, setRemoteToken] = useState(demoToken);

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

  const value = useMemo(
    () => ({
      remotes, addRemote, updateRemote, removeRemote,
      active, setActive,
      remoteAccess, setRemoteAccess, remoteAddr: "192.168.1.105:7789", remoteToken, regenerateRemoteToken,
    }),
    [remotes, addRemote, updateRemote, removeRemote, active, setActive, remoteAccess, setRemoteAccess, remoteToken, regenerateRemoteToken],
  );
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useConnections(): ConnectionsValue {
  const v = useContext(Ctx);
  if (!v) throw new Error("useConnections 必须在 ConnectionsProvider 内使用");
  return v;
}
