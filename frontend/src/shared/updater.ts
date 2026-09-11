// updater.ts —— 更新服务预留（接缝设计）。
// 方案 A：tauri-plugin-updater（官方——需要签名密钥 + 更新服务器）
// 方案 B：自建检查更新（GET /update/check → 版本比对 → 下载安装）
// 当前只做接缝：isUpdateAvailable() + 下载安装（Tauri 模式才生效）。

const UPDATE_CHECK_URL = "https://update.example.com/api/check"; // 替换为真实更新服务器
const CURRENT_VERSION = "0.1.0";

export interface UpdateInfo {
  available: boolean;
  version: string;
  notes?: string;
  downloadUrl?: string;
}

/** 检查更新（远程版本比对）。 */
export async function checkForUpdate(): Promise<UpdateInfo> {
  try {
    const res = await fetch(`${UPDATE_CHECK_URL}?v=${CURRENT_VERSION}`, {
      signal: AbortSignal.timeout(5000),
    });
    if (!res.ok) return { available: false, version: CURRENT_VERSION };
    const data = await res.json();
    return {
      available: data.version > CURRENT_VERSION,
      version: data.version ?? CURRENT_VERSION,
      notes: data.notes,
      downloadUrl: data.downloadUrl,
    };
  } catch {
    // 网络不通 / 更新服务器未部署——静默
    return { available: false, version: CURRENT_VERSION };
  }
}

/** 下载并安装更新（Tauri 模式：打开下载页或调用 tauri updater）。 */
export async function downloadAndInstall(info: UpdateInfo): Promise<void> {
  if (!info.downloadUrl) return;
  const isTauri = typeof window !== "undefined" && "__TAURI_INTERNALS__" in window;
  if (isTauri) {
    // TODO: tauri-plugin-updater 接入（需要签名密钥）
    // await invoke("plugin:updater|download_and_install", { ... })
    window.open(info.downloadUrl, "_blank");
  } else {
    window.open(info.downloadUrl, "_blank");
  }
}
