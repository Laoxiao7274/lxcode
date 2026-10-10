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

// ---- 自更新（壳主进程编排——manifest 拉取/校验/替换全不经过渲染层） ----

export interface UpdateManifestView {
  version: string;
  size: number;
  notes: string[];
}

export interface UpdateBridge {
  /** 当前壳版本（= shell/package.json，版本三方同源）。 */
  version(): Promise<string>;
  /** 检查更新：available = manifest.version > 当前。 */
  check(): Promise<{ available: boolean; manifest: UpdateManifestView | null }>;
  /** 下载 + 整包/逐文件校验 + 后端热替换 + asar 冷替换排程。进度走 onProgress。 */
  download(): Promise<{ ok: boolean }>;
  /** 应用已暂存的更新（后端热替换 + asar 排程）；download 之后调用。 */
  apply(): Promise<{ ok: boolean; needsRestart: boolean }>;
  /** 退出壳（冷替换脚本等壳退出后换 asar 并重新拉起）。 */
  restart(): void;
  onProgress(cb: (p: { percent: number; downloaded: number; total: number }) => void): () => void;
}

interface RawUpdateBridge {
  version(): Promise<unknown>;
  check(): Promise<unknown>;
  download(): Promise<unknown>;
  apply(): Promise<unknown>;
  restart(): void;
  onProgress(cb: (p: unknown) => void): () => void;
}

/** 自更新桥；null = 浏览器模式（演示回落）。 */
export function getUpdateBridge(): UpdateBridge | null {
  const s = (window as unknown as { __LX__?: { update?: RawUpdateBridge } }).__LX__?.update;
  if (!s) return null;
  const cast = <T,>(p: Promise<unknown>): Promise<T> => p as Promise<T>;
  const normalize = (m: unknown): UpdateManifestView | null => {
    if (typeof m !== "object" || m === null) return null;
    const r = m as { version?: unknown; size?: unknown; notes?: unknown };
    if (typeof r.version !== "string") return null;
    return {
      version: r.version,
      size: typeof r.size === "number" ? r.size : 0,
      notes: Array.isArray(r.notes) ? r.notes.filter((n): n is string => typeof n === "string") : [],
    };
  };
  return {
    version: () => cast<string>(s.version()),
    check: async () => {
      const r = await cast<{ available?: unknown; manifest?: unknown }>(s.check());
      const manifest = normalize(r.manifest);
      return { available: r.available === true && manifest !== null, manifest };
    },
    download: () => cast<{ ok?: unknown }>(s.download()).then((r) => ({ ok: r.ok === true })),
    apply: () => cast<{ ok?: unknown }>(s.apply()).then((r) => ({ ok: r.ok === true, needsRestart: true })),
    restart: () => s.restart(),
    onProgress: (cb) => s.onProgress((p) => {
      if (typeof p === "object" && p !== null && typeof (p as { percent?: unknown }).percent === "number") {
        cb(p as { percent: number; downloaded: number; total: number });
      }
    }),
  };
}
