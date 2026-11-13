// 后端事件 → AgentEvent 的**纯映射**（无 I/O、无 this、可单测）。
//
// 与 index.ts 的 reactTo 分工：这里只回答「这个事件在前端长什么样」，
// 「要不要重拉后端事实源」留在 reactTo。混在一起就没法单独测映射——
// 而映射恰恰是最容易写错、也最值得钉住的一层（字段名、默认值、归属）。
import type { AgentEvent, AttachmentRef, ConfirmRequest, ContextUsage, FileRestoreResult, SessionStats, TodoItem } from "../../shared/types";
import { jobFromWire } from "../../shared/jobs";
import { normalizeApproval } from "../../shared/approval";

/** 返回 null = 这个事件不产出前端事件（由 reactTo 的副作用分支处理，或与前端无关）。 */
export function mapEvent(method: string, params: unknown): AgentEvent | null {
  // 助手域事件（assistant.*，助手模式 P1）：payload 与 chat 同构、只换事件名
  //（internal/protocol/assistant.go——assistantEventName 的映射表）。归一成
  // chat.* 等价名走**同一份**映射（一份解析代码两域共用），再打上 space 标记
  //（store 按它把事件归约进助手域的 assistantStates，不进主域）。方法名表
  // 必须与后端 assistantEventName 逐字对齐（tests/assistant-reduce.test.mjs
  // 读两边源码钉住）。未知的 assistant.* 方法名不猜测归属——返回 null（不产出
  // 前端事件），新事件接入时表里加一行。
  if (method.startsWith("assistant.")) {
    const chat = ASSISTANT_TO_CHAT[method];
    if (!chat) return null;
    const ev = mapChatEvent(chat, params);
    // space 标记（store 按它分流到助手域 assistantStates）：AgentEvent 各变体
    // 没有逐个声明 space 键——标记只在 assistant 路径写入，运行时判定一处
    //（store.ts 的 handleAgentEvent），与同一份注释互为表里。
    return ev ? { ...ev, space: "assistant" } as unknown as AgentEvent : null;
  }
  return mapChatEvent(method, params);
}

/** assistant.* → chat.* 等价方法名（与 internal/server/emit.go 的
 *  assistantEventName 互为逆映射；键 = protocol/assistant.go 的事件名常量）。 */
const ASSISTANT_TO_CHAT: Record<string, string> = {
  "assistant.userMessage": "chat.userMessage",
  "assistant.delta": "chat.delta",
  "assistant.toolCall": "chat.toolCall",
  "assistant.toolResult": "chat.toolResult",
  "assistant.confirmRequest": "chat.confirmRequest",
  "assistant.done": "chat.done",
  "assistant.error": "chat.error",
  "assistant.busy": "chat.busy",
  "assistant.dispatchStart": "chat.dispatchStart",
  "assistant.dispatchEnd": "chat.dispatchEnd",
  "assistant.compacted": "chat.compacted",
  "assistant.compacting": "chat.compacting",
  "assistant.todoUpdated": "todo.updated",
  "assistant.filesChanged": "files.changed",
  "assistant.approvalChanged": "chat.approvalChanged",
  "assistant.modelChanged": "chat.modelChanged",
};

