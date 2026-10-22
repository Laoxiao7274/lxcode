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

/** 下载进度（update:progress 事件载荷；stage 区分下载与校验两个阶段）。
 *  stage 可选：老壳只推 percent/downloaded/total，缺省按 downloading 处理。 */
export interface UpdateProgressView {
  percent: number;
  downloaded: number;
  total: number;
  stage: "downloading" | "verifying";
}

export interface UpdateBridge {
  /** 当前壳版本（= shell/package.json，版本三方同源）。 */
  version(): Promise<string>;
  /** 检查更新：available = manifest.version > 当前。 */
  check(): Promise<{ available: boolean; manifest: UpdateManifestView | null }>;
  /** 下载 + 整包/逐文件校验 + 暂存（不碰运行中的文件）。进度走 onProgress。 */
  download(): Promise<{ ok: boolean }>;
  /** 应用已暂存的更新（排程独立 bat：壳退出后完成全部替换与拉起）；download 之后调用。 */
  apply(): Promise<{ ok: boolean; needsRestart: boolean }>;
  /** 退出壳（apply-update.bat 等进程退出后完成全部替换并拉起新版）。 */
  restart(): void;
  onProgress(cb: (p: UpdateProgressView) => void): () => void;
  /** 运行期周期检查发现新版本（主进程推送，见 shell updater.ts 的
   *  startUpdatePolling）。可选：浏览器演示模式无此通道。 */
  onAvailable?(cb: (m: UpdateManifestView) => void): () => void;
  /** 上次更新失败（apply-update.bat 失败标记，壳启动时消费一次）→ 可读原因。
   *  可选：老壳/浏览器演示模式无此通道。 */
  onUpdateFailed?(cb: (message: string) => void): () => void;
}

interface RawUpdateBridge {
  version(): Promise<unknown>;
  check(): Promise<unknown>;
  download(): Promise<unknown>;
  apply(): Promise<unknown>;
  restart(): void;
  onProgress(cb: (p: unknown) => void): () => void;
  onAvailable?(cb: (m: unknown) => void): () => void;
  onUpdateFailed?(cb: (e: unknown) => void): () => void;
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
      // IPC 契约 = shell manualCheckOutcome 三态（latest/available/error）：
      // error 抛出（渲染层走 fail 路径显示原因——手动检查不许静默）；
      // available 还原旧形状 {available, manifest}（UpdateProvider 状态机不变）。
      const r = await cast<{ kind?: unknown; version?: unknown; manifest?: unknown; message?: unknown }>(s.check());
      if (r.kind === "error") {
        throw new Error(typeof r.message === "string" ? r.message : "检查更新失败");
      }
      if (r.kind === "available") {
        const manifest = normalize(r.manifest);
        if (manifest) return { available: true, manifest };
      }
      return { available: false, manifest: null };
    },
    download: () => cast<{ ok?: unknown }>(s.download()).then((r) => ({ ok: r.ok === true })),
    apply: () => cast<{ ok?: unknown }>(s.apply()).then((r) => ({ ok: r.ok === true, needsRestart: true })),
    restart: () => s.restart(),
    onProgress: (cb) => s.onProgress((p) => {
      if (typeof p !== "object" || p === null) return;
      const r = p as { percent?: unknown; downloaded?: unknown; total?: unknown; stage?: unknown };
      if (typeof r.percent !== "number") return;
      cb({
        percent: r.percent,
        downloaded: typeof r.downloaded === "number" ? r.downloaded : 0,
        total: typeof r.total === "number" ? r.total : 0,
        // 老壳没有 stage 字段：一律按下载中（行为与升级前一致）
        stage: r.stage === "verifying" ? "verifying" : "downloading",
      });
    }),
    onAvailable: s.onAvailable
      ? (cb) => s.onAvailable!((m) => {
          const normalized = normalize(m);
          if (normalized) cb(normalized);
        })
      : undefined,
    onUpdateFailed: s.onUpdateFailed
      ? (cb) => s.onUpdateFailed!((e) => {
          const r = (e ?? {}) as { message?: unknown };
          if (typeof r.message === "string" && r.message) cb(r.message);
        })
      : undefined,
  };
}
