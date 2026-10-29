# 精简版 ffmpeg（投屏专用）构建与分发

> 2026-10-27 起投屏第一阶段（`shell/src/screen.ts`）不再要求用户装 full 版 ffmpeg：
> 壳随包带一个只含投屏所需能力的 `ffmpeg.exe`（~5.4MB 实测），装了 full 版的用户仍走 PATH 里的优先级不变（见下「查找顺序」）。

## 一、能力面（对齐 screen.ts 的实际用法）

| 能力 | 用途 | 开关 |
|---|---|---|
| `lavfi` indev | `-f lavfi -i ddagrab=...` | `--enable-indev=lavfi` |
| `ddagrab` 滤镜 | DXGI Desktop Duplication 抓屏（Windows only），输出 D3D11 硬帧 | `--enable-filter=ddagrab` |
| `h264_nvenc` | NVIDIA 硬编 | `--enable-nvenc --enable-ffnvcodec --enable-encoder=h264_nvenc` |
| `h264_qsv` | Intel 硬编 | `--enable-libvpl --enable-encoder=h264_qsv` |
| `h264_amf` | AMD 硬编 | `--enable-amf --enable-encoder=h264_amf` |
| `libx264` | 软编兜底 | `--enable-gpl --enable-libx264 --enable-encoder=libx264` |
| `hwdownload/format/scale` 滤镜 + swscale | 软编回落时把 D3D11 硬帧拉回系统内存 | `--enable-filter=hwdownload,format,scale --enable-swscale` |
| `h264` muxer | `-f h264` 裸 Annex B 流 | `--enable-muxer=h264` |
| `file,pipe` 协议 | `pipe:1` 推流 | `--enable-protocol=file,pipe` |

**全部关闭**：`--disable-everything --disable-autodetect`（解码器、音频、网络、其他 muxer/demuxer、doc、debug）。

## 二、硬编三件套的依赖（都能编进精简版，理由）

三个硬编编码器都是「编译期只链头文件 / 运行时动态加载显卡驱动 DLL」模式：

- **nvenc**：ffnvcodec 头文件包（`mingw-w64-x86_64-ffnvcodec-headers`，MSYS2 官方源），
  编译期不需要 NVIDIA SDK，运行时 `LoadLibrary` NVIDIA 驱动自带的 nvenc API DLL；
- **amf**：AMF SDK 纯头文件包（`mingw-w64-x86_64-amf-headers`），运行时加载 AMD 驱动自带的 `amfrt64.dll`；
- **qsv**：oneVPL dispatcher（`mingw-w64-x86_64-libvpl`，静态库 `libvpl.a` 链入），
  运行时经 dispatcher 找 Intel 驱动的 `libmfx64-gen.dll`。注意 ffmpeg 8.0 的 QSV 依赖
  键是 `libmfx`，`--enable-libvpl` 会以 `vpl.pc` 满足它（两者互斥，新版走 vpl）。

没有对应显卡的机器上，对应编码器只是 `ffmpeg -encoders` 探测不到（screen.ts 的
回落链自动跳过），**不影响 exe 运行**——这正是要把三家都编进去的原因。

libx264 是唯一需要链接的库（`--enable-gpl` 必须，x264 是 GPL）。

## 三、构建步骤

### 1. 工具链（一次性）

在 MSYS2 MinGW64 shell 里：

```bash
pacman -S --needed mingw-w64-x86_64-gcc mingw-w64-x86_64-nasm \
                   mingw-w64-x86_64-libx264 mingw-w64-x86_64-libvpl \
                   mingw-w64-x86_64-ffnvcodec-headers mingw-w64-x86_64-amf-headers
```

### 2. 编译

仓库根（本机验证过的等效命令，见下「验证状态」）：

```bash
./scripts/build-ffmpeg.sh                 # 源码自动下到 temp/ffmpeg-src
./scripts/build-ffmpeg.sh <源码目录> <输出文件>   # 显式指定
```

脚本内部等价于：

```bash
cd <ffmpeg 源码目录>
export PATH=/mingw64/bin:/usr/bin:$PATH
export PKG_CONFIG_PATH=/mingw64/lib/pkgconfig
./configure \
  --disable-everything --disable-autodetect --disable-doc --disable-debug \
  --disable-network --disable-iconv --disable-lzma --disable-zlib --disable-bzlib \
  --disable-sdl2 --disable-outdevs \
  --enable-gpl --enable-static --disable-shared --enable-ffmpeg \
  --enable-indev=lavfi \
  --enable-filter=ddagrab,hwdownload,format,scale \
  --enable-encoder=h264_nvenc,h264_qsv,h264_amf,libx264 \
  --enable-muxer=h264 --enable-protocol=file,pipe \
  --enable-nvenc --enable-ffnvcodec --enable-amf --enable-libvpl --enable-libx264 \
  --enable-d3d11va --enable-swscale
make -j$(nproc) ffmpeg
strip ffmpeg.exe
```

`--enable-d3d11va`：ddagrab 依赖（`ddagrab_filter_deps="d3d11va ..."`），MSYS2 工具链
自带 D3D11 头，无需额外包。

### 3. 预期产物

单文件静态 `ffmpeg.exe`，**实测 5.4MB**（full 版 gyan build ~90MB+，精简主要省在
解码器/滤镜库/网络协议）。二进制静态链接（`--enable-static --disable-shared`），
用户机器零 DLL 依赖，仅运行时动态加载显卡驱动 DLL（不存在则该编码器探测不到、
走回落）。

### 4. 三个非显而易见的坑（2026-10-27 实测踩过）

