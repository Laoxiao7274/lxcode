// screen-restart.test.mjs —— ffmpeg 重启失败处理纯逻辑的单测（screen-restart.ts，
// 与 screen.ts 的 reconcile/close 处理消费同一份判定，测试与生产零漂移）。
//
// 钉三层语义：
//   ① 通用连败熔断：上限 3；want=false 不熔断；成功一次清零；达上限停止重启。
//   ② ACCESS_LOST 时间窗预算（RustDesk DxgiRecoveryState 同款）：10s 窗 ≤3 次；
//      超窗 → fallbackPending（gdigrab 兜底）而非熔断停流；成功帧不清零预算
//      （只有窗口自然过期才重置——~5s 周期抖动的空档呈「成功」形态）；
//      预算跨参数变化重启保留。
//   ③ gdigrab 兜底命令生成：恒 30fps、全桌面、无 hwdownload、首帧 IDR。
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import {
  MAX_CONSECUTIVE_ERRORS,
  FFMPEG_MIN_ALIVE_MS,
  restartTripped,
  afterRestartAttempt,
  attemptSucceeded,
  newStartIntent,
  tripMessage,
  isAccessLostExit,
  highFpsHintMessage,
  ACCESS_LOST_SHORT_BACKOFF_MS,
  ACCESS_LOST_BACKOFF_MAX_MS,
  accessLostBackoffMs,
  fallbackParamChangeExempt,
  ACCESS_LOST_WINDOW_MS,
  ACCESS_LOST_MAX_ATTEMPTS,
  ACCESS_LOST_GRACE_MS,
  FALLBACK_FPS,
  FALLBACK_ENCODER_CHAIN,
  gdigrabArgs,
  emptyAccessLostBudget,
  accessLostFail,
  accessLostRestartIssued,
  accessLostOnSuccess,
  accessLostFallbackDue,
  accessLostBudgetSurvivesParamChange,
  accessLostFallbackMessage,
  fallbackActiveMessage,
  graceUntil,
  graceExempt,
  HIGH_FPS_HINT_THRESHOLD,
} from "../src/screen-restart.ts";

test("连败计数：3 次失败后熔断（scrcpy 同值），成功一次即清零", () => {
  assert.equal(MAX_CONSECUTIVE_ERRORS, 3, "上限与 scrcpy MAX_CONSECUTIVE_ERRORS 同值");
  let n = 0;
  // 三连败
  n = afterRestartAttempt(false, n);
  n = afterRestartAttempt(false, n);
  assert.equal(n, 2);
  assert.equal(restartTripped(true, n), false, "第 2 次失败还不熔断");
  n = afterRestartAttempt(false, n);
  assert.equal(n, 3);
  assert.equal(restartTripped(true, n), true, "连败 3 次必须熔断，停止自动重启");
  // 第 4 次尝试不允许发生（熔断态 reconcile 不再调 startFfmpeg——计数不再增长）
  // 成功一次即清零
  n = afterRestartAttempt(true, 0);
  assert.equal(n, 0);
  assert.equal(restartTripped(true, n), false);
});

test("want=false 时不熔断（停止路径本来就不重启）", () => {
  assert.equal(restartTripped(false, 5), false);
  assert.equal(restartTripped(false, MAX_CONSECUTIVE_ERRORS), false);
  assert.equal(restartTripped(true, MAX_CONSECUTIVE_ERRORS - 1), false, "未达上限不熔断");
});

// P0-1 钉子：spawn 同步成功不算成功——按存活时长判定。存活 < 2s（1.9s，锁屏/
// 驱动坏的常态）= 失败 → 计数累加、熔断可达；存活 ≥ 2s（2.1s）= 成功 → 清零。
test("P0-1 存活时长判定：1.9s 失败累加、2.1s 成功清零", () => {
  assert.equal(FFMPEG_MIN_ALIVE_MS, 2000, "存活门槛 2s");
  assert.equal(attemptSucceeded(0), false, "spawn 即退（0ms）= 失败");
  assert.equal(attemptSucceeded(1999), false);
  assert.equal(attemptSucceeded(FFMPEG_MIN_ALIVE_MS), true, "恰好 2s = 成功");
  assert.equal(attemptSucceeded(2100), true, "存活 2.1s = 成功");
  let n = 0;
  n = afterRestartAttempt(attemptSucceeded(1900), n);
  n = afterRestartAttempt(attemptSucceeded(1900), n);
  n = afterRestartAttempt(attemptSucceeded(1900), n);
  assert.equal(n, 3, "三次短命尝试连败累加——熔断计数不被 spawn 成功复位");
  assert.equal(restartTripped(true, n), true, "锁屏/驱动坏常态下熔断必须可达");
  n = afterRestartAttempt(attemptSucceeded(2100), n);
  assert.equal(n, 0, "存活 ≥ 2s 的尝试清零连败计数");
});