function mapChatEvent(method: string, params: unknown): AgentEvent | null {
  const p = (params ?? {}) as Record<string, unknown>;
  const sessionId = String(p.session_id ?? "");
  // dispatch_id 归属（子 Agent 执行的事件——store 挂 dispatch 卡）
  const dispatchId = p.dispatch_id ? String(p.dispatch_id) : undefined;
  switch (method) {
    case "connection.ready":
      return { type: "ready", server: String(p.server ?? ""), version: String(p.version ?? ""), busy: Boolean(p.busy) };
    case "chat.userMessage": {
      // 协议载荷把消息包在 message 对象里；兼容早期扁平形状。
      const message = (p.message ?? {}) as Record<string, unknown>;
      const text = String(message.content ?? p.content ?? p.text ?? "");
      // seq = 撤回锚点（ChatMessage 上的字段，与历史回放是同一个类型——两条路径
      // 都读 message.seq，不另立字段名）。老后端没有它 → 键**缺席**（不是
      // undefined 值的键）：块不可撤回，但绝不炸（AGENTS.md §5 坑 11）。
      const seq = message.seq;
      // 附件引用（llm.Message.Attachments——后端落盘后的展示元数据）：
      // 数组空/缺失 → 键缺席（无附件块不带这个键）。
      const atts = Array.isArray(message.attachments) && message.attachments.length
        ? (message.attachments as AttachmentRef[])
        : undefined;
      const ev: AgentEvent = typeof seq === "number"
        ? { type: "userMessage", sessionId, text, seq, ...(atts ? { attachments: atts } : {}) }
        : { type: "userMessage", sessionId, text, ...(atts ? { attachments: atts } : {}) };
      return dispatchId ? { ...ev, dispatchId } : ev;
    }
    case "chat.delta":
      return { type: "delta", sessionId, kind: String(p.kind) as "text" | "reasoning", text: String(p.text ?? ""), dispatchId };
    case "chat.toolCall":
      return { type: "toolCall", sessionId, id: String(p.id), name: String(p.name), arguments: String(p.arguments ?? ""), dispatchId };
    case "chat.toolResult": {
      // 附件（share_file 共享）：数组空/缺失 → 键缺席。
      const rAtts = Array.isArray(p.attachments) && (p.attachments as unknown[]).length
        ? (p.attachments as AttachmentRef[])
        : undefined;
      return {
        type: "toolResult", sessionId, id: String(p.id), name: String(p.name),
        content: String(p.content ?? ""), isError: Boolean(p.is_error), dispatchId,
        ...(rAtts ? { attachments: rAtts } : {}),
      };
    }
    case "chat.dispatchStart":
      return {
        type: "dispatchStart",
        sessionId: String(p.owner_session_id ?? ""),
        dispatchId: String(p.dispatch_id ?? ""),
        childSessionId: p.session_id ? String(p.session_id) : undefined,
        agentId: String(p.agent_id ?? ""),
        agentName: String(p.agent_name ?? ""),
        agentColor: String(p.agent_color ?? "#3b82f6"),
        task: String(p.task ?? ""),
        // run_task（助手 → 项目）派发的目标项目名（可选键，缺席 = 常规委派；
        // 键缺席纪律：不写这个键，与 todos 同一条约定）。
        ...(p.project_name ? { projectName: String(p.project_name) } : {}),
        // todo 契约批③：主 Agent 预拆的清单初稿（可选键，缺席 = 子自拆）。
        // 缺席时**不写这个键**（不是写 undefined——deepStrictEqual 按键比对，
        // 且「键缺席 = 未知」是本仓库 wire 的统一约定）。
        ...(Array.isArray(p.todos) ? { todos: p.todos as TodoItem[] } : {}),
      };
    case "chat.dispatchEnd":
      return {
        type: "dispatchEnd",
        sessionId: String(p.owner_session_id ?? ""),
        dispatchId: String(p.dispatch_id ?? ""),
        childSessionId: p.session_id ? String(p.session_id) : undefined,
        result: String(p.result ?? ""),
        isError: Boolean(p.is_error),
        usageTokens: Number(p.usage_tokens ?? 0),
        // 派发结束时子会话清单的定格值（可选键，缺席 = 没建过清单）。
        // 缺席时不写这个键（与 todos 同一条约定）。
        ...(Array.isArray(p.final_todos) ? { finalTodos: p.final_todos as TodoItem[] } : {}),
      };
    case "chat.compacted":
      // 历史被压缩（可能是别的客户端触发的——广播给所有端）。
      // 子会话自己的压缩带 dispatch_id：归属进卡内，不插到主时间线。
      return {
        type: "compacted",
        sessionId,
        before: Number(p.before ?? 0),
        after: Number(p.after ?? 0),
        shadowed: Number(p.shadowed ?? 0),
        summary: String(p.summary ?? ""),
        manual: Boolean(p.manual),
        dispatchId: p.dispatch_id ? String(p.dispatch_id) : undefined,
        // reason：压缩路径的人话说明（可选键，老后端缺席 → undefined）。
        ...(typeof p.reason === "string" && p.reason ? { reason: p.reason } : {}),
        // context：压缩**后**的占用（后端已重算并落库——压缩改写了历史形状，旧数字
        // 一定是错的）。老后端缺席 → undefined，归约器保持旧值不编数。
        context: (p.context as ContextUsage | undefined) ?? undefined,
      };
    case "chat.compacting":
      // 压缩开始/结束信号（compacting true/false，阻塞发送 + 多客户端感知）。
      // dispatch_id 非空 = 子会话自己的压缩（双投进子会话 state）。compacting
      // 显式 Boolean——true/false 都必须上 wire（后端无 omitempty）。
      return {
        type: "compacting", sessionId, compacting: Boolean(p.compacting), dispatchId,
      };
    // 后台任务：载荷**就是 JobInfo 本身**（不是包一层）。时间线归属取
    // owner_session_id（子 Agent 起的任务挂在**父会话**的时间线上——子会话不进
    // 侧栏，按执行会话上卡等于用户在主对话里什么都看不到），空才回落
    // session_id；两者都空 = 无归属（只进顶栏全局面板）。
    case "job.started":
    case "job.settled": {
      const job = jobFromWire(p);
      const owner = job.owner_session_id || job.session_id;
      if (method === "job.started") return { type: "jobStarted", sessionId: owner, job };
      return { type: "jobSettled", sessionId: owner, job };
    }
    case "chat.rewound":
      // 会话回退：seq 这条用户消息及其之后的全部历史**离开当前路径**（一条没删——
      // 旧分支保留在树里，随时能切回来）。这个事件既是「别的客户端回退了」的同步，
      // 也是本地乐观截断的权威确认——归约器按同一份判定处理，重复到达是幂等 no-op
      // （见 reduce.ts 的 reduceRewound）。
      // context：后端重算后的占用（旧路径的测量一定是错的）。未知时整键缺席——
      // 那时归约器回落中性态「—」，不编一个数。
      return { type: "rewound", sessionId, seq: Number(p.seq ?? 0), removed: Number(p.removed ?? 0), context: (p.context as ContextUsage | undefined) ?? undefined,
        // 重算后的整段统计（口径是折叠整段日志，切分支不改数字——拍板项 3）
        stats: (p.stats as SessionStats | undefined) ?? undefined };
    case "chat.leafChanged":
      // 叶指针变更（rewind 与 setLeaf 都发）：leaf 是**会话的共享状态**，可能是别的
      // 客户端动的。前端不在归约器里猜「该怎么变」——ws 层收到就重拉 history + tree
      // （见 index.ts 的事件副作用），这里只把载荷映射出来供提示与诊断用。
      return {
        type: "leafChanged", sessionId,
        leafSeq: Number(p.leaf_seq ?? 0),
        // prev_leaf_seq 缺席（0）时不写这个键：0 是合法的「空路径」，编不出来源
        ...(typeof p.prev_leaf_seq === "number" && p.prev_leaf_seq > 0 ? { prevLeafSeq: p.prev_leaf_seq } : {}),
        removed: Number(p.removed ?? 0),
        context: (p.context as ContextUsage | undefined) ?? undefined,
        files: (p.files as FileRestoreResult | undefined) ?? undefined,
      };
    case "chat.confirmRequest":
      return { type: "confirmRequest", sessionId, request: p as unknown as ConfirmRequest };
    case "todo.updated":
      // dispatchId 非空 = 子会话的清单更新（带归属）：主时间线更新派发卡徽标，
      // 子会话页由双投吃到。空 = 主会话自己的计划条。
      return { type: "todoUpdated", sessionId, items: (p.items as TodoItem[]) ?? [], dispatchId };
    case "chat.done":
      return {
        type: "done",
        sessionId,
        usageTokens: Number(p.usage_tokens ?? 0),
        finishReason: String(p.finish_reason ?? "stop"),
        // 每轮计时/模型：缺席就是 undefined（**不填 0**——0 会被显示成「首字 0ms」的假数据）
        firstTokenMs: typeof p.first_token_ms === "number" ? p.first_token_ms : undefined,
        durationMs: typeof p.duration_ms === "number" ? p.duration_ms : undefined,
        model: typeof p.model === "string" && p.model !== "" ? p.model : undefined,
        dispatchId,
        // 正文预览（done 的 Message.Content——通知正文的来源）。截 ~200 字符：
        // 通知是预览不是全文；undefined = 后端没给（老后端/工具轮），不编空串。
        ...(p.message && typeof (p.message as { content?: unknown }).content === "string"
          ? { content: String((p.message as { content: string }).content).slice(0, 200) }
          : {}),
        // 上下文占用只随主轮来（子轮的 done 不带——后端已按 dispatch 归属收口）
        context: (p.context as ContextUsage | undefined) ?? undefined,
        // 整段会话统计同样只随主轮来（口径见 shared/types.ts 的 SessionStats）
        stats: (p.stats as SessionStats | undefined) ?? undefined,
      };
    case "chat.error":
      return { type: "error", sessionId, message: String(p.message ?? ""), aborted: Boolean(p.aborted),
        // force：超窗强制压缩仍失败的告警（可选键，老后端缺席）。
        ...(p.force === true ? { force: true } : {}) };
    case "chat.busy":
      // dispatchId 非空 = 子会话的忙闲（带归属）：归约侧与发送缓冲区边界都按它
      // 过滤（主会话的忙闲不被子会话翻动），双投路径把它送进子会话自己的 state。
      return { type: "busy", sessionId, busy: Boolean(p.busy), dispatchId };
    case "chat.approvalChanged":
      // 某会话的权限档被改了（多客户端同步）。消费方是会话状态归约
      //（shared/reduce.ts 按 sessionId 落 UIState.approval）——本层只管形状。
      return { type: "approvalChanged", sessionId, approval: normalizeApproval(p.approval) };
    case "chat.modelChanged":
      // 某会话**选中的模型**变了（多客户端同步，与 chat.approvalChanged 同款）。
      // 与 model.changed 的分工：那个是全局注册表变了（重拉 model.list），这个是
      // 会话级选择——消费方是会话状态归约（shared/reduce.ts 按 sessionId 落 model）。
      return { type: "modelChanged", sessionId, model: String(p.model_id ?? "") };
    case "settings.defaultApproval.changed":
      // 新会话默认审批档变了（后端 behavior.json 的行为开关，多客户端同步）。
      return { type: "defaultApprovalChanged", approval: normalizeApproval(p.approval) };
    case "assistant.configChanged":
      // 助手配置被改了（set 后 / 手改 assistant.json 被热加载检出——全局事件，
      // 不带 session_id，所以不进 ASSISTANT_TO_CHAT 的会话事件映射）。
      return {
        type: "assistantConfigChanged",
        config: {
          ...(typeof p.name === "string" && p.name ? { name: p.name } : {}),
          ...(typeof p.persona === "string" && p.persona ? { persona: p.persona } : {}),
          ...(Array.isArray(p.skills) && p.skills.length ? { skills: p.skills.map(String) } : {}),
          ...(typeof p.workflow === "string" && p.workflow ? { workflow: p.workflow } : {}),
        },
      };
    case "session.changed":
      return { type: "sessionChanged", id: String(p.id ?? ""), reason: String(p.reason ?? "") };
    case "files.changed": {
      // 一轮的产物汇总（验收视图——后端按 edit/write_file 收集）
      const f = p as { files?: Array<{ path: string; added: number; deleted: number; diff: string }> };
      if (!Array.isArray(f.files)) return null;
      return {
        type: "filesChanged",
        sessionId,
        files: f.files.map((x) => ({ path: x.path, added: x.added, deleted: x.deleted, diff: x.diff })),
      };
    }
    default:
      // model.changed / project.changed / agent.changed / catalog.changed /
      // search.changed 只重拉事实源，不产出前端事件（见 index.ts 的 reactTo）。
      return null;
  }
}
