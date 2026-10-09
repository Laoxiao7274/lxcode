// UI 事件模型——字段语义与后端 internal/protocol 一一对应（demo 与 live
// 两个 AgentSource 实现都发这套事件，UI 层不感知数据来源）。

/** 任务清单项（对齐 tools.TodoItem）。 */
export interface TodoItem {
  content: string;
  status: "pending" | "active" | "done";
}

/** 上下文占用（对齐 protocol.ContextUsage）：used/window 是压力与环形依据
 *  （used 优先真实 prompt 总量），五个分类是估算拆分（已归一：分类之和 == used）。 */
export interface ContextUsage {
  used: number;
  /** 模型窗口上限（0/缺省 = 未知——不画环形百分比）。 */
  window?: number;
  /** 系统提示词。 */
  system?: number;
  /** 工具声明（wire 上的 JSON Schema）——与 system 分开一类，因为"工具占了窗口多少"
   *  是用户最想知道的其中一件事（DSH 的 ContextMeter 同样单列）。 */
  tools?: number;
  tool_results?: number;
  messages?: number;
  reasoning?: number;
  /** used 是**估算值**（后端按固定密度折算）：端点没回报用量，或库里没有真实测量、
   *  按已加载的历史回落（老会话/后端重启前跑过的会话）。UI 必须标注出来——用户
   *  看不出区别就会拿它做预算判断。缺省/false = 真实用量（provider 回报的
   *  prompt_tokens）。 */
  estimated?: boolean;
}

/** 整段会话的统计（对齐 protocol.SessionStats / DSH 的 sessionStats + tokenUsage）。
 *
 *  与 ContextUsage 的分工：context 回答「此刻窗口里有多少」，stats 回答「这条会话
 *  一共花了多少」。它折叠的是**整段日志**（含被压缩检查点影子掉的消息），所以压缩与
 *  翻页都改不了这些数字；撤回真删了行，数字跟着变小才是对的。
 *
 *  整键缺席 = 还没有任何一步（新会话/纯内存模式）——UI 不渲染统计胶囊，
 *  **不显示一排 0**（那是个假事实）。 */
export interface SessionStats {
  /** 轮数（用户发起的轮数，与右栏「轮次」面板同一口径）与步数（模型调用次数）。 */
  turns: number;
  steps: number;
  /** 墙钟（毫秒）：llm_ms = 各步请求耗时之和；tool_ms = 工具执行耗时之和。 */
  llm_ms: number;
  tool_ms: number;
  /** 首字：ttft_ms / ttft_steps = 均值（只有有首字可测的步才计入）。 */
  ttft_ms: number;
  ttft_steps: number;
  /** 解码：decode_ms = 首字 → 收尾的纯生成耗时，decode_tokens = 同期输出 token。
   *  生成速度 = decode_tokens / decode_ms（扣掉 prefill 才是"吐字速度"）。 */
  decode_ms: number;
  decode_tokens: number;
  /** 计费四桶（provider 回报；未回报 = 0）：未缓存输入 / 缓存读 / 缓存写 / 输出。 */
  input_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  output_tokens: number;
  /** 早期记录（本功能上线前落库的消息）的 token 之和：那时的口径是 provider 的
   *  total_tokens（输入+输出），与上面四桶不同——**单独给出、不混算**。
   *  缺席（0）= 这条会话没有早期记录。UI 在明细里如实说明它是什么，不参与速度。 */
  legacy_tokens?: number;
}

/** 手动压缩的结果（chat.compact 的应答）。 */
export interface CompactOutcome {
  /** false = 没有可压的收益（历史太短 / 摘要不缩水）——不是错误。 */
  compacted: boolean;
  /** 为什么没压（人话，直接显示）。 */
  reason?: string;
  before?: number;
  after?: number;
  shadowed?: number;
}

/** 会话回退的结果（chat.rewind 的应答）。
 *
 *  **不用于渲染**——时间线的截断由 rewound 事件驱动（本地乐观 + 后端广播走
 *  同一条归约路径）。它只回答「后端真删了几条」，供提示与诊断用。 */
export interface RewindOutcome {
  /** 被删除的消息条数（锚点那条 + 它之后的全部）。 */
  removed: number;
}

/** 归档的结果（session.archive 的应答）。
 *
 *  归档是主操作——失败会以请求错误抛出，不走这里。这里只回答「顺带释放工作区
 *  的结果」：归档已经成功，释放失败（脏改动 / 忙）只把原因带回，绝不回滚归档。 */
export interface ArchiveOutcome {
  /** 是否已归档（恒为 true——请求成功即已归档）。 */
  archived: boolean;
  /** 是否至少成功释放了一个工作区目录且没有失败。 */
  releasedWorktree: boolean;
  /** 释放失败的原因（「；」拼接，空串 = 无失败）。 */
  releaseError: string;
}

