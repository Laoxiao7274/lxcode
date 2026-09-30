// 块与 UI 状态的定义 + 纯工具函数（不含事件归约——那是 reduce.ts）。
// 单独一个文件：组件只依赖类型与这几个 helper，不必把整个 reducer 拖进依赖图。

import type { ConfirmRequest, ContextUsage, FileChange, JobInfo, TodoItem } from "./types";

export interface AssistantBlock {
  kind: "assistant";
  uid: number;
  content: string;
  reasoning: string;
  streaming: boolean;
  usageTokens?: number;
  /** 每轮计时/模型（回放与实时两条路径都填——见 shared/turn-stats.ts 的口径说明）。
   *  缺席表示后端没测到/老后端没有这些字段，显示层据此不渲染而不是显示 0。 */
  firstTokenMs?: number;
  durationMs?: number;
  model?: string;
}

export type ThreadBlock =
  /** 用户气泡。seq = 撤回锚点（后端 ChatMessage 上的序号，历史回放与实时事件
   *  同一个类型）——**没有它就不能撤回/编辑**：老后端与更早落库的历史都不带
   *  seq，锚不住就不能动历史（AGENTS.md §5 坑 11：老后端 + 新前端不许炸）。 */
  | { kind: "user"; uid: number; text: string; seq?: number }
  | AssistantBlock
  | { kind: "tool"; uid: number; id: string; name: string; arguments: string; result?: string; isError?: boolean }
  | { kind: "confirm"; uid: number; request: ConfirmRequest; resolved?: "allow" | "deny" }
  | { kind: "files"; uid: number; files: FileChange[] }
  | { kind: "error"; uid: number; message: string; aborted: boolean }
  | {
      /** 历史压缩的标记块（前缀被摘要检查点替换）——可展开看摘要正文。 */
      kind: "compacted";
      uid: number;
      /** 压缩前后的上下文占用（估算 token）。 */
      before: number;
      after: number;
      /** 被替换的历史条数。 */
      shadowed: number;
      /** 摘要正文（markdown）。 */
      summary: string;
      /** 用户主动触发（/compact 或指示器入口）。 */
      manual: boolean;
    }
  | {
      /** 系统提示条（后台任务通告 / 重复调用提醒——种类表见 shared/notices.ts）。
       *  **在历史里是真实 user 角色消息**——模型要把它当用户回合才能回应；但它不是
       *  用户说的话，所以渲染成提示条而不是气泡。
       *  label 是前缀表里的标签（渲染层直接用它，不再硬编码）；text 是去掉前缀后的正文。 */
      kind: "notice";
      uid: number;
      label: string;
      text: string;
    }
  | {
      /** 后台任务卡（本次对话起的任务：命令摘要 + 计时 + 输出 tail + 结束）。
       *  job 直接是后端快照（JobInfo）——started/settled 都是它，卡就地更新。 */
      kind: "job";
      uid: number;
      job: JobInfo;
    }
  | {
      kind: "dispatch";
      uid: number;
      /** dispatch 调用 id（子事件归属键）。 */
      id: string;
      /** 子会话 id（子 Agent 是独立会话：自己的历史/压缩/可续跑）。 */
      sessionId?: string;
      agentId: string;
      agentName: string;
      agentColor: string;
      /** 下发的任务描述（主 Agent 的验收标准在这里）。 */
      task: string;
      /** running | done（done 带 result——子 Agent 的最终回复）。 */
      status: "running" | "done";
      /** 子 Agent 的最终回复（dispatchEnd 回填——验收视图）。 */
      result?: string;
      /** 子执行过程的块（子上下文隔离——delta/工具行挂在这里，不进主时间线）。 */
      subBlocks: ThreadBlock[];
      /** 子 Agent 用量（轮末）。 */
      usageTokens?: number;
      isError?: boolean;
    };

