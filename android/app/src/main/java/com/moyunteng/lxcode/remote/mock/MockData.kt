// mock 数据层 —— 安卓端 P0 原型唯一的假数据源（不连后端、不发网络请求）。
//
// 纪律：字段命名与语义**逐条对齐后端协议**（internal/protocol + internal/llm + internal/tools），
// 每个 Kotlin 属性都在 KDoc 里标注它对应的协议 json 字段。将来接真后端时，这一层整体换成
// WS JSON-RPC 的解析结果即可，页面层不需要改（页面只消费这些类型）。
package com.moyunteng.lxcode.remote.mock

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
 *  * [updatedAt] 在协议里是 ISO 时间戳，桌面端展示成「3 分钟前」——原型里直接存展示串；
 *  * [running] 是 `busyBySession[session_id]` 的投影（协议里它由 chat.done/turnStart
 *    事件维护，不是 SessionMeta 的字段）。
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

/** 项目（对齐桌面端 ProjectMeta：`id` / `name` / `path`）。 */
data class ProjectMeta(val id: String, val name: String, val path: String)

// ===== 对话块（对齐 llm.Message / protocol 事件 / 桌面端 ThreadBlock）=====

/** todo 条目（对齐 `tools.TodoItem`：`content` / `status`）。 */
data class TodoItem(val content: String, val status: String) // pending | active | done

/** 工具调用（对齐 `llm.ToolCall`：`id` / `type` / `function{name,arguments}`）。 */
data class ToolCall(val id: String, val name: String, val arguments: String)

/**
 * 对话线程里的一个渲染块。
 *
 * 与协议/内核的对应关系：
 *  * [User] → `llm.Message{role:"user"}`；
 *  * [Assistant] → `llm.Message{role:"assistant", content, reasoning_content}`
 *    （`first_token_ms` 取 [reasoningMs] 的语义位）；
 *  * [Tool] → `llm.Message{role:"tool"}` + `tool_calls`（结果与调用按 id 配对）；
 *  * [Dispatch] → `chat.dispatchStart/End`（`agent_name` / `task` / `session_id`）；
 *  * [Compaction] → `chat.compacted`（`summary` / `shadowed` / `before` / `after` / `manual`）；
 *  * [Todo] → `tools.TodoItem` 列表（`todo.updated` 事件）；
 *  * [Error] → `chat.turnError`（或连接层错误）。
 */
sealed interface ThreadBlock {
    data class User(val text: String) : ThreadBlock

    data class Assistant(
        val content: String,
        val reasoning: String?,
        val reasoningMs: Long,
        val model: String,
        // ===== 真实后端追加的实测值（mock 数据不填 → 恒 null，展示行走旧硬编码）=====
        // 首字延迟（chat.done 的 first_token_ms；null = 未知）
        val firstTokenMs: Long? = null,
        // 生成速度 tok/s（usage_tokens / duration_ms；null = 未知）
        val tokPerSec: Double? = null,
    ) : ThreadBlock

    data class Tool(
        // 调用 id（chat.toolCall/toolResult 与 llm.ToolCall.id 配对用；mock 数据为空串）
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
    ) : ThreadBlock

    data class Compaction(
        val summary: String,
        val shadowed: Int,
        val before: Int,
        val after: Int,
        val manual: Boolean,
    ) : ThreadBlock

    data class Todo(val items: List<TodoItem>) : ThreadBlock

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
 * 一个模型条目（对齐桌面端 ModelMeta：`id` / `name` / `desc` / `efforts`）。
 * [efforts] 为空 = 该模型不支持推理强度（菜单里的跳转行整个隐藏）。
 */
data class MockModelMeta(
    val id: String,
    val name: String,
    val desc: String,
    val efforts: List<String> = emptyList(),
)

/** 按提供商分组的模型列表（桌面端 providers 里 connected+enabled 的投影）。 */
data class MockModelGroup(val name: String, val models: List<MockModelMeta>)

// ===== 假数据本体 =====

object MockData {

    /** 本机地址（对齐桌面端 `LOCAL_ADDR`）。 */
    const val LOCAL_ADDR = "127.0.0.1:7789"