/** 后台任务的运行状态（对齐 protocol/jobs.Status）。 */
export type JobStatus = "running" | "stopping" | "completed" | "killed" | "failed";

/** 「谁结束的」——空 = 还在跑。用户点「结束」与 agent 自己 kill 在这里分叉：
 *  前者要唤醒 agent 并告诉它「不要重启」，后者是同一轮内的已知动作。 */
export type JobEndedBy = "" | "self" | "user" | "agent" | "backend";

/** 后台任务快照（对齐 protocol.JobInfo，见 docs/jobs.md §4）。
 *
 * 字段名逐字对齐契约（wire 命名按本协议惯例是 snake_case：session_id /
 * ended_by / output_tail）；这是**对外快照**，UI 直接消费它，不再另立一份
 * 视图模型——多一层映射只会让「契约改了前端不知道」多一个静默点。 */
export interface JobInfo {
  id: string;
  /** 任务种类（当前只有 "bash"）。 */
  kind: string;
  /** 一行摘要（命令截断——UI 与通告都用它）。 */
  label: string;
  status: JobStatus;
  ended_by: JobEndedBy;
  /** 退出码 / 信号 / 超时 / 重启（人话，直接显示）。 */
  detail: string;
  /** 执行会话（谁起的任务；空 = 不属于任何会话，只进全局面板）。 */
  session_id: string;
  /** **时间线归属**（顶层会话）：子 Agent 起的任务挂在父会话上，前端据此把它
   *  放进**父会话**的时间线——子会话不进侧栏，按执行会话上卡等于用户在主对话里
   *  什么都看不到。空 = 回落 session_id。 */
  owner_session_id: string;
  /** RFC3339；finished_at 空 = 未结束。 */
  started_at: string;
  finished_at: string;
  /** 最近 64KB 输出（面板直接显示，不必往返 job.log）。 */
  output_tail: string;
  /** 落盘日志路径（恒有——任务结束后仍可读全量）。 */
  output_path: string;
}

/** job.log 的结果：全量输出（后端可能截断，truncated 标明）。 */
export interface JobLogResult {
  data: string;
  truncated: boolean;
}

/** JobAdminSource：后台任务的查看与结束（后端 job.* 直通）。
 *
 * 与 ModelAdminSource / SearchAdminSource 同模式：UI 依赖能力接口而非具体
 * WSAgent；Demo 也实现（演示模式无后端也要能全量跑 UI——起任务、看输出、
 * 点结束）。事件（job.started / job.settled）与 job.list 共用同一份缓存。 */
export interface JobAdminSource {
  /** 当前任务快照（job.list 结果 + job.started/settled 增量，按开始时间倒序）。 */
  jobs(): JobInfo[];
  /** 订阅任务变化（连接建立 / job.started / job.settled；返回退订）。 */
  onJobsChanged(listener: () => void): () => void;
  /** 拉取任务清单（sessionId 为空 = 全部）；结果写回缓存。 */
  listJobs(sessionId?: string): Promise<JobInfo[]>;
  /** **用户点「结束」**——后端走 Manager.Kill(id, EndedUser)，与 agent 的
   *  job_kill 同一条路径（只是 by 不同，两条路径行为永远一致）。 */
  killJob(id: string): Promise<JobInfo>;
  /** 读全量输出（落盘日志——任务结束后仍可读）。 */
  readJobLog(id: string): Promise<JobLogResult>;
}

/** 确认请求（对齐 protocol.ConfirmRequest）。 */
export interface ConfirmRequest {
  id: string;
  name: string;
  arguments: string;
  prompt: string;
  /** 非空 = 子 Agent 的确认（归属 dispatch 卡内）。 */
  dispatch_id?: string;
  /** 请求形态：空 = 高危工具确认（批准/拒绝）；"ask" = ask_user 的提问
   *  （用户以文本回答或跳过）。老后端不带这个键 → 按确认语义处理。 */
  kind?: string;
  /** 提问的预设答案（可空）——渲染成可直接点选的选项按钮。 */
  options?: string[];
}

