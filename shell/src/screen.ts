// screen.ts —— 壳侧屏幕捕获模块（投屏第一阶段：只看）。
//
// 角色划分（internal/protocol/screen.go）：lxcode 后端跑在 SCM 服务（session 0）
// 抓不了用户桌面——ffmpeg 由本模块（壳，用户会话内）拉起；后端只做调度与转发。
// 通道：本模块 1s 轮询后端 GET /screen/ctl（命令电平：want/monitor/quality +
// 自身状态上报），按命令起停 ffmpeg；ffmpeg stdout（H.264 Annex B 裸流）按
// 访问单元（AU）切帧、加 13 字节帧头（FrameHeader，internal/protocol/screen.go）
// 后 POST 到 /screen/stream。
//
// ffmpeg 命令（GDI Desktop Duplication via lavfi ddagrab → NVENC/QSV/AMF/x264）：
//   ffmpeg -hide_banner -loglevel error -f lavfi
//     -i ddagrab=output_idx=N:framerate=30:draw_mouse=1
//     -c:v <encoder> <编码器分支码控参数，见 ffmpegArgs> 
//     -bf 0 -g 60 -f h264 pipe:1
// 回落链 h264_nvenc → h264_qsv → h264_amf → libx264（libx264 追加
// -x264-params keyint=60:repeat-headers=1 保证 SPS/PPS 周期重发）。
//   * 码控参数按编码器分支发：qsv/amf 不认 NVENC 风格参数（详见 ffmpegArgs 注释）：
//     nvenc  -preset p1 -tune ull -rc constqp -qp X
//     qsv    -preset veryfast -low_power 0 -async_depth 1 -global_quality X（CQP）
//     amf    -usage ultralowlatency -rc cqp -qp_i X -qp_p X + 每 2s 强制 IDR
//           （本机驱动实测：ultralowlatency 抑制周期性 IDR 与 SPS/PPS 重发，
//           -g 不生效——黑屏根因，见 ffmpegArgs AMF 分注释）
//     x264   -preset veryfast -tune zerolatency -qp X + repeat-headers
//   * bf=0：无 B 帧 → 解码端零重排序延迟（安卓低延迟解码的关键参数）；
//   * g=60：2 秒一个关键帧——手机断线重连最多 2s 恢复画面；
//   * libx264 的 repeat-headers 让每个关键帧 AU 自带 SPS/PPS（MediaCodec 的
//     csd-0/csd-1 从它提取；NVENC 系默认在每个关键帧前写 SPS/PPS，同效）。
// 画质映射：qp 22（medium）/ 26（low）/ 18（high）——constqp 下码率随内容浮动，
// 「档位」表现为 qp 档位（Stage 1 不做逐内容码率控制）。
//
// 桌面轻提示（隐私）：投屏运行期间托盘 tooltip 变为「Lxcode — 正在投屏…」+
// displayBalloon 一次性气泡；主窗口侧经 pushToRenderer('screen:capturing', …)
// 推状态（渲染层自行决定怎么轻量展示）。
import { spawn, type ChildProcess } from "node:child_process";
import { accessSync } from "node:fs";
import { join } from "node:path";
import { request as httpRequest } from "node:http";
import type { Socket } from "node:net";
import { screen, type Tray } from "electron";
import { getTray } from "./tray";
import { cachedToken } from "./backend-remote";
import { feedAnnexB, flushAu, resetAu, FRAME_FLAG_KEYFRAME } from "./screen-au";
import { restartTripped, afterRestartAttempt, tripMessage, MAX_CONSECUTIVE_ERRORS } from "./screen-restart";

const BACKEND = process.env.LXCODE_ADDR ?? "127.0.0.1:7789";

/** 壳↔后端屏端点用的 token（与 /rpc 同一份 remote.json 门）。每次现取缓存：
 *  token 轮换/开关后下一次轮询即生效——不要在模块加载时快照。 */
function token(): string {
  return cachedToken() ?? "";
}
const POLL_ONLINE_MS = 1000;
const POLL_OFFLINE_MS = 5000;
const QUALITY_QP: Record<string, string> = { low: "26", medium: "22", high: "18" };

