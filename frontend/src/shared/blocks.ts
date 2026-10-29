// 块与 UI 状态的定义 + 纯工具函数（不含事件归约——那是 reduce.ts）。
// 单独一个文件：组件只依赖类型与这几个 helper，不必把整个 reducer 拖进依赖图。

import type { ApprovalMode, ConfirmRequest, ContextUsage, FileChange, JobInfo, SessionStats, TodoItem } from "./types";
import type { AttachmentMeta } from "./attachments";

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
   *  seq，锚不住就不能动历史（AGENTS.md §5 坑 11：老后端 + 新前端不许炸）。
   *  pending = 乐观气泡（本地先画、后端还没回执）——视觉稍淡 + 发送中指示，
   *  回执到达即按 FIFO 移除（见 reduce.ts）；pending 块没有 seq，天然不可撤回。 */
  | { kind: "user"; uid: number; text: string; seq?: number; pending?: boolean;
      /** 乐观气泡的附件标记（图片批次 B）：images/files 是随消息带的张数/个数
       *  （pending 阶段 base64 已交出去，块里只留计数）；真块不带它——真块的
       *  附件卡来自后端回执的 attachments 引用（下面这个键）。 */
      atts?: { images: number; files: number };
      /** 附件引用（后端回执/历史回放带的展示元数据——渲染缩略图卡/文件卡）。
       *  pending 乐观块没有它；老历史由 attachments.ts 的归一化回落给出。 */
      attachments?: AttachmentMeta[] }
  | AssistantBlock
  | { kind: "tool"; uid: number; id: string; name: string; arguments: string; result?: string; isError?: boolean;
      /** 这次执行共享的附件（share_file——实时 toolResult 与回放同一份）。
       *  缺省 = 无（绝大多数工具调用）。 */
      attachments?: AttachmentMeta[] }
  | { kind: "confirm"; uid: number; request: ConfirmRequest; resolved?: "allow" | "deny";
      /** ask 提问被回答时的用户答案摘要（resolved="allow" 时显示在已决卡上；
       *  二元确认永远没有它）。 */
      resolvedAnswer?: string }
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
      /** 子会话时间线的「任务」块：主 Agent 派发的任务说明书（user 角色消息，
       *  但它不是用户说的话——用户没法在子会话页发言，这条的唯一来源是
       *  agent_dispatch 的派发）。渲染成独立的任务卡而不是用户气泡：否则用户
       *  分不清这是 AI 传进去的任务还是自己发的（用户实测报的就是这个）。
       *  实时路径由双投打 task 标记（reduce.ts），回放路径由快照的 child 键分流
       *  （history.ts）——两条路径必须给出同一个块种类。 */
      kind: "task";
      uid: number;
      text: string;
    }
  | {
      /** 系统提示条（重复调用提醒——种类表见 shared/notices.ts；hidden 种类
       *  （后台任务通告 / 系统通告）不产生任何块，到不了这里）。
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
      /** 子执行**正在流式**的那一块（与主时间线的 state.streaming 同一个不变量：
       *  它恒为 subBlocks 逻辑上的最后一块，定型时并入）。分开存是为了让子事件的
       *  delta 也不复制整个时间线（applyToDispatch 每帧只换这一个对象）。
       *  卡本身不渲染子块（卡 = 一行摘要），这里只是归约语义与主时间线同构。 */
      subStreaming?: AssistantBlock;
      /** 子 Agent 用量（轮末）。 */
      usageTokens?: number;
      isError?: boolean;
      /** 子会话的任务清单（todo 契约批②/③）：实时路径由带 dispatch_id 的
       *  todo.updated 驱动（running 时实时更新）；派发结束时定格为 finalTodos
       *  （dispatchEnd 回填）。徽标 = 三态计数，不展开卡内子时间线。 */
      todos?: TodoItem[];
      /** 这次派发期间子会话共享到对话的附件（share_file 等的产物——随调度
       *  结果回流主会话）：主时间线渲染附件条（卡 = 一行摘要的例外：附件不是
       *  子过程，是**成果**）。实时路径来自 dispatch 的 toolResult 事件、回放
       *  路径来自调度结果消息上的同一份元数据——两条路径同一份形状。 */
      attachments?: AttachmentMeta[];
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
  /** **已定型**的块（不含正在流式生成的那一块——见下面的 streaming）。
   *
   *  为什么要分开（2026-12 渲染性能）：流式 delta 是 token 级的（一轮几百条），
   *  原先每帧都 `blocks.slice()` 整数组复制再替换尾块——2000 块的会话在流式期间
   *  每秒几十次全量复制（O(块数) × 帧率）。分开之后 delta 只换 streaming 这一个
   *  对象引用（O(1)），定型（工具调用打断 / done / error / 重拉历史）时才做一次
   *  数组合并。块对象本身仍然不可突变（memo(Block) 靠引用比较）。
   *
   *  渲染侧（Thread / ChildSessionPage）**分两段挂**（blocks 窗口 + streaming），
   *  不拼数组——每帧拼一份逻辑时间线等于把这里省下的 O(块数) 复制又还回去。
   *  需要全量序列的顺序敏感派生（大纲/轮次/标题反查/撤回原文）用 timelineOf()。
   *  其它消费点（派发卡、确认门）只看 blocks——它们要的 user/dispatch/tool 块
   *  全在这里，流式中的 assistant 块既没有 seq 也不参与这些判定。 */
  blocks: ThreadBlock[];
  /** 正在流式生成的 assistant 块（**不在 blocks 里**；null = 当前没有在流式输出）。
   *  由 reduceDelta 逐帧替换（只复制这一块），定型时由 finalizeStreaming 并入
   *  blocks。恒为 blocks 逻辑上的最后一块。 */
  streaming: AssistantBlock | null;
  /** 更早的历史还没拉完（分页装载，chat.history 的 before_seq）：Thread /
   *  子会话页据此在顶部继续显示「加载更早」的入口。false = 已到头，或后端
   *  没报（老后端 → 不分页，行为与改造前一致）。 */
  historyMore: boolean;
  /** 已装载历史里最旧一条消息的 seq（下一次 before_seq 的 anchor；0 = 未知）。 */
  historyFirstSeq: number;
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
  /** 整段会话统计（后端折叠整段日志得出；null = 还没有任何一步——不渲染统计胶囊，
   *  不显示一排 0）。它与 context 的分工：context 是「此刻窗口里有多少」，
   *  stats 是「这条会话一共花了多少」。 */
  stats: SessionStats | null;
  /** 该会话至少完成过一次 history 回放，后续忙碌快照才可保留本地实时块。 */
  historyReady: boolean;
  /** 发送中态（乐观气泡已插、后端 busy 还没到）：submit 后立刻置 true，busy
   *  （生成交接）/ done / error / 发送失败 / 历史重建时收 false。它与 busy 的
   *  分工：sending = 「消息已交出去、还没确认开始生成」，busy = 「生成中」。 */
  sending: boolean;
  /** 该会话实际用的模型（回放快照里带；缺席 = 未知）。
   *  子会话页头显示的是**它自己的**模型——子 Agent 可以用与主会话不同的模型
   *  （AGENTS.md §2.3：子会话是独立会话），拿主会话的模型冒充是假数据。 */
  model: string;
  /** 该会话**此刻**的权限档（会话级状态——后端落库的那份）。null = 未知
   *  （老后端/演示源没报）：显示端不猜，回落 confirm。数据来自两条路：
   *  chat.history 的 approval 键（回放对齐）与 chat.approvalChanged 事件
   *  （实时改档）——reduceSessionStates 按 sessionId 路由，天然按会话隔离。 */
  approval: ApprovalMode | null;
  /** 时间线**重建代次号**（2026-12「撤回闪回来」的配套）：blocks 被全量重建
   *  （historyLoaded 重建分支）时递增；增量路径（分页前插、实时追加）不动它。
   *  Thread 据此区分「换会话的整块回放」（要播入场动画）与「同会话重拉的全量
   *  重建」（不播——用户看着的时间线闪一遍弹跳动画只表达"界面抽风"）。 */
  rebuiltGen: number;
}

