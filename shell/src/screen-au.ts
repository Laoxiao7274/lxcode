// screen-au.ts —— H.264 Annex B 裸流的 AU（访问单元）切帧与 IDR 判定。
//
// 纯函数模块（零 electron 依赖）：screen.ts（主进程，依赖 electron）与
// tests/screen-au.test.mjs（node --test）共用同一份实现——测试与生产零漂移。
//
// 背景：ffmpeg `-f h264` 输出 Annex B 裸流到 stdout。每个访问单元（AU）以
// start code（00 00 01 / 00 00 00 01）开头，AU 之间**没有长度前缀**——唯一的
// 定界方式是「下一个 AU 的 start code 到达」。SPS/PPS 与首个 IDR 属同一 AU
//（ffmpeg 逐 NAL 写出，类型 7/8 紧跟类型 5），一起发：手机端 MediaCodec 从
// 关键帧 AU 提取 csd-0/csd-1（见安卓 ScreenDecoder）。
//
// 帧头（13 字节，internal/protocol/screen.go FrameHeader）：
//   [0] 版本（=1）  [1] flags（bit0 关键帧 / bit1 流结束）
//   [2..4] 载荷长度（大端 24bit）  [5..12] 时间戳（大端 uint64，毫秒）

export const FRAME_HEADER_SIZE = 13;
export const FRAME_VERSION = 1;
export const FRAME_FLAG_KEYFRAME = 0x01;
export const FRAME_FLAG_STOP = 0x02;

/** auBuf 是跨 data 块的积累缓冲（Annex B 无长度前缀，AU 边界要等下一个
 *  start code 到达才能确定）。 */
let auBuf = Buffer.alloc(0);

/** NAL 单元类型（头字节低 5 位；nal = 含 start code 的完整 NAL）。 */
function nalType(nal: Buffer): number {
  const scLen = startCodeLenAt(nal);
  if (scLen === 0 || nal.length <= scLen) return 0;
  return nal[scLen] & 0x1f;
}

/** 在 b 的 off 起找 Annex B start code（4 字节优先判定，3 字节次之）。
 *  返回索引或 -1。 */
export function findStartCode(b: Buffer, off: number): number {
  for (let i = Math.max(off, 0); i < b.length; i++) {
    if (b[i] !== 0 || b[i + 1] !== 0) continue;
    if (b[i + 2] === 1) return i;
    if (i + 3 < b.length && b[i + 2] === 0 && b[i + 3] === 1) return i;
  }
  return -1;
}

/** AU 里有没有 IDR NAL（nal_unit_type 5）。SPS(7)/PPS(8) 与 IDR 同 AU 是
 *  ffmpeg 输出的常规形态——只要出现类型 5 就算关键帧。 */
export function hasIdr(au: Buffer): boolean {
  let off = 0;
  for (;;) {
    const sc = findStartCode(au, off);
    if (sc < 0) return false;
    const nalHdr = sc + (au[sc + 2] === 1 ? 3 : 4);
    if (nalHdr < au.length && (au[nalHdr] & 0x1f) === 5) return true;
    off = sc + 3;
  }
}

/** 构造一帧（13 字节头 + AU 载荷）。 */
export function buildFrame(au: Buffer, ts: number, flags?: number): Buffer {
  const head = Buffer.alloc(FRAME_HEADER_SIZE);
  head[0] = FRAME_VERSION;
  head[1] = flags ?? (hasIdr(au) ? FRAME_FLAG_KEYFRAME : 0);
  const n = au.length;
  head[2] = (n >> 16) & 0xff;
  head[3] = (n >> 8) & 0xff;
  head[4] = n & 0xff;
  head.writeBigUInt64BE(BigInt(ts) & 0xffffffffffffn, 5);
  return Buffer.concat([head, au]);
}

/** 判定 b 开头是不是 start code，返回其长度（3 或 4；0 = 不是）。 */
function startCodeLenAt(b: Buffer): number {
  if (b.length >= 4 && b[0] === 0 && b[1] === 0 && b[2] === 0 && b[3] === 1) return 4;
  if (b.length >= 3 && b[0] === 0 && b[1] === 0 && b[2] === 1) return 3;
  return 0;
}

