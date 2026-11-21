// screen-probe.ts —— 编码器探测失败的纯逻辑（不 import electron，node:test
// 直测；screen.ts 的 probeEncoders/startFfmpeg 消费同一份判定，测试与生产零漂移）。
//
// 背景（0.3.56 实撞）：探测全失败带 60s TTL 缓存，TTL 内 probeEncoders 直接
// 返回 []；而 reconcile 每次 start 前先清空 lastError——于是第二轮起 lastError
// 为空，startFfmpeg 的 `if (!lastError)` 兜底成立，把「驱动/桌面复制起不来」
// 误报成「缺编码器，请重装 ffmpeg」。修复：失败时把真实原因（kind + 文案）
// 与 TTL 时间戳一起缓存，TTL 命中复述同一真实原因。
//
// 背景（0.3.61 实撞）：listEncoders 的 spawn error/超时出口曾一律 resolve 空
// 串，`parseEncodersList("")` 把四候选全判 missing → encoders_missing 误报
// 「缺编译产物请重装」（模拟器场景一直存在）。修复：执行失败与「输出里真没
// 编码器」区分开（ok 信号 + encoders_list_unavailable 分类），失败时跳过预筛
// 全量走真实例化。

/** 探测失败 TTL：驱动瞬时故障（如独显休眠唤醒瞬间 nvcuda.dll 拉不起来）不该
 *  把投屏钉死——TTL 内不重探（省 4×~1s 的真实例化），TTL 过后下次 reconcile
 *  重探。 */
export const PROBE_FAIL_TTL_MS = 60_000;

/** 探测失败三类（文案完全不同）：
 *  - encoders_missing：exe 里根本没编这些编码器 → 重装完整版 ffmpeg；
 *  - encoders_unavailable：在列但真实例化起不来 → 显卡驱动/桌面复制；
 *  - encoders_list_unavailable：`-encoders` 本身跑不起来（spawn 失败/非零
 *    退出且零输出/超时零输出）→ ffmpeg 本体执行失败——此时不知道缺什么，
 *    绝不能判 missing（0.3.61 误报根因），只在全量真实例化也全失败时兜底报告。 */
export type ProbeFailureKind =
  | "encoders_missing"
  | "encoders_unavailable"
  | "encoders_list_unavailable";

/** probeFailureKind 只在**全部**候选都缺失时才判 encoders_missing——文案写
 *  「都不在编译产物里」，部分缺失（如只有 nvenc 没编进去，其他三个在列且能
 *  用）时报「缺失名单」更诚实，且方向指向驱动/兼容而非一律重装。
 *  部分缺失走 encoders_unavailable（驱动方向，附缺失名单）。 */
export function probeFailureKind(missing: string[]): ProbeFailureKind {
  return missing.length > 0 ? "encoders_missing" : "encoders_unavailable";
}

/** 全失败时的分类决策（probeEncoders 消费）：missing=预筛判缺的候选名单。
 *  全部候选都缺 = encoders_missing（重装 ffmpeg）；部分缺/全在列 =
 *  encoders_unavailable（驱动方向）——文案与实际缺失范围必须一致。 */
export function probeFailureKindOf(missing: string[], candidates: readonly string[]): ProbeFailureKind {
  return missing.length === candidates.length && candidates.length > 0
    ? "encoders_missing"
    : "encoders_unavailable";
}

/** 上次探测全失败的记忆：分类 + 已组装的真实文案 + 失败时刻（TTL 起点）。
 *  与「成功缓存」互斥：成功探测必须清掉它（显卡能力恢复后不再复述旧失败）。 */
export interface ProbeFailMemory {
  kind: ProbeFailureKind;
  message: string;
  at: number;
}

/** 探测全失败的真实原因文案（按 kind 三选一，附 ffmpeg 输出摘要）。
 *  encoders_unavailable 且有部分缺失时点名缺失候选——「部分缺失」的修复方向
 *  可能是装了阉割版 ffmpeg 或驱动不配套，不能一律「重装完整版」。 */
export function probeFailMessage(kind: ProbeFailureKind, summary: string, missing: string[] = []): string {
  switch (kind) {
    case "encoders_missing":
      return "ffmpeg 缺少 H.264 编码器（nvenc/qsv/amf/libx264 都不在编译产物里）——请重新安装完整版 ffmpeg（见 docs/ffmpeg-build.md）";
    case "encoders_list_unavailable":
      return `ffmpeg 本体执行失败（-encoders 都跑不起来）——安装可能损坏或被杀毒拦截，请重新安装完整版 ffmpeg（见 docs/ffmpeg-build.md）。ffmpeg 输出：${summary || "（无输出）"}`;
    default: {
      const partial = missing.length > 0 ? `（缺失: ${missing.join(", ")}，其余候选在列但未能启动）` : "";
      return `屏幕捕获/编码器启动失败${partial}（软编码已自动尝试）——可能是显卡驱动或桌面复制不可用。ffmpeg 输出：${summary || "（无输出）"}`;
    }
  }
}

/** TTL 命中判定 + 复述：命中返回记忆（调用方以其 message 重建 lastError、
 *  lastErrKind 恒为 encoder_unavailable），无记忆或已过 TTL 返回 null（调用方
 *  继续真实探测）。独立 now 参数便于测试。 */
export function probeFailCacheHit(memory: ProbeFailMemory | null, now: number): ProbeFailMemory | null {
  return memory && now - memory.at < PROBE_FAIL_TTL_MS ? memory : null;
}

// ---- listEncoders 执行结果判定（纯函数；screen.ts 的 listEncoders 收集完
// 输出后消费，node:test 直测——异步 spawn 逻辑留 screen.ts，真伪判定在这里）----

/** listEncoders 的收口判定：正常退出 = 成功；spawn error/超时/非零退出只有
 *  真的收到输出才算成功（残串按实际内容判定）——零输出 ok=false。空输出曾被
 *  当成「输出里没编码器」→ encoders_missing 误报（0.3.61），这条判定是钉子。
 *  非零退出 + 零输出 = 进程被拉起但立即失败（杀软注入/环境损坏），与 spawn
 *  error 同罪——曾有版本只看 close 与否、无视退出码，误报照旧（0.3.62 复现）。 */
export function encodersListOutcome(ranToCompletion: boolean, out: string, exitCode: number | null = 0): { ok: boolean; out: string } {
  const healthy = ranToCompletion && (exitCode === null || exitCode === 0);
  return { ok: healthy || out.length > 0, out };
}
