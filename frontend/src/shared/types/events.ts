import type { SendAttachments, TodoItem, ApprovalMode, FileChange, HistorySnapshot } from "./chat";
import type { AttachmentRef } from "./attachments";
import type { ContextUsage, SessionStats } from "./context";
import type { AssistantConfig } from "./settings";
import type { FileRestoreResult } from "./rewind";
import type { JobInfo } from "./jobs";

// UI 事件流：确认请求 + AgentEvent 全量（demo 与 live 两个 AgentSource 实现都发这套事件）。

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
  | { type: "userMessage"; sessionId: string; text: string; seq?: number;
      /** 随消息的附件引用（对齐 llm.Message.Attachments——后端落盘后的元数据）。
       *  缺省 = 无附件（老后端/纯文本消息）。渲染附件卡的数据源。 */
      attachments?: AttachmentRef[];
      /** dispatchId 非空 = 子会话的消息（派发方注入的任务说明书，后端 childEmitter
       *  带归属上抛）：主时间线不画（dispatch 卡 = 一行摘要），双投进子会话自己的
       *  state——子会话视图里「新一轮的任务」靠它出现。 */
      dispatchId?: string;
      /** task = 双投进子会话 state 时打的标记（reduceSessionStates：原本带
       *  dispatch_id 的 userMessage 就是任务说明书）。子会话时间线据此渲染成
       *  「任务」块而不是用户气泡——用户没法在子会话页发言，带这个标记的消息
       *  唯一来源是 agent_dispatch 的派发。 */
      task?: boolean }
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
  | { type: "toolResult"; sessionId: string; id: string; name: string; content: string; isError: boolean; dispatchId?: string;
      /** 这次执行共享的附件（share_file）——与结果消息 llm.Message.Attachments
       *  同一份元数据。缺省 = 无。 */
      attachments?: AttachmentRef[] }
  | { type: "confirmRequest"; sessionId: string; request: ConfirmRequest }
  | { type: "todoUpdated"; sessionId: string; items: TodoItem[];
      /** dispatchId 非空 = 子会话的清单更新（带归属）：主时间线更新派发卡徽标，
       *  子会话页由双投吃到。空 = 主会话自己的计划条。 */
      dispatchId?: string }
  | { type: "done"; sessionId: string; usageTokens: number; finishReason: string; dispatchId?: string; context?: ContextUsage;
      /** 整段会话统计（缺省 = 还没有任何一步 / 纯内存模式）。一轮里的每个 done 都带
       *  一份最新的，取最后收到的那份即可（与 DSH 的投影随事件推进同语义）。 */
      stats?: SessionStats;
      /** 每轮计时（后端 internal/agent/timing.go）：首 token 延迟与本轮耗时。**工具轮/非流式
       *  回放没有「首字」这个时刻 → 整键缺席**（不是 0——0 会被显示成「首字 0ms」的假数据）。 */
      firstTokenMs?: number; durationMs?: number;
      /** 本轮实际使用的模型 id（Agent 绑定优先、否则 default 角色）。 */
      model?: string;
      /** 助手最终正文的预览（done 携带的 Message.Content，截 ~200 字符）——
       *  系统通知正文的来源。缺席 = 后端没给（老后端/工具轮），不编空串。 */
      content?: string }
  | { type: "error"; sessionId: string; message: string; aborted: boolean;
      /** force = 超窗强制压缩仍失败的告警（非一轮生成失败）：可选键（老后端缺席），
       *  客户端可据此把提示条做成更强的样式。 */
      force?: boolean }
  | { type: "dispatchStart"; sessionId: string; dispatchId: string; childSessionId?: string; agentId: string; agentName: string; agentColor: string; task: string;
      /** run_task（助手 → 项目）派发的目标项目名（可选键，缺席 = 常规 agent_dispatch
       *  委派——主域派发不受影响，卡不显示项目）。 */
      projectName?: string;
      /** 主 Agent 预拆的清单初稿（todo 契约批③；缺席 = 子自拆）。 */
      todos?: TodoItem[] }
  | { type: "dispatchEnd"; sessionId: string; dispatchId: string; childSessionId?: string; result: string; isError: boolean; usageTokens?: number;
      /** 派发结束时子会话清单的定格值（缺席 = 没建过清单）。 */
      finalTodos?: TodoItem[] }
  /** 请求失败不代表生成失败：不得清空会话、定格正文或解除确认卡。 */
  | { type: "operationError"; message: string }
  /** 传输层连接态翻转（WSAgent 真实 WS 的 open/close——顶栏脉冲点与连接管理
   *  的「已连接」指示的数据源）。DemoAgent 是本地模拟，恒已连接，订阅即发 true。 */
  | { type: "connectionChanged"; connected: boolean }
  /** busy 事件。dispatchId 非空 = 子会话的忙闲翻转（带归属）：主会话侧过滤
   *  （不翻主会话的「生成中」/不触发发送缓冲区边界），双投进子会话自己的
   *  state——子会话页的「生成中」行与停止钮靠它出现。 */
  | { type: "busy"; sessionId: string; busy: boolean; dispatchId?: string }
  /** 某会话的权限档被改了（后端广播 chat.approvalChanged——多客户端/壳+浏览器
   *  同时开着时靠它保持一致；载荷是规范化后的档位，空 = confirm）。
   *  消费方是**会话状态归约**（shared/reduce.ts 按会话落 UIState.approval），
   *  不再有全局设置可同步。 */
  | { type: "approvalChanged"; sessionId: string; approval: ApprovalMode }
  /** 某会话**选中的模型**被改了（后端广播 chat.modelChanged——多客户端/壳+浏览器
   *  同时看着同一条会话时靠它保持一致）。
   *
   *  与 `model.changed` 的分工：那个是全局模型注册表变了（客户端重拉 model.list），
   *  这个是**会话级**选择变了（只更新那条会话的选择态，不动注册表缓存）。消费方
   *  是会话状态归约（shared/reduce.ts 按 sessionId 落 UIState.model）。 */
  | { type: "modelChanged"; sessionId: string; model: string }
  /** 新会话默认审批档被改了（settings.defaultApproval.changed——后端 behavior.json
   *  的行为开关，多客户端同步）。只影响**之后新建**的会话；既有会话的档位是
   *  它们自己的落库值，不受影响。 */
  | { type: "defaultApprovalChanged"; approval: ApprovalMode }
  /** 助手配置被改了（assistant.configChanged——set 后 / 手改 assistant.json 被
   *  热加载检出，多客户端同步）。载荷 = AssistantConfig（get 应答同形状）。
   *  消费方是拓展页的助手页签（配置视图对齐——别的客户端/助手自己改了性格，
   *  打开页签时看到的是最新值）。 */
  | { type: "assistantConfigChanged"; config: AssistantConfig }
  | { type: "sessionChanged"; id: string; reason: string }
  /** 会话列表本身变了（重命名/归档/恢复）——UI 重读 sessions()。 */
  | { type: "sessionsChanged"; running?: string[] }
  /** 项目列表变了（添加）——UI 重读 projects()。 */
  | { type: "projectsChanged" }
  /** 一轮任务的产物汇总（改动文件 + diff 统计——验收视图）。 */
  | { type: "filesChanged"; sessionId: string; files: FileChange[] }
  /** 历史被压缩（前缀替换成摘要检查点）——UI 插一条「已压缩历史」标记块。
   *  dispatchId 非空 = 子会话自己的压缩（归属进 dispatch 卡内，不进主时间线）。 */
  /** 历史被压缩（前缀替换成摘要检查点）——UI 插一条「已压缩历史」标记块。
   *  dispatchId 非空 = 子会话自己的压缩（归属进 dispatch 卡内，不进主时间线）。
   *  context = 压缩**后**的占用（后端 runCompaction 已重算——压缩改写了历史形状，
   *  旧数字一定是错的）；键缺席 = 未知（老后端），归约器保持旧值不编数。 */
  | { type: "compacted"; sessionId: string; before: number; after: number; shadowed: number; summary: string; manual?: boolean; dispatchId?: string;
      /** 压缩路径的人话说明（"自动压缩（摘要）" / "自动压缩（只裁剪）"——只裁剪降级
       *  路径 shadowed=0，标记块用它区分形态）。可选键（老后端缺席）。 */
      reason?: string; context?: ContextUsage }
  /** 压缩开始/结束信号（chat.compacting）：阻塞发送 + 多客户端感知。
   *  compacting=true = 开始（该会话提交入队），false = 结束（成功失败都发——
   *  前端状态复位）。dispatchId 非空 = 子会话自己的压缩（双投进子会话 state）。 */
  | { type: "compacting"; sessionId: string; compacting: boolean; dispatchId?: string }
  /** 会话回退（chat.rewound）：seq 这条用户消息及其之后的全部历史已离开当前路径
   *  （**一条没删**——旧分支保留在树里，随时能切回来）。
   *
   *  归约是**幂等**的：本地乐观截断（发起撤回时立刻清空时间线）已经把锚点删掉了，
   *  后端广播随后到达时找不到锚点就原样返回——不幂等的话重复到达会再切一刀，
   *  把更早的消息也一起删掉。 */
  | { type: "rewound"; sessionId: string; seq: number; removed: number; context?: ContextUsage;
      /** 重算后的整段统计（口径仍是折叠整段日志——切分支不改数字）。 */
      stats?: SessionStats }
  /** 叶指针变更（chat.leafChanged，rewind 与 setLeaf 都发）。
   *
   *  **到达时必须重拉 history + tree**（在 ws 层做，不进归约器）：leaf 是**共享
   *  状态**，可能是别的客户端动的——本地没有"该怎么变"的知识，后端才是事实源。
   *  setLeaf 只发这个事件（不发 chat.rewound）：老客户端收到 rewound 会截断时间线，
   *  而切到另一条分支时那个截断是错的。 */
  | { type: "leafChanged"; sessionId: string; leafSeq: number;
      /** 移动前的叶（客户端据此判断"是不是别人动的"）。 */
      prevLeafSeq?: number;
      removed: number;
      /** 重算后的上下文占用（缺省 = 未知 → 指示器中性态）。 */
      context?: ContextUsage;
      /** 本次带文件还原时的结果（含子会话）。 */
      files?: FileRestoreResult }
  /** 历史载入（连接/切会话后）——重建对话视图。
   *
   *  `paging: true` = 这是**上翻分页**的增量应答（chat.history 的 before_seq）：
   *  归约器把这一页**前插**进现有时间线，而不是重建。区分必须靠这个显式标记，
   *  不能按快照内容猜——`has_more` 在首屏 tail 应答上也会由前端推导出来
   *  （deriveTailPaging），而重拉（rewind/setLeaf 副作用）必须**重建**（否则
   *  回放出来的「中断占位卡」永远修不回来）。 */
  | { type: "historyLoaded"; sessionId: string; history: HistorySnapshot; paging?: boolean }
  /** 会话历史的**装载失败**（resumeSession 的 resume/history 两路任一失败——
   *  P0-1「点不进去永久卡死」的修复）：置进该会话 state 的 historyLoadError，
   *  switching 层让位给错误态 + 重试（重试 = 重新 resumeSession 全量）。成功
   *  装载（historyLoaded）与骨架化都清位。这是**客户端本地事实**（请求失败），
   *  不经 wire——由 WSAgent.resumeSession 直接 emit。 */
  | { type: "historyLoadError"; sessionId: string; message: string }
  /** 后台任务起了/结束（时间线**不**再建任务卡——面板缓存与通告链路消费事件）。
   *  sessionId = 任务归属会话；无归属（空）的任务只进顶栏全局面板。 */
  | { type: "jobStarted"; sessionId: string; job: JobInfo }
  /** 后台任务结束（带 EndedBy——UI 据此显示「你停的 / 它挂了 / 超时 /
   *  后端重启中断」）。同一个 job 的 started 与 settled 共用一张卡。 */
  | { type: "jobSettled"; sessionId: string; job: JobInfo };
