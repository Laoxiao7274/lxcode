// 「回到这里」+「同时还原文件」的二次确认文案（**纯函数**，node:test 直测）。
//
// 为什么要有这一层（docs/session-tree.md §G #6 / §H #1，2026-12 用户拍板）：
// `bash`（以及 MCP / 自定义工具）改的文件**回滚不了**——不确认的话，用户会以为
// 「回到这里」把工作区也还原干净了，实际上一半没还原。文案必须**带具体数字**，
// 不能只说「部分改动无法还原」（用户没法判断要不要继续）。
//
// 三段骨架是固定文案（只填数字），不许各处自己拼：
//   ① 将把对话回到第 N 轮之前，并还原 X 个文件（含 Y 个子 Agent 的改动，涉及 Z 个子会话）
//   ② 另有 M 处改动无法还原（bash / MCP / 自定义工具）——这些文件会保持现状
//   ③ 子会话的对话历史不会回滚，它们仍可继续
import type { FileConflict } from "../../shared/types";

/** 确认框要用的事实（来自树节点 / 轮次分组；**缺席 = 未知，不编 0**）。 */
export interface RewindPlanFacts {
  /** 目标轮次（1 起；缺省 = 不知道第几轮，文案改成"回到这里之前"）。 */
  turn?: number;
  /** 这一段里**可还原**的文件改动数（含子会话）。 */
  files: number;
  /** 其中来自子会话的改动数（files 的子集）。 */
  childFiles?: number;
  /** 这一段里**无法还原**的写次数（bash / MCP / 自定义工具，含子会话里的 bash）。 */
  external: number;
  /** 涉及的子会话数。**只有后端回滚结果才知道**（树骨架里没有这个信息）——
   *  确认前通常是未知，那时文案不提这一句，而不是编一个数。 */
  childSessions?: number;
}

/** 三段文案（数组 = 三个段落，渲染层逐段画）。
 *
 *  数字全部来自事实：`files` 为 0 时第①段改成"不还原文件"（用户打开了开关但这一段
 *  没有文件改动——如实说，别让用户以为会动文件）；`external` 为 0 时**整段省略**
 *  （没有不可还原的东西就不该吓人）。 */
export function rewindConfirmLines(facts: RewindPlanFacts): string[] {
  const files = Math.max(0, facts.files);
  const childFiles = Math.max(0, facts.childFiles ?? 0);
  const external = Math.max(0, facts.external);
  const childSessions = Math.max(0, facts.childSessions ?? 0);
  const where = facts.turn && facts.turn > 0 ? `第 ${facts.turn} 轮之前` : "这里之前";

  const parts: string[] = [];
  if (files > 0) {
    // 子 Agent 的改动是 files 的子集，只有 >0 才提（"含 0 个子 Agent" 是废话）
    const childPart = childFiles > 0
      ? `（含 ${childFiles} 个子 Agent 的改动${childSessions > 0 ? `，涉及 ${childSessions} 个子会话` : ""}）`
      : "";
    parts.push(`将把对话回到${where}，并还原 ${files} 个文件${childPart}。`);
  } else {
    parts.push(`将把对话回到${where}（这一段没有可还原的文件改动）。`);
  }
  if (external > 0) {
    parts.push(
      `另有 ${external} 处改动无法还原（bash / MCP / 自定义工具）——这些文件会保持现状。` +
      `需要一并回退的话，请先用 git 处理。`,
    );
  }
  parts.push("子会话的对话历史不会回滚，它们仍可继续。");
  return parts;
}

/** 冲突清单 → 展示行（文件路径 + 原因 + 是谁改的）。
 *
 *  冲突是 fail-closed 的：整批没执行、leaf 也没动——所以文案要说清"什么都没改"，
 *  否则用户会以为文件已经被覆盖了一半。 */
export function conflictLines(conflicts: FileConflict[]): { path: string; reason: string; session?: string }[] {
  return conflicts.map((c) => ({
    path: c.path,
    // 后端给了人话原因（"磁盘内容与快照期望不符（可能被手动修改或 bash 改过）"）；
    // 缺席时给一句如实的中性说明，不猜具体原因。
    reason: c.reason || "磁盘内容与快照期望不符",
    ...(c.session_id ? { session: c.session_id } : {}),
  }));
}

/** 冲突弹窗的标题/导语（失败原因说清「什么都没改」）。 */
export function conflictHeadline(conflicts: FileConflict[]): string {
  return `有 ${conflicts.length} 个文件与快照不符，整批未执行（对话与文件都没有改动）`;
}

/** 文件还原结果的摘要行（成功路径：还原了几个 / 删了几个 / 跳过几个）。
 *  全 0 时返回空数组（没有文件被动过就什么都不说——不显示一排 0）。 */
export function restoreSummary(files?: {
  restored?: unknown[]; deleted?: unknown[]; skipped?: unknown[]; conflicts?: unknown[]; child_sessions?: string[];
}): string[] {
  if (!files) return [];
  const out: string[] = [];
  const restored = files.restored?.length ?? 0;
  const deleted = files.deleted?.length ?? 0;
  const skipped = files.skipped?.length ?? 0;
  const childSessions = files.child_sessions?.length ?? 0;
  if (restored > 0) out.push(`还原 ${restored} 个文件`);
  if (deleted > 0) out.push(`删除 ${deleted} 个（回滚到当时还不存在）`);
  if (skipped > 0) out.push(`${skipped} 个文件当时没抓快照，未还原`);
  if (childSessions > 0) out.push(`涉及 ${childSessions} 个子 Agent 的改动（它们的对话未回滚）`);
  return out;
}