// ACCESS_LOST 识别钉子（2026-11-24 真机诊断）：stderr 特征命中。
test("ACCESS_LOST 退出识别：887a0026 / AcquireNextFrame failed", () => {
  assert.ok(isAccessLostExit("AcquireNextFrame failed: 887a0026"), "ddagrab 失败特征命中");
  assert.ok(isAccessLostExit("[Parsed_ddagrab_0 @ xxx] AcquireNextFrame failed: 887a0026 | Error during demuxing"), "完整 stderr 尾行命中");
  assert.ok(isAccessLostExit("AcquireNextFrame failed: 887A0026"), "十六进制大小写不敏感");
  assert.ok(isAccessLostExit("887a0026"), "裸错误码命中");
  assert.equal(isAccessLostExit("Cannot load nvcuda.dll"), false, "驱动故障不误判");
  assert.equal(isAccessLostExit(""), false, "空尾行不误判");
  assert.equal(attemptSucceeded(4800, true), false, "ACCESS_LOST 退出 = 失败（哪怕存活过门槛）");
  assert.equal(attemptSucceeded(4800, false), true, "同存活非 ACCESS_LOST = 成功（对照）");
});

// ---- 时间窗预算（RustDesk DxgiRecoveryState 同款，2026-12 重构核心）----

test("时间窗预算：10s 滑动窗 ≤3 次，第 4 次 fallbackPending（不熔断停流）", () => {
  assert.equal(ACCESS_LOST_WINDOW_MS, 10_000, "窗口 10s（RustDesk 同值）");
  assert.equal(ACCESS_LOST_MAX_ATTEMPTS, 3, "窗口预算 3 次（RustDesk 同值）");
  const t0 = 1_000_000;
  let b = emptyAccessLostBudget();
  b = accessLostFail(b, t0);            // 首次：开窗
  assert.equal(b.attempts, 1);
  assert.equal(b.windowStarted, t0);
  assert.equal(accessLostFallbackDue(b), false);
  b = accessLostFail(b, t0 + 4_000);    // 窗内第 2 次（锚滑到当下）
  assert.equal(accessLostFallbackDue(b), false, "第 2 次仍给自愈机会");
  b = accessLostFail(b, t0 + 8_000);    // 窗内第 3 次 → 预算耗尽
  assert.equal(b.attempts, 3);
  assert.equal(accessLostFallbackDue(b), true, "第 3 次窗内失败即耗尽预算（放行额度 2 次重试，RustDesk 同口径）");
  b = accessLostFail(b, t0 + 9_000);    // 已超预算，状态保持
  assert.equal(accessLostFallbackDue(b), true, "兜底态保持");
  assert.equal(restartTripped(true, 0), false, "兜底不熔断——停流换成 gdigrab 重启");
});

test("时间窗预算：成功帧不清零（~5s 周期抖动必须能凑满预算）", () => {
  const t0 = 1_000_000;
  let b = emptyAccessLostBudget();
  // 实撞形态：~4.8s 一死，每次都过 2s 存活门槛呈「成功」空档——窗口内的
  // 成功不清预算，只有静默 >10s（上次失败 10s 前的成功）才重置。
  b = accessLostFail(b, t0);
  b = accessLostFail(b, t0 + 4_800);
  // 中间的「成功」（如 4.8s 存活后正常退出一次）：不清预算
  b = accessLostOnSuccess(b, t0 + 5_000);
  assert.equal(b.attempts, 2, "窗口内成功不清预算（周期抖动空档不清）");
  b = accessLostFail(b, t0 + 9_600);
  assert.equal(accessLostFallbackDue(b), true, "周期抖动凑满预算 → 兜底可达（旧连败计数凑不满的病根）");
});

