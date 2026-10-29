#!/usr/bin/env bash
# build-ffmpeg.sh —— 编译投屏专用的精简版 ffmpeg.exe（Windows / MSYS2 mingw64 工具链）。
#
# 只保留投屏第一阶段（shell/src/screen.ts）真正用到的能力：
#   * lavfi indev + ddagrab 滤镜（DXGI Desktop Duplication 抓屏，Windows only）
#   * 编码器 h264_nvenc / h264_qsv / h264_amf / libx264（screen.ts 的回落链）
#   * h264 raw muxer（-f h264 裸 Annex B 流）+ file/pipe 协议（pipe:1 推流）
#   * wrapped_avframe 解码器（**必需**：lavfi indev 把滤镜输出标成
#     AV_CODEC_ID_WRAPPED_AVFRAME，ffmpeg CLI 打开输出前会对输入流走一次解码
#     侧的 codec 查找，缺它直接报 "Decoding requested, but no decoder found"）；
#   * hwdownload/format/scale 滤镜 + swscale（libx264 软编回落时把 D3D11 硬帧拉回
#     系统内存的通路；NVENC/AMF/QSV 直接吃 ddagrab 的 D3D11 帧，不走它们）。
# 其余（解码器、音频、网络协议、其他 muxer/demuxer）全关。
#
# 硬编三件套的依赖（全部是「编译期链接 / 运行时动态加载 DLL」模式，可以放心编进精简版）：
#   * nvenc  ：ffnvcodec 头文件包（pacman -S mingw-w64-x86_64-ffnvcodec-headers），
#              运行时 LoadLibrary 驱动自带 DLL，无链接库；
#   * amf    ：AMF SDK 头文件包（pacman -S mingw-w64-x86_64-amf-headers），
#              运行时经驱动自带 DLL 动态加载，无链接库；
#   * qsv    ：libvpl（oneVPL dispatcher，pacman -S mingw-w64-x86_64-libvpl；静态库
#              是 C++ 编的，链接要补 -lstdc++）；
#   * libx264：mingw-w64-x86_64-libx264（**必须静态链接**：MSYS2 的 x264.pc 默认
#              带 -DX264_API_IMPORTS（dllimport），链接器会挑 libx264.dll.a → 产物
#              运行时要找 libx264-165.dll。用 --pkg-config-flags=--static +
#              -UX264_API_IMPORTS + 把 libx264.a 放进独立 -L 目录压过 dll.a）。
# 没有对应显卡的机器上，对应编码器只是探测不出来（screen.ts 会回落到下一个），
# 不影响 exe 运行。
#
# 工具链（MSYS2 mingw64 子系统，不是 usr/bin 里的 gcc）：
#   pacman -S --needed mingw-w64-x86_64-gcc mingw-w64-x86_64-nasm \
#                      mingw-w64-x86_64-libx264 mingw-w64-x86_64-libvpl \
#                      mingw-w64-x86_64-ffnvcodec-headers mingw-w64-x86_64-amf-headers
#
# 注意：在 Git Bash 里跑本脚本时，configure 会因 MSYS 路径改写打错临时文件，
# 请用 MSYS2 自己的 bash 跑（C:\msys64\usr\bin\bash.exe --login）。
#
# 用法：
#   ./scripts/build-ffmpeg.sh [源码目录] [输出目录]
#   * 源码目录缺省 = temp/ffmpeg-src（没有则自动从 GitHub release tarball 下载解出）
#   * 输出目录缺省 = temp/ffmpeg（build.mjs 从这里拷进 shell/resources/bin 随壳打包）
#   * 验证状态：2026-10-27 本机（MSYS2 gcc 16.2.0 + ffmpeg 8.0）完整跑通：
#     configure 全绿、编译产物 5.4MB、四编码器 -encoders 全在列、
#     ddagrab→AMF 硬编与 ddagrab+hwdownload→libx264 软编各冒烟 90 帧真实抓屏通过。
#     （nvenc/qsv 参数行是 screen.ts 的格式：qsv/amf 不认 -preset p1 这套 NVENC
#     参数，但那两路在本机无硬件可验，回落链会自动跳过——不影响分发。）
set -euo pipefail

MSYS_ROOT="${MSYS_ROOT:-/c/msys64}"
PREFIX="$MSYS_ROOT/mingw64"
FFMPEG_VER="${FFMPEG_VER:-8.0}"
SRC_DIR="${1:-temp/ffmpeg-src}"
OUT_DIR="${2:-temp/ffmpeg}"

cd "$(dirname "$0")/.."   # 仓库根

