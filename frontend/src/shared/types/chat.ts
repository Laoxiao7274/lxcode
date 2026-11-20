import type { ContextUsage, SessionStats } from "./context";
import type { ConfirmRequest } from "./events";
import type { AttachmentRef } from "./attachments";
import type { FileRestoreResult } from "./rewind";

// 会话域：清单项、会话/项目元数据、历史快照与消息、发送参数（chat.* 的请求/应答 wire 形态）。

/** 任务清单项（对齐 tools.TodoItem）。 */
export interface TodoItem {
  content: string;
  status: "pending" | "active" | "done";
}

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
  /** 此刻是否在生成（老后端缺席 = 空闲，不编状态）。新连上的客户端错过
   *  busy=true 广播时，列表据此直接亮运行指示。 */
  running?: boolean;
}

/** hello 应答（协议版本匹配 + 连接时刻的忙闲快照）。 */
export interface HelloResult {
  server: string;
  version: string;
  busy: boolean;
  /** 握手时刻在生成的会话 id（缺省 = 无在跑，不编状态）。 */
  busySessions?: string[];
}

/** 历史快照（chat.history 的载荷——重建视图用）。 */
export interface HistorySnapshot {
  sessionId: string;
  messages: HistoryMessage[];
  busy: boolean;
  pending: ConfirmRequest | null;
  /** 快照里的任务清单（wire `todos`）。**整键缺席 = 未知**（老后端/演示源）——
   *  归约器据此保留本地已显示的清单，不猜；键在（哪怕是空数组）= 后端事实
   *  「无清单」，如实覆盖（用户清空清单后刷新不该复活旧清单）。 */
  todos?: TodoItem[];
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
  /** 该会话落库的输入草稿（chat.draft 写的）。缺省 = 后端没存过（老后端/空串）
   *  ——预填侧据此保留本地内存记账，不把缺席当"草稿为空"去清。 */
  draft?: string;
  /** child = 这是**子会话**的历史（childHistory 装载的快照）。子会话时间线里
   *  非通告的 user 消息只有一种来源——主 Agent 派发的任务说明书（子会话页只读，
   *  用户无法在子会话里发言），渲染成「任务」块而不是用户气泡；主会话快照不带
   *  这个键，user 消息照旧是用户气泡（两条路径共用 historyBlocks，靠它分流）。 */
  child?: boolean;
  /** 这次回放对应的叶（0 = 空路径；缺省 = 老后端没回这个键）。
   *  树面板拿它高亮「你在这里」，也拿它判断「回到最新」该往哪走。 */
  leafSeq?: number;
  /** 分页装载（chat.history 的 tail/before_seq）的窗口元数据。
   *
   *  `hasMore` 只在 **before_seq 请求**的应答里出现（协议 HasMore 的 omitempty）：
   *  缺省 = 这不是一次分页应答（老后端/首次 tail 拉取）——前端按「不再上翻」处理。
   *  `firstSeq` = 本窗口首条消息的 seq（下一次 before_seq 的 anchor；空窗口 0）。
   *  两个键都在时才能继续上翻：拿 0 去请求会被后端判 CodeInvalidParams。 */
  hasMore?: boolean;
  firstSeq?: number;
  /** 该会话**此刻正在跑**的派发快照（缺省 = 没有/老后端）。
   *  观察端（安卓/新开窗口）错过 dispatchStart 广播时靠它把回放重建的
   *  「中断」占位卡补上子会话 id——点开子会话与卡内进度事件路由由此恢复。
   *  派发结束即从后端注销（不会把已收尾的报成运行中）。 */
  runningDispatches?: RunningDispatchInfo[];
}

/** 一次进行中派发的观察快照（chat.history 的 running_dispatches 元素）。
 *  与 dispatchStart 事件字段的对应：dispatchId ↔ dispatch_id、childSessionId ↔
 *  session_id、agentName ↔ agent_name、task ↔ task。 */
export interface RunningDispatchInfo {
  dispatchId: string;
  childSessionId: string;
  agentName: string;
  task: string;
}