test("时间窗预算：静默 >10s 过期重置（环境恢复后给新一轮 DXGI 机会）", () => {
  const t0 = 1_000_000;
  let b = emptyAccessLostBudget();
  b = accessLostFail(b, t0);
  b = accessLostFail(b, t0 + 1_000);
  b = accessLostFail(b, t0 + 2_000);    // 3 次，预算耗尽（fallbackPending）
  // 成功 + 静默已过期（距**最后一次失败** >10s——锚点滑动后的过期锚）→ 全量重置
  b = accessLostOnSuccess(b, b.windowStarted + ACCESS_LOST_WINDOW_MS + 1);
  assert.equal(b.attempts, 0, "静默过期重置");
  assert.equal(b.windowStarted, 0);
  assert.equal(accessLostFallbackDue(b), false, "兜底态随过期一并清（环境恢复过完整 10s）");
  // 失败路径同理：静默过期后的新失败开新段、attempts 归 1
  b = accessLostFail(b, t0 + ACCESS_LOST_WINDOW_MS + 2_000);
  assert.equal(b.attempts, 1, "静默过期后新失败开新段 attempts=1");
  assert.equal(b.windowStarted, t0 + ACCESS_LOST_WINDOW_MS + 2_000);
});

test("时间窗预算：跨参数变化重启保留（显示器增删的重启不洗掉预算）", () => {
  const t0 = 1_000_000;
  let b = emptyAccessLostBudget();
  b = accessLostFail(b, t0);
  b = accessLostFail(b, t0 + 4_000);
  // 静默未过期时参数变化重启（显示器增删）：预算保留
  assert.equal(accessLostBudgetSurvivesParamChange(b, b.windowStarted + 1_000), true, "窗内预算跨参数变化重启保留");
  // 静默过期：清（环境恢复边界，不做「显示器变化」启发式识别）
  assert.equal(accessLostBudgetSurvivesParamChange(b, b.windowStarted + ACCESS_LOST_WINDOW_MS + 1), false, "静默过期即清");
  // 超预算（fallbackPending）且静默未过期：同样保留——重启不能洗掉兜底态
  b = accessLostFail(b, b.windowStarted + 5_000);
  assert.equal(accessLostFallbackDue(b), true);
  assert.equal(accessLostBudgetSurvivesParamChange(b, b.windowStarted + 500), true, "兜底态跨参数变化重启保留");
});

test("restartPending 排程闭环：失败置位、spawn 落地清位", () => {
  const t0 = 1_000_000;
  let b = emptyAccessLostBudget();
  b = accessLostFail(b, t0);
  assert.equal(b.restartPending, true, "失败后排程重启");
  b = accessLostRestartIssued(b);
  assert.equal(b.restartPending, false, "spawn 实际发生清位");
  // 未置位时幂等
  assert.equal(accessLostRestartIssued(b).restartPending, false);
});

// ---- 重启 grace（区分「存活了」与「不算失败」）----

test("重启 grace：ACCESS_LOST 链重启后 2s 内的非 ACCESS_LOST 失败不计预算", () => {
  assert.equal(ACCESS_LOST_GRACE_MS, 2000, "grace 窗 2s");
  const spawnAt = 1_000_000;
  const until = graceUntil(spawnAt);
  assert.equal(until, spawnAt + 2000);
  // grace 内非 ACCESS_LOST 失败：豁免（不记账）
  assert.equal(graceExempt(until, spawnAt + 1_000, false), true, "grace 内非 AL 失败豁免");
  // ACCESS_LOST 失败不受豁免（它就是要靠预算累计判定的病）
  assert.equal(graceExempt(until, spawnAt + 1_000, true), false, "grace 不豁免 ACCESS_LOST");
  // grace 过后恢复记账
  assert.equal(graceExempt(until, spawnAt + 2_500, false), false, "grace 过后恢复记账");
  // 无 grace（until=0，首启/非链上重启）：不豁免
  assert.equal(graceExempt(0, spawnAt + 1_000, false), false, "无授予不豁免（防 spawn 即崩无限免计重试）");
});

// ---- gdigrab 兜底命令生成 ----