/** 把一块 stdout 数据并入缓冲，按 NAL 分组切出完整帧并逐帧回调 emit
 *  （回调收到完整帧 = 13 字节头 + 载荷）。返回发了几帧。
 *
 *  分组语义（H.264 访问单元）：Annex B 里每个 NAL 都有自己的 start code，
 *  而**一个 AU 可以含多个 NAL**（SPS(7)/PPS(8)/SEI(6)/AUD(9) 等前置 NAL +
 *  slice NAL）。纯按 start code 切会把 SPS/PPS 切成独立"帧"，MediaCodec 拿
 *  不到同帧的 csd。所以：逐 NAL 缓冲，遇到 **slice NAL（type 1/5）** 完成
 *  （下一个 start code 到达）即把「累积的前置 NAL + 这个 slice」作为一帧
 *  发出；slice 之后到下一个 slice 之间的 NAL（可能直接是下一 AU 的 SPS）
 *  归下一帧。flags 由组内是否含 type 5 决定（buildFrame 自动判）。
 *
 *  防御：缓冲超过 8MB（正常帧顶多几百 KB）= 坏流，丢弃重来。 */
export function feedAnnexB(chunk: Buffer, emit: (frame: Buffer) => void): number {
  auBuf = auBuf.length === 0 ? Buffer.from(chunk) : Buffer.concat([auBuf, chunk]);
  let sent = 0;
  for (;;) {
    // 定位缓冲首部：第一个 start code（丢弃杂散前导字节）
    const first = findStartCode(auBuf, 0);
    if (first < 0) {
      if (auBuf.length > 8 << 20) auBuf = Buffer.alloc(0);
      return sent;
    }
    if (first > 0) auBuf = Buffer.from(auBuf.subarray(first));
    const scLen = startCodeLenAt(auBuf);
    if (scLen === 0) return sent;
    // 找下一个 NAL 边界
    const next = findStartCode(auBuf, scLen);
    if (next < 0) {
      if (auBuf.length > 8 << 20) auBuf = Buffer.alloc(0);
      return sent; // 当前 NAL 还没收完
    }
    const nal = Buffer.from(auBuf.subarray(0, next)); // 完整 NAL（含 start code）
    auBuf = Buffer.from(auBuf.subarray(next));
    const type = nalType(nal);
    // NAL 分组：SPS/PPS/SEI/AUD 先攒着；slice NAL（1/5）到了就出一帧
    if (type === 1 || type === 5) {
      groupBufs.push(nal);
      emit(buildFrame(Buffer.concat(groupBufs), 0));
      groupBufs = [];
      sent++;
    } else if (type === 7 || type === 8 || type === 6 || type === 9) {
      groupBufs.push(nal);
    }
    // 其余 NAL 类型（暂不参与分组）丢弃
  }
}

/** 当前 AU 的前置 NAL 累积（SPS/PPS/SEI/AUD；slice 到达即随帧发出）。 */
let groupBufs: Buffer[] = [];

/** resetAu 清缓冲：跨会话残尾不串帧。 */
export function resetAu(): void {
  auBuf = Buffer.alloc(0);
  groupBufs = [];
}

/** flushAu：ffmpeg 退出时收尾——缓冲里的尾 NAL 若是 slice 已无法定界
 *  （要等下一个 NAL），把「前置 NAL + 当前 NAL」直接成帧发出（正常流里
 *  尾帧总是 slice，这是安全的收尾）。返回是否发出了一帧。 */
export function flushAu(emit: (frame: Buffer) => void): boolean {
  if (auBuf.length === 0) {
    auBuf = Buffer.alloc(0);
    return false;
  }
  const first = findStartCode(auBuf, 0);
  if (first < 0) {
    auBuf = Buffer.alloc(0);
    groupBufs = [];
    return false;
  }
  if (first > 0) auBuf = Buffer.from(auBuf.subarray(first));
  const scLen = startCodeLenAt(auBuf);
  if (scLen === 0 || auBuf.length <= scLen) {
    auBuf = Buffer.alloc(0);
    return false;
  }
  const nal = Buffer.from(auBuf.subarray(0));
  auBuf = Buffer.alloc(0);
  const type = nalType(nal);
  if (type === 1 || type === 5) {
    groupBufs.push(nal);
    emit(buildFrame(Buffer.concat(groupBufs), 0));
    groupBufs = [];
    return true;
  }
  groupBufs = [];
  return false;
}

/** 从完整帧（头+载荷）拆出头字段（手机端同款解析的 TS 对照实现）。 */
export function parseFrame(frame: Buffer): { flags: number; ts: number; au: Buffer } | null {
  if (frame.length < FRAME_HEADER_SIZE) return null;
  if (frame[0] !== FRAME_VERSION) return null;
  const len = (frame[2] << 16) | (frame[3] << 8) | frame[4];
  const au = frame.subarray(FRAME_HEADER_SIZE, FRAME_HEADER_SIZE + len);
  if (au.length !== len) return null;
  let ts = 0;
  for (let i = 5; i < 13; i++) ts = ts * 256 + frame[i];
  return { flags: frame[1], ts, au };
}