/** 历史消息（llm.Message 的 wire 形态）。 */
export interface HistoryMessage {
  role: string;
  content: string;
  /** 撤回锚点（后端 ChatMessage 的字段，与实时 chat.userMessage 的载荷是同一个
   *  类型）。老后端没有它 → 块不可撤回（动作禁用），绝不炸（AGENTS.md §5 坑 11）。 */
  seq?: number;
  /** 消息级父指针（会话树的边；0/缺省 = root）。与 `sessions.parent_id`（**会话级**
   *  父子，子 Agent = 独立会话）是两个不同的东西，一眼分得开。老后端没有它。 */
  parent_seq?: number;
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
  /** 附件引用（user 消息 = 用户发的图/文件；tool 消息 = share_file 共享的）。
   *  缺省 = 无附件（老行/纯文本消息）。 */
  attachments?: AttachmentRef[];
  /** 图片引用（对齐 llm.ImageRef——视觉请求通道的老字段）：老后端没有
   *  attachments 列时的**回落**渲染源（图片缩略图卡）。 */
  images?: Array<{ path: string; mime: string }>;
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
 *  与后端 protocol.ChatSendImage 同形（2026-10 图片批次 B）。
 *
 *  **传输形态（2026-12 用户拍板：本地路径优先，base64 兜底）**：Electron 壳里
 *  有真实磁盘路径的文件走 path 形态（path 非空、data 为空——后端按路径读盘，
 *  WS 帧里没有文件内容）；粘贴截图、虚拟文件（无路径）与浏览器模式仍走 base64。
 *  file 是**仅内存**的降级凭据：后端读不到 path（文件在 staging 后被移动/删除）
 *  时，用它把同一个文件读成 base64 重发一次——绝不序列化（File 过 wire/草稿
 *  持久化都会退化成 {}，两处注释都钉了这条）。 */
export interface ChatSendImage {
  mime: string;
  data: string;
  /** 展示名（可选；后端 path 形态没带时回落路径 basename，base64 形态回落「图片」）。 */
  name?: string;
  /** 本地真实路径（webUtils.getPathForFile 解析）；非空 = path 形态。 */
  path?: string;
  /** File 对象（仅内存，不进 wire）；path 形态发送失败时降级 base64 重发用。 */
  file?: File;
}

/** chat.send 携带的一个文件附件：name 为原名（后端净化），data 为纯 base64。
 *  与后端 protocol.ChatSendFile 同形。path/file 的语义与 ChatSendImage 同款。 */
export interface ChatSendFile {
  name: string;
  data: string;
  /** 本地真实路径；非空 = path 形态（后端按路径读盘，data 为空）。 */
  path?: string;
  /** 文件夹内相对路径（拖文件夹改造，2026-12，wire 键 rel_path）：展示用——
   *  后端记进消息元数据与「[附件]」行，落盘名仍为随机 hex。空 = 散文件
   *  （老客户端形态）。 */
  rel_path?: string;
  /** File 对象（仅内存，不进 wire）——降级 base64 重发用。 */
  file?: File;
}

/** 一条消息的附件集合（Composer 暂存 / 队列条目 / SendOptions 共用同一形状——
 *  队列里引用同一数组，不再复制）。 */
export interface SendAttachments {
  images: ChatSendImage[];
  files: ChatSendFile[];
}

/** 发送选项：随消息携带的请求级参数（不传 = 后端默认）。
 *
 *  审批档**不在其中**（2026-12 拍板）：approval 是后端持久化的会话级状态
 *  （chat.approval 改 → 落库 → approvalChanged 广播），会话创建时用 behavior
 *  默认档初始化——chat.send 再带 approval 会静默覆盖会话档且不广播，正是
 *  「不持久化/不隔离」的根因之一。协议侧参数仍保留解析（老客户端兼容）。 */
export interface SendOptions {
  /** 推理强度（仅对声明 reasoning 能力的模型生效）。 */
  effort?: string;
  /** 执行 Agent 的名单 id（空 = 主 Agent——后端按 Agent 四层组合提示词、
   *  模型绑定与工具白名单跑这一轮）。 */
  agent?: string;
  /** 图片附件（视觉请求；空 = 无图）。 */
  images?: ChatSendImage[];
  /** 文件附件（后端落盘 + 消息文本追加附件行；空 = 无文件）。 */
  files?: ChatSendFile[];
}

/** chat.history 请求参数（wire `ChatHistoryParams`）。session_id 缺省 = 当前焦点会话。 */
export interface ChatHistoryParams {
  session_id?: string;
  /** 尾部窗口：只拉当前历史的最后 N 条（0/缺省 = 全量——老客户端语义不变）。
   *  慢链路（公网隧道）分页加载用：先拉尾部，上滑到顶再增量补头部。
   *  与 before_seq 组合时语义变为「anchor 之前最多 N 条」的窗口大小。 */
  tail?: number;
  /** 前缀分页 anchor（2026-12 安卓向上翻页）：>0 = 返回这条消息**之前**（不含它）
   *  最多 tail 条。seq 是 llm.Message.Seq（库序号，每条消息都带），客户端原样记录
   *  再原样传回；不在历史里 / ≤0 → CodeInvalidParams。缺省 0 = 不分页。 */
  before_seq?: number;
}