/** 调度工具的 id（后端 tools.DispatchToolName 的前端对应物）——**唯一字面量**。
 *
 *  它是唯一一个「渲染形态不是工具行」的工具：实时路径（reduce.ts 的 toolCall）
 *  不为它建 tool 块（它的形态是 dispatch 卡，随后由 dispatchStart 挂卡），
 *  回放路径（history.ts 的 reduceHistory）为它建 dispatch 块。两条路径必须用
 *  同一个判定字符串——写第二份字面量的代价是两条路径静默分叉：刷新之后所有
 *  子 Agent 卡退化成一行光秃秃的 `agent_dispatch` 工具行（卡片、Agent 名、
 *  任务、结论全没了），而实时路径看起来一切正常（用户实测报的就是这个）。
 *
 *  必须匹配 `^[a-zA-Z0-9_-]{1,64}$`（点号会被严格网关 400 拒收整轮，
 *  见 AGENTS.md §5 坑 13）；tests/dispatch-tool-id.test.mjs 与
 *  tests/history-dispatch.test.mjs 各钉一处。 */
export const DISPATCH_TOOL_NAME = "agent_dispatch";

export interface UIState {
  blocks: ThreadBlock[];
  busy: boolean;
  pending: ConfirmRequest | null;
  todos: TodoItem[];
  /** 当前会话 id（sessionChanged/new|resumed|started 同步——侧栏高亮与
   *  标题的唯一事实源；并入 store 消灭 App 的第二份订阅与双渲染）。 */
  currentId: string;
  /** 请求类失败的一次性提示（operationError——App 之前自持的状态）。 */
  operationError: string | null;
  /** 上下文占用（后端测量；null = 未知——刚切会话/后端刚重启，指示器显示
   *  中性态而不是编一个数）。 */
  context: ContextUsage | null;
  /** 该会话至少完成过一次 history 回放，后续忙碌快照才可保留本地实时块。 */
  historyReady: boolean;
}

export const initial: UIState = { blocks: [], busy: false, pending: null, todos: [], currentId: "", operationError: null, context: null, historyReady: false };

// 块的唯一序号——React 渲染的稳定 key（index 作 key 在插入新块时
// 会错位复用组件实例，是重复渲染类怪象的根因）。
let uidSeq = 0;
export const nextUid = () => ++uidSeq;

/** shell 拷贝 + 单点替换（map 变体的免回调版——toolResult/resolve 这类
 *  「只改个别块」的路径，O(n) 指针拷贝无逐元素闭包）。 */
export function withBlock(state: UIState, uid: number, patch: (b: never) => ThreadBlock): UIState {
  const blocks = state.blocks.slice();
  const idx = blocks.findIndex((b) => b.uid === uid);
  if (idx >= 0) blocks[idx] = patch(blocks[idx] as never);
  return { ...state, blocks };
}

/** 确认卡落位：同 id 的工具行（toolCall 已建）就地换成确认卡——uid 不变。
 *  不能"追加一张卡"：那样工具行与确认卡两行并存，批准时 confirm 卡又转
 *  成工具行，同一 id 出现两条工具行，而 toolResult 的 findIndex 只回填
 *  第一条——第二条永远停在"执行中…"且不可展开（running 时 expandable=false）。
 *  没有对应工具行（如确认先于 toolCall 到达）才追加新卡。 */
export function placeConfirm(blocks: ThreadBlock[], request: ConfirmRequest): ThreadBlock[] {
  const idx = blocks.findIndex((b) => b.kind === "tool" && b.id === request.id);
  const next = blocks.slice();
  if (idx >= 0) {
    next[idx] = { kind: "confirm", uid: next[idx].uid, request };
    return next;
  }
  next.push({ kind: "confirm", uid: nextUid(), request });
  return next;
}

/** 压缩检查点的定界标记（对齐后端 agent/compaction_prompt.go）。 */
const CHECKPOINT_OPEN = "<compacted-summary>";
const CHECKPOINT_CLOSE = "</compacted-summary>";

// ===== 用户气泡的三个动作：复制 / 编辑 / 撤回 =====
//
// 判定全部抽成这里的纯函数（而不是埋在 Block 组件里）：撤回的锚点语义是
// 「这条及其之后的全部历史一起消失」——写错一个下标只会**静默丢历史**，
// 编译器和类型系统都拦不住，而这正是用户最不能接受的一类 bug（"我撤回了上一条，
// 结果下面三条也没了 / 没删干净"）。tests/message-actions.test.mjs 钉住这几点。

