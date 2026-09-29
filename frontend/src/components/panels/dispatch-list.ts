// 「子 Agent 执行」列表的纯函数（**不 import React**——面板组件与 node:test 共用同一份
// 判定，测试不必拉起渲染层）。
//
// 为什么只认 kind === "dispatch"：时间线上能表达「子 Agent 在干活」的块只有它一种。
// 判定必须落在**块的 kind** 上，不能靠字段嗅探（"这个块带不带 task 字段"）：派发的任务
// 描述与后台任务卡、以及 agent_dispatch 那条工具调用行的内容同源，按字段筛会把同一件
// 事在面板里列两遍——而工具调用行**不携带子 Agent 的状态与用量**（running/done 与
// usageTokens 都在 dispatch 块上），混进来只会得到一行没有状态、点了也没有卡可跳的空壳。
// 这个仓库已经为同一类放宽吃过亏（tests/notices.test.mjs：按"带 text"筛会把提示条列进
// 「我发过的消息」）。
import type { ThreadBlock } from "../../shared/blocks";

export interface DispatchItem {
  /** 块的唯一序号——跳转锚点（Thread 给每张卡挂 data-uid，按它扩窗 + 滚 + 高亮）。 */
  uid: number;
  /** Agent 名字（用 agentColor 上色——与时间线卡片同一个视觉语言）。 */
  agentName: string;
  /** Agent 身份色。老后端 / 更早落库的历史可能没带——没带就交给 CSS 的中性色。 */
  agentColor?: string;
  /** 下发的任务描述（**不截断**：展示宽度是渲染层的事，纯函数不预设侧栏有多宽）。 */
  task: string;
  status: "running" | "done";
  /** 结束但失败（子 Agent 报错）——与 status 正交：面板要能一眼分开 running/done/失败三态。 */
  isError: boolean;
  /** 子 Agent 用量（轮末才有）。 */
  usageTokens?: number;
  /** 子会话 id（子 Agent 是独立会话：自己的历史/压缩，可续跑）。 */
  sessionId?: string;
}

/** 块列表 → 子 Agent 执行列表：**只取 dispatch 块**（kind === "dispatch"）。
 *
 *  顺序保持时间线原序（面板是按时间读的），uid 直接用块自己的 uid（跳转锚点）。 */
export function dispatchItems(blocks: ThreadBlock[]): DispatchItem[] {
  const items: DispatchItem[] = [];
  for (const block of blocks) {
    if (block.kind !== "dispatch") continue;
    items.push({
      uid: block.uid,
      agentName: block.agentName,
      agentColor: block.agentColor,
      task: block.task,
      // 状态归一到两态：残缺事件/老后端可能给出别的字符串，除 done 之外一律按运行中呈现
      // ——宁可显示"执行中"（用户知道还要等），也不要显示一个空白状态。
      status: block.status === "done" ? "done" : "running",
      // 缺席按 false 收窄成布尔：面板按它选色，undefined 会让 data-state 落空。
      isError: block.isError === true,
      usageTokens: block.usageTokens,
      sessionId: block.sessionId,
    });
  }
  return items;
}