/** AgentSource 推给 UI 的事件流（对齐服务端广播事件）。 */
export type AgentEvent =
  | { type: "ready"; server: string; version: string; busy: boolean }
  | { type: "sessionFocused"; id: string }
  /** seq = 撤回锚点（ChatMessage 上的字段；历史回放与实时事件是**同一个类型**，
   *  所以两条路径都读 message.seq）。老后端没有它 → 块不可撤回，但绝不炸。 */
  | { type: "userMessage"; sessionId: string; text: string; seq?: number }
  /** 乐观用户气泡（**本地事件，不经后端**——体验修复批次 2）：send 发起后立即在
   *  当前会话插一个 pending 用户块，真后端的 userMessage 回执（排在 worktree 准备
   *  等工作之后）到达时按 FIFO 去重。failed = 发送失败（ws 的 catch）：移除最旧的
   *  pending 块。demo 模式不发自它——它的 userMessage 是同步 emit 的，没有空窗。 */
  | { type: "optimisticUser"; sessionId: string; text: string; failed?: boolean;
      /** 随消息携带的附件（带图/带文件时失败要装回输入框附件区——文本丢了
       *  附件不能丢，见 store 的 onSendFailed 钩子）。 */
      atts?: SendAttachments }
  | { type: "delta"; sessionId: string; kind: "text" | "reasoning"; text: string; dispatchId?: string }
  | { type: "toolCall"; sessionId: string; id: string; name: string; arguments: string; dispatchId?: string }
  | { type: "toolResult"; sessionId: string; id: string; name: string; content: string; isError: boolean; dispatchId?: string }
  | { type: "confirmRequest"; sessionId: string; request: ConfirmRequest }
  | { type: "todoUpdated"; sessionId: string; items: TodoItem[] }
  | { type: "done"; sessionId: string; usageTokens: number; finishReason: string; dispatchId?: string; context?: ContextUsage;
      /** 整段会话统计（缺省 = 还没有任何一步 / 纯内存模式）。一轮里的每个 done 都带
       *  一份最新的，取最后收到的那份即可（与 DSH 的投影随事件推进同语义）。 */
      stats?: SessionStats;
      /** 每轮计时（后端 internal/agent/timing.go）：首 token 延迟与本轮耗时。**工具轮/非流式
       *  回放没有「首字」这个时刻 → 整键缺席**（不是 0——0 会被显示成「首字 0ms」的假数据）。 */
      firstTokenMs?: number; durationMs?: number;
      /** 本轮实际使用的模型 id（Agent 绑定优先、否则 default 角色）。 */
      model?: string }
  | { type: "error"; sessionId: string; message: string; aborted: boolean }
  | { type: "dispatchStart"; sessionId: string; dispatchId: string; childSessionId?: string; agentId: string; agentName: string; agentColor: string; task: string }
  | { type: "dispatchEnd"; sessionId: string; dispatchId: string; childSessionId?: string; result: string; isError: boolean; usageTokens?: number }
  /** 请求失败不代表生成失败：不得清空会话、定格正文或解除确认卡。 */
  | { type: "operationError"; message: string }
  | { type: "busy"; sessionId: string; busy: boolean }
  /** 某会话的权限档被改了（后端广播 chat.approvalChanged——多客户端/壳+浏览器
   *  同时开着时靠它保持一致；载荷是规范化后的档位，空 = confirm）。 */
  | { type: "approvalChanged"; sessionId: string; approval: ApprovalMode }
  | { type: "sessionChanged"; id: string; reason: string }
  /** 会话列表本身变了（重命名/归档/恢复）——UI 重读 sessions()。 */
  | { type: "sessionsChanged" }
  /** 项目列表变了（添加）——UI 重读 projects()。 */
  | { type: "projectsChanged" }
  /** 一轮任务的产物汇总（改动文件 + diff 统计——验收视图）。 */
  | { type: "filesChanged"; sessionId: string; files: FileChange[] }
  /** 历史被压缩（前缀替换成摘要检查点）——UI 插一条「已压缩历史」标记块。
   *  dispatchId 非空 = 子会话自己的压缩（归属进 dispatch 卡内，不进主时间线）。 */
  | { type: "compacted"; sessionId: string; before: number; after: number; shadowed: number; summary: string; manual?: boolean; dispatchId?: string }
  /** 会话回退（chat.rewound）：seq 这条用户消息及其之后的全部历史已被删除。
   *
   *  归约是**幂等**的：本地乐观截断（发起撤回时立刻清空时间线）已经把锚点删掉了，
   *  后端广播随后到达时找不到锚点就原样返回——不幂等的话重复到达会再切一刀，
   *  把更早的消息也一起删掉。 */
  | { type: "rewound"; sessionId: string; seq: number; removed: number; context?: ContextUsage;
      /** 重算后的整段统计（撤回真删了行，步数/token 跟着变小）。 */
      stats?: SessionStats }
  /** 历史载入（连接/切会话后）——全量重建对话视图。 */
  | { type: "historyLoaded"; sessionId: string; history: HistorySnapshot }
  /** 后台任务起了（时间线插一张任务卡）。sessionId = 任务归属会话——
   *  无归属（空）的任务只进顶栏全局面板，不进任何会话时间线。 */
  | { type: "jobStarted"; sessionId: string; job: JobInfo }
  /** 后台任务结束（带 EndedBy——UI 据此显示「你停的 / 它挂了 / 超时 /
   *  后端重启中断」）。同一个 job 的 started 与 settled 共用一张卡。 */
  | { type: "jobSettled"; sessionId: string; job: JobInfo };

