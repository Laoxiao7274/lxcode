// 点击侧栏项目行 →「过滤到该项目 + 当前标签页切到该项目最上面的会话」。
//
// 为什么抽纯函数：这是**交互语义**（判定逻辑），不是渲染细节——判定与渲染分开，
// node 单测不需要 DOM 就能逐条钉住（同 dispatch-primary.ts 的做法）。
//
// 口径（三处事实源对齐）：
// - 会话顺序 = 侧栏列表顺序 = 后端 session.list 的口径（store.List：updated_at
//   DESC、归档沉底）。前端**不重排**——侧栏第一条就是这里的第一条，两处数字
//   永远一致；前端重排的后果是「切到的会话不在列表顶上」，用户看不出来为什么。
// - 「未分组」走同一函数（workspace=""），行为一致（用户拍板：恒有范围）。
// - 过滤语义恒有选中（点项目行切换且不可取消），所以目标 workspace 已知——
//   目标列表在调用方就是过滤后的 `list`。

import type { SessionMeta } from "./types";

/** 「未分组」过滤目标（无归属会话的家——不依赖真实项目 id）。
 *  原先定义在 Sidebar（App 启动默认范围用它）；项目行交互语义的判定搬进本文件后
 *  实体跟着搬（LOOSE 是判定的输入之一），Sidebar re-export 保持既有 import 面不变。 */
export const LOOSE = "";

/**
 * 计算点击项目后应切入的会话 id。
 *
 * @param list 当前范围的会话列表（调用方已按 workspace 过滤——侧栏展示的那份）
 * @param currentId 当前打开的会话 id
 * @returns 应切入的会话 id；空串 = 不切换（项目无会话——侧栏显示空态，当前会话
 *          保持，如实）；null = 目标就是当前会话，无操作（连 setFilter 都不动）。
 */
export function projectEntryTarget(list: readonly SessionMeta[], currentId: string): string | null {
  // 边界 1：项目没有会话 → 不切换（保持当前会话），侧栏如实显示空态。
  // `list[0]?.id ?? ""`：第一条不存在 → 空串。
  const first = list[0]?.id ?? "";
  if (!first) return "";
  // 边界 2：最上面的会话就是当前已打开的 → 无操作（不重触发 resume——
  // resume 不是免费的：真链路走 session.resume + chat.history 两个往返）。
  if (first === currentId) return null;
  return first;
}

/**
 * 点击项目行的完整动作（纯判定 + 副作用按回调注入，不 import React）：
 * 过滤 + 回聊天 + 切到该项目第一条会话。三个回调身份稳定（useCallback），
 * 供 Sidebar 的项目行/未分组行共用（含键盘 Enter 路径）。
 *
 * 幂等性（边界 4，双击/快速连点）：目标已打开或项目无会话时**零副作用**
 * （连 setFilter 都不触发——目标 workspace 已经是它）。
 */
export function openProject(args: {
  /** 项目 id（"" = 未分组）。 */
  workspace: string;
  /** 该项目（过滤后）的会话列表——侧栏展示的那份，顺序即列表顶到底。 */
  list: readonly SessionMeta[];
  currentId: string;
  setFilter: (f: string) => void;
  resumeSession: (id: string) => void | Promise<void>;
  /** 回到聊天工作区标签（焦点链路的既有收口）。 */
  onOpenChat: () => void;
}): void {
  const { workspace, list, currentId, setFilter, resumeSession, onOpenChat } = args;
  const target = projectEntryTarget(list, currentId);
  if (target === null) return; // 目标就是当前会话：无操作（不重触发 resume）
  setFilter(workspace);
  onOpenChat();
  if (target) void resumeSession(target);
}