test("gdigrab 兜底命令：恒 30fps、全桌面、amf 内存帧直灌（无 hwdownload）", () => {
  assert.equal(FALLBACK_FPS, 30, "兜底恒 30fps（GDI BitBlt CPU 开销上限）");
  const args = gdigrabArgs("h264_amf", "26");
  assert.equal(args.join(" ").includes("gdigrab"), true, "gdigrab 输入");
  const iIdx = args.indexOf("-i");
  assert.equal(args[iIdx + 1], "desktop", "全桌面（virtual screen，兜底降级语义）");
  const frIdx = args.indexOf("-framerate");
  assert.equal(args[frIdx + 1], "30", "fps 恒 30 不管原档位");
  assert.ok(args.includes("-draw_mouse"), "gdigrab 用 -draw_mouse（ddagrab 是 draw_mouse=1）");
  assert.equal(args.includes("hwdownload"), false, "内存帧直灌，无 hwdownload（libx264 分支才有下载链问题）");
  assert.equal(args.includes("lavfi"), false, "无 lavfi/ddagrab");
  // amf 分支：ultralowlatency 抑制周期性 IDR，必须 force_key_frames + forced_idr；
  // gte(t,n_forced*2) 在 t=0 即为真 → 首帧 IDR + 每 2s 周期 IDR
  assert.ok(args.includes("ultralowlatency"), "amf 低延迟档");
  const fkIdx = args.indexOf("-force_key_frames");
  assert.ok(args[fkIdx + 1].includes("gte(t,n_forced*2)"), "首帧 + 每 2s 强制 IDR");
  assert.ok(args.includes("-forced_idr"), "I 帧钉成 IDR");
  // 公共段与主命令一致
  assert.ok(args.includes("-bf"), "无 B 帧");
  assert.ok(args.includes("pipe:1"), "裸 Annex B 到 stdout");
  assert.ok(args.includes("h264_amf"), "编码器仍硬编（amf 吃 gdigrab 内存帧，ffmpeg 自动 hwupload）");
  // 回落链不含 nvenc/qsv（没验证过的路径不进兜底）
  assert.deepEqual([...FALLBACK_ENCODER_CHAIN], ["h264_amf", "libx264"]);
});

test("gdigrab 兜底命令：libx264 分支内存帧直灌 + repeat-headers（无 hwdownload）", () => {
  const args = gdigrabArgs("libx264", "26");
  assert.equal(args.includes("hwdownload"), false, "gdigrab 出的已是内存帧，hwdownload 会报「不属于硬件帧」");
  assert.equal(args.includes("zerolatency"), true);
  const xpIdx = args.indexOf("-x264-params");
  assert.ok(args[xpIdx + 1].includes("repeat-headers=1"), "SPS/PPS 周期重发（手机端 csd 提取依赖）");
  assert.ok(args[xpIdx + 1].includes("keyint=60"), "2s GOP（30fps×2）");
  const gIdx = args.indexOf("-g");
  assert.equal(args[gIdx + 1], "60", "公共段 -g 与 GOP 换算一致");
  // **format=yuv420p 钉子**：gdigrab 的 bgra 带 alpha，无显式 format 时自动
  // 缩放挑 yuv444p，与 -profile:v high（4:2:0）冲突报 -22（2026-11-24 实测）。
  // 主路径 hwdownload 链有显式 format，兜底链漏了——同一坑两处都要钉。
  const vfIdx = args.indexOf("-vf");
  assert.ok(vfIdx > 0, "x264 分支带 -vf");
  assert.equal(args[vfIdx + 1], "format=yuv420p", "显式转 4:2:0（high profile 只收）");
  // amf 分支不受影响（它声明支持 bgra，吃内存帧原样）——反向钉住别误加
  const amfArgs = gdigrabArgs("h264_amf", "26");
  assert.equal(amfArgs.includes("-vf"), false, "amf 分支无 -vf（bgra 直灌）");
});

