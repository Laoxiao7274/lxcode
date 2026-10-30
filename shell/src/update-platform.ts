// update-platform.ts —— 更新链路的平台契约（纯函数，无 Electron 依赖，可单测）。
//
// 背景（2026-12 拍板：manifest 按平台分文件）：Windows 与 mac 的更新包内容
// 不同（后端二进制 lxcode.exe vs lxcode；mac 无 ffmpeg/投屏），site-backend
// 因此按平台出两份**同构** manifest（字段 version/url/sha256/size/files 完全
// 同一形状，url 同为相对文件名、按清单所在目录解析）：
//   manifest.json      = Windows（老契约一字不动——存量 Windows 客户端只认它）
//   manifest-mac.json  = mac（与 manifest.json 同目录；发版时两份都上传）
// 客户端按平台拼 URL（updater.ts 的 manifestUrl → platformManifestUrl）：
// Windows 保持默认 URL 不变；darwin 把末段文件名替换为 manifest-mac.json，
// 目录与 LXCODE_UPDATE_URL 的覆盖语义不变。
//
// 平台守卫是「mac 拉 Windows 包」的最后一道闸：mac 客户端若因故拿到 Windows
// 的更新包（清单含 .exe，或缺 darwin 后端 bin/lxcode），apply 阶段会把 exe
// 替换进 .app（bin 路径对不上就是空替换/坏替换）——调研确认这是现存隐患。
// 守卫在 verifyStagedFiles 之后、排程之前执行；Windows 侧对称校验（缺
// lxcode.exe / 混入 darwin 产物同样中止）——两端都宁可更新失败，不可装错
// 平台。update-pending 在排程前会整体重置，守卫失败不留脏暂存。

/** 守卫的输入形状 = ApplyFileEntry 的 target 子集（避免为守卫引入循环依赖）。 */
export interface PlatformGuardEntry {
  /** 安装目录内的目标绝对路径（Windows 反斜杠 / darwin 正斜杠）。 */
  target: string;
}

const norm = (p: string): string => p.replace(/\\/g, "/");
const isExe = (p: string): boolean => norm(p).toLowerCase().endsWith(".exe");

/** manifest URL 按平台：Windows 原样；darwin 把 base 的末段文件名替换为
 *  manifest-mac.json（同目录）。base 解析失败时原样返回（行为退回旧版：
 *  fetch 会以明确的网络/404 错误失败，不在这里把错误变得难懂）。 */
export function platformManifestUrl(base: string, platform: string): string {
  if (platform !== "darwin") return base;
  try {
    const u = new URL(base);
    const dir = u.pathname.slice(0, u.pathname.lastIndexOf("/") + 1);
    u.pathname = `${dir}manifest-mac.json`;
    return u.toString();
  } catch {
    return base;
  }
}

/** 平台守卫：staged 替换清单必须与当前平台匹配，否则抛错中止 applyUpdate。
 *  platform 参数化以便单测；生产调用传 process.platform。
 *  只设 win32/darwin 两道闸（linux 等平台无壳分发，AGENTS.md §2.1——不设闸
 *  保持现状，不假装覆盖不存在的产品形态）。 */
export function assertStagedPlatform(files: readonly PlatformGuardEntry[], platform: string): void {
  if (platform === "darwin") {
    const exe = files.find((f) => isExe(f.target));
    if (exe) {
      throw new Error(
        `更新包与当前平台不匹配（含 Windows 可执行文件 ${norm(exe.target)}）——` +
          `mac 安装不允许替换 .exe，更新已中止（现有安装未受影响）。` +
          `这通常是更新源指向了 Windows 的 manifest（mac 应为同目录的 manifest-mac.json）`,
      );
    }
    const backend = files.find((f) => norm(f.target).endsWith("bin/lxcode"));
    if (!backend) {
      throw new Error(
        "更新包与当前平台不匹配（缺少 mac 后端 resources/bin/lxcode）——" +
          "拒绝把这份清单替换进 .app，更新已中止（现有安装未受影响）",
      );
    }
    return;
  }
  if (platform === "win32") {
    const darwinBackend = files.find((f) => {
      const n = norm(f.target);
      return n.endsWith("bin/lxcode") && !isExe(n);
    });
    if (darwinBackend) {
      throw new Error(
        `更新包与当前平台不匹配（含 mac 后端 ${norm(darwinBackend.target)}）——` +
          "Windows 安装不接受 darwin 产物，更新已中止（现有安装未受影响）",
      );
    }
    const winBackend = files.find((f) => norm(f.target).toLowerCase().endsWith("bin/lxcode.exe"));
    if (!winBackend) {
      throw new Error(
        "更新包与当前平台不匹配（缺少 Windows 后端 resources/bin/lxcode.exe）——" +
          "拒绝排程替换，更新已中止（现有安装未受影响）",
      );
    }
  }
}
