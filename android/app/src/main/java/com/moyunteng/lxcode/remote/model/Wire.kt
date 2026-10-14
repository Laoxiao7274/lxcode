// wire 类型层 —— 安卓端与后端协议（internal/protocol + internal/llm + internal/tools）
// 对齐的数据类型与显示辅助。前身是 P0 原型的 MockData.kt：假数据本体已删除
// （无连接时 UI 显示连接引导态，不显示编造的会话），只留下「逐字段对齐协议」的
// 类型定义——页面层只消费这些类型，接真后端时（RealBackend）不需要改页面。
package com.moyunteng.lxcode.remote.model

// ===== 连接（对齐桌面端 shared/connections.tsx 的 RemoteConn + 内置本机）=====

/**
 * 一个后端连接。
 *
 * 桌面端对应：`RemoteConn { id, name, addr, token }`；本机是内置回落默认（不在 remotes 里）。
 *
 * @param id 连接 id（"local" = 本机）
 * @param name 显示名（本机恒为「本机」）
 * @param addr `host:port`（本机恒 `127.0.0.1:7789`）
 * @param token 连接凭证（本机为空）
 * @param local 是否本机（本机不显示 Token 掩码、不显示删除）
 */
data class BackendConn(
    val id: String,
    val name: String,
    val addr: String,
    val token: String,
    val local: Boolean = false,
)

/** 遮罩 Token：保头尾各 4 位、中间 ••••（对齐桌面端 `maskToken`）。 */
fun maskToken(token: String): String =
    if (token.length <= 8) token else token.take(4) + "••••" + token.takeLast(4)

/** 从地址带出显示名（对齐桌面端 `nameFromAddr`：取 host 段）。 */
fun nameFromAddr(addr: String): String {
    val a = addr.trim()
    if (a.isEmpty()) return a
    val noScheme = a.substringAfter("://", a)
    return noScheme.substringBefore("/").substringBefore(":")
}

/** 地址格式校验（对齐桌面端 RemoteForm 的正则）。 */
private val ADDR_RE = Regex("""^(https?://)?[a-zA-Z0-9.-]+(:\d{1,5})(/.*)?$""")

/** 地址合法（空串也算「还没填」，不报错）。 */
fun addrValid(addr: String): Boolean = addr.isEmpty() || ADDR_RE.matches(addr)

// ===== 会话与项目（对齐 protocol.SessionMeta / 桌面端 ProjectMeta）=====

/**
 * 会话列表条目。
 *
 * 对齐 `protocol.SessionMeta`：`id` / `title` / `updated_at` / `messages` /
 * `archived` / `workspace`。额外两列是**客户端投影**：
 *  * [updatedAt] 在协议里是 ISO 时间戳，桌面端展示成「3 分钟前」——安卓侧转换后的展示串；
 *  * [running] 是 `chat.busy` 广播的投影（协议里它由 busy 事件维护，不是 SessionMeta 的字段）。
 */
data class SessionMeta(
    val id: String,
    val title: String,
    val updatedAt: String,
    val messages: Int,
    val archived: Boolean = false,
    val workspace: String = "",
    val running: Boolean = false,
)

/** 项目（对齐 protocol.ProjectMeta：`id` / `name` / `path`）。 */
data class ProjectMeta(val id: String, val name: String, val path: String)

// ===== 对话块（对齐 llm.Message / protocol 事件 / 桌面端 ThreadBlock）=====

/** todo 条目（对齐 `tools.TodoItem`：`content` / `status`）。 */
data class TodoItem(val content: String, val status: String) // pending | active | done

/**
 * 派发卡内的一行子工具实时态（chat.toolCall / chat.toolResult 带 dispatch_id 的事件归并）。
 * [running] = 已调用未回结果；[durationMs] 在结果到达时按本地计时补齐（协议 toolResult
 * 不带耗时，tool 消息的 duration_ms 只在历史里有——实时流用本地计时，可回放历史对齐）。
 */