    /** 连接列表：1 条本机 + 3 条远程。 */
    val connections: List<BackendConn> = listOf(
        BackendConn("local", "本机", LOCAL_ADDR, "", local = true),
        BackendConn("r-work", "工作室主机", "10.0.0.8:7789", "a1b2c3d4-e5f6-7890-abcd-ef1234567890"),
        BackendConn("r-cloud", "云服务器", "api.lxcode.example.com:7789", "lx_live_9f8e7d6c5b4a3210"),
        BackendConn("r-nas", "家里 NAS", "192.168.1.50:7789", "nas-token-0001-ffff"),
    )

    /** 当前连接（其中一条远程为 active —— 用于展示「当前」标签与「断开」态）。 */
    const val activeConnId = "r-work"

    /** 项目分组（2 个）。 */
    val projects: List<ProjectMeta> = listOf(
        ProjectMeta("p-lxcode", "lxcode", "C:\\Users\\xzy\\Desktop\\my\\lxcode"),
        ProjectMeta("p-site", "官网站点", "C:\\Users\\xzy\\Desktop\\my\\site"),
    )

    /** 会话列表（2 个项目分组共 5 条 + 未分组 3 条 = 8 条）。 */
    val sessions: List<SessionMeta> = listOf(
        SessionMeta("s-p0", "安卓端 P0 页面原型", "3 分钟前", 42, workspace = "p-lxcode", running = true),
        SessionMeta("s-compact", "compaction 影子区间修复", "1 小时前", 18, workspace = "p-lxcode"),
        SessionMeta("s-toolpair", "工具配对不变量排查", "昨天", 27, workspace = "p-lxcode"),
        SessionMeta("s-landing", "落地页文案改写", "2 小时前", 9, workspace = "p-site"),
        SessionMeta("s-seo", "SEO 元数据补全", "3 天前", 5, workspace = "p-site"),
        SessionMeta("s-goctx", "解释一下 Go 的 context 包", "5 分钟前", 6),
        SessionMeta("s-weekly", "写一封周报邮件", "上周", 3),
        SessionMeta("s-meeting", "整理会议纪要", "上周", 4, running = true),
    )

    /** 当前打开的会话（点会话行进入的线程）。 */
    const val currentSessionId = "s-p0"

    /** 上下文占用：分类之和 == used（68400）。 */
    val contextUsage = ContextUsage(
        used = 68_400,
        window = 200_000,
        system = 5_200,
        tools = 9_800,
        toolResults = 12_400,
        messages = 36_000,
        reasoning = 5_000,
        estimated = false,
    )

    /** 会话统计：生成速度 = 1726 / 126000ms ≈ 13.7 tok/s（与线程里的展示行一致）。 */
    val sessionStats = SessionStats(
        turns = 12,
        steps = 34,
        llmMs = 184_000,
        toolMs = 42_000,
        ttftMs = 9_744, // 均值 9744/12 = 812ms
        ttftSteps = 12,
        decodeMs = 126_000,
        decodeTokens = 1_726,
        inputTokens = 45_200,
        cacheReadTokens = 128_000,
        cacheWriteTokens = 32_000,
        outputTokens = 1_726,
    )

    /** 任务清单（输入框上方那张卡的数据源）。 */
    val todos: List<TodoItem> = listOf(
        TodoItem("通读 :design 组件签名", "done"),
        TodoItem("写 mock 数据层（协议对齐）", "done"),
        TodoItem("实现 6 个页面 + 导航根", "active"),
        TodoItem("云机逐页截图验收", "pending"),
    )

    /** 模型名（会话当前实际使用的模型）——线程消息块里的模型署名。 */
    const val model = "deepseek-v3.2"

    // ===== 模型注册表（composer 模型菜单数据源，对齐桌面端 settings 的 providers 投影）=====

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

    /** 默认选中模型 / 推理强度档（与 [model] 同源）。 */
    const val DEFAULT_MODEL_ID = "deepseek-v3.2"
    const val DEFAULT_EFFORT = "medium"

    /**
     * 模型菜单 mock：2 个提供商分组各 2 条；
     * deepseek-v3.2 支持全部四档、deepseek-chat 不支持 effort（验证跳转行隐藏）。
     */
    val modelGroups: List<MockModelGroup> = listOf(
        MockModelGroup(
            "DeepSeek",
            listOf(
                MockModelMeta("deepseek-v3.2", "deepseek-v3.2", "Anthropic 格式", EFFORT_IDS),
                MockModelMeta("deepseek-chat", "deepseek-chat", "OpenAI 兼容"),
            ),
        ),
        MockModelGroup(
            "OpenAI 兼容",
            listOf(
                MockModelMeta("gpt-5.2", "gpt-5.2", "OpenAI 兼容", listOf("low", "medium", "high")),
                MockModelMeta("kimi-k2.5", "kimi-k2.5", "Anthropic 格式", listOf("medium")),
            ),
        ),
    )

