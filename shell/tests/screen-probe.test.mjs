// screen-probe.test.mjs —— 编码器探测失败缓存的纯逻辑单测（screen-probe.ts，
// 与 screen.ts 的 probeEncoders/startFfmpeg 消费同一份判定，测试与生产零漂移）。
//
// 钉的是 0.3.56 实撞的误报链：探测全失败带 60s TTL 缓存 → TTL 内重试若只
// 返回 [] 不复述原因，reconcile 每轮 start 前清掉的 lastError 不会重建，
// startFfmpeg 的 `if (!lastError)` 兜底就把「驱动/桌面复制起不来」误报成
// 「缺编码器，请重装 ffmpeg」。修复 = 失败原因与 TTL 时间戳一起缓存，
// TTL 命中复述同一真实原因。
import test from "node:test";
import assert from "node:assert/strict";
import {
  PROBE_FAIL_TTL_MS,
  probeFailureKind,
  probeFailureKindOf,
  probeFailMessage,
  probeFailCacheHit,
  encodersListOutcome,
  isDllNotFoundExit,
} from "../src/screen-probe.ts";


test("失败分类：有候选缺编译 = encoders_missing，全在列起不来 = encoders_unavailable", () => {
  assert.equal(probeFailureKind(["h264_nvenc", "h264_amf"]), "encoders_missing");
  assert.equal(probeFailureKind([]), "encoders_unavailable");
});

test("真实原因文案按 kind 分叉：missing 指向重装，unavailable 指向驱动/桌面复制并附输出", () => {
  const missing = probeFailMessage("encoders_missing", "");
  assert.ok(missing.includes("重新安装"), "缺编译的文案必须指向重装 ffmpeg");
  const unavailable = probeFailMessage("encoders_unavailable", "Cannot load nvcuda.dll");
  assert.ok(unavailable.includes("驱动或桌面复制"), "起不来的文案必须指向驱动/桌面复制");
  assert.ok(unavailable.includes("Cannot load nvcuda.dll"), "必须附 ffmpeg 输出摘要——手机端无从排查的教训");
  assert.ok(!missing.includes("驱动"), "两类文案不能互换");
});

test("TTL 命中复述同一真实原因（0.3.56 误报链的钉子）", () => {
  const memory = {
    kind: "encoders_unavailable",
    message: probeFailMessage("encoders_unavailable", "Cannot load nvcuda.dll"),
    at: 1_000_000,
  };
  // reconcile 下一轮 start（清 lastError 后）TTL 内重试：命中 → 返回记忆，
  // 调用方以 memory.message 重建 lastError → 报的还是驱动/桌面复制，不是兜底。
  const hit = probeFailCacheHit(memory, memory.at + PROBE_FAIL_TTL_MS - 1);
  assert.equal(hit, memory, "TTL 内必须命中且原样返回——复述与首次失败同一文案");
  assert.equal(hit.message, memory.message);
  // TTL 过期：不命中 → 调用方继续真实探测（驱动瞬时故障不把投屏钉死）。
  assert.equal(probeFailCacheHit(memory, memory.at + PROBE_FAIL_TTL_MS), null);
  assert.equal(probeFailCacheHit(memory, memory.at + PROBE_FAIL_TTL_MS + 1), null);
});

test("无记忆（首次/成功后）不命中：不编造原因", () => {
  assert.equal(probeFailCacheHit(null, Date.now()), null);
});

test("成功探测清掉失败记忆后，同刻不再复述旧失败", () => {
  // 模拟 probeEncoders 成功分支的清理：memory = null 之后命中判定恒 null。
  const memory = { kind: "encoders_missing", message: "旧失败", at: Date.now() };
  assert.notEqual(probeFailCacheHit(memory, Date.now()), null);
  assert.equal(probeFailCacheHit(null, Date.now()), null, "成功后清缓存——显卡能力恢复不再复述旧失败");
});

// ---- 0.3.61：encoders_missing 误报（模拟器场景「一直存在」）的钉子 ----

test("listEncoders 收口判定：执行失败/超时零输出 = ok:false（不与「真没编码器」混淆）", () => {
  // close（正常退出）恒成功——即使输出为空（老 ffmpeg 可能无候选输出）。
  assert.deepEqual(encodersListOutcome(true, ""), { ok: true, out: "" });
  // spawn error / 超时且零输出：ok=false——曾 resolve 空串被判 encoders_missing。
  assert.equal(encodersListOutcome(false, "").ok, false);
  // 超时但有部分输出：残串按实际内容判定，ok=true。
  const partial = encodersListOutcome(false, " V....D libx264 ......... libx264 H.264");
  assert.equal(partial.ok, true);
  assert.ok(partial.out.includes("libx264"));
});

