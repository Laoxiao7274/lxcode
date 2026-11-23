// bundled ffmpeg 运行时依赖（MinGW 运行时 DLL）清单判定与查找的纯函数
// （build.mjs 打包链调用，node --test 直测）。独立成模块的理由同
// ffmpeg-encoders.mjs：build.mjs 是「import 即执行打包流程」的脚本，
// 测试不能直接 import 它。
//
// 背景（0.3.62 发版实撞）：ffmpeg 8.0 用 MSYS2 mingw64 gcc 编译，尽管
// configure 走 --enable-static --disable-shared，链接进来的 C++ 运行时
// （-lstdc++，libvpl 静态库是 C++ 编的）与线程/GCC 支持层仍是动态的——
// exe 的 PE import table 里有 libwinpthread-1.dll / libstdc++-6.dll /
// libgcc_s_seh-1.dll 三个 MinGW 运行时 DLL。开发者机器装了 MSYS2（PATH
// 命中）才跑得起来；普通用户机器 GUI 进程 PATH 无 mingw64 → 进程加载器
// 直接 STATUS_DLL_NOT_FOUND（0xC0000135）秒退 → 投屏在所有非开发机上必挂。
// 治本 = 打包链把这三个 DLL 与 ffmpeg.exe 放进同一目录（Windows 加载器
// 按加载序查 ffmpeg.exe 同目录），入库 scripts/dll/（~2.6MB 一次性成本，
// 发版不依赖构建机装没装 MSYS2）。

/** ffmpeg.exe 运行必需的 MinGW 运行时 DLL（与 ffmpeg.exe 同目录分发）。 */
export const FFMPEG_RUNTIME_DLLS = [
  "libwinpthread-1.dll",
  "libstdc++-6.dll",
  "libgcc_s_seh-1.dll",
];

/** dir（经 dirLister 列目录）里缺哪些必需文件（[] = 齐；大小写不敏感——
 *  NTFS 不区分，打包前查入库目录、打包后查 resources/bin 都用这一份判定。
 *  dirLister 抛错（目录不存在）= 全部视为缺失——build.mjs 的调用方 catch
 *  不到这一层语义差异，fail 方向安全。 */
export function missingFfmpegRuntimeDlls(dirLister, dir) {
  let names;
  try {
    names = dirLister(dir);
  } catch {
    return [...FFMPEG_RUNTIME_DLLS];
  }
  const present = new Set(names.map((n) => String(n).toLowerCase()));
  return FFMPEG_RUNTIME_DLLS.filter((d) => !present.has(d));
}
