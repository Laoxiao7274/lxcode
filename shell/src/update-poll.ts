// update-poll.ts —— 更新周期检查的纯逻辑（不 import electron，node:test 直测）。
//
// 背景（2026-12 体验修复）：更新检查原先只在渲染层启动时做一次（UpdateProvider
// 挂载 1.2s 后查一回），应用开着就永远发现不了新版本。周期检查挪到壳主进程：
// 窗口隐藏到托盘也照常查，发现新版本经 update:available 推给渲染层 + 托盘气泡。
// 本文件只放可单测的纯逻辑——排程延迟（基线+抖动）、检查器循环、手动检查三态。
//
// 纪律：
//   - 周期 = 6h 基线 + [0, 30min) 随机抖动——抖动避免大量客户端同一时刻扎堆拉
//     manifest（发布后的尖峰）；基线不做成配置，改频率就是改这里（发版频率低）。
//   - 自动检查失败静默（网络差不能烦人），手动检查失败必须带原因（三态里的
//     error 态）——两条路径的容错语义刻意不同。

/** 周期检查基线：每 6 小时一次。 */
export const UPDATE_CHECK_INTERVAL_MS = 6 * 3600_000;

/** 抖动窗：在基线上叠加 [0, JITTER) 的随机延迟，避免客户端扎堆。 */
export const UPDATE_CHECK_JITTER_MS = 30 * 60_000;

/** 下一次周期检查的延迟：6h 基线 + [0, 30min) 随机抖动。
 *  rand 注入（默认 Math.random）——测试用假 rand 钉住边界。 */
export function nextCheckDelayMs(rand: () => number = Math.random): number {
  return UPDATE_CHECK_INTERVAL_MS + Math.floor(rand() * UPDATE_CHECK_JITTER_MS);
}

// ---- 手动检查三态 ----

/** 手动「检查更新」的结局：有新版（带版本号 + 完整 manifest——设置面板要展示
 *  大小/notes，下载管线也直接消费它）/ 已是最新 / 失败（带人话原因）。
 *  手动检查不许静默失败——失败态直接进设置页的 error 展示。 */
export type ManualCheckOutcome<M = unknown> =
  | { kind: "latest" }
  | { kind: "available"; version: string; manifest: M }
  | { kind: "error"; message: string };

/** 把一次检查调用归成三态。run 注入（真实实现 = updater 的 checkUpdate）：
 *  resolve 且 available 且 manifest 带版本号 → available（manifest 原样透传，
 *  不在这里改写形状）；resolve 且无新版 → latest；reject → error
 *  （Error.message 优先，非 Error 原样转字符串）。 */
export async function manualCheckOutcome<M extends { version?: unknown }>(
  run: () => Promise<{ available: boolean; manifest: M | null }>,
): Promise<ManualCheckOutcome<M>> {
  let r: Awaited<ReturnType<typeof run>>;
  try {
    r = await run();
  } catch (e) {
    return { kind: "error", message: e instanceof Error ? e.message : String(e) };
  }
  return r.available && r.manifest && typeof r.manifest.version === "string"
    ? { kind: "available", version: r.manifest.version, manifest: r.manifest }
    : { kind: "latest" };
}

// ---- 周期检查器 ----

export interface UpdatePollerDeps {
  /** 一次检查（真实实现 = updater.checkUpdate；失败上抛）。 */
  check: () => Promise<{ available: boolean }>;
  /** 下一次检查的延迟（真实实现 = nextCheckDelayMs）。每次排程现取——
   *  抖动是逐次独立的，不是启动时定死一个值循环用。 */
  nextDelay: () => number;
  /** 检查发现新版本（available=true）时回调；失败不回调。 */
  onAvailable: () => void;
  /** 定时器注入（真实实现 = setTimeout/clearTimeout；测试用手动触发桩）。 */
  setTimer: (fn: () => void, ms: number) => unknown;
  clearTimer: (handle: unknown) => void;
}

export interface UpdatePoller {
  /** 开始排程。首次检查同样延迟 nextDelay()——启动时的检查由渲染层负责，
   *  周期检查从「启动后一个周期」才开始，不做重复的立即检查。 */
  start(): void;
  /** 停止：取消已排程的一次，之后不再排程。可重新 start。 */
  stop(): void;
}

/** 周期检查器：按 nextDelay() 逐次排程检查——发现新版本回调 onAvailable；
 *  检查失败静默（自动检查不该烦人），但无论成败都排下一次（一次网络抖动
 *  不能让周期检查永久停摆）。check 抛错绝不逃出定时器回调。 */
export function createUpdatePoller(deps: UpdatePollerDeps): UpdatePoller {
  let running = false;
  let handle: unknown = null;

  const schedule = (): void => {
    if (!running) return;
    handle = deps.setTimer(() => {
      handle = null;
      void (async () => {
        try {
          const r = await deps.check();
          if (r.available) deps.onAvailable();
        } catch {
          // 自动检查失败静默——真实实现里 checkUpdate 已落 update.log
        }
        schedule(); // 成败都排下一次
      })();
    }, deps.nextDelay());
  };

  return {
    start() {
      if (running || handle !== null) return;
      running = true;
      schedule();
    },
    stop() {
      running = false;
      if (handle !== null) {
        deps.clearTimer(handle);
        handle = null;
      }
    },
  };
}
