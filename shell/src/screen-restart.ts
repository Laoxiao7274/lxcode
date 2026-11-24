// screen-restart.ts —— ffmpeg 重启失败处理的纯逻辑（不 import electron，
// node:test 直测；screen.ts 的 reconcile 消费同一份判定，测试与生产零漂移）。
//
// 失败处理三层（各自独立，语义不同）：
//   ① 通用连败熔断（scrcpy MAX_CONSECUTIVE_ERRORS 同款）：编码器坏/驱动崩这
//      类持久故障——非 ACCESS_LOST 的短命失败 3 次停止自动重启，等用户显式重试；
//   ② ACCESS_LOST 时间窗预算（RustDesk DxgiRecoveryState 同款）：桌面复制
//      失效是瞬时环境病（显示器 PnP/DWM 模式切换/UAC 安全桌面），~5s 周期
//      抖动下每次都活过 2s 存活门槛、连败计数永远凑不满——连续计数防不住
//      它（2026-11-24 真机 19 连环重启的根因）。改为 10s 滑动窗 ≤3 次；
//   ③ gdigrab 软捕获兜底：预算耗尽**不熔断停流**，重启 ffmpeg 但命令切
//      gdigrab（GDI 抓屏，CPU 捕获）——DXGI 桌面复制会话持续拿不到时 GDI
//      老接口仍能抓（不走 AcquireNextFrame，不受桌面复制会话失效影响），
//      牺牲 CPU/帧率保住「画面还在」。

/** 连败上限（scrcpy MAX_CONSECUTIVE_ERRORS = 3 同值）——仅通用通道。 */
export const MAX_CONSECUTIVE_ERRORS = 3;

/** 一次尝试的存活门槛：ffmpeg 起来后存活满 2s 才算「这次成功」（清零连败计数）。
 *  P0-1：spawn 同步成功不算成功——锁屏/驱动坏时 ffmpeg 异步立即退出是常态，
 *  spawn 成功即清零会让计数 0↔1 震荡、熔断永不触发。 */
export const FFMPEG_MIN_ALIVE_MS = 2000;

/** ACCESS_LOST 失败后的短退避（毫秒）。实际重试节奏 = max(轮询间隔, 此值)
 *  ——重试仍经 reconcile 的轮询环单一 spawn 入口（绕过它会有并发 reconcile
 *  双 spawn 竞态），此值只作为下限门。
 *  500ms（2026-11-24 调优，实测下限不动）：重启 spawn 全链实测 ~0.2s（编码器
 *  探测结果模块级缓存，无逐次 ~1s 重探），500ms 退避让整轮重启落在 ~0.7s，
 *  给 DWM/DXGI 收敛留更从容的窗口（300ms 时重启竞争曾撞出 DuplicateOutput
 *  access denied 竞速失败）。 */
export const ACCESS_LOST_SHORT_BACKOFF_MS = 500;

/** ACCESS_LOST 预算时间窗（毫秒）：RustDesk DxgiRecoveryState 的 10s 滑动窗
 *  同值——窗口内 ACCESS_LOST 失败次数超预算 → 转 gdigrab 兜底。 */
export const ACCESS_LOST_WINDOW_MS = 10_000;

/** 窗口内允许的 ACCESS_LOST 失败次数（RustDesk 同值 3）。**成功帧不清零
 *  预算**（只有窗口自然过期才重置）——周期性抖动的存活空档恰恰呈「成功」
 *  形态，清了预算就回到旧病的永远凑不满。 */
export const ACCESS_LOST_MAX_ATTEMPTS = 3;

/** 按存活时长判定一次尝试的成败（P0-1）：spawn 后存活 ≥ 2s **且非 ACCESS_LOST
 *  退出** = 成功；不足 2s（含 spawn 即退）或以 ACCESS_LOST 退出 = 失败。
 *  2026-11-24 真机诊断：144fps 下 ddagrab 每 ~4.8s 死于 DXGI_ERROR_ACCESS_LOST
 *  （887a0026）——只看存活时长的话每次都过 2s 门槛被当成功清零，连败计数
 *  0↔1 震荡（现由时间窗预算取代连败计数，见 accessLostFail）。调用方把
 *  返回值传给 afterRestartAttempt（非 ACCESS_LOST 路径）。 */
export function attemptSucceeded(lifetimeMs: number, accessLost = false): boolean {
  return lifetimeMs >= FFMPEG_MIN_ALIVE_MS && !accessLost;
}