// 兜底语义钉子（screen.ts 接线层）：
//   ① 兜底重启不走主链编码器探测（ddagrab 硬帧管线 ≠ gdigrab 内存帧管线）；
//   ② 兜底态上报 fallback_active（诊断面板可见「兼容模式捕获中」）；
//   ③ 显式重试清兜底态回主路径；④ 通用连败熔断文案不含 ACCESS_LOST。
test("源码钉子：兜底重启 / fallback_active 上报 / 显式重试退出兜底", () => {
  const src = readFileSync(join(import.meta.dirname, "..", "src", "screen.ts"), "utf8");
  assert.ok(src.includes("startFallbackFfmpeg"), "兜底重启函数存在");
  assert.ok(src.includes("gdigrabArgs("), "兜底命令走纯函数生成");
  assert.ok(src.includes("fallback_active:"), "ShellState 上报兜底态（诊断面板可见）");
  assert.ok(src.includes("fallbackActiveMessage"), "兜底态周期性提示");
  // 显式重试（epoch 变化）全量清预算含兜底态
  const retryIdx = src.indexOf("accessLostBudget = emptyAccessLostBudget(); // 显式重试");
  assert.ok(retryIdx > 0, "显式重试清兜底态（回 DXGI 主路径）");
  // 首启/非链上重启不授予 grace（防 spawn 即崩无限免计重试）
  const graceGrant = src.match(/if \(accessLostBudget\.windowStarted !== 0\) \{\s*\n\s*graceUntilTs = graceUntil/);
  assert.ok(graceGrant, "主链重启的 grace 只在 ACCESS_LOST 链上授予");
  assert.ok(src.includes("fallback_active: fallbackCaptureActive(runningFallback"), "上报含预算兜底位（兜底态统一口径：runningFallback ∪ fallbackPending）");
});

// ACCESS_LOST 预算升级兜底文案可读（诊断面板 + 手机端）。
test("兜底文案：指明兼容模式与恢复路径", () => {
  const msg = accessLostFallbackMessage();
  assert.ok(msg.includes("GDI 兼容模式"), "点名兼容模式");
  assert.ok(msg.includes("60/120"), "提示降帧率");
  assert.ok(msg.includes("重新开始投屏"), "指向恢复路径");
  const active = fallbackActiveMessage();
  assert.ok(active.includes("兼容模式"), "兜底周期提示可见");
});

// 高帧率不稳提示（诊断面板可见）：>60fps 下窗口内失效达 3 次附带降帧率建议。
test("高帧率不稳提示：阈值 3 次、文案指向降帧率", () => {
  assert.equal(HIGH_FPS_HINT_THRESHOLD, 3, "提示门槛 3 次（144fps 下 ~15-20s 内出现）");
  const hint = highFpsHintMessage();
  assert.ok(hint.includes("60/120"), "建议降到 60/120fps");
  assert.ok(hint.includes("高帧率"), "点名高帧率场景");
  // 源码钉子：提示只在 fps>60 且窗口内失效达阈值时附带（screen.ts close 回调）
  const src = readFileSync(join(import.meta.dirname, "..", "src", "screen.ts"), "utf8");
  assert.ok(src.includes("ffmpegFps > 60 && accessLostBudget.attempts >= HIGH_FPS_HINT_THRESHOLD"), "提示判定必须同时看帧率与窗口内失效次数");
});

test("P1-5 显式重试判定：epoch 变化 = 新的开始意图；老后端（无 epoch）恒否", () => {
  assert.equal(newStartIntent(3, 4), true, "epoch 变化 = 显式重试（熔断恢复路径）");
  assert.equal(newStartIntent(3, 3), false, "epoch 未变（普通轮询）= 不重试");
  assert.equal(newStartIntent(undefined, undefined), false);
  assert.equal(newStartIntent(undefined, 1), true, "首见 epoch = 新的开始意图（此时计数为 0，无副作用）");
});

// close 回调实例一致性闸门（screen.ts 接线层，纯函数层表达不了进程身份）：
// 旧进程被 stop/替换后它的 close 回调迟到，必须不计任何成败、不碰新实例引用。
test("源码钉子：close 回调先判实例一致性（thisSpawn）再做失败记账", () => {
  const src = readFileSync(join(import.meta.dirname, "..", "src", "screen.ts"), "utf8");
  const closeFn = src.match(/ffmpeg\.on\("close",[\s\S]*?\n  \}\);/)?.[0];
  assert.ok(closeFn, "screen.ts 应有 ffmpeg close 回调");
  const gateIdx = closeFn.indexOf("ffmpeg !== thisSpawn");
  const countIdx = closeFn.indexOf("attemptSucceeded");
  assert.ok(gateIdx >= 0, "close 回调必须先判 ffmpeg !== thisSpawn（旧实例退出不计成败）");
  assert.ok(countIdx > gateIdx, "存活时长计数必须在实例闸门之后（旧实例退出不走计数）");
  // grace 豁免判定在闸门之后（旧实例退出不走豁免/记账）
  const graceIdx = closeFn.indexOf("graceExempt");
  assert.ok(graceIdx > gateIdx, "grace 豁免判定必须在实例闸门之后");
});