test("三分类文案分叉：list_unavailable 指 ffmpeg 本体执行失败，不判缺编译产物", () => {
  const lu = probeFailMessage("encoders_list_unavailable", "");
  assert.ok(lu.includes("ffmpeg 本体执行失败"), "第三分类必须说清是 -encoders 都跑不起来");
  assert.ok(lu.includes("重新安装"), "修复路径仍是重装完整版");
  assert.ok(!lu.includes("nvenc/qsv/amf/libx264 都不在编译产物里"), "不能把执行失败说成缺编译产物");
  const missing = probeFailMessage("encoders_missing", "");
  assert.ok(!missing.includes("本体执行失败"), "两类文案不能互换");
});

test("listEncoders 收口判定：非零退出 + 零输出 = ok:false（进程被拉起但失败≠成功）", () => {
  // close 且 code=0：成功。
  assert.equal(encodersListOutcome(true, "", 0).ok, true);
  // 0.3.62 复现路径：进程被拉起、close 正常回调但 code 非 0、stdout 空——
  // 曾判 ok:true → parseEncodersList("") → encoders_missing 误报。
  assert.equal(encodersListOutcome(true, "", 1).ok, false);
  assert.equal(encodersListOutcome(true, "", -1).ok, false);
  assert.equal(encodersListOutcome(true, "", 3221225786).ok, false);
  // 非零退出但有输出：残串按实际内容判定（截断只可能漏、不会多报）。
  assert.equal(encodersListOutcome(true, " V....D libx264 ....", 1).ok, true);
  // 超时/spawn error 语义不变（code 缺省 0 仅在 close 前超时时使用）。
  assert.equal(encodersListOutcome(false, "").ok, false);
  assert.equal(encodersListOutcome(false, "partial", 0).ok, true);
});

test("probeFailureKindOf：全部候选缺失才判 encoders_missing，部分缺失走驱动方向", () => {
  const chain = ["h264_nvenc", "h264_qsv", "h264_amf", "libx264"];
  assert.equal(probeFailureKindOf(chain, chain), "encoders_missing");
  // 部分缺失（如 nvenc 没编进去，其他三个在列）——文案写「部分/点名缺失」，
  // 修复方向是驱动/装机不配套，不是一律重装。
  assert.equal(probeFailureKindOf(["h264_nvenc"], chain), "encoders_unavailable");
  assert.equal(probeFailureKindOf([], chain), "encoders_unavailable");
});

test("部分缺失文案点名缺失候选且不写「都不在编译产物里」", () => {
  const msg = probeFailMessage("encoders_unavailable", "", ["h264_nvenc"]);
  assert.ok(msg.includes("h264_nvenc"), "必须点名缺失候选");
  assert.ok(!msg.includes("都不在编译产物里"), "部分缺失不能说「都不在」");
  assert.ok(msg.includes("驱动"), "方向指向驱动/桌面复制");
  // 全部缺失文案不变（旧契约）：仍写「都不在编译产物里」+ 重装指引。
  const missing = probeFailMessage("encoders_missing", "", ["h264_nvenc", "h264_qsv", "h264_amf", "libx264"]);
  assert.ok(missing.includes("都不在编译产物里"));
  assert.ok(missing.includes("重新安装"));
});

// ---- 0.3.62 P3 排查：真实 -encoders 输出的预筛钉子 ----
// 背景：误报深挖时怀疑 parseEncodersList 正则与真实行格式失配（P3）。
// 实测**未坐实**（下述夹具四候选全命中），钉住它防未来 ffmpeg 行格式变化
// 引入真失配。夹具是装机 ffmpeg 实测原文（含行首空格/标志位差异：
// h264_qsv 无 D 标志、libx264 无 h264_ 前缀）。
// 注：正则构造在 screen.ts:314（import 链含 electron，node:test 直载会炸），
// 夹具复刻同一模板串；screen.ts 有 tsc 类型校验兜底两处不同步。