export const initial: UIState = { blocks: [], streaming: null, historyMore: false, historyFirstSeq: 0, busy: false, pending: null, todos: [], currentId: "", operationError: null, context: null, stats: null, historyReady: false, model: "", sending: false, approval: null, rebuiltGen: 0 };

/** 逻辑时间线 = 已定型块 + 正在流式的那一块（拼在末尾）。
 *
 *  只在**渲染**与**顺序敏感的纯函数**（大纲/轮次/导出）里用；`state.blocks`
 *  仍然是「已定型」的那一份（撤回、派发卡、确认门只认它）。返回新数组是必要的
 *  ——组件要一个可 map 的序列；这一步的代价是 O(块数)，但它只发生在需要全量的
 *  地方，不在流式热路径上。 */
export function timelineOf(state: Pick<UIState, "blocks" | "streaming">): ThreadBlock[] {
  return state.streaming ? [...state.blocks, state.streaming] : state.blocks;
}

/** 把正在流式的那一块**并入**已定型块（定型：工具调用打断 / 轮末 done /
 *  出错 / 重拉历史）。没有流式块时原样返回（连对象都不换——避免无变化的重渲染）。 */
export function finalizeStreaming(state: UIState, patch?: Partial<AssistantBlock>): UIState {
  const cur = state.streaming;
  if (!cur) return state;
  const block: AssistantBlock = { ...cur, ...patch, streaming: false };
  return { ...state, blocks: [...state.blocks, block], streaming: null };
}

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
export function rewindParams(sessionId: string, seq: number, files?: boolean): { session_id: string; seq: number; files?: boolean } {
  // files 只在 true 时写这个键（omitempty 语义：false 与缺席等价，少一个字节的
  // 歧义——后端按 p.Files 判，缺省就是不动文件）
  return files ? { session_id: sessionId, seq, files: true } : { session_id: sessionId, seq };
}