1. **`wrapped_avframe` 解码器必须编入**：lavfi indev 把滤镜输出流标成
   `AV_CODEC_ID_WRAPPED_AVFRAME`，ffmpeg CLI 打开输出前要做输入流解码侧的 codec
   查找——缺它直接报 `Decoding requested, but no decoder found for: wrapped_avframe`。
2. **x264/vpl 必须真静态链**：MSYS2 的 `lib/` 下 `.a`（静态）与 `.dll.a`（导入库）
   并存，且 x264.pc 默认 Cflags 带 `-DX264_API_IMPORTS`（dllimport 语义）——
   默认参数链出来的 exe 运行时要找 `libx264-165.dll`。脚本用
   `--pkg-config-flags=--static` + `-UX264_API_IMPORTS` + 把 `.a` 拷进独立
   `-L` 目录压过 `.dll.a`。
3. **libvpl 静态库是 C++ 编的**：链接要补 `-lstdc++`（`--extra-libs=-lstdc++`）；
   另外 MSYS2 的 `windres` 在部分环境 popen 管道失败，make 时传
   `WINDRES='windres --use-temp-file'`。

## 四、验证状态（2026-10-27 本机实测，全绿）

- 工具链：MSYS2 mingw64 gcc 16.2.0 / nasm 2.16 / ffmpeg 8.0（GitHub release tarball）；
- configure 退出码 0，`config_components.h` 确认
  `CONFIG_{H264_NVENC,H264_QSV,H264_AMF,LIBX264}_ENCODER=1`、
  `CONFIG_DDAGRAB_FILTER/H264_MUXER/LAVFI_INDEV/HWDOWNLOAD_FILTER=1`；
- 编译产物 `ffmpeg.exe` **5.4MB**（stripped，静态）；
- `-encoders` 列出 `libx264 / h264_amf / h264_nvenc / h264_qsv` 四编码器全在列；
- **冒烟（真实抓屏 90 帧，1920×1080@30）**：
  - `ddagrab → h264_amf`（-usage ultralowlatency -rc cqp）：✅ 345KB Annex B 裸流，
    NAL 起始码 + SPS 头确认，30fps 速度 0.999x；
  - `ddagrab + hwdownload,format=bgra → libx264`（screen.ts 同款参数）：✅ 254KB，
    SPS `00 00 00 01 67 ...` 确认；
  - `h264_nvenc`：本机无 NVIDIA 卡，`Cannot load nvcuda.dll`（预期内的回落路径，
    screen.ts 探测会跳过）；`h264_qsv` 同理（无 Intel 卡 + 其不接受 `-preset p1`）。
    两路在本机验证不了编译后行为，但编码器编入、探测逻辑（`-encoders` 文本匹配）
    已验证。
- 注意：qsv/amf 编码器**不认** NVENC 风格的 `-preset p1 -tune ull -rc constqp`
  参数（`Undefined constant or missing '(' in 'p1'`）——screen.ts 当前对回落链的
  所有硬编都发 NVENC 参数，真机回落到 amf/qsv 时会参数解析失败（本次实测 amd 上
  需要 `-usage ultralowlatency -quality speed -rc cqp -qp_i 22 -qp_p 22`）。
  **screen.ts 已按编码器分支发参（2026-10-27 修复，见 `shell/src/screen.ts` 的
  `ffmpegArgs`）**。（第一阶段里 amf/qsv 只是理论回落路径，多数用户
  是 NVIDIA 或走 x264，影响面小，但已记录待修）。修复后各分支码控参数
  （清晰度档 → qp 26/22/18 落到 X）：

  | 编码器 | 码控参数 |
  |---|---|
  | h264_nvenc | `-preset p1 -tune ull -rc constqp -qp X` |
  | h264_amf | `-usage ultralowlatency -rc cqp -qp_i X -qp_p X`（本机 AMD 冒烟通过） |
  | h264_qsv | `-preset veryfast -low_power 0 -async_depth 1 -global_quality X`（CQP；qsv 顶层无 -rc/-qp_i 选项；**本机无 Intel 卡未真机验证**） |
  | libx264 | `-preset veryfast -tune zerolatency -qp X` + repeat-headers |

  公共段（ddagrab 输入、-bf 0、-g 60、-f h264）四分支一致。

## 五、分发方案（已实现）

**随壳打包（extraResources），不首跑下载**。理由：

- 更新机制（`shell/src/updater.ts`）就是现成的全量分发通道（`update-<version>.zip` +
  manifest 逐文件校验），精简 ffmpeg 只有十几 MB，进 zip 的体积增量可接受；
- 首跑下载要新增下载/校验/失败重试链路，且首跑环境（SCM 服务 + 壳）比安装时复杂；
- ffmpeg 基本不随版本迭代，缓存命中后 differentialPackage 关闭（`differentialPackage:false`），
  不会拖慢后续更新包。

实现：

- `scripts/build-ffmpeg.sh` 产物落 `temp/ffmpeg/ffmpeg.exe`（缺省输出目录），
  `scripts/build.mjs` 打包时自动拷进 `shell/resources/bin/`（与 lxcode 后端同一
  extraResources 目录，`shell/package.json` 的 `extraResources` 整目录拷贝，无需改；
  没有预编译产物则跳过并打警告，壳回落 PATH 查找，功能不劣化）；
- 更新 zip 的 `UPDATE_FILES` 追加 `resources/bin/ffmpeg.exe`（存在才打进去）。

### screen.ts 查找顺序（已改）

```
LXCODE_FFMPEG 环境变量 → bundled（resources/bin/ffmpeg.exe）→ PATH
```

bundled 路径经 `process.resourcesPath` 解析（开发模式无 resources 目录时自动跳过）。
