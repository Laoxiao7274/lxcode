// 历史快照 → UI 状态：消息序列重建 blocks（回放路径与实时路径必须一致）。

import type { HistorySnapshot } from "./types";
import { type AssistantBlock, type ThreadBlock, type UIState, DISPATCH_TOOL_NAME, nextUid, checkpointBody, placeConfirm } from "./blocks";
import { noticeBody, noticeLabel } from "./notices";

/** dispatch 调用的参数（后端 internal/tools/dispatch.go 的 JSON 形状：
 *  `{agent, task, context?, session?}`）。 */
export interface DispatchArgs {
  /** 目标 Agent 的名单 id（空 = 历史里没有 / 参数坏了）。 */
  agent: string;
  /** 任务描述与验收标准。 */
  task: string;
  /** 续跑的子会话 id（可选）。**不是权威值**——续跑时它可能为空，
   *  真正用的那个 id 在结果的提示行里（见 splitDispatchResult）。 */
  session: string;
}

/** 解析 agent_dispatch 的 arguments（纯函数，不 import React——可直接单测）。
 *
 *  **坏 JSON 不许抛**：历史来自 SQLite，参数被 max_tokens 截断（AGENTS.md §5
 *  坑 12）、老库里的旧形状都是既成事实——加载路径抛异常会让**整个会话打不开**
 *  （用户看到的是"会话坏了"，而不是"这一次调度少了一张卡"）。解析不动就回落
 *  空值：卡照建，只是没有 Agent 名与任务（比整段历史消失诚实得多）。
 *
 *  这里刻意不做保守修复（后端 jsonrepair 那套）：回放是**只读**的，修不了
 *  也不该改历史——修出来的参数是猜的，而猜错的 task 会被当成真的任务展示。 */
export function parseDispatchArgs(raw: string | undefined | null): DispatchArgs {
  const none: DispatchArgs = { agent: "", task: "", session: "" };
  if (!raw) return none;
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return none;
  }
  if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) return none;
  const o = parsed as Record<string, unknown>;
  const str = (k: string) => (typeof o[k] === "string" ? (o[k] as string) : "");
  return { agent: str("agent"), task: str("task"), session: str("session") };
}

/** 子会话 id 提示行（后端 internal/agent/tools.go 在 dispatch 结果末尾追加）：
 *  `\n\n[子会话 id: <id> —— 只在这次**没做完**时填进 session 参数续跑；已经给出结论就别再派]`
 *  它是**给模型看的续跑线索**，不是给用户看的结论——回放时剥掉。
 *  子会话 id 从这里取是**权威**的：续跑时 arguments.session 可能为空，
 *  而这一行永远是本次真正用的那个子会话。 */
const SUB_SESSION_HINT = /\[子会话 id:\s*([^\]\s]+)[^\]]*\]/;

/** 拆分 dispatch 的工具结果：结论正文 + 子会话 id（没有提示行时 id 为空串）。 */
export function splitDispatchResult(raw: string): { body: string; sessionId: string } {
  const text = raw ?? "";
  const m = SUB_SESSION_HINT.exec(text);
  if (!m) return { body: text.replace(/\s+$/, ""), sessionId: "" };
  // 提示行可能不在末尾（后端目前追加在末尾）——按位置摘除而不是"取最后一行"，
  // 摘完清掉尾部空行（提示行前面本来就垫了一个空行）
  const body = (text.slice(0, m.index) + text.slice(m.index + m[0].length)).replace(/\s+$/, "");
  return { body, sessionId: m[1] };
}

/** 回放时**没有配对结果**的调度的说明正文。
 *
 *  重启后历史里只有 tool_call、没有 role=tool 结果 = 那次派发在结束前就中断了
 *  （进程被杀 / 用户取消 / 后端崩）。**不许假装 running**：卡片会永远转着
 *  spinner 让用户干等一个不会来的结果。所以定格成 done + isError，并把
 *  "为什么没有结论"写在卡里——用户看到的是一句实话。 */
export const DISPATCH_INTERRUPTED = "这次派发在结束前中断了——历史里没有结果记录（重启前它没跑完）。子 Agent 的执行过程在它自己的会话里，本会话看不到结论。";

/** 有配对结果、但结果正文里没有结论（例如只回了一行子会话 id 提示）时的占位。
 *  与后端"（子 Agent 没有产出文本结论）"同款口径——是事实陈述，不是编的结论。 */
export const DISPATCH_NO_TEXT = "（子 Agent 没有产出文本结论）";