/** 装机 ffmpeg（0.3.61 bundled，2026-11-20 实测）的 -encoders 输出原文。 */
const REAL_ENCODERS_OUTPUT = [
  "Encoders:",
  " V..... = Video",
  " A..... = Audio",
  " S..... = Subtitle",
  " .F.... = Frame-level multithreading",
  " ..S... = Slice-level multithreading",
  " ...X.. = Codec is experimental",
  " ....B. = Supports draw_horiz_band",
  " .....D = Supports direct rendering method 1",
  " ------",
  " V....D libx264              libx264 H.264 / AVC / MPEG-4 AVC / MPEG-4 part 10 (codec h264)",
  " V....D h264_amf             AMD AMF H.264 Encoder (codec h264)",
  " V....D h264_nvenc           NVIDIA NVENC H.264 encoder (codec h264)",
  " V..... h264_qsv             H.264 / AVC / MPEG-4 AVC / MPEG-4 part 10 (Intel Quick Sync Video acceleration) (codec h264)",
  "",
].join("\n");

test("预筛正则匹配真实 -encoders 输出（P3 排查夹具）：PROBE_CHAIN 四候选全命中", () => {
  const chain = ["h264_nvenc", "h264_qsv", "h264_amf", "libx264"];
  for (const e of chain) {
    // 模板串逐字符同 screen.ts parseEncodersList——改生产正则必须同步这里
    const re = new RegExp(`^\\s*[VAFS.]+[.\\w]*\\s+${e}\\s`, "m");
    assert.ok(re.test(REAL_ENCODERS_OUTPUT), `${e} 必须命中真实输出（含行首空格与标志位）`);
  }
});

// ---- 0.3.63 投屏事故的观测增强：0xC0000135（STATUS_DLL_NOT_FOUND）细分文案 ----
// 背景：bundled ffmpeg.exe 动态依赖 MinGW 运行时 DLL（libwinpthread-1/
// libstdc++-6/libgcc_s_seh-1），不在 ffmpeg.exe 同目录时进程加载器直接
// 0xC0000135 秒退、零输出——旧文案「安装可能损坏或被杀毒拦截」让用户瞎猜。
// 治本在打包链（scripts/build.mjs 随拷三 DLL + 校验闸）；这条钉住文案分叉。

test("isDllNotFoundExit：0xC0000135 有符号/无符号两种形态都认，其余不认", () => {
  assert.equal(isDllNotFoundExit(3221225785), true, "无符号形态（libuv GetExitCodeProcess DWORD 直取）");
  assert.equal(isDllNotFoundExit(-1073741515), true, "有符号形态（int 截断路径）");
  assert.equal(isDllNotFoundExit(3221225786), false, "0xC0000136 不是——不能把相邻 NTSTATUS 误翻译");
  assert.equal(isDllNotFoundExit(-1073741516), false);
  assert.equal(isDllNotFoundExit(1), false);
  assert.equal(isDllNotFoundExit(0), false);
  assert.equal(isDllNotFoundExit(null), false, "spawn error/超时没有退出码，不是 DLL 缺失的证据");
});

test("list_unavailable + 0xC0000135：文案翻译成「缺运行时 DLL/安装不完整」，不再误导去查杀毒", () => {
  const dll = probeFailMessage("encoders_list_unavailable", "", [], 3221225785);
  assert.ok(dll.includes("运行时 DLL"), "必须点名缺的是运行时 DLL");
  assert.ok(dll.includes("libwinpthread-1.dll") && dll.includes("libstdc++-6.dll") && dll.includes("libgcc_s_seh-1.dll"), "必须列出三个 DLL 名——用户/支持能直接核对");
  assert.ok(dll.includes("安装不完整"), "指向安装不完整而非杀毒/损坏");
  assert.ok(dll.includes("重新安装"), "修复路径仍是重装完整版");
  assert.ok(!dll.includes("杀毒"), "0xC0000135 不该再指向杀毒拦截");
  // 有符号形态同款文案。
  assert.ok(probeFailMessage("encoders_list_unavailable", "", [], -1073741515).includes("运行时 DLL"));
  // 其余退出码保持现有分类文案（不连带改变）。
  const other = probeFailMessage("encoders_list_unavailable", "boom", [], 1);
  assert.ok(!other.includes("运行时 DLL"), "非 0xC0000135 退出码走原文案");
  assert.ok(other.includes("杀毒") && other.includes("boom"), "原文案保留输出摘要");
  // null（spawn error/超时）也走原文案。
  const none = probeFailMessage("encoders_list_unavailable", "", [], null);
  assert.ok(!none.includes("运行时 DLL") && none.includes("本体执行失败"));
  // 0xC0000135 细分只作用于 list_unavailable：其他 kind 不受 exitCode 影响。
  assert.ok(probeFailMessage("encoders_missing", "", [], 3221225785).includes("编译产物"));
  assert.ok(probeFailMessage("encoders_unavailable", "", [], 3221225785).includes("驱动"));
});