test("熔断上报文案：停止重启的原因对手机端可读，指向重新开始投屏重试", () => {
  const msg = tripMessage(3);
  assert.ok(msg.includes("3"), "应带连败次数");
  assert.ok(msg.includes("重新开始投屏"), "应指向手机端重新开始投屏（P1-5 的恢复路径）");
  assert.ok(msg.includes("重试"), "点重新开始即可重试（显式重试语义，P1-5）");
  assert.notEqual(msg, "", "错误原因不能为空——手机端无从排查");
});

// 显示器拓扑取证（main.ts 接线层）：display-added/removed 事件写 renderer.log，
// 与 [screen] 的 ACCESS_LOST 重启日志对照（±1s）即「疑似显示拓扑变化触发」。
test("源码钉子：显示器拓扑变化事件取证日志（main.ts）", () => {
  const src = readFileSync(join(import.meta.dirname, "..", "src", "main.ts"), "utf8");
  assert.ok(src.includes('screen.on("display-added"'), "监听 display-added");
  assert.ok(src.includes('screen.on("display-removed"'), "监听 display-removed");
  assert.ok(src.includes("display topology changed"), "日志带统一检索前缀（[screen] display topology changed）");
  // 落 renderer.log（appendRendererLog）——打包形态主进程 stdout 不可见
  const addedIdx = src.indexOf('screen.on("display-added"');
  const logIdx = src.indexOf("appendRendererLog", addedIdx);
  assert.ok(logIdx > addedIdx, "拓扑事件写 renderer.log（取证依赖打包形态可见性）");
});

// ---- 0.3.68 投屏重启风暴两连修 ----

// Bug A 纯函数钉子：兜底豁免的判定面（fallbackParamChangeExempt）。
test("兜底豁免判定：兜底存续 + want 电平未变 = 豁免；电平变化不豁免", () => {
  // 兜底 ffmpeg 在跑（runningFallback）且电平未变：豁免——不是真实意图
  assert.equal(fallbackParamChangeExempt(true, false, true), true, "gdigrab 在跑 + 电平未变 = 豁免");
  // 预算已耗尽（fallbackPending，兜底进程死了还没重起）：同样豁免——
  // 否则参数分支会趁兜底未落地把它洗回主链 ddagrab（又记账）
  assert.equal(fallbackParamChangeExempt(false, true, true), true, "fallbackPending + 电平未变 = 豁免");
  // 非兜底态：不豁免（正常参数变化照旧重启）
  assert.equal(fallbackParamChangeExempt(false, false, true), false, "非兜底态不豁免");
  // want 电平变了（换显示器/换档位/换帧率）：不豁免——真实意图，兜底让位
  assert.equal(fallbackParamChangeExempt(true, false, false), false, "兜底在跑但电平变了 = 不豁免（真实意图）");
  assert.equal(fallbackParamChangeExempt(false, true, false), false, "pending 但电平变了 = 不豁免");
  assert.equal(fallbackParamChangeExempt(true, true, false), false, "兜底态 + 电平变化 = 让位（防兜底态永久钉死）");
});

// Bug A 源码钉子：reconcile 的参数变化分支必须先走兜底豁免（判定面用 want
// 电平轮间比较，不与兜底进程的 monitor=-1/fps=30 降级参数比——那是旧病根）。
test("源码钉子：reconcile 参数变化分支的兜底豁免（want 电平判定面）", () => {
  const src = readFileSync(join(import.meta.dirname, "..", "src", "screen.ts"), "utf8");
  const exemptIdx = src.indexOf("fallbackParamChangeExempt(runningFallback");
  const paramIdx = src.indexOf("wantCmd.monitor !== ffmpegMonitor || q !== ffmpegQuality");
  assert.ok(exemptIdx > 0, "reconcile 有兜底豁免分支");
  assert.ok(paramIdx > exemptIdx, "豁免分支必须在参数变化分支之前（否则兜底先被杀）");
  // 判定面 = want 电平轮间比较（lastWant），不是与 ffmpegMonitor/ffmpegFps 比
  const snapIdx = src.indexOf("lastWant = { ...wantCmd }");
  assert.ok(snapIdx > 0, "want 电平轮间快照存在（lastWant）");
  assert.ok(src.includes("lastWant !== null"), "快照判空（首轮 lastWant=null 不豁免）");
  // 显式重试清兜底标志（epoch 分支让位回主路径）
  assert.ok(src.includes("runningFallback = false;\r\n      accessLostRetryAt = 0;") || src.includes("runningFallback = false;\n      accessLostRetryAt = 0;"), "显式重试清 runningFallback");
  // stopFfmpeg 清兜底标志、startFallbackFfmpeg 置位
  assert.ok(src.includes("runningFallback = false; // 兜底态随进程停止退出"), "stopFfmpeg 清 runningFallback");
  assert.ok(src.includes("runningFallback = true; // 兜底态：reconcile 的参数变化分支据此豁免"), "startFallbackFfmpeg 置 runningFallback");
});

