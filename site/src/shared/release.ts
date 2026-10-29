// 发布域模型：**逐字段对齐 scripts/build.mjs 的产物契约**。
//
// 为什么不自己发明一套字段：客户端更新器（frontend/src/shared/update.tsx 注释里的
// 后端化路径）要读的是构建脚本产出的 manifest.json，后台管理页只是这份清单的
// 生产端。两端字段一旦漂移，症状是「后台显示发布成功、客户端永远说已是最新」——
// 所以契约在本文件里单点定义，测试钉住（tests/release.test.mjs）。

/** 发布渠道。stable = 默认更新链；beta = 需在客户端显式切换才收到。 */
export type Channel = "stable" | "beta";

/**
 * 产物类别。三者的差别不是「格式」而是「更新路径」：
 * - installer：NSIS 一键安装包，**唯一能带 Electron/Chromium 升级**的全量包；
 * - portable：win-unpacked 免安装版，全量包，绿色分发用；
 * - update：update-<version>.zip 增量更新包，只含 resources/app.asar 与后端 exe，
 *   由客户端两级替换（后端热替换 / asar 冷替换）。
 */
export type ArtifactKind = "installer" | "portable" | "update";

/** 发布状态：草稿不进更新链；撤回 = 从更新链摘掉但保留历史。 */
export type ReleaseStatus = "draft" | "published" | "revoked";

export interface Artifact {
  kind: ArtifactKind;
  /** 文件名（含扩展名）。update 包的名字必须与 manifest.url 一致。 */
  name: string;
  size: number;
  sha256: string;
}

/** 更新清单：与 shell/release/manifest.json 逐字段一致。 */
export interface Manifest {
  version: string;
  /** 更新包文件名（相对静态目录）。 */
  url: string;
  /** 更新包整体 sha256（客户端下载后校验）。 */
  sha256: string;
  /** 更新包字节数。 */
  size: number;
  /** zip 内路径 → 单文件 sha256（客户端逐文件校验，避免整包重下）。 */
  files: Record<string, string>;
}

export interface Release {
  version: string;
  channel: Channel;
  status: ReleaseStatus;
  /** ISO 8601（UTC）。草稿为 null。 */
  publishedAt: string | null;
  /** 更新提示里的要点（客户端 UpdateToast 直接展示）。 */
  notes: string[];
  artifacts: Artifact[];
  manifest: Manifest;
  /** 低于此版本必须走全量安装包（更新包的替换路径不覆盖旧布局）。 */
  minVersion: string;
  /** 强制更新：客户端不允许跳过。 */
  required: boolean;
  /** Electron/Chromium 版本。与上一版不同时，更新包不可用（渲染层依赖壳的 Chromium）。 */
  electronVersion: string;
  /** 下载计数：后端按产物 GET 累加（真实计数，不是编的）。 */
  downloads: number;
}

/**
 * 更新日志分类。四类与 base.css 的 .tag-feat / .tag-fix / .tag-breaking / .tag-docs
 * 一一对应——分类是数据的一部分，配色是展示的一部分，两边不许各写各的。
 */
export const CHANGELOG_KINDS = ["features", "fixes", "breaking", "docs"] as const;
export type ChangelogKind = (typeof CHANGELOG_KINDS)[number];

/** 一条更新日志（后端 /api/changelog 返回；id 是服务端主键）。 */
export interface ChangelogEntry {
  id: number;
  version: string;
  /** ISO 8601（UTC），与 Release.publishedAt 同口径。 */
  date: string;
  kind: ChangelogKind;
  text: string;
}

/**
 * 产物下载地址：静态目录下的文件名（与 manifest.url 同一口径——
 * url 就是相对发布目录的文件名，不是绝对地址）。
 */
export function artifactUrl(name: string): string {
  return `/releases/${name}`;
}

/** 更新包 zip 内必须存在的路径（= 安装目录相对路径，见 build.mjs 的 UPDATE_FILES）。 */
export const REQUIRED_UPDATE_FILES = ["resources/app.asar", "resources/bin/lxcode.exe"] as const;

/** 版本号：只认三段数字（与 shell/package.json 的 version 同形）。 */
const VERSION_RE = /^\d+\.\d+\.\d+$/;

export function isValidVersion(v: string): boolean {
  return VERSION_RE.test(v.trim());
}

/**
 * 从文件名里解析版本号。发布页用它自动填版本，避免手抄错版本号
 * （版本号写错是发版最贵的一类错：安装包名、二进制烙印、manifest 三方同源，改一处不改另两处就是不一致）。
 */
export function parseVersionFromName(name: string): string | null {
  const m = name.match(/(\d+\.\d+\.\d+)/);
  return m ? m[1] : null;
}

export function updatePackageName(version: string): string {
  return `update-${version}.zip`;
}

/** electron-builder 的 NSIS 产物名（productName 含空格，故这里是 `Lxcode Setup <ver>.exe`）。 */
export function installerName(version: string): string {
  return `Lxcode Setup ${version}.exe`;
}

export function portableName(version: string): string {
  return `Lxcode-${version}-win-x64.zip`;
}

/**
 * 语义化版本比较：a > b 返回正数。只比较三段数字，够用且不引依赖
 * （站点原型不值得为 semver 引一个包；正式化时若要支持 prerelease 再换）。
 */
export function compareVersions(a: string, b: string): number {
  const pa = a.split(".").map((n) => parseInt(n, 10) || 0);
  const pb = b.split(".").map((n) => parseInt(n, 10) || 0);
  for (let i = 0; i < 3; i++) {
    const d = (pa[i] ?? 0) - (pb[i] ?? 0);
    if (d !== 0) return d > 0 ? 1 : -1;
  }
  return 0;
}