data class ChildTool(
    val id: String,
    val name: String,
    val title: String,
    val argsSummary: String,
    val running: Boolean,
    val isError: Boolean,
    val durationMs: Long,
)

/**
 * 对话线程里的一个渲染块。
 *
 * 与协议/内核的对应关系：
 *  * [User] → `llm.Message{role:"user"}`；
 *  * [Assistant] → `llm.Message{role:"assistant", content, reasoning_content}`
 *    （`first_token_ms` 取 [Assistant.reasoningMs] 的语义位）；
 *  * [Tool] → `llm.Message{role:"tool"}` + `tool_calls`（结果与调用按 id 配对）；
 *  * [Dispatch] → `chat.dispatchStart/End`（`agent_name` / `task` / `session_id`）；
 *  * [Compaction] → `chat.compacted`（`summary` / `shadowed` / `before` / `after` / `manual`）；
 *  * [Todo] → `tools.TodoItem` 列表（`todo.updated` 事件）；
 *  * [Notice] → 通告前缀的 user 消息（对齐桌面端 shared/notices.ts）；
 *  * [Error] → `chat.error`（或连接层错误）。
 */
sealed interface ThreadBlock {
    data class User(val text: String) : ThreadBlock

    data class Assistant(
        val content: String,
        val reasoning: String?,
        val reasoningMs: Long,
        val model: String,
        // ===== 真实后端的实测值（历史回放带 first_token_ms / usage_tokens÷duration_ms）=====
        // 首字延迟（chat.done 的 first_token_ms；null = 未知）
        val firstTokenMs: Long? = null,
        // 生成速度 tok/s（usage_tokens / duration_ms；null = 未知）
        val tokPerSec: Double? = null,
        // 是否流式进行中（true = 打字机逐字揭示；false = 历史整块呈现）——
        // 打字机只用于实时流式增量，历史回放必须直接显示全文（对齐桌面端
        // AnswerBody 的 committed/tail 语义：历史 = snap 全量落下）。
        val streaming: Boolean = false,
    ) : ThreadBlock

    data class Tool(
        // 调用 id（chat.toolCall/toolResult 与 llm.ToolCall.id 配对用）
        val id: String = "",
        val name: String,
        val title: String,
        val argsSummary: String,
        val command: String,
        val cwd: String,
        val output: String?,
        val running: Boolean,
        val isError: Boolean,
        val durationMs: Long,
    ) : ThreadBlock

    data class Dispatch(
        val agentName: String,
        val task: String,
        val conclusion: String,
        val sessionId: String,
        val done: Boolean,
        val isError: Boolean,
        // dispatch_id = 事件归属键（子确认/子工具归并进卡）；
        // childTools = 子会话至今的工具调用数（卡内一行「子会话执行中 · N 个工具调用」）；
        // childRuns = 子会话实时工具流（chat.toolCall/toolResult 带 dispatch_id 归并进卡，
        // 卡内可展开看到子工具名/状态/耗时——桌面端「子会话标签页实时流」的安卓单页等价物）；
        // childTodosDone/childTodosTotal = 子 Agent 的清单进度（todo.updated 带 dispatch_id
        // 的广播归并——手机上看派发进度的轻量形态；-1 = 该派发没有清单）
        val dispatchId: String = "",
        val childTools: Int = 0,
        val childRuns: List<ChildTool> = emptyList(),
        val childTodosDone: Int = -1,
        val childTodosTotal: Int = 0,
    ) : ThreadBlock

    data class Compaction(
        val summary: String,
        val shadowed: Int,
        val before: Int,
        val after: Int,
        val manual: Boolean,
    ) : ThreadBlock

    data class Todo(val items: List<TodoItem>) : ThreadBlock

    /**
     * 系统提示条（后台任务通告 / 重复调用提醒）。
     *
     * 这类消息在历史与实时流里都是**真实 user 角色消息**（模型必须把它当用户回合
     * 才会回应——后端硬约束），但它不是用户说的话：按**文本前缀**识别（前缀表在
     * RealBackend，对齐桌面端 shared/notices.ts），渲染成一行提示条而不是用户气泡。
     */
    data class Notice(val label: String, val text: String) : ThreadBlock