/** 帧头（与 Go protocol.FrameHeader 一致）：1 版本 + 1 flags + 3 保留 + 8 ts。 */
interface ShellState {
  running: boolean;
  monitor: number;
  encoder: string;
  width: number;
  height: number;
  error?: string;
  err_kind?: "reason" | "ffmpeg_missing" | "encoder_unavailable";
  monitors: Array<{ id: number; name: string; primary: boolean; width: number; height: number; attached: boolean }>;
}

interface Command {
  want: boolean;
  monitor: number;
  quality: string;
}

let ffmpeg: ChildProcess | null = null;
let ffmpegMonitor = -1;
let ffmpegEncoder = "";
let ffmpegQuality = "";
let lastError = "";
let lastErrKind: ShellState["err_kind"] = undefined;
/** ffmpeg stderr 尾行环形缓冲（每次起进程清空）——崩溃时并入错误上报，
 *  「异常退出」必须带原因（真机教训：只报 code 时手机端完全无从排查）。 */
const STDERR_TAIL_MAX = 8;
const stderrTail: string[] = [];

/** 取 stderr 最后几行（拼成单行，去掉换行避免上报 JSON 里串行）。 */
function stderrTailText(): string {
  return stderrTail.slice(-STDERR_TAIL_MAX).join(" | ");
}
let wantCmd: Command = { want: false, monitor: 0, quality: "medium" };
let pollTimer: NodeJS.Timeout | null = null;
let streamSocket: Socket | null = null;
let monitorsCache: ShellState["monitors"] = [];

// ---- AU 切帧 → 帧头封装 → 推流（实现见 screen-au.ts；此处只留传输策略）----

let auTs = 0;

/** 把一块 ffmpeg stdout 并进 AU 切帧器，产出的每帧（13 字节头 + AU）直接
 *  写进推流连接。时间戳在 emitFrame 里统一加（切帧器对时间戳无感）。 */
function feedStdout(chunk: Buffer): void {
  feedAnnexB(chunk, emitFrame);
}

/** emitFrame：帧 → 推流连接（背压丢帧：非关键帧优先丢，关键帧压着也要写）。 */
function emitFrame(frame: Buffer): void {
  if (!streamSocket || streamSocket.destroyed) return;
  const isKey = (frame[1] & FRAME_FLAG_KEYFRAME) !== 0;
  auTs += 33; // 30fps 恒定帧距（诊断用；手机端不依赖它做解码时序）
  frame.writeBigUInt64BE(BigInt(auTs % 0xffffffffffff), 5);
  // 背压：写缓冲超过 ~400KB（约 10-40 帧）就丢非关键帧——丢一帧只是轻微
  // 卡顿，丢关键帧要等下一个 GOP（2s）才能恢复画面。
  if (!isKey && streamSocket.writableLength > 400_000) return;
  streamSocket.write(frame);
}

// ---- ffmpeg 进程管理 ----

/** 编码器探测预热：投屏未启动时提前在后台把 probeEncoders 跑完（每个候选
 *  ~1s，串行最长 4 个 ≈ 数秒），真机「点开始要等好几秒」的根因之一——
 *  首次 startFfmpeg 现场探测把 start→出流的空窗拉长数秒，用户以为没点上
 *  又点。壳进程常驻，预热一次终身受益（结果模块级缓存）。 */
export function warmUpEncoderProbe(): void {
    setTimeout(() => {
        const bin = ffmpegPath();
        if (!bin) return;
        void probeEncoders(bin).then((list) => {
            console.log(`[screen] 编码器探测预热完成: ${list.join(", ") || "（无）"}`);
        });
    }, 3_000); // 壳启动稳定后 3s 再跑（不与启动期 IO 抢）
}

/** ffmpeg 可执行文件：LXCODE_FFMPEG 覆盖 → bundled（resources/bin/ffmpeg.exe，
 *  精简构建随壳打包，见 docs/ffmpeg-build.md）→ PATH（用户自装的 full 版）。 */