/** ACCESS_LOST 预算状态（RustDesk DxgiRecoveryState 同款四字段）：
 *  - attempts：本段抖动期内累计的 ACCESS_LOST 失败次数；
 *  - windowStarted：窗口锚点（0 = 无窗口）。**滑动锚**：每次窗内失败把锚点
 *    滑到当下——「窗口」实际是 10s 无失败的静默判据，不是固定起点。固定
 *    起点挡不住 ~5s 周期抖动（3 次失败跨 ~11s，固定窗刚好过期重置、预算
 *    永远凑不满，测试钉住这一点）；滑动锚下只要失败间隔 <10s 预算就持续
 *    累计，失败间隔 >10s（环境恢复了 10s）才开新段；
 *  - restartPending：失败后已排程重启、新进程还没起起来（spawn 时清）；
 *  - fallbackPending：预算耗尽 → 转 gdigrab 兜底（重启命令换 GDI 捕获）。 */
export interface AccessLostBudget {
  attempts: number;
  windowStarted: number;
  restartPending: boolean;
  fallbackPending: boolean;
}

export function emptyAccessLostBudget(): AccessLostBudget {
  return { attempts: 0, windowStarted: 0, restartPending: false, fallbackPending: false };
}

/** ACCESS_LOST 失败记账（纯函数，screen.ts 的 close 回调消费）：
 *  - 无窗口（首次失败 / 静默 >10s 后的新失败）→ 开新段，attempts=1，
 *    fallback 不变（>10s 无失败 = 环境恢复过一段时间，给新一轮 DXGI 机会）；
 *  - 窗内（上次失败 <10s）→ attempts+1 且锚点滑到当下；attempts 超预算 →
 *    fallbackPending（转 gdigrab 兜底，**不熔断停流**——用户侧画面不中断，
 *    只是捕获方式降级）。
 *  同时置 restartPending（调用方排程短退避重试；spawn 时经
 *  accessLostRestartIssued 清位）。 */
export function accessLostFail(b: AccessLostBudget, now: number): AccessLostBudget {
  if (b.windowStarted === 0 || now - b.windowStarted >= ACCESS_LOST_WINDOW_MS) {
    return { attempts: 1, windowStarted: now, restartPending: true, fallbackPending: b.fallbackPending };
  }
  const attempts = b.attempts + 1;
  return {
    ...b,
    attempts,
    windowStarted: now, // 滑动锚：锚点滑到本次失败（10s 静默才是过期）
    restartPending: true,
    // 第 3 次窗内失败即耗尽预算（≤2 次给自愈机会；第 3 次证明失效模式在
    // 持续——「≤3 次」指放行额度 2 次重试，RustDesk 同口径）。
    fallbackPending: b.fallbackPending || attempts >= ACCESS_LOST_MAX_ATTEMPTS,
  };
}

/** spawn 实际发生时清 restartPending（排程 → 落地的闭环；纯函数便于直测）。 */
export function accessLostRestartIssued(b: AccessLostBudget): AccessLostBudget {
  return b.restartPending ? { ...b, restartPending: false } : b;
}

/** 通用成功是否清 ACCESS_LOST 预算：**只在静默过期后清**（上次失败 >10s 的
 *  「成功」= 环境恢复了一段完整时间，重置开新段）。10s 内的成功不清——
 *  周期性抖动的空档恰恰呈「成功」形态，清了预算就永远凑不满。 */
export function accessLostOnSuccess(b: AccessLostBudget, now: number): AccessLostBudget {
  if (b.windowStarted !== 0 && now - b.windowStarted >= ACCESS_LOST_WINDOW_MS) {
    return emptyAccessLostBudget();
  }
  return b;
}

/** 预算是否已耗尽（兜底待触发）：screen.ts 据此决定重启命令走 ddagrab 主
 *  路径还是 gdigrab 兜底。 */
export function accessLostFallbackDue(b: AccessLostBudget): boolean {
  return b.fallbackPending;
}

/** 预算是否跨参数变化重启保留：**保留**。显示器增删引发的参数变化重启本身
 *  就是 ACCESS_LOST 的最常见诱因（PnP 抖动），把预算洗掉会让「插拔显示器 →
 *  反复 ACCESS_LOST → 每次换参数洗预算」的循环永远走不到兜底；识别「这次
 *  参数变化是不是显示器变化」不可靠（Electron 只给几何/id，无拓扑序号），
 *  不做启发式。真正的恢复边界是**静默过期**（上次失败 >10s）：参数稳定 +
 *  DXGI 恢复后 10s 无新失败，窗口过期即清（accessLostOnSuccess 同一条边界）。 */