// Bug B-① 钉子：探测结果复用——成功缓存终身 + 失败 TTL 内不重探，且所有
// spawn 路径走统一入口（ensureEncoderProbe，分层短路不重复真实例化）。
test("源码钉子：编码器探测结果跨重启复用（ensureEncoderProbe 统一入口）", () => {
  const src = readFileSync(join(import.meta.dirname, "..", "src", "screen.ts"), "utf8");
  // startFfmpeg 不再直连 probeEncoders——走 ensureEncoderProbe（带预热/重探接线）
  assert.ok(src.includes("await ensureEncoderProbe(true)"), "startFfmpeg 走 ensureEncoderProbe");
  assert.ok(src.includes("if (encoderProbe) return encoderProbe;"), "成功缓存终身（真实例化只跑一次）");
  assert.ok(src.includes("if (probeInFlight) return probeInFlight;"), "在途去重（并发调用共用一次探测）");
  assert.ok(src.includes("scheduleWarmRetry()"), "探测失败后排一次空闲重探（自愈）");
  // 日志如实化：不再恒报「（真实例化）」（0.3.68 取证被误导的根因）
  assert.ok(src.includes("缓存命中"), "探测日志区分命中与真实例化");
});

// Bug B-② 钉子：退避序列化——500ms → 1s → 2s → 4s → 封顶 8s（窗口预算内
// 拉开失败间隔，重启风暴的重建频率降一个量级）。
test("ACCESS_LOST 退避序列：500ms 起步逐次翻倍封顶 8s", () => {
  assert.equal(ACCESS_LOST_SHORT_BACKOFF_MS, 500);
  assert.equal(ACCESS_LOST_BACKOFF_MAX_MS, 8_000, "退避封顶 8s（重启风暴不再 ~5s 周期烧会话）");
  assert.equal(accessLostBackoffMs(1), 500);
  assert.equal(accessLostBackoffMs(2), 1_000);
  assert.equal(accessLostBackoffMs(3), 2_000);
  assert.equal(accessLostBackoffMs(4), 4_000);
  assert.equal(accessLostBackoffMs(5), 8_000, "封顶");
  assert.equal(accessLostBackoffMs(9), 8_000, "超过序列继续封顶");
  assert.equal(accessLostBackoffMs(0), 500, "边界（异常输入钳到基数）");
  // 源码钉子：close 回调的 AL 分支用序列（高帧率下），低帧率维持 500ms 不变
  const src = readFileSync(join(import.meta.dirname, "..", "src", "screen.ts"), "utf8");
  assert.ok(src.includes("ffmpegFps > 60 ? accessLostBackoffMs(accessLostBudget.attempts) : ACCESS_LOST_SHORT_BACKOFF_MS"), "AL 退避：高帧率走序列、低帧率维持 500ms");
});

// Bug B-③ 钉子：控制面/数据面解耦——reconcile 不再跑在 tick 的 await 链上
// （秒级探测曾饿过 shellStale 4s 保鲜窗，后端报壳离线拒绝手机 start），
// reconcile 跑超 2s 补一次空 ShellState 心跳把 lastSeen 推进保鲜窗。
test("源码钉子：轮询环与探测解耦 + 探测期心跳补偿", () => {
  const src = readFileSync(join(import.meta.dirname, "..", "src", "screen.ts"), "utf8");
  const tickIdx = src.indexOf("void reconcile().finally(");
  assert.ok(tickIdx > 0, "reconcile 挪出 tick await 链（后台 void 跑）");
  // 心跳补偿：reconcile 耗时 >2s（shellStale 4s 的一半）时补发一次上报
  const compIdx = src.indexOf("if (dt > 2000)", tickIdx);
  assert.ok(compIdx > tickIdx, "探测/重连链拖 >2s 触发心跳补偿");
  assert.ok(src.includes("probeStallCount++"), "被饿探针计数（验证修复的长期观测）");
  assert.ok(src.includes("async function postState"), "心跳补偿走独立上报函数");
});
