// screen-probe.ts —— 编码器探测失败的纯逻辑（不 import electron，node:test
// 直测；screen.ts 的 probeEncoders/startFfmpeg 消费同一份判定，测试与生产零漂移）。
//
// 背景（0.3.56 实撞）：探测全失败带 60s TTL 缓存，TTL 内 probeEncoders 直接
// 返回 []；而 reconcile 每次 start 前先清空 lastError——于是第二轮起 lastError
// 为空，startFfmpeg 的 `if (!lastError)` 兜底成立，把「驱动/桌面复制起不来」
// 误报成「缺编码器，请重装 ffmpeg」。修复：失败时把真实原因（kind + 文案）
// 与 TTL 时间戳一起缓存，TTL 命中复述同一真实原因。

/** 探测失败 TTL：驱动瞬时故障（如独显休眠唤醒瞬间 nvcuda.dll 拉不起来）不该
 *  把投屏钉死——TTL 内不重探（省 4×~1s 的真实例化），TTL 过后下次 reconcile
 *  重探。 */
export const PROBE_FAIL_TTL_MS = 60_000;

/** 探测失败要区分「真的缺编码器」与「编码器在列但起不来」——文案完全不同
 *  （前者让用户重装 ffmpeg，后者指向显卡驱动/桌面复制）。 */
export type ProbeFailureKind = "encoders_missing" | "encoders_unavailable";

export function probeFailureKind(missing: string[]): ProbeFailureKind {
  return missing.length > 0 ? "encoders_missing" : "encoders_unavailable";
}

/** 上次探测全失败的记忆：分类 + 已组装的真实文案 + 失败时刻（TTL 起点）。
 *  与「成功缓存」互斥：成功探测必须清掉它（显卡能力恢复后不再复述旧失败）。 */
export interface ProbeFailMemory {
  kind: ProbeFailureKind;
  message: string;
  at: number;
}

/** 探测全失败的真实原因文案（按 kind 二选一，附 ffmpeg 输出摘要）。 */
export function probeFailMessage(kind: ProbeFailureKind, summary: string): string {
  return kind === "encoders_missing"
    ? "ffmpeg 缺少 H.264 编码器（nvenc/qsv/amf/libx264 都不在编译产物里）——请重新安装完整版 ffmpeg（见 docs/ffmpeg-build.md）"
    : `屏幕捕获/编码器启动失败（软编码已自动尝试）——可能是显卡驱动或桌面复制不可用。ffmpeg 输出：${summary || "（无输出）"}`;
}

/** TTL 命中判定 + 复述：命中返回记忆（调用方以其 message 重建 lastError、
 *  lastErrKind 恒为 encoder_unavailable），无记忆或已过 TTL 返回 null（调用方
 *  继续真实探测）。独立 now 参数便于测试。 */
export function probeFailCacheHit(memory: ProbeFailMemory | null, now: number): ProbeFailMemory | null {
  return memory && now - memory.at < PROBE_FAIL_TTL_MS ? memory : null;
}