export function accessLostBudgetSurvivesParamChange(b: AccessLostBudget, now: number): boolean {
  return !(b.windowStarted !== 0 && now - b.windowStarted >= ACCESS_LOST_WINDOW_MS);
}

/** 用户显式重试是否全量清预算：是（epoch 变化 / want false→true 边沿）——
 *  那是人的明确意图，与窗口过期同级别的清零边界。 */
export const ACCESS_LOST_BUDGET_RESET_ON_EXPLICIT_RETRY = true;

/** 重启后 grace 窗口（毫秒）：**重启后** 2s 内的非 ACCESS_LOST 静默/失败
 *  不计任何通道的失败账。与 FFMPEG_MIN_ALIVE_MS（2s）的区别：MIN_ALIVE 只
 *  回答「这次算不算成功」，grace 回答「这次失败要不要记账」——区分
 *  「存活了」与「不算失败」。 */
export const ACCESS_LOST_GRACE_MS = 2000;

/** grace 截止时刻（spawn 时刻 + 2s）。**只授予「跟随 ACCESS_LOST 失败的重启」**
 *  ——正常首启/参数变化重启不授予：若每次重启都白送 2s 豁免，「spawn 即崩
 *  （非 ACCESS_LOST）」的持久故障会拿到无限次免计重试，通用熔断永远够不着
 *  （每次崩都在自己的 grace 里）。跟随 ACCESS_LOST 的重启则安全有界：豁免
 *  的是 DXGI 收敛期（~0.7s 一轮的重启撞进旧会话收敛窗口）的瞬时抖动，下一
 *  次死亡若仍非 ACCESS_LOST，其后的重启不再授予 grace → 计数恢复累加。 */
export function graceUntil(now: number): number {
  return now + ACCESS_LOST_GRACE_MS;
}

/** 一次非 ACCESS_LOST 退出是否落在 grace 豁免窗内（true = 不计失败账，
 *  也**不**当成功清零——中性处理，避免把抖动误读为恢复）。ACCESS_LOST
 *  退出不受豁免——它就是要靠预算累计判定的病。 */
export function graceExempt(graceUntilTs: number, now: number, accessLost: boolean): boolean {
  return !accessLost && graceUntilTs > 0 && now < graceUntilTs;
}

/** 熔断判定：命令仍要求运行（want）且通用连败达上限 → 停止自动重启。
 *  want=false 时不熔断（停止路径本来就不再重启，计数另行清零）。
 *  两条通道刻意分开且**语义不同**：consecutiveFailures 是通用崩溃（编码器坏/
 *  驱动崩，scrcpy 同款 3 次）→ 熔断停流；ACCESS_LOST 走时间窗预算 → 转
 *  gdigrab 兜底（fallbackPending），**不再熔断停流**——第三参保留旧签名
 *  向后兼容（旧调用方传 streak 的语义已由 budget.fallbackPending 取代）。 */
export function restartTripped(
  want: boolean,
  consecutiveFailures: number,
  _accessLostStreak = 0,
): boolean {
  return want && consecutiveFailures >= MAX_CONSECUTIVE_ERRORS;
}

/** 高帧率不稳提示门槛：>60fps 下 ACCESS_LOST 失败达此次数，上报文案附带
 *  「建议降到 60/120fps」（144fps 真机 ~5s 一死，3 次 ≈ 15-20s）。 */
export const HIGH_FPS_HINT_THRESHOLD = 3;

/** 从 ffmpeg 退出时的输出尾行识别 ACCESS_LOST（ddagrab 的失败特征）。
 *  887a0026 = DXGI_ERROR_ACCESS_LOST；AcquireNextFrame failed 一并命中
 *  （同属桌面复制会话失效家族，重试语义相同）。 */
export function isAccessLostExit(tailText: string): boolean {
  return /887a0026|AcquireNextFrame failed/i.test(tailText);
}

/** 预算耗尽转兜底时的上报文案（err_kind=reason，随 shell state 上报）。 */
export function accessLostFallbackMessage(): string {
  return `桌面复制频繁失效（连续 ${ACCESS_LOST_MAX_ATTEMPTS}+ 次 ACCESS_LOST），已切换 GDI 兼容模式捕获（30fps）——建议降低帧率（60/120fps），DXGI 恢复后在手机端点「重新开始投屏」切回`;
}