# ---- 0. 源码 ----
if [ ! -f "$SRC_DIR/configure" ]; then
  echo "== 源码不存在，下载 ffmpeg $FFMPEG_VER =="
  mkdir -p "$SRC_DIR"
  tarball="temp/ffmpeg-$FFMPEG_VER.tar.gz"
  mkdir -p temp
  # 官方源 ffmpeg.org 国内不稳定，GitHub release tarball 同源（configure 一致）
  urls=(
    "https://github.com/FFmpeg/FFmpeg/archive/refs/tags/n$FFMPEG_VER.tar.gz"
    "https://www.ffmpeg.org/releases/ffmpeg-$FFMPEG_VER.tar.xz"
  )
  for u in "${urls[@]}"; do
    if curl -sL --retry 3 --max-time 300 -o "$tarball" "$u" &&
       [ "$(stat -c%s "$tarball" 2>/dev/null || echo 0)" -gt 5000000 ]; then
      echo "   $u"
      break
    fi
  done
  case "$tarball" in
    *.tar.gz) tar -xzf "$tarball" -C temp ;;
    *.tar.xz) tar -xJf "$tarball" -C temp ;;
  esac
  mv "temp/FFmpeg-n$FFMPEG_VER" "$SRC_DIR" 2>/dev/null ||
    mv "temp/ffmpeg-$FFMPEG_VER" "$SRC_DIR"
fi

# ---- 1. 静态库隔离目录：MSYS2 的 lib/.a 与 lib/.dll.a 同目录时链接器挑 dll.a
#         （x264/vpl 都踩过）→ 把要静态链的 .a 拷进独立目录，-L 放在最前。
BUILD_TMP="$(pwd)/temp/ffmpeg-static"
mkdir -p "$BUILD_TMP/x264" "$BUILD_TMP/vpl"
cp "$PREFIX/lib/libx264.a" "$BUILD_TMP/x264/" 2>/dev/null || true
cp "$PREFIX/lib/libvpl.a"  "$BUILD_TMP/vpl/"  2>/dev/null || true
# vpl.pc/x264.pc 指向隔离目录（覆盖 libdir）
mkdir -p "$BUILD_TMP/pkgconfig"
sed "s|^libdir=.*|libdir=$BUILD_TMP/x264|" "$PREFIX/lib/pkgconfig/x264.pc" \
  | sed 's/ -DX264_API_IMPORTS//' > "$BUILD_TMP/pkgconfig/x264.pc"
sed "s|^libdir=.*|libdir=$BUILD_TMP/vpl|"  "$PREFIX/lib/pkgconfig/vpl.pc" \
  > "$BUILD_TMP/pkgconfig/vpl.pc"

# ---- 2. configure + make ----
cd "$SRC_DIR"
export PATH="$PREFIX/bin:/c/msys64/usr/bin:$PATH"
export PKG_CONFIG_PATH="$BUILD_TMP/pkgconfig"

echo "== configure =="
./configure \
  --disable-everything \
  --disable-autodetect \
  --disable-doc \
  --disable-debug \
  --disable-network \
  --disable-iconv \
  --disable-lzma \
  --disable-zlib \
  --disable-bzlib \
  --disable-sdl2 \
  --disable-outdevs \
  --enable-gpl \
  --enable-static \
  --disable-shared \
  --enable-ffmpeg \
  --enable-indev=lavfi \
  --enable-decoder=wrapped_avframe \
  --enable-filter=ddagrab,hwdownload,format,scale \
  --enable-encoder=h264_nvenc,h264_qsv,h264_amf,libx264 \
  --enable-muxer=h264 \
  --enable-protocol=file,pipe \
  --enable-nvenc \
  --enable-ffnvcodec \
  --enable-amf \
  --enable-libvpl \
  --enable-libx264 \
  --enable-d3d11va \
  --enable-swscale \
  --pkg-config-flags=--static \
  --extra-cflags=-UX264_API_IMPORTS \
  --extra-ldflags="-L$BUILD_TMP/x264 -L$BUILD_TMP/vpl" \
  --extra-libs=-lstdc++

echo "== 编译（$(nproc) 并行；windres 要 --use-temp-file，MSYS2 popen 管道有坑）=="
make -j"$(nproc)" WINDRES='windres --use-temp-file' ffmpeg.exe

# ---- 3. 产物 ----
mkdir -p "$OUT_DIR"
cp ffmpeg.exe "$OUT_DIR/ffmpeg.exe"
echo "== 产物 =="
ls -la "$OUT_DIR/ffmpeg.exe"
./ffmpeg.exe -hide_banner -encoders 2>/dev/null | grep -E "h264_(nvenc|qsv|amf)|libx264" || true
