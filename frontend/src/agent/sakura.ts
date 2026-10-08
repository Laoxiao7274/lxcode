// 樱花frp 渲染层桥：主进程（shell/src/sakura.ts）持有全部状态与 frpc 进程，
// 渲染层只发意图、收状态推送。浏览器模式没有宿主桥 → getBridge() 返回 null，
// 连接域回落演示数据（connections.tsx）。
import type { SakuraAccount, SakuraTunnel } from "../shared/connections";

/** 主进程推送的完整状态快照（shell/src/sakura.ts SakuraState 的渲染层投影）。 */
export interface SakuraStateView {
  loggedIn: boolean;
  account: SakuraAccount | null;
  ban: { title: string; reason: string } | null;
  tunnels: SakuraTunnel[];
  runningId: number | null;
  frpcReady: boolean;
  frpcHint: string | null;
  /** 进行中的操作描述（null = 空闲）。 */
  busy: string | null;
  /** 最近一次用户操作的失败原因。 */
  error: string | null;
}

export interface SakuraBridge {
  getState(): Promise<SakuraStateView>;
  login(key: string): Promise<SakuraStateView>;
  logout(): Promise<SakuraStateView>;
  refresh(): Promise<SakuraStateView>;
  createTunnel(): Promise<SakuraStateView>;
  startTunnel(id: number): Promise<SakuraStateView>;
  stopTunnel(): Promise<SakuraStateView>;
  removeTunnel(id: number): Promise<SakuraStateView>;
  onState(cb: (s: SakuraStateView) => void): () => void;
}

/** 主进程状态的隧道视图与连接域的 SakuraTunnel 同形（load/used 可空）。 */
type RawState = SakuraStateView;

function bridge(): SakuraBridge | null {
  const host = (window as unknown as { __LX__?: { sakurafrp?: RawBridge } }).__LX__;
  if (!host?.sakurafrp) return null;
  const s = host.sakurafrp;
  const cast = <T>(p: Promise<unknown>): Promise<T> => p as Promise<T>;
  return {
    getState: () => cast<SakuraStateView>(s.getState()),
    login: (key) => cast<SakuraStateView>(s.login(key)),
    logout: () => cast<SakuraStateView>(s.logout()),
    refresh: () => cast<SakuraStateView>(s.refresh()),
    createTunnel: () => cast<SakuraStateView>(s.createTunnel()),
    startTunnel: (id) => cast<SakuraStateView>(s.startTunnel(id)),
    stopTunnel: () => cast<SakuraStateView>(s.stopTunnel()),
    removeTunnel: (id) => cast<SakuraStateView>(s.removeTunnel(id)),
    onState: (cb) => s.onState((raw) => cb(raw as SakuraStateView)),
  };
}

interface RawBridge {
  getState(): Promise<unknown>;
  login(key: string): Promise<unknown>;
  logout(): Promise<unknown>;
  refresh(): Promise<unknown>;
  createTunnel(): Promise<unknown>;
  startTunnel(id: number): Promise<unknown>;
  stopTunnel(): Promise<unknown>;
  removeTunnel(id: number): Promise<unknown>;
  onState(cb: (s: unknown) => void): () => void;
}

export { bridge as getSakuraBridge };
