// screen-params.ts —— 投屏参数映射的**纯函数**（fps/清晰度档/level 自适应/
// GOP 换算/背压深度）。screen.ts 消费同一份实现（测试与生产零漂移——同
// screen-probe.ts / screen-restart.ts 模式）。
//
// 帧率链路（2026-12 帧率/清晰度上限扩展）：
//   手机 screen.start(fps) → 后端白名单归一（protocol.NormalizeFPS）→
//   命令电平 cmd.fps → 本模块 normalizeFps/framerateArgs → ddagrab framerate=
//   → -g 按 2s GOP 换算（fps*2）→ amf 的 -force_key_frames 表达式不变
//   （按时间 2s 强制，与 fps 无关）。
//
// level 自适应（H.264 level，MaxMBPS = 每秒宏块上限）：
//   1080p = 8160 MB/帧。4.2 = 522240 MB/s → 1080p@64fps 封顶；
//   5.1  = 983040 MB/s → 1080p@120fps；5.2 = 2073600 MB/s → 1080p@144fps
//   （144×8160 = 1175040 > 5.1 上限，必须 5.2）。
//   保守阶梯：≤60fps 恒 4.2（兼容面最广）；120/144fps 用 5.2（安卓端
//   ScreenDecoder.configure 失败已有三级降级链 + 诊断面板显示 SPS level——
//   不支持 5.2 的设备 configure 拒绝后降级或报错可见，不静默花屏）。
//   放到 5.2 而不是 5.1 的理由：144fps 需要 5.2，5.1 只覆盖 120fps；少一档
//   分支少一处组合，且主流中高端手机（2020+）硬解声明普遍到 5.2。

/** 帧率白名单（升序；与 Go protocol.ScreenFPSOptions 逐值对齐）。 */
export const FPS_OPTIONS = [30, 60, 120, 144] as const;

/** 默认帧率（用户不选 = 30——避免所有用户无感知吃带宽）。 */
export const FPS_DEFAULT = 30;

/** 帧率归一：白名单精确命中才生效，其余（含 0/undefined = 老后端不带该键）回落默认。 */
export function normalizeFps(fps: number | undefined): number {
  return (FPS_OPTIONS as readonly number[]).includes(fps ?? 0) ? (fps as number) : FPS_DEFAULT;
}

/** 清晰度档 → qp（constqp/CQP 档）。ultra 见 ultraQualityArgs。 */
export const QUALITY_QP: Record<string, string> = {
  low: "26",
  medium: "22",
  high: "18",
  ultra: "14",
};

/** 清晰度白名单（Go normalizeQuality 同序）。 */
export const QUALITY_OPTIONS = ["low", "medium", "high", "ultra"] as const;

/** 清晰度档归一（未知值回落 medium）。 */
export function normalizeQuality(q: string | undefined): string {
  return (QUALITY_OPTIONS as readonly string[]).includes(q ?? "") ? (q as string) : "medium";
}

/** GOP 换算：2 秒一个关键帧 → g = fps*2（断线重连最多 2s 恢复画面）。 */
export function gopSize(fps: number): number {
  return Math.max(1, normalizeFps(fps) * 2);
}

/**
 * H.264 level 自适应（"4.2" / "5.2"）：按帧率阶梯。
 * 分辨率维度：本服务分辨率恒为显示器原生素材（1080p~4K 常见）——4K@30
 * 需要 level 5.1（983k < 4×8160×30 = 979200 恰好压线过），显式钉 4.1 会
 * 直接被编码器拒或超规。所以 level 不能只看 fps，还要看分辨率规模：
 * 用「每秒宏块数」与各 level 的 MaxMBPS 比，选最小的满足档。
 * 宏块 = 16×16 像素；帧 MB = ceil(w/16)×ceil(h/16)。
 */
export function h264Level(fps: number, width: number, height: number): string {
  const f = normalizeFps(fps);
  const w = width > 0 ? width : 1920;
  const h = height > 0 ? height : 1080;
  const mbPerFrame = Math.ceil(w / 16) * Math.ceil(h / 16);
  const mbPerSec = mbPerFrame * f;
  // MaxMBPS（H.264 Annex A）：4.2=522240、5.1=983040、5.2=2073600。
  if (mbPerSec <= 522_240) return "4.2";
  if (mbPerSec <= 983_040) return "5.1";
  return "5.2";
}

/**
 * ultra 档的码控：CQP 14 + 码率上限保护。
 *
 * 取舍（三家编码器的 capped-VBR 语法差异大，cqp 是各自已验证的公共路径）：
 *  - nvenc/amf/qsv 的 capped VBR 参数面各不相同（nvenc `-rc vbr -maxrate -bufsize`、
 *    amf `-rc vbr_peak -b:v -maxrate -bufsize`、qsv `-b:v -maxrate -bufsize` 但
 *    其 CQP 分支已按 global_quality 表达）——为单一档位引入三条未在本机验证的
 *    码控分支风险大于收益；
 *  - 折衷：ultra = 各家同款 CQP 14（画质逼近目标），复杂画面的码率爆炸由
 *    **壳侧背压**兜底（emitFrame 丢非关键帧 + 后端按 fps 加深的队列 + 手机端
 *    丢帧门）——链路本来就是实时流，编码端不会无限堆积；
 *  - 真机若实测 ultra 在复杂画面把隧道顶满（表现为频繁丢帧而非花屏），再按
 *    编码器分支加 maxrate 上限（届时三家语法逐个真机验证）。
 */
export function isUltra(quality: string): boolean {
  return normalizeQuality(quality) === "ultra";
}

/**
 * 壳侧推流背压字节阈值：按帧率缩放（400KB 基准按 30fps≈10-40 帧标定；
 * 144fps 高码率下帧更大更密，同字节阈值变成 <10 帧——按 fps 线性放大保持
 * 「≈0.3s 帧量」的吸收深度）。上限 1.2MB（本地回环 + WS 隧道双程缓冲，
 * 太深只是把延迟藏进缓冲）。
 */
export function backpressureBytes(fps: number): number {
  const f = normalizeFps(fps);
  return Math.min(1_200_000, Math.round((400_000 * f) / FPS_DEFAULT));
}