/** 会话列表条目（对齐 protocol.SessionMeta；workspace 用于侧栏按工作区分组）。 */
export interface SessionMeta {
  id: string;
  title: string;
  updatedAt: string;
  messages: number;
  /** 所属工作区（项目路径的末段；空 = 未分组）。 */
  workspace?: string;
  /** 归档态——侧栏不显示，设置「归档任务」里可恢复。 */
  archived?: boolean;
}

/** 历史快照（chat.history 的载荷——重建视图用）。 */
export interface HistorySnapshot {
  sessionId: string;
  messages: HistoryMessage[];
  busy: boolean;
  pending: ConfirmRequest | null;
  todos: TodoItem[];
  /** 上下文占用（缺省 = 未知——刚切会话/后端刚重启，指示器显示中性态）。 */
  context?: ContextUsage;
  /** 整段会话统计（缺省 = 还没有任何一步——不渲染统计胶囊）。 */
  stats?: SessionStats;
  /** 压缩检查点在 messages 里的下标（这些消息渲染成「已压缩历史」块，不是用户气泡）。 */
  checkpoints?: number[];
  /** 该会话实际用的模型 id（子会话就是它自己 Agent 的模型）。未知 → 整键缺席，
   *  显示中性态——不编一个模型名。 */
  model?: string;
  /** 该会话**此刻**的权限档（后端 LiveApproval）。缺省 = 后端没报（老后端/演示源）
   *  ——显示端不猜，保持本地设置不动。主会话之间隔离的显示依据。 */
  approval?: ApprovalMode;
}

/** 历史消息（llm.Message 的 wire 形态）。 */
export interface HistoryMessage {
  role: string;
  content: string;
  /** 撤回锚点（后端 ChatMessage 的字段，与实时 chat.userMessage 的载荷是同一个
   *  类型）。老后端没有它 → 块不可撤回（动作禁用），绝不炸（AGENTS.md §5 坑 11）。 */
  seq?: number;
  /** 每轮计时/用量/模型（后端 messages 表那四列）。**回放路径必须与实时路径给出同一组
   *  数字**——只填实时不填回放的话，用户刷新一次这些数字就全没了（本仓库吃过三次亏的
   *  那类 bug）。缺席的项不写这个键（不是 0——0 会被显示成「首字 0ms」的假数据）。 */
  first_token_ms?: number;
  duration_ms?: number;
  model?: string;
  usage_tokens?: number;
  reasoning_content?: string;
  tool_calls?: Array<{
    id?: string;
    function?: { name: string; arguments?: string };
  }>;
  tool_call_id?: string;
}

/** 改动文件条目（一轮任务结束时的产物汇总——Codex 的 diff 中心形态）。 */
export interface FileChange {
  path: string;
  /** 增加行数 / 删除行数（diff 统计）。 */
  added: number;
  deleted: number;
  /** 精简 diff 文本（Codex 风格渲染：@ 文件头、- 红行、+ 绿行）。 */
  diff: string;
}

/** 项目（侧栏「项目」分组的数据源；对应后端 projects 表）。 */
export interface ProjectMeta {
  id: string;
  name: string;
  path: string;
}

/** 权限模式三档（协议值域：chat.send 与 chat.approval 的 approval 参数）。 */
export type ApprovalMode = "auto" | "confirm" | "strict";

/** chat.send 携带的一张图片附件：data 为纯 base64（不带 data: 前缀）。
 *  与后端 protocol.ChatSendImage 同形（2026-10 图片批次 B）。 */
export interface ChatSendImage {
  mime: string;
  data: string;
}

/** chat.send 携带的一个文件附件：name 为原名（后端净化），data 为纯 base64。
 *  与后端 protocol.ChatSendFile 同形。 */
export interface ChatSendFile {
  name: string;
  data: string;
}

/** 一条消息的附件集合（Composer 暂存 / 队列条目 / SendOptions 共用同一形状——
 *  队列里引用同一数组，不再复制）。 */
export interface SendAttachments {
  images: ChatSendImage[];
  files: ChatSendFile[];
}

/** 发送选项：随消息携带的请求级参数（不传 = 后端默认）。 */
export interface SendOptions {
  /** 推理强度（仅对声明 reasoning 能力的模型生效）。 */
  effort?: string;
  /** 权限模式：auto 高危自动 / confirm 高危确认（默认）/ strict 只读。 */
  approval?: ApprovalMode;
  /** 执行 Agent 的名单 id（空 = 主 Agent——后端按 Agent 四层组合提示词、
   *  模型绑定与工具白名单跑这一轮）。 */
  agent?: string;
  /** 图片附件（视觉请求；空 = 无图）。 */
  images?: ChatSendImage[];
  /** 文件附件（后端落盘 + 消息文本追加附件行；空 = 无文件）。 */
  files?: ChatSendFile[];
}