/** 兜底期间的周期性提示（诊断面板可见「兼容模式捕获中」）。 */
export function fallbackActiveMessage(): string {
  return "GDI 兼容模式捕获中（桌面复制不可用，30fps）——点「重新开始投屏」可切回高清模式";
}

/** 高帧率不稳提示（附带在 ACCESS_LOST 重试文案里，诊断面板可见）。 */
export function highFpsHintMessage(): string {
  return "高帧率下桌面复制不稳定，建议降到 60/120fps";
}

/** P1-5：start epoch 变化 = 新的开始意图（用户在手机端显式点了「开始投屏」）。
 *  熔断态下 want 电平不变，壳无法从电平感知「又点了一次」——后端每次
 *  screen.start 递增 start_epoch，壳按 epoch 变化识别显式重试。老后端不下发
 *  该字段（cmdEpoch undefined）恒 false：行为与旧版一致（只在 want=false 清零）。
 *  纯函数：screen.ts 的 reconcile 消费，node:test 直测。 */
export function newStartIntent(lastEpoch: number | undefined, cmdEpoch: number | undefined): boolean {
  return cmdEpoch !== undefined && cmdEpoch !== lastEpoch;
}

/** 一次重启尝试后的新计数：这次尝试成功（存活 ≥ 门槛）→ 清零（连败中断）；
 *  失败（spawn 失败/无编码器/存活 < 2s 退出）→ +1。started 参数保留
 *  （向后兼容：spawn 成功但存活时长未知时传 true）。
 *  ACCESS_LOST 失败不走本函数——它在独立通道记账（accessLostFail）。 */
export function afterRestartAttempt(started: boolean, consecutiveFailures: number): number {
  return started ? 0 : consecutiveFailures + 1;
}

/** 熔断上报文案（err_kind=reason，随 shell state 轮询上报——手机端可读）。 */
export function tripMessage(consecutiveFailures: number): string {
  return `ffmpeg 连续 ${consecutiveFailures} 次异常退出，已停止自动重启——请在手机端点「重新开始投屏」重试`;
}

// ---- gdigrab 软捕获兜底（纯函数：命令生成，screen.ts 的兜底重启消费）----
//
// 预算耗尽（fallbackPending）时**不熔断停流**：重启 ffmpeg 但命令切 gdigrab
// （GDI 抓屏，CPU 捕获）。像素路径（2026-12 ffmpeg 8.0 源码核对 + 本机 AMD
// 卡实测）：
//   * gdigrab 出系统内存帧（bgra）；h264_amf 声明支持 bgra 像素格式
//     （`-h encoder=h264_amf`：Supported pixel formats 含 bgra）且 ffmpeg
//     自动插入 hwupload 上传 D3D11 → **amf 硬编直接吃 gdigrab 内存帧，无需
//     显式 hwupload 滤镜**（ddagrab→amf 的 D3D11 帧路径与 gdigrab→amf 的
//     内存帧路径都是 ffmpeg 自动协商，本机冒烟 5 帧出流通过）；
//   * libx264：内存帧直灌，**不带 hwdownload**（它会把系统内存帧报
//     「不属于硬件帧」），同款 x264-params repeat-headers 保证 SPS/PPS 周期
//     重发（手机端 csd 提取依赖）；
//   * nvenc/qsv：自动 hwupload 理论可达但本机无该硬件、兜底语义是「能跑
//     就行」——兜底编码器链只含 amf → libx264（FALLBACK_ENCODER_CHAIN），
//     不把没验证过的路径带进兜底。
//
// **兜底降级语义（有意为之，注释钉住）**：
//   * 全桌面（`-i desktop` = virtual screen）不挑显示器——gdigrab 的逐屏
//     采集要么 offset/size 手算坐标（又添新故障面），要么 `-i title`（窗口
//     名不稳定）。兜底的目标是「画面还在」，全桌面 + 手机端已有 letterbox
//     足够（分辨率变为全桌面拼接尺寸，宽高比不符时手机端黑边显示）；
//   * fps 恒 30（FALLBACK_FPS，不管原档位）——GDI BitBlt 抓屏 CPU 开销大，
//     144fps 会把一核打满；兜底本来就是在牺牲性能保可用性；
//   * 退出兜底：用户显式重试（epoch 变化 / want false→true 边沿）→ 预算
//     全量清零，回 DXGI 主路径；预算窗口自然过期同理。兜底态经 ShellState
//     的 fallback_active 上报（诊断面板可见「兼容模式捕获中」）。