function ffmpegPath(): string | null {
  const override = process.env.LXCODE_FFMPEG;
  if (override) {
    return existsExecutable(override) ? override : null;
  }
  // 打包形态：extraResources 的 bin/ 里带了 bundled ffmpeg（开发模式无此目录，跳过）
  try {
    const bundled = join(process.resourcesPath ?? "", "bin", "ffmpeg.exe");
    if (bundled && existsExecutable(bundled)) return bundled;
  } catch {
    /* 开发模式 process.resourcesPath 不存在或不可用 → 走 PATH */
  }
  for (const dir of (process.env.PATH ?? "").split(";")) {
    if (!dir) continue;
    const p = join(dir.trim(), "ffmpeg.exe");
    if (existsExecutable(p)) return p;
  }
  // Git Bash 形态下 PATH 分隔是 ":"；兜底再按 ":" 扫一遍
  for (const dir of (process.env.PATH ?? "").split(":")) {
    if (!dir || !dir.includes("/")) continue;
    const p = join(dir.trim(), "ffmpeg.exe");
    if (existsExecutable(p)) return p;
  }
  return null;
}

function existsExecutable(p: string): boolean {
  try {
    accessSync(p, 1 /* X_OK */);
    return true;
  } catch {
    return false;
  }
}

/** 探测编码器可用性——**真实例化**，不是 `-encoders` 文本匹配：
 *  `-encoders` 列出的是编译进 exe 的编码器名，与机器上有没有对应显卡无关
 *  （2026-10 真机：AMD 卡上 `-encoders` 照列 h264_nvenc → 回落链选中它 →
 *  运行时 `Cannot load nvcuda.dll` → 立即异常退出，手机端只见「ffmpeg 异常
 *  退出」）。所以每个候选编码器跑一次最小真实管线（同款参数 + -frames:v 1，
 *  只开路到 muxer 就收，~1s），实际能出帧的才进回落链。libx264 静态链入
 *  恒可用（兜底永不落空）。结果缓存——探测一次之后的起停不再打子进程。 */
let encoderProbe: string[] | null = null;
const PROBE_CHAIN = ["h264_nvenc", "h264_qsv", "h264_amf", "libx264"] as const;

/** 单个编码器的最小真实管线（返回码 0 = 编码器真的能出帧）。
 *  参数与 ffmpegArgs 各分支一致（改码控参数时两处同步）。 */
function probeArgs(encoder: string, qp: string): string[] {
  const args = [
    "-hide_banner", "-loglevel", "error",
    "-f", "lavfi", "-i", "ddagrab=output_idx=0:framerate=30:draw_mouse=1",
    "-c:v", encoder,
  ];
  if (encoder === "h264_nvenc") {
    args.push("-preset", "p1", "-tune", "ull", "-rc", "constqp", "-qp", qp);
  } else if (encoder === "h264_amf") {
    args.push(
      "-usage", "ultralowlatency", "-rc", "cqp", "-qp_i", qp, "-qp_p", qp,
      "-forced_idr", "1", "-force_key_frames", "expr:gte(t,n_forced*2)",
    );
  } else if (encoder === "h264_qsv") {
    args.push("-preset", "veryfast", "-low_power", "0", "-async_depth", "1", "-global_quality", qp);
  } else {
    args.push(
      "-preset", "veryfast", "-tune", "zerolatency",
      "-x264-params", "keyint=60:min-keyint=60:repeat-headers=1:scenecut=0",
      "-qp", qp,
    );
  }
  // 兼容性约束（与 ffmpegArgs 同步——改这里必须改那里）：显式钉死 profile/level，
  // 不让编码器用默认值（AMF 默认可能报 High 5.2 级别，超出部分手机解码器的
  // profileLevels 支持表 → MediaCodec.configure「该设备不支持此流参数」）。
  args.push("-profile:v", "high", "-level:v", "4.1");
  // 1 帧就收：不落盘、不给壳的 stdout 泵喂帧（走单独子进程）；null 化由
  // "-frames:v 1 -f h264 pipe:1" + 立即 drain 实现（精简构建没有 null muxer）。
  args.push("-bf", "0", "-g", "60", "-frames:v", "1", "-f", "h264", "pipe:1");
  return args;
}

