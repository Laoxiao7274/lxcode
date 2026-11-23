// build.mjs 打包校验闸（resources/bin 必须含 ffmpeg.exe + 三个 MinGW 运行时
// DLL）里判定逻辑的契约测试（纯函数在 ffmpeg-runtime.mjs——build.mjs 是
// import 即执行的脚本，测试不能直接 import 它）。
//
// 背景（0.3.62 发版实撞）：bundled ffmpeg.exe 动态依赖三个 MinGW 运行时
// DLL，不在打包产物里 → 普通用户机器 STATUS_DLL_NOT_FOUND 秒退、投屏全挂，
// 开发者机器（装了 MSYS2）测不出来。校验闸漏判 = 把坏包发出去（用户可感知
// 的功能损坏）；误判 = fail 一次正常构建。正反两向都要钉。
//
// 运行: node --test scripts/ffmpeg-runtime.test.mjs
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { FFMPEG_RUNTIME_DLLS, missingFfmpegRuntimeDlls } from './ffmpeg-runtime.mjs';

test('清单固定为三个 MinGW 运行时 DLL（与 ffmpeg.exe 同目录分发的那三个）', () => {
  assert.deepEqual(FFMPEG_RUNTIME_DLLS.sort(), ['libgcc_s_seh-1.dll', 'libstdc++-6.dll', 'libwinpthread-1.dll']);
});

test('目录齐全：missing 为空（不 fail build）', () => {
  const lister = () => ['lxcode.exe', 'ffmpeg.exe', 'libwinpthread-1.dll', 'libstdc++-6.dll', 'libgcc_s_seh-1.dll'];
  assert.deepEqual(missingFfmpegRuntimeDlls(lister, 'bin'), []);
});

test('缺一个 DLL：报缺项（0.3.62 事故形态，校验闸必须拦下）', () => {
  const lister = () => ['ffmpeg.exe', 'libstdc++-6.dll', 'libgcc_s_seh-1.dll'];
  assert.deepEqual(missingFfmpegRuntimeDlls(lister, 'bin'), ['libwinpthread-1.dll']);
});

test('全缺（目录不存在/为空）：报全部缺项', () => {
  assert.deepEqual(missingFfmpegRuntimeDlls(() => [], 'bin'), FFMPEG_RUNTIME_DLLS);
  assert.deepEqual(missingFfmpegRuntimeDlls(() => { throw new Error('ENOENT'); }, 'bin'), FFMPEG_RUNTIME_DLLS);
});

test('大小写不敏感（NTFS 不区分大小写，realdir 与产物大小写漂移不误判）', () => {
  const lister = () => ['FFMPEG.EXE', 'LibWinPthread-1.DLL', 'LIBSTDC++-6.dll', 'libGCC_s_SEH-1.DLL'];
  assert.deepEqual(missingFfmpegRuntimeDlls(lister, 'bin'), []);
});