/** gdigrab 兜底的帧率：恒 30（GDI BitBlt CPU 开销的务实上限）。 */
export const FALLBACK_FPS = 30;

/** 兜底命令的编码器回落链：amf（硬编吃内存帧，本机冒烟通过）→ libx264
 *  （纯软编，30fps 1080p 实测可行的终极兜底）。**不含 nvenc/qsv**：
 *  没验证过的路径不进兜底。 */
export const FALLBACK_ENCODER_CHAIN = ["h264_amf", "libx264"] as const;

/** gdigrab 兜底命令生成（纯函数，screen.ts 直接 spawn）。
 *  与主命令（screen.ts ffmpegArgs）的差异：无 lavfi/ddagrab（换 gdigrab
 *  indev、`-draw_mouse` 是 gdigrab 的选项名，不是 ddagrab 的 draw_mouse=1）、
 *  无 -vf hwdownload（内存帧直灌）、码控参数按编码器分支（与主命令同款）。
 *  公共段（-bf 0、-g 2s GOP、-f h264 pipe:1、profile/level）与主命令一致。
 *
 *  首帧 IDR：AMF 分支的 `expr:gte(t,n_forced*2)` 在 t=0（n_forced=0）即为
 *  真 → 首帧被强制为 I 帧，配合 -forced_idr 1 钉成 IDR——重启后手机端
 *  立刻拿到可解码锚点；x264 首帧天然是 IDR（编码器初始化即 I 帧）且
 *  repeat-headers 让它自带 SPS/PPS。nvenc/qsv 不进兜底链，不涉及。
 *
 *  **x264 分支带 `-vf format=yuv420p`**（不带 hwdownload——内存帧直灌）：
 *  gdigrab 的 bgra 带 alpha，自动缩放会挑 yuv444p，与 -profile:v high 冲突
 *  报 -22；amf 分支吃 bgra 原样（它声明支持），无滤镜。 */
export function gdigrabArgs(encoder: string, qp: string, fps = FALLBACK_FPS): string[] {
  const gop = Math.max(1, Math.round(fps * 2)); // 2s GOP（与主命令 gopSize 同口径）
  const args = [
    "-hide_banner", "-loglevel", "error",
    "-f", "gdigrab", "-framerate", String(fps), "-draw_mouse", "1",
    "-i", "desktop",
    "-c:v", encoder,
  ];
  if (encoder === "h264_amf") {
    // 与主命令 AMF 分支同款：ultralowlatency 抑制周期性 IDR 与 SPS/PPS 重发
    //（-g 不生效），必须 force_key_frames + forced_idr；表达式 t=0 即触发
    // → 首帧 IDR + 每 2s 周期 IDR 一次满足。
    args.push(
      "-usage", "ultralowlatency", "-rc", "cqp", "-qp_i", qp, "-qp_p", qp,
      "-forced_idr", "1", "-force_key_frames", "expr:gte(t,n_forced*2)",
    );
  } else {
    // libx264（内存帧直灌，无 hwdownload）：zerolatency + repeat-headers；
    // scenecut=0 防 GOP 中途插 IDR（主命令同款）。
    // **format=yuv420p 必须有**：gdigrab 输出带 alpha 的 bgra，没有显式
    // format 滤镜时自动缩放会挑 yuv444p（alpha:1），与下面的 -profile:v
    // high（只收 4:2:0）冲突报 -22 Invalid argument（2026-11-24 新 ffmpeg
    // 实测）——主路径（screen.ts ffmpegArgs 的 hwdownload 链）有显式
    // format=yuv420p，兜底链此前漏了，同一坑两处都要钉。
    args.push(
      "-vf", "format=yuv420p",
      "-preset", "veryfast", "-tune", "zerolatency",
      "-x264-params", `keyint=${gop}:min-keyint=${gop}:repeat-headers=1:scenecut=0`,
      "-qp", qp,
    );
  }
  // level 恒 4.2：兜底恒 30fps，4.2 覆盖到 1080p 绰绰有余（全桌面 4K@30
  // 需要 5.1——level 偏高只影响压缩效率，不影响解码兼容性，保守取 4.2）。
  args.push("-profile:v", "high", "-level:v", "4.2");
  args.push("-bf", "0", "-g", String(gop), "-f", "h264", "pipe:1");
  return args;
}