async function probeOne(bin: string, encoder: string): Promise<boolean> {
  return new Promise<boolean>((resolve) => {
    const p = spawn(bin, probeArgs(encoder, "26"), {
      stdio: ["ignore", "ignore", "ignore"],
      windowsHide: true,
    });
    let settled = false;
    const done = (ok: boolean): void => {
      if (!settled) { settled = true; try { p.kill(); } catch { /* already dead */ } resolve(ok); }
    };
    p.on("error", () => done(false));
    p.on("close", (code) => done(code === 0));
    setTimeout(() => done(false), 15_000); // x264 1 帧 <1s；15s 是死机/驱动卡死的保险
  });
}

async function probeEncoders(bin: string): Promise<string[]> {
  if (encoderProbe) return encoderProbe;
  const available: string[] = [];
  for (const e of PROBE_CHAIN) {
    if (await probeOne(bin, e)) available.push(e);
  }
  encoderProbe = available;
  return encoderProbe;
}

/** 组装 ffmpeg 参数（monitor/quality/encoder）。码控参数**按编码器分支**发——
 *  qsv/amf 不认 NVENC 风格的 `-preset p1 -tune ull -rc constqp`（本机 AMD 实测
 *  `Undefined constant or missing '(' in 'p1'`），回落编码器必须各用各的参数面。
 *  清晰度档统一为 qp 档（QUALITY_QP），各分支落到自己的码控参数：
 *    nvenc  → constqp + -qp；amf → cqp + -qp_i/-qp_p；qsv → CQP + -global_quality
 *    （qsv 顶层无 -rc/-qp_i 选项，CQP 经 AVCodecContext.global_quality 表达）；
 *    libx264 → -qp（zerolatency tune）。 */
function ffmpegArgs(monitor: number, quality: string, encoder: string): string[] {
  const qp = QUALITY_QP[quality] ?? QUALITY_QP.medium;
  const args = [
    "-hide_banner", "-loglevel", "error",
    "-f", "lavfi", "-i", `ddagrab=output_idx=${monitor}:framerate=30:draw_mouse=1`,
    "-c:v", encoder,
  ];
  if (encoder === "h264_nvenc") {
    args.push("-preset", "p1", "-tune", "ull", "-rc", "constqp", "-qp", qp);
  } else if (encoder === "h264_amf") {
    // 本机 AMD 实测（2026-10-27 冒烟通过）：-usage/-rc 均为 amf 专有枚举，cqp 档
    // 用 -qp_i/-qp_p 指定 I/P 帧 qp。不额外发 -quality speed（保守：默认即可）。
    // 关键帧周期（2026-10-27 真机取证）：本机驱动上 `-usage ultralowlatency` 会
    // **完全抑制周期性 IDR**——-g/-gops_per_idr/-header_spacing 全部不生效，
    // 60s 流只有开头 1 个 IDR 且 SPS/PPS 只出现一次 → 中途接入/重连的手机拿不到
    // csd（手机端黑屏根因）。绕过：-force_key_frames 表达式 + -forced_idr 把 I 帧
    // 钉成 IDR——实测每 2s 出完整「SPS+PPS+IDR」AU，帧内容解码验证非黑。
    args.push(
      "-usage", "ultralowlatency", "-rc", "cqp", "-qp_i", qp, "-qp_p", qp,
      "-forced_idr", "1", "-force_key_frames", "expr:gte(t,n_forced*2)",
    );
  } else if (encoder === "h264_qsv") {
    // 保守取值（本机无 Intel 卡，未真机验证）：
    //   -preset veryfast（qsv 枚举 0-7，veryfast=7 是最快档）；
    //   -low_power 0 强制全功率模式（auto 在部分旧驱动会落 VD 低功路，低延迟
    //   BRC/高频_qp 档受限——显式 0 规避不确定性）；
    //   -async_depth 1 把异步队列压到 1 帧，降低编码端流水线延迟；
    //   CQP 走 -global_quality（qsv 顶层无 -rc/-qp_i/-qp_p；不给 bitrate 时
    //   qsv 按 CQP 解释 global_quality）。
    args.push(
      "-preset", "veryfast", "-low_power", "0", "-async_depth", "1",
      "-global_quality", qp,
    );
  } else if (encoder === "libx264") {
    args.push(
      "-preset", "veryfast", "-tune", "zerolatency",
      "-x264-params", "keyint=60:min-keyint=60:repeat-headers=1:scenecut=0",
      "-qp", qp,
    );
  } else {
    // 未知编码器名（理论上走不到：probeEncoders 只从白名单选）。保守发
    // 通用参数——不给编码器专有选项，靠 -g/-bf 公共段 + 默认码控。
    args.push("-qp", qp);
  }
  // 兼容性约束：显式 -profile:v high -level:v 4.1（覆盖几乎所有手机的解码器
  // profileLevels；AMF 默认可能输出 level 5.x，个别手机 MediaCodec.configure
  // 直接拒——「该设备不支持此流参数」。不明示就让编码器报默认值，出问题不可控）。
  args.push("-profile:v", "high", "-level:v", "4.1");
  args.push("-bf", "0", "-g", "60", "-f", "h264", "pipe:1");
  return args;
}

