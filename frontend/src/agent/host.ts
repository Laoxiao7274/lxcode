// 宿主访问器：后端远程访问（token / 开关 / 局域网地址）——渲染层唯一入口。
// 浏览器模式无宿主桥 → 全部返回 null（连接域回落演示数据）。
export interface RemoteControlState {
  enabled: boolean;
  token: string;
  /** 本机局域网地址 lanIp:port（无可用网卡时为空串）。 */
  lanAddr: string;
}

export interface RemoteControlBridge {
  get(): Promise<RemoteControlState>;
  enable(): Promise<RemoteControlState>;
  disable(): Promise<RemoteControlState>;
  rotate(): Promise<RemoteControlState>;
  refresh(): Promise<RemoteControlState>;
}

interface RawBridge {
  tokenSync(): string | null;
  get(): Promise<unknown>;
  enable(): Promise<unknown>;
  disable(): Promise<unknown>;
  rotate(): Promise<unknown>;
  refresh(): Promise<unknown>;
}

/** WS 连接用的后端 token（同步——主进程启动时已缓存）。null = 无桥/未就绪。 */
export function backendToken(): string | null {
  const h = (window as unknown as { __LX__?: { backendRemote?: RawBridge } }).__LX__;
  return h?.backendRemote?.tokenSync() ?? null;
}

export function getRemoteControlBridge(): RemoteControlBridge | null {
  const s = (window as unknown as { __LX__?: { backendRemote?: RawBridge } }).__LX__?.backendRemote;
  if (!s) return null;
  const cast = <T,>(p: Promise<unknown>): Promise<T> => p as Promise<T>;
  return {
    get: () => cast<RemoteControlState>(s.get()),
    enable: () => cast<RemoteControlState>(s.enable()),
    disable: () => cast<RemoteControlState>(s.disable()),
    rotate: () => cast<RemoteControlState>(s.rotate()),
    refresh: () => cast<RemoteControlState>(s.refresh()),
  };
}