/** 按版本号降序（新版本在前）。不修改入参。 */
export function sortByVersionDesc<T extends { version: string }>(list: T[]): T[] {
  return [...list].sort((a, b) => compareVersions(b.version, a.version));
}

/**
 * 字节数人性化（发布清单里的体积一律走它）。
 * 口径与产品端 `frontend/src/shared/connections.tsx` 的 `humanBytes` 一致：
 * 1024 进制、四档单位、≥100 取整、其余一位小数——两处显示同一份产物的大小必须同形。
 */
export function formatBytes(bytes: number): string {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let v = bytes;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  return `${v >= 100 ? Math.round(v) : v.toFixed(1)} ${units[i]}`;
}

/** 某个渠道下、处于「已发布」状态的最新版本；没有则 null。 */
export function latestPublished(list: Release[], channel: Channel): Release | null {
  const pub = list.filter((r) => r.status === "published" && r.channel === channel);
  if (pub.length === 0) return null;
  return sortByVersionDesc(pub)[0];
}

export function findArtifact(release: Release, kind: ArtifactKind): Artifact | null {
  return release.artifacts.find((a) => a.kind === kind) ?? null;
}

export interface PublishCheck {
  level: "pass" | "warn" | "fail";
  /** 校验项名称（后台清单里显示的一行）。 */
  label: string;
  detail: string;
}

/**
 * 发布前校验：后台管理页的「发布前检查清单」由它生成，**与发布按钮的可用性同源**
 * （不做两份判定，否则会出现「清单全绿但按钮灰着」这种自相矛盾的状态）。
 */
export function checkReleaseDraft(
  draft: {
    version: string;
    channel: Channel;
    artifacts: Artifact[];
    electronVersion: string;
    previousElectronVersion: string | null;
    /** 草稿里 manifest 声明的更新包哈希（与更新包实际哈希比对，避免清单指错文件）。 */
    sha256OfUpdate: string;
  },
  existing: Release[],
): PublishCheck[] {
  const out: PublishCheck[] = [];

  if (!isValidVersion(draft.version)) {
    out.push({ level: "fail", label: "版本号格式", detail: "必须形如 0.2.0（三段数字）" });
  } else {
    const dup = existing.find((r) => r.version === draft.version);
    out.push(
      dup
        ? { level: "fail", label: "版本号唯一性", detail: `${draft.version} 已存在（${statusText(dup.status)}），发布前请先撤回或删除` }
        : { level: "pass", label: "版本号唯一性", detail: `${draft.version} 未被占用` },
    );
  }

  const byKind = (k: ArtifactKind) => draft.artifacts.filter((a) => a.kind === k);
  const installer = byKind("installer")[0];
  const portable = byKind("portable")[0];
  const update = byKind("update")[0];

  // 全量安装包是分发的**主路径**：没有它，用户无法首次安装，也拿不到 Electron 升级。
  if (!installer) {
    out.push({ level: "fail", label: "安装包", detail: "缺少 NSIS 安装包：首次安装与 Electron 升级都走它" });
  } else {
    out.push({
      level: installer.name === installerName(draft.version) ? "pass" : "warn",
      label: "安装包命名",
      detail:
        installer.name === installerName(draft.version)
          ? `文件名与版本一致：${installer.name}`
          : `建议命名为 ${installerName(draft.version)}（electron-builder 默认产物名），当前 ${installer.name}`,
    });
  }

  // 更新包：文件名必须与 manifest.url 一致，否则客户端按 url 取不到文件（404 且只有下载时才暴露）
  if (!update) {
    out.push({ level: "fail", label: "更新包", detail: "缺少 update-<version>.zip：客户端增量更新链断掉" });
  } else if (update.name !== updatePackageName(draft.version)) {
    out.push({
      level: "fail",
      label: "更新包命名",
      detail: `必须叫 ${updatePackageName(draft.version)}，清单里的 url 直接指向它`,
    });
  } else if (update.sha256 !== draft.sha256OfUpdate) {
    out.push({ level: "fail", label: "更新包哈希", detail: "manifest.sha256 与更新包实际哈希不一致" });
  } else {
    out.push({ level: "pass", label: "更新包", detail: `${update.name}（${formatBytes(update.size)}，哈希已核对）` });
  }

  if (!portable) {
    out.push({ level: "warn", label: "免安装版", detail: "缺少 win-unpacked 全量包：绿色分发与排障需要它" });
  }

  // Electron 换代时更新包不可用：壳的 Chromium 无法靠 zip 替换，必须重装。
  if (draft.electronVersion !== draft.previousElectronVersion && draft.previousElectronVersion) {
    out.push({
      level: "warn",
      label: "Electron 版本变化",
      detail: `Electron ${draft.previousElectronVersion} → ${draft.electronVersion}：更新包只能换 app.asar 与后端，壳必须发全量安装包`,
    });
  }

  out.push({
    level: out.some((c) => c.level === "fail") ? "warn" : "pass",
    label: "可发布性",
    detail: out.some((c) => c.level === "fail") ? "存在阻断项，发布按钮保持禁用" : "清单齐备，可以发布",
  });

  return out;
}

export function canPublish(checks: PublishCheck[]): boolean {
  return !checks.some((c) => c.level === "fail");
}

function statusText(s: ReleaseStatus): string {
  return s === "published" ? "已发布" : s === "draft" ? "草稿" : "已撤回";
}
