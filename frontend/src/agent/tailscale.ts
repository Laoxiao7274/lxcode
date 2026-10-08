// Tailscale 渲染层桥：CLI 检测与 serve 发布全在壳主进程（shell/src/tailscale.ts），
// 渲染层只发意图、收状态推送。浏览器模式无宿主桥 → getTailscaleBridge() 返回 null，
// 页面回落「需要在壳内运行」提示。
export interface TailscaleStateView {
  installed: boolean;
  /** tailscaled 后端状态：Running / NeedsLogin / Stopped / ""（未安装）。 */
  backendState: string;
  /** 本机在 tailnet 里的 DNS 名（去尾点）。 */
  deviceName: string;
  /** tailnet IPv4（100.x.y.z）。 */
  ipv4: string;
  serveOn: boolean;
  /** serve 开启后的访问地址（https://deviceName）。 */
  serveUrl: string;
  busy: string | null;
  error: string | null;
  hint: string | null;
}

export interface TailscaleBridge {
  getState(): Promise<TailscaleStateView>;
  serveOn(): Promise<TailscaleStateView>;
  serveOff(): Promise<TailscaleStateView>;
  refresh(): Promise<TailscaleStateView>;
  onState(cb: (s: TailscaleStateView) => void): () => void;
}

interface RawBridge {
  getState(): Promise<unknown>;
  serveOn(): Promise<unknown>;
  serveOff(): Promise<unknown>;
  refresh(): Promise<unknown>;
  onState(cb: (s: unknown) => void): () => void;
}

export function getTailscaleBridge(): TailscaleBridge | null {
  const host = (window as unknown as { __LX__?: { tailscale?: RawBridge } }).__LX__;
  const s = host?.tailscale;
  if (!s) return null;
  const cast = <T,>(p: Promise<unknown>): Promise<T> => p as Promise<T>;
  return {
    getState: () => cast<TailscaleStateView>(s.getState()),
    serveOn: () => cast<TailscaleStateView>(s.serveOn()),
    serveOff: () => cast<TailscaleStateView>(s.serveOff()),
    refresh: () => cast<TailscaleStateView>(s.refresh()),
    onState: (cb) => s.onState((raw) => cb(raw as TailscaleStateView)),
  };
}
