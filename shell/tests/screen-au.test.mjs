// screen-au.test.mjs —— 壳侧投屏 AU 切帧/帧头封装的单测（screen-au.ts 纯函数，
// 与生产零漂移：screen.ts 直接 import 同一份实现）。
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  feedAnnexB, resetAu, flushAu, hasIdr, buildFrame, parseFrame,
  FRAME_HEADER_SIZE, FRAME_VERSION, FRAME_FLAG_KEYFRAME,
} from "../src/screen-au.ts";

function sc3() { return Buffer.from([0, 0, 1]); }
function sc4() { return Buffer.from([0, 0, 0, 1]); }
/** NAL 头字节：type 5 = IDR，type 1 = 非 IDR slice，7/8 = SPS/PPS。 */
function nal(type) { return Buffer.from([0x60 | type]); }

test("AU 切帧：两帧粘连可切开（尾帧经 flushAu 收尾），载荷逐字节无损", () => {
  const au1 = Buffer.concat([sc4(), nal(1), Buffer.from([0xaa, 0xbb])]);
  const au2 = Buffer.concat([sc3(), nal(1), Buffer.from([0xcc])]);
  const frames = [];
  resetAu();
  feedAnnexB(Buffer.concat([au1, au2]), (f) => frames.push(f));
  assert.equal(frames.length, 1, "AU2 的边界要等下一个 start code，此刻只出 AU1");
  assert.ok(flushAu((f) => frames.push(f)), "flushAu 应收尾尾帧");
  assert.equal(frames.length, 2);
  const f1 = parseFrame(frames[0]);
  const f2 = parseFrame(frames[1]);
  assert.ok(f1 && f2);
  assert.deepEqual([...f1.au], [...au1]);
  assert.deepEqual([...f2.au], [...au2]);
});

test("AU 切帧：半帧缓冲（AU 跨多次 data 到达）不误切", () => {
  const au1 = Buffer.concat([sc4(), nal(1), Buffer.from([1, 2, 3])]);
  const au2 = Buffer.concat([sc3(), nal(1), Buffer.from([9])]);
  const frames = [];
  resetAu();
  feedAnnexB(au1.subarray(0, 3), (f) => frames.push(f)); // 只有 start code
  assert.equal(frames.length, 0, "无下一帧边界不产出");
  feedAnnexB(au1.subarray(3), (f) => frames.push(f)); // AU1 完整，但仍无边界
  assert.equal(frames.length, 0, "AU 完整但下一个 start code 未到不产出");
  feedAnnexB(au2, (f) => frames.push(f));
  assert.equal(frames.length, 1, "AU2 仍要等下一边界/flush");
  flushAu((f) => frames.push(f));
  assert.equal(frames.length, 2);
  assert.deepEqual([...parseFrame(frames[0]).au], [...au1]);
  assert.deepEqual([...parseFrame(frames[1]).au], [...au2]);
});

test("IDR 判定：nal_unit_type 5 为关键帧，type 1 不是", () => {
  const idr = Buffer.concat([sc4(), Buffer.from([0x65]), Buffer.from([1])]); // 0x65 & 0x1f = 5
  const non = Buffer.concat([sc4(), Buffer.from([0x41]), Buffer.from([1])]); // 0x41 & 0x1f = 1
  assert.equal(hasIdr(idr), true);
  assert.equal(hasIdr(non), false);
});

test("SPS/PPS + IDR 同 AU：关键帧标记置位（MediaCodec csd 来源）", () => {
  const au = Buffer.concat([sc4(), Buffer.from([0x67]), Buffer.from([2, 3]),
    sc4(), Buffer.from([0x68]), Buffer.from([4]),
    sc4(), Buffer.from([0x65]), Buffer.from([5])]);
  assert.equal(hasIdr(au), true);
  resetAu();
  const frames = [];
  feedAnnexB(Buffer.concat([au, Buffer.concat([sc3(), Buffer.from([0x41]), Buffer.from([6])])]), (f) => frames.push(f));
  assert.equal(frames.length, 1, "SPS/PPS/IDR 同 AU 只发一帧");
  const f = parseFrame(frames[0]);
  assert.ok(f);
  assert.equal((f.flags & FRAME_FLAG_KEYFRAME) !== 0, true, "SPS/PPS+IDR 帧应带关键帧标记");
  assert.deepEqual([...f.au], [...au]);
});

test("帧头形状：版本/长度/flags 逐字段对齐 Go 侧 FrameHeader", () => {
  const au = Buffer.from([1, 2, 3, 4]);
  const frame = buildFrame(au, 123456, FRAME_FLAG_KEYFRAME);
  assert.equal(frame.length, FRAME_HEADER_SIZE + au.length);
  assert.equal(frame[0], FRAME_VERSION);
  assert.equal(frame[1], FRAME_FLAG_KEYFRAME);
  const len = (frame[2] << 16) | (frame[3] << 8) | frame[4];
  assert.equal(len, au.length);
  let ts = 0;
  for (let i = 5; i < 13; i++) ts = ts * 256 + frame[i];
  assert.equal(ts, 123456);
});

test("非 IDR 帧 flags 为 0", () => {
  resetAu();
  const frames = [];
  const au = Buffer.concat([sc3(), Buffer.from([0x41]), Buffer.from([7])]);
  feedAnnexB(Buffer.concat([au, sc3(), Buffer.from([0x41]), Buffer.from([8])]), (f) => frames.push(f));
  assert.equal(frames.length, 1);
  const f = parseFrame(frames[0]);
  assert.equal(f.flags & FRAME_FLAG_KEYFRAME, 0);
});

test("resetAu 清缓冲：跨会话残尾不串帧", () => {
  resetAu();
  feedAnnexB(sc4(), () => {}); // 留半帧
  resetAu();
  const frames = [];
  const au1 = Buffer.concat([sc3(), Buffer.from([0x41]), Buffer.from([7])]);
  const au2 = Buffer.concat([sc3(), Buffer.from([0x41]), Buffer.from([8])]);
  feedAnnexB(Buffer.concat([au1, au2]), (f) => frames.push(f));
  flushAu((f) => frames.push(f));
  assert.equal(frames.length, 2);
  assert.deepEqual([...parseFrame(frames[0]).au], [...au1]);
});

test("杂散前导字节被丢弃（流首对齐到 start code）", () => {
  resetAu();
  const frames = [];
  const au1 = Buffer.concat([sc4(), Buffer.from([0x41]), Buffer.from([7])]);
  const au2 = Buffer.concat([sc3(), Buffer.from([0x41]), Buffer.from([8])]);
  feedAnnexB(Buffer.concat([Buffer.from([0xde, 0xad]), au1, au2]), (f) => frames.push(f));
  assert.equal(frames.length, 1);
  assert.deepEqual([...parseFrame(frames[0]).au], [...au1]);
});