/**
 * AgentSource 是数据源抽象：demo（脚本编排）与 live（WS 连后端）实现
 * 同一接口。浏览器与 Electron 渲染层复用相同 JSON-RPC 适配器。
 */
export interface AgentSource {
  /** 订阅事件流（返回退订函数）。 */
  subscribe(listener: (ev: AgentEvent) => void): () => void;
  /** 发送消息（一轮开始；opts 携带 effort/approval，缺省 = 后端默认）。 */
  send(sessionId: string, text: string, opts?: SendOptions): void;
  /** 中途改权限档（**立刻生效于运行中的一轮**——不是等下一次 chat.send）。
   *  持久化由设置层负责（settings 管下次开应用，这个方法管当前这一轮）。 */
  setApproval(mode: ApprovalMode): Promise<void>;
  /** 裁决确认门（目标会话显式传入，避免切换焦点后误投）。 */
  confirm(sessionId: string, id: string, allow: boolean): Promise<void>;
  /** 回答 ask_user 的提问（确认门的「提问」形态）：文本答案发给持有挂起
   *  请求的会话（子会话的提问由父会话代理——与 confirm 同一条路由规则）。 */
  answer(sessionId: string, id: string, text: string): Promise<void>;
  /** 取消指定会话的生成。 */
  cancel(sessionId: string): void;
  /** 手动压缩指定会话的历史。 */
  compact(sessionId: string): Promise<CompactOutcome>;
  /** 会话回退：删掉 seq 这条用户消息及其之后的全部历史（内存 + 库）并重算上下文
   *  占用。时间线截断由 rewound 事件驱动（本地乐观 + 后端广播同一条归约路径）；
   *  实现负责在调用失败时用后端真相对齐（重放历史）——本地删了而后端没删，
   *  用户会以为撤回了、刷新又全回来。 */
  rewind(sessionId: string, seq: number): Promise<RewindOutcome>;
  /** 新会话（可选归属项目 id——会话挂在项目分组下）。 */
  newSession(workspace?: string): Promise<string>;
  /** 释放干净项目会话的 worktree 目录，保留分支与会话数据。 */
  releaseWorktree(id: string): Promise<void>;
  /** 起一个后台合并进程（后台任务面板「合并请求」入口——与 merge_request 工具
   *  同一条后端路径 chat.mergeRequest）。返回任务 id；targetBranch 空 = 默认
   *  lxcode/integration。失败抛错（未分组会话 / 已有在跑的合并进程等，文案照后端）。 */
  mergeRequest(sessionId: string, targetBranch?: string): Promise<string>;
  /** 恢复会话。 */
  resumeSession(id: string): Promise<void>;
  /** 重命名会话。 */
  renameSession(id: string, title: string): void;
  /** 归档会话（当前会话被归档时自动切到新会话）。
   *  releaseWorktree 为真时，归档成功后顺带释放该会话（及其子会话）的工作区目录：
   *  只移除干净的检出目录，保留会话记录与 Git 分支——释放失败不影响归档，
   *  原因由应答的 releaseError 带回（见 ArchiveOutcome）。 */
  archiveSession(id: string, releaseWorktree?: boolean): Promise<ArchiveOutcome>;
  /** 从归档恢复。 */
  unarchiveSession(id: string): void;
  /** 读**子会话**的历史（子 Agent = 独立会话，AGENTS.md §2.3：它自己的 messages
   *  与压缩检查点在库里另存一份，父会话历史里没有这些明细）。
   *
   *  用途：DispatchCard 展开时懒加载卡内子执行过程。**不落进 store**——它是那张卡
   *  的只读补充，不参与主时间线归约（实时子事件照旧走 dispatchId 归属）。
   *  失败必须向上抛（调用方显示明确原因，不许静默降级成"子会话本来就是空的"）：
   *  子会话不存在 / 老后端没有 chat.history 的这个用法 / 断连超时都要看得见。 */
  childHistory(sessionId: string): Promise<HistorySnapshot>;
  /** 会话列表。 */
  sessions(): SessionMeta[];
  /** 项目列表。 */
  projects(): ProjectMeta[];
  /** 添加项目（注册目录为 git 仓库——已有仓库不动，没有则 init）。 */
  addProject(name: string, path: string): void;
  /** 读项目守则（项目根 AGENTS.md——项目级「自定义指令」，每轮现读进提示词）。 */
  readInstructions(projectId: string): Promise<ProjectInstructions>;
  /** 写项目守则（项目根 AGENTS.md，原子写）。 */
  saveInstructions(projectId: string, content: string): Promise<void>;
  /** 显示名（顶栏徽标）。 */
  label: string;
  /** 切换后端地址（连接管理「连谁」——真连接切换，不是 UI 状态）。
   *  仅 WSAgent 实现：addr 换成远程后端，token 为该连接的凭证（null = 本机
   *  回落，走宿主 token）。切换即断开重连；会话状态由后端各自持久化。
   *  缺省（DemoAgent）= 无切换。 */
  setBackend?(addr: string, token?: string | null): void;
  /** 模型注册表管理；缺省时设置面板使用独立的本地演示目录。 */
  modelAdmin?: ModelAdminSource;
  /** Agent 名单与拓展目录管理（M1）；缺省时前端用内存种子自管（demo）。 */
  agentAdmin?: AgentAdminSource;
  /** 网页搜索渠道管理（M4）；缺省时前端用内存演示渠道自管。 */
  searchAdmin?: SearchAdminSource;
  /** 后台任务（jobs）；两个实现都提供——顶栏面板与时间线卡片共用它。 */
  jobAdmin?: JobAdminSource;
}