/** 起 ffmpeg（monitor/quality/encoder 已定）。编码器按可用性回落。 */
async function startFfmpeg(monitor: number, quality: string): Promise<void> {
  const bin = ffmpegPath();
  if (!bin) {
    lastError = "找不到 ffmpeg——请安装 ffmpeg 并加入 PATH（或设置 LXCODE_FFMPEG 指向 ffmpeg.exe）";
    lastErrKind = "ffmpeg_missing";
    console.log(`[screen] ${lastError}`);
    return;
  }
  const available = await probeEncoders(bin);
  console.log(`[screen] 编码器探测（真实例化）: ${available.join(", ") || "（无）"}`);
  const chain = ["h264_nvenc", "h264_qsv", "h264_amf", "libx264"];
  const encoder = chain.find((e) => available.includes(e));
  if (!encoder) {
    lastError = "ffmpeg 里没有可用的 H.264 编码器（nvenc/qsv/amf/libx264 都没有）——请重新安装完整版 ffmpeg";
    lastErrKind = "encoder_unavailable";
    console.log(`[screen] ${lastError}`);
    return;
  }
  await startStreamSocket();
  ffmpegQuality = quality;
  ffmpegMonitor = monitor;
  ffmpegEncoder = encoder;
  resetAu();
  auTs = 0;
  console.log(`[screen] 启动 ffmpeg: monitor=${monitor} encoder=${encoder} quality=${quality}`);
  ffmpeg = spawn(bin, ffmpegArgs(monitor, quality, encoder), {
    stdio: ["ignore", "pipe", "pipe"],
    windowsHide: true,
  });
  ffmpeg.stdout?.on("data", (d: Buffer) => feedStdout(d));
  stderrTail.length = 0;
  ffmpeg.stderr?.on("data", (d: Buffer) => {
    for (const line of d.toString().split(/\r?\n/)) {
      const msg = line.trim();
      if (!msg) continue;
      console.log(`[screen][ffmpeg] ${msg}`);
      stderrTail.push(msg);
      if (stderrTail.length > STDERR_TAIL_MAX * 2) stderrTail.splice(0, stderrTail.length - STDERR_TAIL_MAX);
    }
  });
  ffmpeg.on("error", (e) => {
    lastError = `ffmpeg 进程启动失败: ${e.message}`;
    lastErrKind = "reason";
  });
  ffmpeg.on("close", (code) => {
    console.log(`[screen] ffmpeg 退出 code=${code}`);
    ffmpeg = null;
    ffmpegEncoder = "";
    if (wantCmd.want) {
      // 崩溃重启：命令电平还在（手机还要求投屏）→ 下个轮询周期自动重启。
      // stderr 尾行并入上报——手机端「异常退出」要能看到原因（如
      // Cannot load nvcuda.dll），否则轮询环里反复炸一次就-blind 重启一次。
      const tail = stderrTailText();
      restartFailures++;
      lastError =
        restartFailures >= MAX_CONSECUTIVE_ERRORS
          ? tripMessage(restartFailures)
          : `ffmpeg 异常退出（code=${code}）${tail ? `：${tail}` : ""}，正在自动重启`;
      lastErrKind = "reason";
    }
  });
}

