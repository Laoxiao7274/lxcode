// bundled ffmpeg 编码器校验的纯函数（build.mjs 打包时调用，node --test 直测）。
// 独立成模块的原因：build.mjs 是「import 即执行打包流程」的脚本，测试不能
// 直接 import 它——纯函数放这里，build.mjs 与测试各取所需。
//
// 解析 `ffmpeg -encoders` 输出，判断回落链依赖的编码器是否都编译进 exe。
// 行形如「 V....D h264_nvenc  NVIDIA NVENC H.264 encoder (codec h264)」：
// 第二列才是编码器名，按整词匹配避免前缀撞名。
export function missingFfmpegEncoders(encodersOutput, required) {
  const listed = new Set();
  for (const line of encodersOutput.split(/\r?\n/)) {
    const m = line.match(/^\s*[VAFS.]+\S*\s+(\S+)\s/);
    if (m) listed.add(m[1]);
  }
  return required.filter((name) => !listed.has(name));
}
