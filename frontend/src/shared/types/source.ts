import type { AgentEvent } from "./events";
import type { AttachmentRef } from "./attachments";
import type { TodoItem, SessionMeta, ProjectMeta, HistorySnapshot, ApprovalMode, SendOptions, ChatSendImage, ChatSendFile } from "./chat";
import type { ContextUsage, SessionStats } from "./context";
import type { CompactOutcome, RewindOutcome, SetLeafOutcome, ChatTreeResult, ArchiveOutcome, FileRestoreResult } from "./rewind";
import type { GitOverview } from "./git";
import type { ProjectInstructions, AssistantConfig, TodoPromptSource } from "./settings";
import type { ModelAdminSource } from "./catalog";
import type { AgentAdminSource } from "./agent-admin";
import type { SearchAdminSource } from "./search";
import type { JobAdminSource, LogsSource, LogsTailResult } from "./jobs";

// AgentSource / AssistantSource —— 数据源抽象（demo 与 live 实现同一接口）。

/**
 * AgentSource 是数据源抽象：demo（脚本编排）与 live（WS 连后端）实现
 * 同一接口。浏览器与 Electron 渲染层复用相同 JSON-RPC 适配器。
 */
export interface AgentSource {
  /** 订阅事件流（返回退订函数）。 */
  subscribe(listener: (ev: AgentEvent) => void): () => void;
  /** 发送消息（一轮开始；opts 携带 effort 等请求级参数，缺省 = 后端默认）。
   *  审批档不在 opts 里——它是后端持久化的会话级状态（见 SendOptions）。 */
  send(sessionId: string, text: string, opts?: SendOptions): void;
  /** 中途改**当前会话**的权限档（chat.approval：后端落库 + 广播，立刻生效于
   *  运行中的一轮）。会话档经 approvalChanged 广播 / chat.history 的 approval
   *  回读——归约层按会话落状态。 */
  setApproval(mode: ApprovalMode): Promise<void>;
  /** 落库某会话的输入草稿（chat.draft：防抖写入，后端只存 text——多端共享的
   * 「后写覆盖」语义；不广播，切会话/装载历史时经 chat.history 的 draft 键取别人的
   *  最新值）。sessionId 显式传入——写哪个会话由调用方在事件发生那一刻决定，
   *  不取连接焦点（防抖回调时焦点可能已经切走）。demo 源 no-op（无后端）。 */
  setDraft(sessionId: string, text: string): Promise<void>;
  /** 改**当前会话**选中的模型（chat.model：后端落库 + 广播，只改这一条会话）。
   *
   *  与 ModelAdminSource.setRole 的分工：那个改全局 default 角色（= 新会话的默认
   *  模型，设置页用），这个改当前会话——原先模型选择只能走 role.set，于是一个会话
   *  切模型所有会话（含之后新建的）全跟着变，正是"模型选择作用域"那个 bug。
   *  会话 id 取连接焦点（UI 的操作永远发生在某个已聚焦的会话上）；**显式传
   *  sessionId 时按它寻址**——子会话页切模型带子会话 id（视图打开期间连接焦点
   *  仍是父会话，不能落错会话）。 */
  setSessionModel(modelId: string, sessionId?: string): Promise<void>;
  /** 读「新会话默认审批档」（后端 behavior.json 的行为开关）。 */
  defaultApproval(): Promise<ApprovalMode>;
  /** 写「新会话默认审批档」（settings.defaultApproval.set：落盘 + 广播）。
   *  只影响之后新建的会话，不改既有会话的档位。 */
  setDefaultApproval(mode: ApprovalMode): Promise<void>;
  /** 裁决确认门（目标会话显式传入，避免切换焦点后误投）。 */
  confirm(sessionId: string, id: string, allow: boolean): Promise<void>;
  /** 回答 ask_user 的提问（确认门的「提问」形态）：文本答案发给持有挂起
   *  请求的会话（子会话的提问由父会话代理——与 confirm 同一条路由规则）。 */
  answer(sessionId: string, id: string, text: string): Promise<void>;
  /** 取消指定会话的生成。 */
  cancel(sessionId: string): void;
  /** 手动压缩指定会话的历史。 */
  compact(sessionId: string): Promise<CompactOutcome>;
  /** 重拉一条会话的历史（chat.history → historyLoaded 事件全量重建）。
   *  压缩成功后用：检查点卡要落到语义位置（前缀处）、被压缩的旧气泡要从时间线
   *  消失——这两条只能靠重拉对齐（chat.compacted 事件只是往时间线末尾插标记块）。
   *  可选能力：缺省实现（演示源）没有可拉的后端历史，返回时抛错或 no-op 由
   *  实现自定——调用方（use-compaction）在缺席时跳过重拉。 */
  reloadHistory?(sessionId: string): Promise<void>;
  /** 会话回退（chat.rewind）：把 leaf 移到 seq 的父节点（**一条不删**——旧分支保留
   *  在树里，随时能在右侧面板切回来）并重算上下文占用。时间线截断由 rewound 事件
   *  驱动（本地乐观 + 后端广播同一条归约路径）；实现负责在调用失败、或带文件还原
   *  发生冲突（应答 removed=0）时用后端真相对齐（重放历史）——本地删了而后端没删，
   *  用户会以为回退了、刷新又全回来。
   *
   *  files = true 时同时还原文件（fail-closed：有冲突则整批不执行、leaf 不动）。 */
  rewind(sessionId: string, seq: number, files?: boolean): Promise<RewindOutcome>;
  /** 切叶（chat.setLeaf）：leaf 直接指向 seq（**该节点留在路径上**，继续对话即从它
   *  长出新分支）。seq = 0 = 空路径（回到第一条消息之前）。
   *
   *  与 rewind 的差别只有落点差一格：rewind → parent(seq)（那条消息自己离开路径，
   *  因为它要改写重发）；setLeaf → seq 本身（「从这里接着长」）。
   *
   *  **不返回 messages**（后端刻意不回）：切叶之后要重载的不止消息，还有树/统计/
   *  上下文/检查点下标——实现必须在应答后自己重拉 chat.history。
   *  老后端没有这个方法 → 实现应抛错（调用方据此禁用行尾按钮，不静默 no-op）。 */
  setLeaf(sessionId: string, seq: number, files?: boolean): Promise<SetLeafOutcome>;
  /** 读会话树骨架（chat.tree）：节点 + 父子关系 + 当前 leaf + 分支摘要。
   *
   *  `maxNodes` 缺省 2000（0 = 后端缺省；传 0 也用于「加载全部」——后端按 0 回落
   *  缺省值，**不是**无上限，见 protocol.ChatTreeParams）。老后端没有这个方法 →
   *  实现应抛错，面板据此回落到线性轮次视图（不报错、不空屏）。 */
  tree(sessionId: string, opts?: { maxNodes?: number; include?: "compact" | "full" }): Promise<ChatTreeResult>;
  /** 新会话（可选归属项目 id——会话挂在项目分组下）。 */
  newSession(workspace?: string): Promise<string>;
  /** 起一个后台合并进程（**遗留**：合并流程已退役——后端如实回说明并拒绝）。
   *  保留签名便于将来恢复隔离模式。 */
  mergeRequest(sessionId: string, targetBranch?: string): Promise<string>;
  /** 恢复会话。 */
  resumeSession(id: string): Promise<void>;
  /** 忘掉一批会话的「已装载」记忆（可选能力，缺省 = no-op）。
   *  S3 states 淘汰的配套：连接层的 loadedSessions 是「本地秒回」的依据——记忆
   *  还在而 store 的 state 已被淘汰时，重进该会话会秒回到一份空 state。淘汰方
   *  （store）在淘汰时把记忆一并清掉，重进走全量装载（session.resume +
   *  chat.history，busy 会话另有 busy_sessions 补拉链兜底）。 */
  forgetLoadedSessions?(ids: readonly string[]): void;
  /** 重命名会话。 */
  renameSession(id: string, title: string): void;
  /** 归档会话（当前会话被归档时自动切到新会话）。原先的 releaseWorktree 参数与
   *  ArchiveOutcome 的 releasedWorktree/releaseError 已随直连模式移除
   *  （2026-12 用户拍板）：归档不再顺带释放 worktree 目录。 */
  archiveSession(id: string): Promise<ArchiveOutcome>;
  /** 从归档恢复。 */
  unarchiveSession(id: string): void;
  /** 读**子会话**的历史（子 Agent = 独立会话，AGENTS.md §2.3：它自己的 messages
   *  与压缩检查点在库里另存一份，父会话历史里没有这些明细）。
   *
   *  用途：DispatchCard 展开时懒加载卡内子执行过程。**不落进 store**——它是那张卡
   *  的只读补充，不参与主时间线归约（实时子事件照旧走 dispatchId 归属）。
   *  失败必须向上抛（调用方显示明确原因，不许静默降级成"子会话本来就是空的"）：
   *  子会话不存在 / 老后端没有 chat.history 的这个用法 / 断连超时都要看得见。 */
  childHistory(sessionId: string, beforeSeq?: number): Promise<HistorySnapshot>;
  /** 上翻分页：拉 beforeSeq 之前的一页历史（chat.history 的 before_seq + tail），
   *  结果经 historyLoaded 增量装载（与首屏同一条归约路径）。主时间线与子会话页
   *  共用；缺省实现（演示源）= 没有更早的历史。 */
  historyBefore?(sessionId: string, beforeSeq: number): Promise<void>;
  /** 会话列表。 */
  sessions(): SessionMeta[];
  /** 项目列表。 */
  projects(): ProjectMeta[];
  /** 添加项目（注册目录为 git 仓库——已有仓库不动，没有则 init）。
   *  成功 resolve 新项目的 ProjectMeta（含 id——调用方「创建后立即进入」的依据）；
   *  失败 reject（调用方显示原因，对话框不关）。 */
  addProject(name: string, path: string): Promise<ProjectMeta>;
  /** 从项目列表移除注册（**只解绑**：不删磁盘目录、不删会话——会话留在库里，
   *  侧栏不再按项目分组）。失败向上抛（有会话在跑时后端拒绝，调用方显示原因）。 */
  removeProject(id: string): Promise<void>;
  /** 读项目守则（项目根 AGENTS.md——项目级「自定义指令」，每轮现读进提示词）。 */
  readInstructions(projectId: string): Promise<ProjectInstructions>;
  /** 写项目守则（项目根 AGENTS.md，原子写）。 */
  saveInstructions(projectId: string, content: string): Promise<void>;
  /** Git 管理页：项目主检出的只读快照（状态 / 分支 / 最近提交）。
   *  projectId 空 = 当前会话归属的项目（未分组会话报错）。失败向上抛。 */
  gitOverview(projectId?: string): Promise<GitOverview>;
  /** Git 管理页：主检出工作区里单个文件的未提交差异（懒加载——点开文件才拉）。
   *  失败向上抛（文件不可读 / 路径越界等，文案照后端）。 */
  gitDiff(projectId: string, path: string): Promise<string>;
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
  /** todo-first 提示词档位（行为开关）；缺省时设置面板用本地状态（demo）。 */
  todoPromptAdmin?: TodoPromptSource;
  /** 后台任务（jobs）；两个实现都提供——顶栏面板与时间线卡片共用它。 */
  jobAdmin?: JobAdminSource;
  /** 后端日志查看（logs.tail，桌面错误日志查看器 2026-12）；缺省（demo）时
   *  设置面板「日志」分区回自解释文案（演示模式没有后端日志可看）。
   *  两个实现都提供：WSAgent 转发 LogsAdmin 域；DemoAgent 实现自解释拒绝。 */
  logsAdmin?: LogsSource;
  /** 读后端日志尾（logs.tail 的顶层转发——与 jobAdmin 的 readJobLog 同款
   *  双路访问形态）。缺省（demo 无后端日志）= undefined。 */
  tail?(bytes?: number): Promise<LogsTailResult>;
  /** 助手域（assistant.* 方法域，助手模式 P1）；缺省时助手页签显示"当前源不支持"。
   *  DemoAgent 也实现（演示模式无后端也要能全量跑助手页签）。 */
  assistant?: AssistantSource;
}