function stopFfmpeg(): void {
  if (ffmpeg) {
    console.log("[screen] 停止 ffmpeg");
    // 尾帧收尾（flushAu）要在 kill 之前：stdout 数据泵可能还有缓冲帧
    try { flushAu(emitFrame); } catch { /* 流已坏，放弃尾帧 */ }
    try { ffmpeg.kill(); } catch { /* already dead */ }
    ffmpeg = null;
  }
  ffmpegEncoder = "";
  if (streamSocket && !streamSocket.destroyed) streamSocket.end();
}

// ---- 推流通道（本地回环 HTTP POST 长体 → 后端 /screen/stream）----
//
// 用 http.request 的 ClientRequest 作可写端（chunked 编码自动处理）：帧到达率
// 30fps，每 AU 一发裸 TCP 最简单，但后端端点统一是 HTTP（复用路由/门/日志），
// chunked POST 一次开路后当纯可写流用，开销可忽略。

async function startStreamSocket(): Promise<void> {
  if (streamSocket && !streamSocket.destroyed) return;
  await new Promise<void>((resolve) => {
    const [host, port] = BACKEND.split(":");
    const req = httpRequest({
      host, port: Number(port),
      path: `/screen/stream${token() ? `?token=${encodeURIComponent(token())}` : ""}`,
      method: "POST",
      headers: { "Content-Type": "application/octet-stream", "Transfer-Encoding": "chunked" },
    }, (res) => {
      res.resume();
    });
    req.on("error", (e) => { console.log(`[screen] 推流连接失败: ${e.message}`); resolve(); });
    // 流式请求对象本身就是可写端——直接当 streamSocket 用（Node http
    // ClientRequest 实现 Writable；chunked 编码自动处理）。
    streamSocket = req as unknown as Socket;
    resolve();
  });
}

// ---- 显示器枚举 ----
//
// 不引原生模块：ffmpeg 的 ddagrab 拿 output_idx（DirectX 输出索引），与系统
// 显示器序号一致；枚举交给 ffmpeg 一次性子进程（-filter_complex ddagrab=inputs=N
// 探测太重）——直接用 Electron 的 screen API（主进程可用）拿显示器几何 +
// 按坐标系映射 output_idx。Windows 的 DXGI output 枚举顺序与 Electron screen
// API 的顺序**不保证一致**，Stage 1 用 screen.getAllDisplays() 的顺序近似
// （单屏/双屏绝大多数场景一致），并在 UI 上给出名称/分辨率让用户核对。

function enumerateMonitors(): ShellState["monitors"] {
  const primary = screen.getPrimaryDisplay();
  return screen.getAllDisplays().map((d, i) => ({
    id: i,
    name: (d.id === primary.id ? "主显示器" : `显示器 ${i + 1}`) + ` (${d.size.width}×${d.size.height})`,
    primary: d.id === primary.id,
    width: d.size.width,
    height: d.size.height,
    attached: true,
  }));
}

// ---- 轮询循环 ----

// ffmpeg 重启连败熔断（判定逻辑在 screen-restart.ts 纯函数，node:test 直测）：
// 连续崩溃重启达 3 次后停止重启并上报错误——否则轮询环里每秒炸一次重启一次
// （编码器坏/驱动崩时无限循环），手机端只见「正在自动重启」闪烁。用户重新
// start 时计数清零重试。
let restartFailures = 0;