/** 消息序列 → blocks（**唯一一份映射**）。
 *
 *  两条路径共用它：① 回放路径 reduceHistory（刷新/切会话重建主时间线）；
 *  ② 子会话历史的渲染（DispatchCard 展开时懒加载子会话快照，App 的
 *  handleLoadChild 调它）。**不许写第二份映射**——分叉的代价是同一段历史在
 *  两条路径上换一张脸（上一轮那个 bug 就是回放路径给 agent_dispatch 建了工具行，
 *  刷新之后子 Agent 卡退化成一行光秃秃的 `agent_dispatch`）。
 *  tests/child-history.test.mjs ① 逐字段钉住"两份产出完全一致"。
 *
 *  配对规则：assistant 的 tool_calls 先开块；后续 role=tool 的消息
 *  按 tool_call_id 回填对应块的 result（服务端的存储顺序保证可达）。
 *  **调度调用（agent_dispatch）建的是 dispatch 卡，不是工具行**——与实时路径
 *  （reduce.ts 的 toolCall + dispatchStart）形态一致。
 *  压缩检查点（checkpoints 下标）渲染成 compacted 块——子会话也是会话，
 *  它自己的压缩检查点走的就是这一条。 */
export function historyBlocks(h: HistorySnapshot): ThreadBlock[] {
  const blocks: ThreadBlock[] = [];
  const checkpoints = new Set(h.checkpoints ?? []);
  // 缺 messages 键的快照（老后端 / 演示快照）不许让加载路径抛异常——与
  // 「畸形历史要么在写边界拦住、要么在读侧兜底」同一条纪律（AGENTS.md §5 坑 12）。
  const messages = h.messages ?? [];
  let lastAssistant: AssistantBlock | null = null;
  for (let i = 0; i < messages.length; i++) {
    const m = messages[i];
    if (m.role === "user") {
      // 压缩检查点不是用户说的话：渲染成标记块（否则会变成一个巨大的用户气泡，
      // 把真正的用户消息淹没——这是回放路径与实时路径必须一致的地方）
      if (checkpoints.has(i)) {
        blocks.push({ kind: "compacted", uid: nextUid(), before: 0, after: 0, shadowed: 0, summary: checkpointBody(m.content), manual: false });
      } else {
        // 提示条（后台任务通告 / 重复调用提醒）是 **user 角色**消息（模型要当作用户
        // 回合才能回应），但它不是用户说的话——按**前缀表**识别并渲染成提示条
        //（回放路径与实时路径必须一致，否则刷新之后同一句话换了张脸）
        const label = noticeLabel(m.content);
        if (label) {
          blocks.push({ kind: "notice", uid: nextUid(), label, text: noticeBody(m.content) });
        } else {
          // seq = 撤回锚点（后端 ChatMessage 上的字段）。**回放路径与实时路径
          // 都读它**——少一条的话刷新之后同一条消息就再也撤不回（而界面上看起来
          // 一切正常）。只在真的给了时才写这个键：`seq: undefined` 会让既有断言
          // 多出一个键，而 canRewind 判的是 typeof === "number"，行为完全一致。
          blocks.push(
            typeof m.seq === "number"
              ? { kind: "user", uid: nextUid(), text: m.content, seq: m.seq }
              : { kind: "user", uid: nextUid(), text: m.content },
          );
        }
      }
      lastAssistant = null;
    } else if (m.role === "assistant") {
      const a: AssistantBlock = {
        kind: "assistant", uid: nextUid(), content: m.content,
        reasoning: m.reasoning_content ?? "", streaming: false,
      };
      blocks.push(a);
      lastAssistant = a;
      // assistant 携带的工具调用：紧跟块（保持原顺序）
      for (const tc of m.tool_calls ?? []) {
        const id = tc.id ?? "";
        const name = tc.function?.name ?? "";
        if (name === DISPATCH_TOOL_NAME) {
          // 调度调用的渲染形态是 dispatch 卡（**不是工具行**）——实时路径同样
          // 不为它建工具行（reduce.ts 的 toolCall，随后 dispatchStart 挂卡）。
          // 回放建工具行的后果就是用户报的那个：刷新之后卡片、Agent 名、任务、
          // 结论全没了，只剩一行 `agent_dispatch`。
          const args = parseDispatchArgs(tc.function?.arguments);
          blocks.push({
            kind: "dispatch", uid: nextUid(), id,
            // 续跑时 arguments.session 就是那个子会话；结果里的提示行优先
            //（role=tool 分支会用 splitDispatchResult 覆盖它）
            sessionId: args.session || undefined,
            agentId: args.agent,
            // 名字与颜色要 Agent 注册表，纯函数里没有——留空，由 DispatchCard
            // 按 agentId 从 useAgents() 回落（渲染层才有注册表）
            agentName: "", agentColor: "",
            task: args.task,
            // 先按"中断"定格：历史里**没有**配对结果 = 那次派发在结束前就断了，
            // 不是"正在跑"。结果到了（role=tool 分支）再回填成 done + 正文。
            status: "done", isError: true, result: DISPATCH_INTERRUPTED,
            // **子块恒为空数组**：子 Agent 是**独立会话**，它自己的 messages
            // （子过程：思考、工具调用、结果）在库里另存一份，父会话的历史里
            // 根本没有这些明细——回放无从重建，只能空着。
            // 这是**诚实的降级**（不是 bug、更不许编内容）：卡里有任务与结论，
            // 想看子过程就去子会话自己的历史（卡头的子会话 id 是钥匙）。
            subBlocks: [],
          });
          continue;
        }
        blocks.push({
          kind: "tool", uid: nextUid(), id,
          name, arguments: tc.function?.arguments ?? "",
        });
      }
    } else if (m.role === "tool") {
      // 工具结果回填（按 tool_call_id 找块；找不到则丢弃——防御坏数据）
      const id = m.tool_call_id ?? "";
      // ① 普通工具行：原逻辑不动（**只在还没有结果时回填**——重复的结果不该
      //    覆盖先到的那条）
      const target = blocks.find((b) => b.kind === "tool" && b.id === id) as
        | Extract<ThreadBlock, { kind: "tool" }>
        | undefined;
      if (target && target.result === undefined) {
        target.result = m.content;
        target.isError = false;
      }
      // ② 调度卡：同一个调用 id 的 dispatch 块。**这一条不能少**——回放重建的
      //    卡建出来就是"中断"态，结果到了必须能回填；只找 kind === "tool"
      //    的话每张卡都停在中断态（用户看到的还是"没有结论"）。
      //    历史里一个调用 id 只对应一条结果，所以这里直接覆盖，不必判空。
      const card = blocks.find((b) => b.kind === "dispatch" && b.id === id) as
        | Extract<ThreadBlock, { kind: "dispatch" }>
        | undefined;
      if (card) {
        const { body, sessionId } = splitDispatchResult(m.content);
        // 结论正文（尾部那行"子会话 id"提示是给模型看的续跑线索，已剥掉）。
        // 正文空 = 子 Agent 没留下文本结论——用占位说明，不留空白。
        card.result = body || DISPATCH_NO_TEXT;
        card.status = "done";
        // 历史里的 tool 消息**不带 is_error**（llm.Message 没有这个字段），
        // 所以回放分不出"子 Agent 报错"与"正常结束"——不编：报错说明本来就在
        // 正文里（后端错误路径的正文以"错误: "开头），用户读得到。
        card.isError = false;
        // 子会话 id 优先取结果里的（权威值：续跑时 arguments.session 可能为空，
        // 而这一行永远是本次真正用的那个子会话）
        if (sessionId) card.sessionId = sessionId;
      }
      // 工具结果后正文续写：新开 assistant 块（下一条 assistant 自然处理）
      lastAssistant = null;
    }
  }
  // 没有任何输出的进行中轮次不重现（streaming 重建成本高，历史里也少见）。
  // 挂起的确认必须重建：刷新/切会话后待裁决的确认卡不能只留在 state.pending
  //（没有任何组件渲染 pending——只有 blocks 里的 confirm 块才会画出来），
  // 否则会话卡在 busy=true 且用户无从批准（死锁）。
  // 用 placeConfirm 就地替换：后端在确认门挂起前已把 assistant 的 tool_calls
  // 落库，所以历史里必然有一行同 id 的工具行——若直接追加会重现"两行同 id"。
  const pending = h.pending ?? null;
  return pending ? placeConfirm(blocks, pending) : blocks;
}

/** 历史快照 → UI 状态（回放路径的入口）：块重建**复用 historyBlocks**，
 *  这里只负责把它装进 UIState 并带上快照里的会话级状态。
 *
 *  单独一层的理由：子会话历史只需要块（它不参与主时间线的归约——卡内的
 *  子时间线是**只读补充**），而主时间线需要 busy/pending/todos/context。
 *  两份产出共用同一个 historyBlocks，映射不可能分叉。 */
export function reduceHistory(state: UIState, h: HistorySnapshot): UIState {
  const pending = h.pending ?? null;
  return {
    ...state,
    blocks: historyBlocks(h),
    busy: false,
    pending,
    todos: h.todos ?? [],
    // 未知占用（后端刚重启/刚切会话）置 null —— 指示器显示中性态，
    // 不沿用上一会话的数字（那是别人的窗口占用）
    context: h.context ?? null,
    historyReady: true,
  };
}
