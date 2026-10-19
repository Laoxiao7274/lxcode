// 设置页提供商卡的展开状态（问题 3）：**默认全部收缩**，用户点开才展开。
//
// 为什么不再默认展开：提供商一多（一家一个卡、每卡几十个模型），设置页打开就是
// 一面墙；用户来找的通常是「某一家的某一个模型」，先看名单再点进去更省事。
//
// 状态存在 localStorage（键固定，会话内/重启都记得住）；读不到/写失败都当
// 「没有记忆」——**默认收缩**。`forceExpand` 是「新建/编辑后自动展开对应项」的
// 入口：它压过记忆（用户刚建完就该看到自己的成果），且不写回存储——
// 下次进设置页仍按用户自己的展开习惯来。
const KEY = "lxcode.settings.providerExpanded";

export function readExpanded(): Record<string, boolean> {
  try {
    const raw = localStorage.getItem(KEY);
    if (!raw) return {};
    const parsed: unknown = JSON.parse(raw);
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return {};
    const out: Record<string, boolean> = {};
    for (const [k, v] of Object.entries(parsed as Record<string, unknown>)) {
      if (typeof v === "boolean") out[k] = v;
    }
    return out;
  } catch {
    // 隐私模式 / 存储被清 / 坏 JSON：都退回「没有记忆」= 全部收缩
    return {};
  }
}

export function writeExpanded(id: string, expanded: boolean): void {
  try {
    const all = readExpanded();
    all[id] = expanded;
    localStorage.setItem(KEY, JSON.stringify(all));
  } catch {
    // 存不下就只是记不住，不影响展开/收起本身
  }
}

/** 「新建/编辑后自动展开对应项」：把该 id 记成展开。
 *  与用户手动展开同一条存储——用户下次进设置页看到的是他离开时的样子，
 *  刚建好的那一家也还在开着（不需要一次性的强制展开通道）。 */
export function rememberExpand(id: string): void {
  writeExpanded(id, true);
}

/** 单个提供商是否展开：没有记忆 = 收缩（默认收起是这条规则的唯一默认值）。 */
export function isExpanded(id: string, all: Record<string, boolean> = readExpanded()): boolean {
  return all[id] === true;
}