export function startScreenPolling(): void {
  if (pollTimer) return;
  const tick = async (): Promise<void> => {
    let nextDelay = POLL_ONLINE_MS;
    try {
      const state: ShellState = {
        running: !!ffmpeg,
        monitor: ffmpegMonitor,
        encoder: ffmpegEncoder,
        width: 0,
        height: 0,
        error: lastError,
        err_kind: lastErrKind,
        monitors: enumerateMonitors(),
      };
      monitorsCache = state.monitors;
      const res = await fetch(`http://${BACKEND}/screen/ctl${token() ? `?token=${encodeURIComponent(token())}` : ""}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(state),
        signal: AbortSignal.timeout(3000),
      });
      if (res.ok) {
        const body = (await res.json()) as { cmd: Command; poll_ms: number; idle_poll_ms: number };
        wantCmd = body.cmd;
        const interval = wantCmd.want ? (body.poll_ms ?? POLL_ONLINE_MS) : (body.idle_poll_ms ?? POLL_OFFLINE_MS);
        nextDelay = interval;
        await reconcile();
      } else {
        nextDelay = POLL_OFFLINE_MS;
      }
    } catch {
      nextDelay = POLL_OFFLINE_MS; // 后端不可达：退避，不折腾 ffmpeg
    }
    pollTimer = setTimeout(tick, nextDelay);
  };
  void tick();
}

export function stopScreenPolling(): void {
  if (pollTimer) clearTimeout(pollTimer);
  pollTimer = null;
  stopFfmpeg();
}

/** reconcile：把命令电平对齐到进程现实（start/stop/参数变化重启）。 */
async function reconcile(): Promise<void> {
  if (wantCmd.want) {
    const q = wantCmd.quality || "medium";
    if (!ffmpeg) {
      if (restartTripped(wantCmd.want, restartFailures)) {
        // 熔断：不再自动重启，错误保持上报（err_kind=reason），手机端可见；
        // 用户重新 start（want 电平重置）时 reconcile 的 stop 分支清零重试。
        console.log(`[screen] 连续 ${restartFailures} 次启动失败，熔断——等待用户重新开始投屏`);
        updateCapturingHint(true);
        return;
      }
      lastError = "";
      lastErrKind = undefined;
      await startFfmpeg(wantCmd.monitor, q);
      restartFailures = afterRestartAttempt(!!ffmpeg, restartFailures);
      updateCapturingHint(true);
    } else if (wantCmd.monitor !== ffmpegMonitor || q !== ffmpegQuality) {
      // 参数变化：重启（Stop 再 Start，下一轮询周期生效）
      stopFfmpeg();
      await startFfmpeg(wantCmd.monitor, q);
    }
  } else if (ffmpeg) {
    stopFfmpeg();
    clearRestartFailures();
    lastError = "";
    lastErrKind = undefined;
    updateCapturingHint(false);
  } else {
    // want=false 且未在跑：计数清零（下次 start 是全新尝试）。
    // 熔断态的 lastError 保留上报——手机端要能看到「为什么停了」。
    clearRestartFailures();
  }
}

/** 用户显式重新开始投屏时清零连败计数（命令 want 电平 false→true 边沿）。 */
function clearRestartFailures(): void {
  restartFailures = 0;
}

// ---- 桌面轻提示（隐私指示）----

let hintShown = false;

/** 投屏运行指示：托盘 tooltip + 一次性气泡；主窗口状态经 console 转发由
 *  渲染层 Toast 自行展示（Stage 1 最小化：托盘是必显的，窗口提示是增强）。 */
function updateCapturingHint(on: boolean): void {
  if (on === hintShown) return;
  hintShown = on;
  const tray: Tray | null = getTray();
  if (!tray) return;
  try {
    tray.setToolTip(on ? "Lxcode — 正在向手机投屏" : "Lxcode");
    if (on) {
      tray.displayBalloon({
        title: "Lxcode 正在投屏",
        content: "你的手机正在查看此电脑的屏幕。投屏由手机端控制，停止后自动结束。",
      });
    }
  } catch {
    // 气泡/tooltip 失败不影响捕获本身
  }
}

// ---- 导出（诊断/测试）----

export function screenStateSnapshot(): ShellState {
  return {
    running: !!ffmpeg,
    monitor: ffmpegMonitor,
    encoder: ffmpegEncoder,
    width: 0,
    height: 0,
    error: lastError,
    err_kind: lastErrKind,
    monitors: monitorsCache,
  };
}