/** chat.rewind 的参数形状（与 Go internal/protocol 逐字一致）。
 *
 *  单独一个构造器而不是各处手写对象字面量：这个项目的协议坑是「字段名错一个
 *  不编译报错、只静默丢字段」——后端收到 `seq_no` 只会当作 seq 缺失，撤回
 *  "成功"返回 removed=0 而历史一条没删。参数只有这一处来源，测试钉住它。 */
export function rewindParams(sessionId: string, seq: number): { session_id: string; seq: number } {
  return { session_id: sessionId, seq };
}

/** 撤回锚点：seq 命中的那条 user 块下标（-1 = 时间线里没有它）。
 *
 *  只认 user 块：提示条（notice）在历史里也是 user 角色消息，但它不是用户说的
 *  话——给它挂撤回动作等于让用户能"撤回"一句自己没说过的话。 */
export function rewindIndex(blocks: ThreadBlock[], seq: number): number {
  return blocks.findIndex((b) => b.kind === "user" && b.seq === seq);
}

/** 撤回计划：**锚点及其之后的块全部消失**（含锚点自己），锚点的文本回到输入框。
 *
 *  返回 null = 这个 seq 不在时间线里，调用方必须当"没撤"处理并给明确反馈。两种
 *  情形都会走到这里：① 老后端/演示历史没有 seq；② 已经撤过了（后端广播重复
 *  到达）——后者原样返回才是幂等，猜一个位置截断会误删更早的消息。 */
export function planRewind(blocks: ThreadBlock[], seq: number): { blocks: ThreadBlock[]; text: string } | null {
  const idx = rewindIndex(blocks, seq);
  if (idx < 0) return null;
  const anchor = blocks[idx];
  if (anchor.kind !== "user") return null; // 类型收窄（rewindIndex 已保证，无运行时分支）
  // slice 保留的是**原块对象**（引用相等）：重建会丢掉展开态这类本地状态
  return { blocks: blocks.slice(0, idx), text: anchor.text };
}

/** 这条块能不能挂撤回/编辑动作：老后端与更早落库的历史没有 seq——锚不住就不能
 *  动历史。类型守卫：调用方拿到 true 之后可以安全读 block.seq。 */
export function canRewind(block: ThreadBlock): block is Extract<ThreadBlock, { kind: "user" }> & { seq: number } {
  return block.kind === "user" && typeof block.seq === "number";
}

/** 编辑态：只记锚点与原文本——**一个字的历史都不动**。
 *
 *  这是「编辑」与「撤回」的分界：编辑是"我可能改主意"，一点就把后面的对话毁掉
 *  是不可接受的；真正的撤回推迟到下次发送前（先 chat.rewind 再 chat.send）。
 *  撤回则相反——点下去历史立刻清空，用户要的就是"重来"。 */
export interface EditDraft {
  seq: number;
  text: string;
}

/** 进入编辑态（没有 seq 的块返回 null——调用方给明确反馈，不静默）。 */
export function beginEdit(block: ThreadBlock): EditDraft | null {
  if (!canRewind(block)) return null;
  return { seq: block.seq, text: block.text };
}

/** 取消编辑：回到无编辑态。历史一个字都不动——取消 = 当作没编辑过。
 *  刻意不接收也不返回 blocks：这个函数**没有能改历史的手**，取消不可能误删。 */
export function cancelEdit(): null {
  return null;
}

/**
 * 从检查点消息内容里剥出摘要正文：后端把摘要包成「前言 + 定界标记 + 正文」，
 * 前端只该展示正文（前言是给模型看的，不该出现在界面上）。
 * 标记缺失时原样返回——历史坏数据不该让界面渲染空白。
 */
export function checkpointBody(content: string): string {
  const start = content.indexOf(CHECKPOINT_OPEN);
  const end = content.lastIndexOf(CHECKPOINT_CLOSE);
  if (start < 0 || end <= start) return content;
  return content.slice(start + CHECKPOINT_OPEN.length, end).trim();
}
