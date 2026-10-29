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

/** 节点选择列表项（主进程投影：候选节点 + 已测出的延迟）。 */
export interface SakuraNode {
  id: number;
  name: string;
  /** frps 主机名（测延迟与拼地址用）。 */
  host: string;
  /** 负载百分比（null = 未知 → UI 显示「—」）。 */
  load: number | null;
  /** 能否用来创建隧道（离线/等级不够/无地址 → false）。 */
  usable: boolean;
  /** 不可用原因（可用时 null）。 */
  reason: string | null;
  /** 实测延迟 ms（null = 还没测出/测不出）。 */
  latency: number | null;
}

/** sakura:listNodes 的返回：候选节点 + 当前用户组等级。 */
export interface SakuraNodeList {
  nodes: SakuraNode[];
  /** 用户组等级（UI 标注「需更高等级」的依据）。 */
  groupLevel: number;
  /** 是否会异步补延迟（壳内 true；演示数据 false → 延迟位显示「—」而不是「测试中…」）。 */
  probing: boolean;
}

/** 单节点延迟探测结果（异步推送：列表先出，延迟逐个填）。 */
export interface SakuraLatencyEvent {
  host: string;
  latency: number | null;
}

export interface SakuraBridge {
  getState(): Promise<SakuraStateView>;
  login(key: string): Promise<SakuraStateView>;
  logout(): Promise<SakuraStateView>;
  refresh(): Promise<SakuraStateView>;
  /** 节点选择列表（含已缓存的延迟；其余延迟经 onNodeLatency 异步补）。 */
  listNodes(): Promise<SakuraNodeList>;
  /** nodeId 省略 = 自动选（负载最低）；传了就由主进程校验候选与可用性。 */
  createTunnel(nodeId?: number | null): Promise<SakuraStateView>;
  startTunnel(id: number): Promise<SakuraStateView>;
  stopTunnel(): Promise<SakuraStateView>;
  removeTunnel(id: number): Promise<SakuraStateView>;
  onState(cb: (s: SakuraStateView) => void): () => void;
  onNodeLatency(cb: (e: SakuraLatencyEvent) => void): () => void;
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
    listNodes: () => cast<SakuraNodeList>(s.listNodes()),
    createTunnel: (nodeId) => cast<SakuraStateView>(s.createTunnel(nodeId)),
    startTunnel: (id) => cast<SakuraStateView>(s.startTunnel(id)),
    stopTunnel: () => cast<SakuraStateView>(s.stopTunnel()),
    removeTunnel: (id) => cast<SakuraStateView>(s.removeTunnel(id)),
    onState: (cb) => s.onState((raw) => cb(raw as SakuraStateView)),
    onNodeLatency: (cb) => s.onNodeLatency((raw) => cb(raw as SakuraLatencyEvent)),
  };
}

interface RawBridge {
  getState(): Promise<unknown>;
  login(key: string): Promise<unknown>;
  logout(): Promise<unknown>;
  refresh(): Promise<unknown>;
  listNodes(): Promise<unknown>;
  createTunnel(nodeId?: number | null): Promise<unknown>;
  startTunnel(id: number): Promise<unknown>;
  stopTunnel(): Promise<unknown>;
  removeTunnel(id: number): Promise<unknown>;
  onState(cb: (s: unknown) => void): () => void;
  onNodeLatency(cb: (e: unknown) => void): () => void;
}

export { bridge as getSakuraBridge };
