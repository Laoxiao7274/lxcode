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
}

export type ThreadBlock =
  | { kind: "user"; uid: number; text: string }
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
