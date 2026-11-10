// 侧栏 UI 状态的持久化（第一步四件套之 4）：选中项目（workspace filter）。
//
// 为什么抽独立模块：App 持有 filter（use-workspace-nav.ts），启动恢复与每次
// 变更落盘是它的两处小动作；读写收口在一处（键、容错、回落值单点定义），
// 与 provider-expanded.ts / app-mode.ts 的 localStorage 记忆层同构。
//
// 容错口径（同先例）：localStorage 不可用（隐私模式/存储被清/坏值）=「没有
// 记忆」，回落 LOOSE（未分组）——范围恒有选中，LOOSE 就是启动默认，不编造。
// 写失败只是记不住，不影响切换本身。
//
// 注意：恢复值不在这里校验「项目还存在」——启动时项目列表尚未从后端回来
// （projects 为空），无法校验；不存在项目的回落由 Sidebar 既有 effect 收口
// （当前范围的项目被移除 → setFilter(LOOSE)），两条路径共用同一个判定。

const KEY = "lxcode.ui.lastProject";

/** 读上次选中的项目范围（"" = 未分组也算一次有效记忆）。 */
export function readLastProject(): string {
  try {
    return localStorage.getItem(KEY) ?? "";
  } catch {
    return ""; // 存储不可用 = 没有记忆 = 启动默认（未分组）
  }
}

/** 记下当前选中的项目范围（"" = 未分组——也持久化）。 */
export function writeLastProject(workspace: string): void {
  try {
    localStorage.setItem(KEY, workspace);
  } catch {
    // 存不下就只是记不住
  }
}