/** chat.setLeaf 的参数形状（与 Go internal/protocol 逐字一致）。
 *
 *  与 rewindParams 同一个理由单独成函数：wire 字段名错一个不会编译报错，只会
 *  静默丢字段（后端当作 seq 缺失 → 把用户送到一个他没点的位置）。 */
export function setLeafParams(sessionId: string, seq: number, files?: boolean): { session_id: string; seq: number; files?: boolean } {
  return files ? { session_id: sessionId, seq, files: true } : { session_id: sessionId, seq };
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

/** 撤回的回填时机（乐观，2026-12）：**点击撤回那一刻就把原文装回输入框**——
 *  后端确认只做校正，失败回滚（把注入的草稿清掉 + 报错）。
 *
 *  为什么要改（原实现是「rewind 成功才回填」）：回填要等一个 WS 往返，而后端
 *  rewind 的应答链上有删历史 + 占用重算 + **整段日志折叠统计**（emit.go 的
 *  sessionStatsOf），大会话实测 1~2s——这期间输入框是空的，用户看到「时间线
 *  已经清了、文本却迟迟不来」。时间线的乐观截断本来就发生在请求发出前，回填
 *  却排在应答之后，两半不一致；把回填也提到请求之前，点击 → 下一帧输入框就有
 *  内容，与「历史先清」对齐。
 *
 *  为什么现在敢乐观（当初 fail-closed 的理由是否还成立）：后端拒绝（配对校验）
 *  时文本已进输入框——所以失败路径必须**回滚**：rollback 把注入的草稿清掉
 *  （输入框恢复原样），时间线由 ws 的失败对齐路径复原（rewind 失败 →
 *  chat.history 重放，见 agent/ws），两边一起回到撤回前。注入时**显式带上
 *  会话 id**（sid——调用方在点击那一刻捕获的归属会话）：回填/回滚可能发生在
 *  用户已切到别的会话之后（后端应答是异步的），「当前会话」在 promise 回调里
 *  已经不可信——按 sid 落，才不会把 A 的撤回文本注进 B 的输入框（跨会话泄漏，
 *  用户实测报的就是这个）。
 *
 *  抽成纯函数：App 组件依赖太重，node:test 钉不住——把「乐观回填 + 失败回滚
 *  + 会话归属」这个契约钉在这里。 */
export function rewindThenRestore(opts: {
  rewind: () => Promise<unknown>;
  text: string;
  /** 撤回发生时会话 id（点击那一刻的归属——不是回调时刻的「当前会话」）。 */
  sid: string;
  inject: (sid: string, text: string) => void;
  /** 失败回滚：清掉注入进该会话的草稿（时间线由 ws 的重放路径复原，这里不碰）。 */
  rollback: (sid: string) => void;
  report: (message: string) => void;
}): void {
  opts.inject(opts.sid, opts.text);
  void opts.rewind().then(
    () => undefined,
    (e: unknown) => {
      opts.rollback(opts.sid);
      opts.report(`撤回失败: ${e instanceof Error ? e.message : String(e)}`);
    },
  );
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
