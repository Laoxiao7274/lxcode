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

/** 侧栏分节折叠态的持久化键（2026-12 分层重构）：JSON {"projects":bool,"assistants":bool}
 *  ——值 true = 收起。整键 JSON 而不是每节一个键：后续加节不用再扩键名，读写一处收口。 */
const COLLAPSED_KEY = "lxcode.ui.sidebarSections";

/** 分节折叠态（Sidebar 的「项目」与「助手」两节头可点击收缩/展开）。 */
export interface SidebarSectionsCollapsed {
  projects: boolean;
  assistants: boolean;
}

const DEFAULT_COLLAPSED: SidebarSectionsCollapsed = { projects: false, assistants: false };

function normalizeCollapsed(raw: unknown): SidebarSectionsCollapsed {
  const v = (raw ?? {}) as Partial<Record<keyof SidebarSectionsCollapsed, unknown>>;
  // 逐字段归一：坏值（非布尔）按「展开」处理——不编造「记住收起」。
  return {
    projects: typeof v.projects === "boolean" ? v.projects : false,
    assistants: typeof v.assistants === "boolean" ? v.assistants : false,
  };
}

/** 读分节折叠态（无记忆/坏值 = 两节都展开——展开是默认态，不猜用户收过）。 */
export function readSidebarSections(): SidebarSectionsCollapsed {
  try {
    const raw = localStorage.getItem(COLLAPSED_KEY);
    if (!raw) return DEFAULT_COLLAPSED;
    return normalizeCollapsed(JSON.parse(raw));
  } catch {
    return DEFAULT_COLLAPSED;
  }
}

/** 写分节折叠态（写失败只是记不住，不影响折叠动作本身）。 */
export function writeSidebarSections(state: SidebarSectionsCollapsed): void {
  try {
    localStorage.setItem(COLLAPSED_KEY, JSON.stringify(state));
  } catch {
    // 存不下就只是记不住
  }
}

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