/** 渠道私有设置项的声明（后端 websearch.OptionSpec 的 wire 形态）。
 *
 * 声明来自后端适配器，取值来自用户的 search.json——面板按声明渲染输入项，
 * 所以「渠道多了一个设置」不需要改前端。
 *
 * choices 非空时渲染成下拉：档位/版本这类有限枚举让用户手打，打错了要么被
 * 后端硬校验拦下（白填一次），要么静默改变计费。 */
export interface SearchChannelOption {
  key: string;
  label: string;
  placeholder?: string;
  /** 取值约束与注意事项（渲染在输入项下方）。 */
  hint?: string;
  /** 环境变量回退来源（提示用户也可用环境变量配）。 */
  env_var?: string;
  /** 缺了它渠道就不就绪（面板据此标必填）。 */
  required?: boolean;
  /** 未配置时的默认取值。 */
  default?: string;
  /** 有限枚举（非空时渲染成下拉）。 */
  choices?: string[];
}

/** 搜索渠道（后端 websearch.Channel 的 wire 形态）。
 *
 * 「代码元数据 + 用户配置」合并后的视图：label/desc/doc_url/needs_key 等来自
 * 后端内置的渠道适配器，api_key/base_url/enabled 来自用户的 search.json。
 * 前端不持有渠道清单——新增渠道只改后端，UI 自动出现。 */
export interface SearchChannel {
  id: string;
  label: string;
  desc: string;
  /** 获取 key 的入口（空 = 无需 key）。 */
  doc_url?: string;
  /** 约定环境变量名（提示用户也可用环境变量配）。 */
  env_var?: string;
  needs_key: boolean;
  /** 面板是否应给出 API Key 输入框。
   *
   * 多数渠道与 needs_key 同值；Exa 是「缺 key 也就绪、有 key 走直连」——
   * 只看 needs_key 会把它的输入框藏掉，用户永远进不了直连路径。
   * 可选是因为老后端（未热重载）没这个字段，此时回落到 needs_key。 */
  accepts_key?: boolean;
  /** 需要自填实例地址（自建渠道）。 */
  needs_url: boolean;
  /** 零配置渠道：必须由用户手动启用才生效（如 DuckDuckGo 抓取）。 */
  opt_in?: boolean;
  /** 刻意默认就绪的零配置渠道（如 Exa 的免配置通道）——不填任何配置即可用。 */
  default_ready?: boolean;
  /** 设置面板分组键（free/general/cn/serp/other）。 */
  category: string;
  /** 分组显示名（后端给出，前端不内置一份分类表）。 */
  category_label: string;
  api_key?: string;
  base_url?: string;
  /** 私有设置项的声明（面板据此渲染输入项；空 = 该渠道没有私有设置）。 */
  option_specs?: SearchChannelOption[];
  /** 私有设置项的当前取值（已解析：配置 → 环境变量 → 默认值）。
   *
   * 注意：**可能包含默认值**（用户没填也有值），所以不能用「有没有值」
   * 判断「用户配过没有」——那是 stored 的职责。 */
  options?: Record<string, string>;
  /** 配置文件里是否真的存在该渠道的条目（用户配过东西）。
   *
   * 面板据此决定要不要显示「清除」按钮：零配置渠道（Exa 的免配置通道）
   * 不填任何东西也就绪，拿 configured 当判据会让按钮出现在空卡片上。 */
  stored?: boolean;
  enabled: boolean;
  primary: boolean;
  /** 就绪（凭据/端点齐备且已启用）——搜索时会用到它。 */
  configured: boolean;
}

/** 渠道快照（search.channels.list 结果 + search.changed 载荷）。 */
export interface SearchChannelsSnapshot {
  channels: SearchChannel[];
  /** 主渠道 id（空 = 未指定，按预设顺序降级）。 */
  primary?: string;
  /** 是否存在至少一个就绪渠道——false 时 web_search 不可用。 */
  ready: boolean;
}

/** 单渠道测试结果（search.test）。 */
export interface SearchTestResult {
  provider: string;
  answer?: string;
  results: Array<{ title: string; url: string; snippet?: string }>;
  elapsed_ms: number;
}