    data class Error(val message: String) : ThreadBlock
}

// ===== 上下文占用 / 会话统计（对齐 protocol.ContextUsage / protocol.SessionStats）=====

/**
 * 上下文占用（对齐 `protocol.ContextUsage`）。
 *
 * `used` 优先取 provider 真实 prompt 总量；五个分类按 used 归一（之和恒等于 used）。
 * [estimated] 为真 = 估算值，UI 上必须与真实用量区分（加 `~` 前缀）。
 */
data class ContextUsage(
    val used: Int,
    val window: Int,
    val system: Int,
    val tools: Int,
    val toolResults: Int,
    val messages: Int,
    val reasoning: Int,
    val estimated: Boolean = false,
)

/**
 * 整段会话统计（对齐 `protocol.SessionStats`）。
 *
 * 时间单位毫秒；生成速度 = [decodeTokens] / [decodeMs]。
 */
data class SessionStats(
    val turns: Int,
    val steps: Int,
    val llmMs: Long,
    val toolMs: Long,
    val ttftMs: Long,
    val ttftSteps: Int,
    val decodeMs: Long,
    val decodeTokens: Int,
    val inputTokens: Int,
    val cacheReadTokens: Int,
    val cacheWriteTokens: Int,
    val outputTokens: Int,
    val legacyTokens: Int = 0,
)

/** 生成速度（tok/s）——口径与桌面端 `decodeTokensPerSec` 一致。 */
fun SessionStats.tokensPerSec(): Double? =
    if (decodeMs <= 0 || decodeTokens <= 0) null else decodeTokens * 1000.0 / decodeMs

/** 时间胶囊文案（对齐桌面端 `timePillLabel`：「N 轮 · M 步 · X tok/s」）。 */
fun SessionStats.timePillLabel(): String {
    val parts = mutableListOf("$turns 轮", "$steps 步")
    tokensPerSec()?.let { parts.add(String.format("%.1f tok/s", it)) }
    return parts.joinToString(" · ")
}

/** 工具耗时人性化（毫秒 → 「1.2s」/「820ms」）。 */
fun formatMs(ms: Long): String =
    if (ms < 1000) "${ms}ms" else String.format("%.1fs", ms / 1000.0)

// ===== 模型注册表（composer 模型菜单数据源，对齐桌面端 settings 的 providers 投影）=====

/**
 * 一个模型条目（对齐 ModelConfig 的注册表投影：`id` / `display_name` / `format` /
 * `capabilities.reasoning`）。[efforts] 为空 = 该模型不支持推理强度（菜单里的
 * 跳转行整个隐藏）——派生自能力声明，对非推理端点传 effort 参数会 400。
 */
data class ModelMeta(
    val id: String,
    val name: String,
    val desc: String,
    val efforts: List<String>,
)

/** 按端点（base_url host）分组的模型列表（桌面端 providers 里 connected+enabled 的投影）。 */
data class ModelGroup(val name: String, val models: List<ModelMeta>)

// ===== 推理强度档位（固定 4 档——后端协议值域：minimal/low/medium/high）=====

/** 全部 effort 档 id（对齐桌面端 settings.tsx EFFORTS 的 id 顺序）。 */
val EFFORT_IDS: List<String> = listOf("minimal", "low", "medium", "high")

/** effort 中文 label（桌面端 settings.tsx：minimal→极低 / low→低 / medium→中 / high→高）。 */
val EFFORT_LABELS: Map<String, String> = linkedMapOf(
    "minimal" to "极低",
    "low" to "低",
    "medium" to "中",
    "high" to "高",
)

/** effort 档 id → 中文 label（未知 id 原样回落）。 */
fun effortLabel(id: String): String = EFFORT_LABELS[id] ?: id

/** 本机地址（对齐桌面端 `LOCAL_ADDR`）。 */
const val LOCAL_ADDR = "127.0.0.1:7789"
