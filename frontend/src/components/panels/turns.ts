// 「轮次树」的纯函数（**不 import React**——面板组件与 node:test 共用同一份判定，
// 测试不必拉起渲染层）。
//
// 为什么要有这一层：右栏原先并排两个平铺列表（「已发送消息」+「子 Agent 执行」），
// 子 Agent 列表里看不出某次派发是哪一轮派出去的——用户原话「不方便找到对应的子 Agent」。
// 按轮次分组是唯一能回答这个问题的结构：轮次的边界就是用户消息的边界。
//
// 为什么只认 kind === "user" 开新轮：提示条（kind === "notice"）在**历史里也是真实
// user 角色消息**（后端必须把它当用户回合，模型才会回应它——见 shared/blocks.ts 的
// notice 分支注释），但它不是用户说的话，而且它是**轮内**注入的（internal/agent/repeat.go
// 的重复调用提醒落在轮边界上）——把它当轮次边界会把同一轮劈成两半，子 Agent 归到错误的
// 轮次上。判定必须落在**块的 kind** 上，不能靠角色、也不能靠「有没有 text」猜：这个仓库
// 为同一类放宽吃过两次亏（tests/notices.test.mjs 与 tests/panels.test.mjs 的回归钉子）。
import type { ThreadBlock } from "../../shared/blocks";
import { outlineLabel } from "./outline";
import { dispatchItems, type DispatchItem } from "./dispatch-list";

export interface TurnGroup {
  /** 轮次序号（1 起，按时间顺序）。它是**产出的分组**的序号，不是 user 块的序号：
   *  第一条 user 之前的那一组也占一个号——序号跳号（1、3、4）会让人以为丢了一轮。 */
  turn: number;
  /** 该轮用户消息的块 uid（跳转锚点）。第一条 user 之前的块自成一组时没有锚点 → null
   *  （那一组只有组头没有正文，面板按 null 渲染成不可点的「会话开始」行）。 */
  uid: number | null;
  /** 用户消息原文（**不截断**：展示宽度是渲染层的事，纯函数不预设侧栏有多宽）。
   *  uid 为 null 的那一组没有用户消息，这里是空串。 */
  text: string;
  /** 单行摘要（复用 outlineLabel——同一件事不要有第二份实现：折叠换行 + 按码点截断）。 */
  label: string;
  /** 这一轮派出的子 Agent（复用 dispatchItems 的产出）。没有派发的轮次是空数组，
   *  不是 null——面板按 .length 判定，空数组让它少一层空值判断。 */
  agents: DispatchItem[];
}

/** 块列表 → 按轮次分组的树：一条 user 块开一轮，其后的 dispatch 块都归入**它前面
 *  最近的那条 user 块**所在轮。
 *
 *  归入判定的唯一事实源仍是 dispatchItems（**不许有第二份「什么算一次派发」的判定**）：
 *  工具行（agent_dispatch 那条调用）与带 task 字段的后台任务卡都长得很像派发，判定散成
 *  两份就会一处混进来、另一处不混——那正是 tests/subagent-panel.test.mjs 钉住的坑。
 *  所以这里不逐块挑 dispatch，而是把块按轮次**切段**，每段交给 dispatchItems。 */
export function turnGroups(blocks: ThreadBlock[]): TurnGroup[] {
  // 轮次边界：只认 user 块（见文件头注释）。记下标而不是就地建组——dispatch 要按
  // 「它前面最近的那条 user」归属，用区间切片才能保证不重不漏。
  const bounds: { uid: number; text: string; start: number }[] = [];
  blocks.forEach((block, index) => {
    if (block.kind !== "user") return;
    bounds.push({ uid: block.uid, text: block.text, start: index });
  });

  const groups: TurnGroup[] = [];
  // 第一条 user 之前就有派发：自成一组（没有可跳的锚点 → uid: null）。**只在它真有
  // 派发时才产出**——否则面板上会顶出一个没有正文、点了也没反应的组头（空组的噪音）。
  const headEnd = bounds.length > 0 ? bounds[0].start : blocks.length;
  const before = dispatchItems(blocks.slice(0, headEnd));
  if (before.length > 0) {
    groups.push({ turn: 1, uid: null, text: "", label: "", agents: before });
  }

  bounds.forEach((bound, i) => {
    // 本轮的区间是 [这条 user, 下一条 user)——dispatch 归入它前面最近的那条 user，
    // 靠的就是「切片只取本轮区间」这一条，不必再逐块记住「当前轮是谁」。
    const end = i + 1 < bounds.length ? bounds[i + 1].start : blocks.length;
    // 正文收窄成字符串再交给 outlineLabel：历史来自 SQLite，残缺记录是既成事实，而
    // outlineLabel 直接 .replace 会抛——纯函数抛异常的下场是整块面板白屏（outline.ts 不在
    // 本次改动范围，所以在入口这里收窄）。空正文照旧产出组头（用户仍要能点它跳转）。
    const text = typeof bound.text === "string" ? bound.text : "";
    groups.push({
      turn: groups.length + 1,
      uid: bound.uid,
      text,
      label: outlineLabel(text),
      agents: dispatchItems(blocks.slice(bound.start, end)),
    });
  });
  return groups;
}