/** SearchAdminSource：搜索渠道的查看与管理（后端 search.* 直通）。
 *
 * 与 ModelAdminSource 同模式：UI 依赖能力接口而非具体 WSAgent；
 * 测试按钮只测单渠道、不降级（降级会把「这个渠道坏了」测成「搜索正常」）。 */
export interface SearchAdminSource {
  /** 当前快照（search.channels.list 结果缓存）。 */
  channels(): SearchChannelsSnapshot;
  /** 订阅渠道变化（连接建立/search.changed；返回退订）。 */
  onChannelsChanged(listener: () => void): () => void;
  /** 保存（upsert）渠道配置。options 是渠道私有设置（整体覆盖，键名见 option_specs）。 */
  saveChannel(
    id: string,
    patch: { apiKey?: string; baseUrl?: string; options?: Record<string, string>; enabled?: boolean },
  ): Promise<void>;
  /** 删除渠道配置（回到未配置状态）。 */
  removeChannel(id: string): Promise<void>;
  /** 设主渠道（空 id = 清空）。 */
  setPrimary(id: string): Promise<void>;
  /** 测试单个渠道（不降级）。 */
  testChannel(id: string, query?: string): Promise<SearchTestResult>;
}

/** 项目守则（项目根 AGENTS.md）的读取结果——项目级「自定义指令」。 */
export interface ProjectInstructions {
  /** 守则文件绝对路径（服务端解析：项目根 + 固定文件名）。 */
  path: string;
  content: string;
  /** 文件是否存在（false = 该项目还没写守则，不是错误）。 */
  exists: boolean;
  /** 读取异常说明（如超大跳过）；空 = 正常。 */
  note?: string;
}

/** 后端模型注册表（config.ModelConfig 的 wire 形态，snake_case）。 */
export interface ModelEntry {
  id: string;
  display_name?: string;
  base_url: string;
  api_key?: string;
  format?: string;
  model: string;
  context_window?: number;
  max_output_tokens?: number;
  capabilities?: { tools?: boolean; vision?: boolean; json_output?: boolean; reasoning?: boolean };
  enabled: boolean;
}

/** ModelAdminSource：模型注册表的查看与管理（后端 model.* 直通）。
 * 独立能力接口——UI 面板依赖它而非具体 WSAgent；Demo 不实现。 */
export interface ModelAdminSource {
  /** 当前快照（model.list 结果缓存）。 */
  models(): { models: ModelEntry[]; roles: Record<string, string> };
  /** 订阅注册表变化（连接建立/model.changed；返回退订）。 */
  onModelsChanged(listener: () => void): () => void;
  /** 新增注册表条目；校验失败以 rejected Promise 返回。 */
  addModel(entry: Partial<Omit<ModelEntry, "id">> & { id: string; base_url?: string }): Promise<void>;
  /** 更新（以现有条目为底套 patch）。 */
  updateModel(entry: ModelEntry): Promise<void>;
  /** 删除。 */
  removeModel(id: string): Promise<void>;
  /** 可见性开关。 */
  setModelEnabled(id: string, enabled: boolean): Promise<void>;
  /** 角色绑定（default/vision）。 */
  setRole(role: string, modelId: string): Promise<void>;
  /** 可选模型目录的厂商清单（refresh = 忽略 TTL 强制重拉）。 */
  catalogProviders(refresh?: boolean): Promise<CatalogProviderList>;
  /** 某厂商的模型明细（目录按需拉，不随厂商清单一起下发）。 */
  catalogModels(provider: string, refresh?: boolean): Promise<CatalogModelList>;
  /** 探测一个端点**实际**提供的模型清单。
   *  id = 已注册条目（用它那条的连接配置，key 不过 wire）；
   *  或 baseUrl + apiKey + format = 还没进注册表的新端点。 */
  discoverModels(input: { id?: string; baseUrl?: string; apiKey?: string; format?: string }): Promise<DiscoverResult>;
}

/** 目录里的一个模型（后端 modelcatalog.Model 的 wire 形态）。 */
export interface CatalogModel {
  id: string;
  name?: string;
  context_window?: number;
  max_output_tokens?: number;
  tools?: boolean;
  vision?: boolean;
  json_output?: boolean;
  reasoning?: boolean;
  /** 非正常状态（deprecated / beta）；空 = 正常。 */
  status?: string;
  /** 发布日期 YYYY-MM-DD（目录按它倒序）。 */
  released?: string;
}

/** 目录里的一个厂商（后端 modelcatalog.Provider 的 wire 形态）。 */
export interface CatalogProvider {
  id: string;
  name: string;
  doc?: string;
  /** 端点地址：注册表 base_url 的建议值。 */
  api?: string;
  /** key 的环境变量名（表单提示用）。 */
  env?: string[];
  format: string;
  model_count: number;
  /** 只在 catalogModels 的结果里有明细。 */
  models?: CatalogModel[];
}