/** 助手域（assistant.* 方法域，助手模式 P1 前端接线）。
 *
 * 后端（internal/server/dispatch_assistant.go）：assistant.* 是**独立方法域**，
 * 只寻址助手域运行时（space='assistant' 的会话）——主会话 id 传进来报错，
 * 主域 chat.* 传助手会话 id 同样报错（域隔离，前端按域路由，不交叉）。
 * 参数与应答**复用 chat 既有 wire 形状**（ChatSendParams / ChatHistoryParams /
 * SessionResult / ToolConfirmParams / CompactResult——只换方法名），事件
 * （assistant.* 十四事件）payload 与 chat 同构、只换事件名——所以本接口的
 * 类型面全部复用 chat 域类型，不另立 wire 形状（golden 也因此无需新样本：
 * 复用的每个 wire 类型都已有样本覆盖）。
 *
 * 与 AgentSource 的分工：AgentSource 是主域（chat.*）的门面；助手域是另一套
 * 会话空间（侧栏不出现、id 空间隔离），经这个可选能力口挂载——与 modelAdmin /
 * jobAdmin 同模式。DemoAgent 也实现（演示模式无后端也要能全量跑 UI）。 */
export interface AssistantSource {
  /** 助手域会话列表（assistant.list → SessionMeta[] + running 位；wire 形状
   *  与 session.list 同源——一份解析代码两域共用）。 */
  list(): Promise<SessionMeta[]>;
  /** 某项目的助手任务子会话（assistant.tasks → 同构应答）：项目模式侧栏
   *  「来自助手」小节的数据源。按 updated_at 倒序；空数组 = 该项目没有
   *  助手任务（小节不渲染）。 */
  tasks(projectId: string): Promise<SessionMeta[]>;
  /** 新助手会话（assistant.new → SessionResult）。 */
  newSession(): Promise<string>;
  /** 发送一轮（assistant.send，参数 = ChatSendParams 的子集）。images/files =
   *  随消息的附件（2026-12 助手附件——wire 形状与校验和 chat.send 同款；
   *  空 = 无附件；只附件不打字也允许）。 */
  send(sessionId: string, text: string, opts?: { effort?: string; approval?: string; images?: ChatSendImage[]; files?: ChatSendFile[] }): void;
  /** 装载助手会话历史（assistant.history → historyLoaded 事件，助手域路由）。
   *  失败向上抛（AssistantPage 显示原因，不静默降级成"空历史"）。 */
  history(sessionId: string, beforeSeq?: number): Promise<HistorySnapshot>;
  /** 装载助手会话 run_task 子会话的历史（子会话库行继承父 space='assistant'，
   *  域内寻址走 assistant.history——与主域 childHistory 同形状）。 */
  childHistory(sessionId: string, beforeSeq?: number): Promise<HistorySnapshot>;
  /** 取消（assistant.cancel：寻址助手会话或它正在跑的 run_task 子会话）。 */
  cancel(sessionId: string): void;
  /** 裁决助手会话的确认门（assistant.confirm；answer 非空 = ask_user 提问回答）。 */
  confirm(sessionId: string, id: string, allow: boolean, answer?: string): Promise<void>;
  /** 手动压缩（assistant.compact）。 */
  compact(sessionId: string): Promise<CompactOutcome>;
  /** 改助手会话的执行档位（assistant.approval——chat.approval 同款只换域）。
   *  乐观对齐由 assistant.approvalChanged 广播回写（与主域同一纪律）。 */
  setApproval(sessionId: string, mode: string): void;
  /** 切助手会话的模型（assistant.model——chat.model 同款只换域，助手内置
   *  Agent 不做 BindAgentModel）。不乐观改，由 assistant.modelChanged 对齐。 */
  setModel(sessionId: string, modelId: string): void;
  /** 读助手配置（assistant.config.get → AssistantConfig；各字段空 = 未设置）。 */
  getConfig(): Promise<AssistantConfig>;
  /** 写助手配置（assistant.config.set——**全量替换**：带什么存什么；
   *  后端校验 skills/workflow 的 id 真实存在，无效报错上抛）。 */
  setConfig(cfg: AssistantConfig): Promise<void>;
}