    /** 全部模型的平铺列表（按 id 查找用）。 */
    val allModels: List<MockModelMeta> = modelGroups.flatMap { it.models }

    /** 线程历史：八类消息块各至少一次。 */
    val thread: List<ThreadBlock> = listOf(
        ThreadBlock.User(
            "帮我把安卓端的 P0 页面原型做出来，全部用 mock 数据驱动，" +
                "每类消息块都要能看到。",
        ),
        ThreadBlock.Assistant(
            reasoning = "先读 design 模块的组件签名，确认 LxButton / LxListItem / LxConfirmDialog 的" +
                "参数；再看桌面端连接管理与侧栏的布局，照它的文案与结构做，不自己发明。",
            reasoningMs = 4_200,
            content = "我先勘察现场：读 :design 的 token 与 12 个组件，再对照桌面端 " +
                "`ConnectionManager.tsx` / `Sidebar.tsx` / `thread/` 的布局。\n\n" +
                "结论：导航根用 Screen 枚举切换，会话列表为首页，连接页照连接管理弹窗结构做成一整页。",
            model = model,
        ),
        ThreadBlock.Tool(
            name = "read_file",
            title = "读文件",
            argsSummary = "app/build.gradle.kts",
            command = "",
            cwd = "",
            output = "1→plugins {\n2→    id(\"com.android.application\")\n" +
                "3→    id(\"org.jetbrains.kotlin.android\")\n...",
            running = false,
            isError = false,
            durationMs = 120,
        ),
        ThreadBlock.Tool(
            name = "search",
            title = "搜索",
            argsSummary = "LxButton|LxListItem|LxConfirmDialog",
            command = "",
            cwd = "",
            output = null,
            running = true,
            isError = false,
            durationMs = 0,
        ),
        ThreadBlock.Dispatch(
            agentName = "frontend-dev",
            task = "按桌面端 thread.css 的几何实现 8 类消息块（用户气泡/助手正文/思考/工具卡）",
            conclusion = "已实现 8 类块，思考块折叠态一行、工具卡带状态点与耗时。",
            sessionId = "child-9f3a2b1c",
            done = true,
            isError = false,
        ),
        ThreadBlock.Tool(
            name = "bash",
            title = "bash",
            argsSummary = "gradle assembleDebug",
            command = "./gradlew assembleDebug",
            cwd = "C:\\Users\\xzy\\Desktop\\my\\lxcode\\android",
            output = "FAILURE: Build failed with an exception.\n\n" +
                "* What went wrong:\nExecution failed for task ':app:compileDebugKotlin'.\n" +
                "e: Unresolved reference: LxToggle",
            running = false,
            isError = true,
            durationMs = 3_200,
        ),
        ThreadBlock.Compaction(
            summary = "**目标**：产出安卓端 P0 全量页面原型（mock 驱动）。\n\n" +
                "**已完成**：勘察 design 组件签名、连接管理与侧栏结构对齐。\n\n" +
                "**进行中**：页面实现与逐页截图验收。",
            shadowed = 12,
            before = 48_000,
            after = 12_000,
            manual = false,
        ),
        ThreadBlock.Todo(todos),
        ThreadBlock.Assistant(
            reasoning = null,
            reasoningMs = 0,
            content = "8 类消息块都已就位。输入区那一行保持不折行：模型名、effort、审批档、" +
                "上下文环与统计胶囊各占固定宽度。",
            model = model,
        ),
        ThreadBlock.Error("连接中断：websocket closed (1006) —— 已退回本机，点顶栏连接药丸重连。"),
    )

    // ===== 配对凭证（扫码用）=====

    /** 配对地址（对方机器「连接 → 远程访问」面板里展示的地址）。 */
    const val PAIRING_ADDR = "10.0.0.8:7789"

    /** 配对 Token（对方机器展示的连接凭证）。 */
    const val PAIRING_TOKEN = "a1b2c3d4-e5f6-7890-abcd-ef1234567890"

    /** 二维码内容：把「地址 + Token」拼成一段 JSON（安卓独有新增件）。 */
    val pairingPayload: String =
        """{"lxcode":1,"name":"工作室主机","addr":"$PAIRING_ADDR","token":"$PAIRING_TOKEN"}"""
}