/** 厂商清单载荷。 */
export interface CatalogProviderList {
  fetched_at: string;
  /** 手上这份已过期且这次没刷新成功——UI 要如实提示，不假装新鲜。 */
  stale?: boolean;
  providers: CatalogProvider[];
}

/** 某厂商的模型清单载荷。 */
export interface CatalogModelList {
  fetched_at: string;
  stale?: boolean;
  provider: string;
  models: CatalogModel[];
}

/** 探测到的一个模型。元数据由后端按 id 从目录回填（best-effort）；缺省 = 未知。 */
export interface DiscoveredModel {
  id: string;
  name?: string;
  context_window?: number;
  max_output_tokens?: number;
  tools?: boolean;
  vision?: boolean;
  json_output?: boolean;
  reasoning?: boolean;
}

/** 端点探测结果。 */
export interface DiscoverResult {
  /** 实际请求的地址（用户填的 base_url 会被归一化，回显便于核对）。 */
  endpoint: string;
  format: string;
  models: DiscoveredModel[];
}

/** AgentAdminSource：Agent 名单与拓展目录的查看与管理（后端 agent.与
 * catalog.两组方法直通——M1 注册表与目录）。与 ModelAdminSource 同模式：
 * UI 依赖能力接口而非具体 WSAgent；Demo 不实现（前端内存种子自管）。 */
export interface AgentAdminSource {
  /** 当前 Agent 名单（agent.list 结果缓存——主 Agent 首位）。 */
  agents(): AgentAdminEntry[];
  /** 拓展目录（catalog.*.list 结果缓存）。 */
  modules(): AgentAdminModule[];
  tools(): AgentAdminTool[];
  mcpServers(): AgentAdminMcServer[];
  /** 订阅名单/目录变化（连接建立/agent.changed/catalog.changed；返回退订）。 */
  onChanged(listener: () => void): () => void;
  addAgent(agent: AgentAdminEntry): Promise<void>;
  updateAgent(agent: AgentAdminEntry): Promise<void>;
  removeAgent(id: string): Promise<void>;
  addModule(module: AgentAdminModule): Promise<void>;
  updateModule(module: AgentAdminModule): Promise<void>;
  removeModule(id: string): Promise<void>;
  addTool(tool: AgentAdminTool): Promise<void>;
  updateTool(tool: AgentAdminTool): Promise<void>;
  removeTool(id: string): Promise<void>;
  addMcServer(server: AgentAdminMcServer): Promise<void>;
  updateMcServer(server: AgentAdminMcServer): Promise<void>;
  removeMcServer(id: string): Promise<void>;
}

/** 后端 Agent 名单条目（protocol.AgentEntry 的 wire 形态，snake_case——
 * is_main 与前端的 isMain 映射在适配层做）。 */
export interface AgentAdminEntry {
  id: string;
  name: string;
  desc: string;
  color: string;
  model: string;
  tools: string[];
  workflow: string;
  skills: string[];
  delegates: string[];
  approval: string;
  enabled: boolean;
  is_main?: boolean;
  prompt: string;
  protocol: string;
  custom?: boolean;
}

/** 后端模块目录条目（protocol.ModuleEntry）。 */
export interface AgentAdminModule {
  id: string;
  desc: string;
  kind: "process" | "skill";
  body: string;
  custom: boolean;
}

/** 后端工具目录条目（protocol.ToolEntry）。 */
export interface AgentAdminTool {
  id: string;
  desc: string;
  risk: "low" | "high";
  source: "builtin" | "binary" | "mcp";
  params?: Array<{ name: string; type: string; required?: boolean; desc?: string }>;
  doc?: string;
  server?: string;
  command?: string;
  example?: string;
  package_file?: string;
  custom: boolean;
}

/** 后端 MCP 服务器条目（protocol.McServerEntry）。 */
export interface AgentAdminMcServer {
  id: string;
  desc: string;
  transport: "stdio" | "sse";
  command?: string;
  args?: string[];
  env?: Record<string, string>;
  url?: string;
  enabled: boolean;
  custom: boolean;
  /** 运行期连接状态：connected | error | stopped。
   *
   * 与 enabled 是**两件事**：enabled = 用户想开，status = 真的连上了
   * （连不上时 enabled 仍是 true——卡片必须显示真实状态，否则用户以为能用）。 */
  status?: McpRuntimeStatus;
  /** 已列举到的工具数（未连接时为 0）。 */
  tool_count?: number;
  /** 最近一次连接/列举失败的原因（成功时为空）。 */
  last_error?: string;
  /** stdio 子进程 stderr 的尾部（诊断用）。 */
  stderr?: string;
}

/** MCP 服务器的运行期连接状态。 */
export type McpRuntimeStatus = "connected" | "error" | "stopped";
