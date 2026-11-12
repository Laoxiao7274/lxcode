// build.mjs 打包校验（bundled ffmpeg 必须编进回落链四编码器）里解析逻辑的
// 契约测试（纯函数在 ffmpeg-encoders.mjs——build.mjs 是 import 即执行的脚本，
// 测试不能直接 import 它）。误判「缺编码器」会 fail 一次正常构建，漏判则把
// 四路全败的 ffmpeg 发出去（无独显机器投屏直接不可用，且报误导文案）。
// 所以正反两向都要钉。
//
// 运行: node --test scripts/ffmpeg-encoders.test.mjs
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { missingFfmpegEncoders } from './ffmpeg-encoders.mjs';

const REQUIRED = ['h264_nvenc', 'h264_qsv', 'h264_amf', 'libx264'];

// 真实 -encoders 输出的采样（截取，含行头 flag 列与说明列）
const sample = `ffmpeg version N-111618-gc9523b6a8f Copyright (c) 2000-2023 the FFmpeg developers
 V.....D a64multi             Multicolor charset for Commodore 64 (codec a64_multi)
 V....D libx264               libx264 H.264 / AVC / MPEG-4 AVC / MPEG-4 part 10 (codec h264)
 V....D libx264rgb            libx264 RGB (codec h264)
 V.....D h264_amf             AMD AMF H.264 encoder (codec h264)
 V.....D h264_nvenc           NVIDIA NVENC H.264 encoder (codec h264)
 V.....D h264_qsv             Hardware acceleration for H.264 (codec h264)
 A.....D aac                  AAC (Advanced Audio Coding)`;

test('四编码器齐全：missing 为空（不 fail build）', () => {
  assert.deepEqual(missingFfmpegEncoders(sample, REQUIRED), []);
});

test('缺一个软编（精简构建漏 libx264）：报缺项', () => {
  const out = sample.split('\n').filter((l) => !l.includes('libx264 ')).join('\n');
  assert.deepEqual(missingFfmpegEncoders(out, REQUIRED), ['libx264']);
});

test('全缺（输出为空/跑不起来）：报全部缺项', () => {
  assert.deepEqual(missingFfmpegEncoders('', REQUIRED), REQUIRED);
});

test('前缀撞名不误判：h264_nvenc2 在列不代表 h264_nvenc 在列', () => {
  const out = ' V.....D h264_nvenc2      hypothetical (codec h264)';
  assert.deepEqual(missingFfmpegEncoders(out, ['h264_nvenc']), ['h264_nvenc']);
});

test('说明列提到编码器名不算在列（必须出现在名字列）', () => {
  const out = ' V....D libx264            mentions h264_amf in description (codec h264)';
  assert.deepEqual(missingFfmpegEncoders(out, ['h264_amf']), ['h264_amf']);
});
