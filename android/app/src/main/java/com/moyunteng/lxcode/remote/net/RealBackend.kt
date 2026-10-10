// RealBackend —— 真实后端会话态（安卓端）。
//
// 职责：把 LxWsClient 的事件流归约成 UI 可观察状态（Compose mutableState），
// 类型复用 mock 包里「逐字段对齐协议」的 SessionMeta / ThreadBlock / ContextUsage /
// SessionStats（MockData.kt 开头的纪律：接真后端时这一层换成 WS 解析结果，页面不动）。
//
// 事件 → 块的映射（见 android/PROTOCOL-NOTES.md §5/§7）：
//   chat.userMessage → User 块；chat.delta → Assistant 块流式增长（kind=reasoning 进思考链）；
//   chat.toolCall/toolResult → Tool 块（按调用 id 配对）；chat.done → 对齐最终正文 +
//   更新 context/stats；chat.confirmRequest → 挂起确认（UI 弹 LxConfirmDialog → tool.confirm）；
//   chat.error → Error 块；todo.updated → Todo 块；dispatch/compacted → 对应卡。
//   dispatch_id 非空的事件属于子会话：confirmRequest 必带 dispatch_id（协议契约），先于
//   过滤器处理并弹确认门；toolCall 计入派发卡的子工具计数；其余丢弃（不进主时间线）。
package com.moyunteng.lxcode.remote.net

import android.util.Log
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.moyunteng.lxcode.remote.mock.ChildTool
import com.moyunteng.lxcode.remote.mock.ContextUsage
import com.moyunteng.lxcode.remote.mock.SessionMeta
import com.moyunteng.lxcode.remote.mock.SessionStats
import com.moyunteng.lxcode.remote.mock.ThreadBlock
import com.moyunteng.lxcode.remote.mock.TodoItem
import org.json.JSONObject
import java.time.Instant
import java.time.temporal.ChronoUnit

/** 连接阶段（连接页/顶栏药丸的依据）。 */
enum class RealPhase { Offline, Connecting, Connected, Failed }

/** 挂起的工具确认（对齐 protocol.ConfirmRequest）。 */
data class PendingConfirm(
    val sessionId: String,
    val id: String,
    val name: String,
    val arguments: String,
    val prompt: String,
)

/**
 * 真实后端状态机。一个实例挂在 [com.moyunteng.lxcode.remote.ui.MockAppState] 上，
 * 「真实后端」开关打开时各页面从这里取数据；关闭时页面走原 mock 路径（零改动）。
 */
class RealBackend {

    private val client = LxWsClient(
        eventListener = { m, p -> onEvent(m, p) },
        stateListener = { connected, msg -> onState(connected, msg) },
    )

    // ===== 可观察状态 =====

    var phase by mutableStateOf(RealPhase.Offline)
        private set
    var phaseMessage by mutableStateOf<String?>(null)
        private set

    /** 连接地址（默认本机；连接页可改）。 */
    var addr by mutableStateOf("127.0.0.1:7789")

    /** 会话列表（session.list；对齐 protocol.SessionMeta）。 */
    val sessions = mutableStateListOf<SessionMeta>()

    /** 当前会话（session.new / session.resume / 点会话行）。 */
    var currentSessionId by mutableStateOf("")
        private set

    /** 当前会话时间线（真实块；mock 模式不读它）。 */
    val blocks = mutableStateListOf<ThreadBlock>()

    /** 上下文占用（chat.done / chat.history 的 context 键；null = 未知 → UI 显示「—」）。 */
    var contextUsage by mutableStateOf<ContextUsage?>(null)
        private set

    /** 整段会话统计（stats 键；null = 还没有任何一步 → 统计胶囊不渲染）。 */
    var sessionStats by mutableStateOf<SessionStats?>(null)
        private set

    /** 会话此刻的模型（chat.history.model / chat.done.model）。 */
    var sessionModel by mutableStateOf("")
        private set

    /** 会话忙闲（chat.busy / done/error 收敛）。 */
    var busy by mutableStateOf(false)
        private set

    /** 挂起的工具确认（chat.confirmRequest；null = 无）。 */
    var confirm by mutableStateOf<PendingConfirm?>(null)
        private set

    /** 当前会话标题（会话列表条目；未找到 = 新对话占位）。 */
    val sessionTitle: String
        get() = sessions.firstOrNull { it.id == currentSessionId }?.title ?: "新对话"

    val connected: Boolean get() = phase == RealPhase.Connected

    // ===== 连接生命周期 =====

    /** 连接（真实开关打开 / 重试按钮）。 */
    fun connect(addr: String = this.addr) {
        this.addr = addr
        phase = RealPhase.Connecting
        phaseMessage = null
        client.connect(addr, token = "")
    }

    /** 断开（真实开关关闭）。 */
    fun disconnect() {
        client.close()
        phase = RealPhase.Offline
        phaseMessage = null
    }

    /** 重试（断线 UI 的按钮；addr 传当前输入框地址——改了地址点重试应连新地址）。 */
    fun retry(addr: String = this.addr) {
        Log.i(TAG, "手动重试连接 $addr")
        connect(addr)
    }

    private fun onState(connected: Boolean, message: String?) {
        if (connected) {
            phase = RealPhase.Connected
            phaseMessage = null
            // 重连后忙闲以服务端事实为准（切入既有会话时 chat.history 会带回真实
            // busy 键重设）：留着断线前的 busy=true 会让「发送」一直当「停止」用，
            // 而停止的收敛依赖后端事件——假后端/断线场景会卡死在生成中。
            busy = false
            refreshSessions()
        } else if (phase != RealPhase.Offline) {
            // 用户主动 disconnect 后的迟到回调不再标失败
            phase = RealPhase.Failed
            phaseMessage = message
        }
    }

    // ===== 对外操作 =====

    /** 刷新会话列表（session.list）。 */
    fun refreshSessions() {
        client.call("session.list", null) { err, result ->
            if (err != null) {
                Log.w(TAG, "session.list 失败: ${err.optString("message")}")
                return@call
            }
            applySessionList(result)
        }
    }

    /** 新建会话并切入（会话列表「新对话」；发送前无会话时也走这里）。 */
    fun newSession(onReady: (String) -> Unit = {}) {
        client.call("session.new", null) { err, result ->
            if (err != null) {
                Log.w(TAG, "session.new 失败: ${err.optString("message")}")
                blocks.add(ThreadBlock.Error("新建会话失败: ${err.optString("message")}"))
                return@call
            }
            val id = (result as? JSONObject)?.optString("session_id").orEmpty()
            Log.i(TAG, "session.new → $id")
            if (id.isNotEmpty()) {
                switchTo(id)
                onReady(id)
            }
        }
    }

    /** 切入既有会话：session.resume → chat.history（先历史后焦点，避免闪空）。 */
    fun openSession(id: String) {
        client.call("session.resume", JSONObject().put("id", id)) { err, _ ->
            if (err != null) Log.w(TAG, "session.resume($id) 失败: ${err.optString("message")}")
            loadHistory(id)
        }
    }

    /** 发送一条消息（chat.send；内容经事件流到达）。 */
    fun send(text: String) {
        val t = text.trim()
        if (t.isEmpty()) return
        if (currentSessionId.isEmpty()) {
            newSession { send(t) }
            return
        }
        busy = true
        Log.i(TAG, "chat.send session=$currentSessionId text=${t.take(80)}")
        client.call(
            "chat.send",
            JSONObject().put("session_id", currentSessionId).put("text", t),
        ) { err, result ->
            if (err != null) {
                busy = false
                val msg = err.optString("message")
                Log.w(TAG, "chat.send 被拒: code=${err.optInt("code")} $msg")
                blocks.add(ThreadBlock.Error("发送失败: $msg"))
            } else {
                Log.i(TAG, "chat.send accepted=${(result as? JSONObject)?.optBoolean("accepted")}")
            }
        }
    }

    /** 裁决确认门（tool.confirm；allow=false = 拒绝，后端合成「已取消，未执行」）。 */
    fun resolveConfirm(allow: Boolean) {
        val c = confirm ?: return
        Log.i(TAG, "tool.confirm id=${c.id} allow=$allow")
        client.call(
            "tool.confirm",
            JSONObject().put("session_id", c.sessionId).put("id", c.id).put("allow", allow),
        ) { err, _ ->
            if (err != null) {
                Log.w(TAG, "tool.confirm 失败: ${err.optString("message")}")
                blocks.add(ThreadBlock.Error("确认裁决失败: ${err.optString("message")}"))
            }
            confirm = null
        }
    }

    /** 取消当前轮（chat.cancel）。 */
    fun cancel() {
        if (currentSessionId.isEmpty()) return
        Log.i(TAG, "chat.cancel session=$currentSessionId")
        client.call("chat.cancel", JSONObject().put("session_id", currentSessionId)) { _, _ -> }
    }

    /**
     * 手动压缩当前会话（chat.compact）。结果经 [onDone] 回调（compacted=false 时
     * reason 说明为什么没压——不是错误码）；压缩成功本身由 chat.compacted 事件
     * 插「已压缩历史」块，这里只在失败/无收益时补一条说明。
     */
    fun compact(onDone: ((compacted: Boolean, reason: String) -> Unit)? = null) {
        if (currentSessionId.isEmpty()) return
        Log.i(TAG, "chat.compact session=$currentSessionId")
        client.call("chat.compact", JSONObject().put("session_id", currentSessionId)) { err, result ->
            if (err != null) {
                val msg = err.optString("message")
                Log.w(TAG, "chat.compact 失败: $msg")
                blocks.add(ThreadBlock.Error("压缩失败: $msg"))
                onDone?.invoke(false, msg)
                return@call
            }
            val o = result as? JSONObject
            val compacted = o?.optBoolean("compacted") ?: false
            val reason = o?.optString("reason").orEmpty()
            Log.i(TAG, "chat.compact → compacted=$compacted reason=$reason")
            if (!compacted && reason.isNotEmpty()) {
                blocks.add(ThreadBlock.Error("压缩未执行：$reason"))
            }
            onDone?.invoke(compacted, reason)
        }
    }

    /** 重命名会话（session.rename；成功后刷新列表——session.changed 广播也会触发刷新）。 */
    fun renameSession(id: String, title: String) {
        Log.i(TAG, "session.rename id=$id title=$title")
        client.call(
            "session.rename",
            JSONObject().put("id", id).put("title", title),
        ) { err, _ ->
            if (err != null) Log.w(TAG, "session.rename 失败: ${err.optString("message")}")
            refreshSessions()
        }
    }

    /** 归档会话（session.archive，archived=true；归档后列表刷新即消失——侧栏只显示未归档）。 */
    fun archiveSession(id: String) {
        Log.i(TAG, "session.archive id=$id")
        client.call(
            "session.archive",
            JSONObject().put("id", id).put("archived", true),
        ) { err, _ ->
            if (err != null) {
                Log.w(TAG, "session.archive 失败: ${err.optString("message")}")
            }
            refreshSessions()
        }
    }

    // ===== 事件归约 =====

    /** 当前流式 assistant 块的下标（-1 = 无；toolCall/done 后归 -1，下一步开新块）。 */
    private var streamingIdx = -1

    private fun onEvent(method: String, p: JSONObject) {
        val dispatchId = p.optString("dispatch_id", "")
        // 确认门先行：子 Agent 的确认请求**必带 dispatch_id**（协议契约 protocol.ConfirmRequest、
        // dispatch_ws_test.go 钉住），不能进下面的子会话丢弃分支——否则真实模式（主 Agent 唯一
        // 工具是 agent_dispatch，高危确认全来自子派发）确认弹窗永不出现、会话挂死。
        // 归属照桌面端契约（AGENTS.md §2.3）：子确认按 request.dispatch_id 归到本会话的派发卡，
        // 主会话自己的确认（dispatch_id 空）仍按 session_id 归属；裁决经 resolveConfirm 原样回
        // tool.confirm（session_id 取载荷里的子会话 id）。
        if (method == "chat.confirmRequest") {
            if (dispatchId.isNotEmpty()) {
                if (dispatchCardIdx(dispatchId) < 0) {
                    Log.w(TAG, "confirmRequest 无归属派发卡（非当前会话的子派发？）dispatch=$dispatchId")
                    return
                }
            } else if (p.optString("session_id", "") != currentSessionId) {
                return
            }
            confirm = PendingConfirm(
                sessionId = p.optString("session_id", ""),
                id = p.optString("id"),
                name = p.optString("name"),
                arguments = p.optString("arguments"),
                prompt = p.optString("prompt"),
            )
            return
        }
        // 派发卡事件（dispatchStart/End）自身也带 dispatch_id，但归属键是 owner_session_id——
        // 必须先于下面的子会话过滤器处理，否则派发卡建不出来、后续子事件全部无归属。
        if (method == "chat.dispatchStart" || method == "chat.dispatchEnd") {
            if (p.optString("owner_session_id") != currentSessionId) return
            if (method == "chat.dispatchStart") {
                blocks.add(
                    ThreadBlock.Dispatch(
                        agentName = p.optString("agent_name"),
                        task = p.optString("task"),
                        conclusion = "",
                        sessionId = p.optString("session_id", ""),
                        done = false,
                        isError = false,
                        dispatchId = p.optString("dispatch_id", ""),
                    ),
                )
            } else {
                val i = blocks.indexOfLast { it is ThreadBlock.Dispatch && it.done.not() }
                // dispatchEnd 用 owner+dispatch 对齐卡；原型期取最后一张未完结卡
                if (i >= 0) {
                    val d = blocks[i] as ThreadBlock.Dispatch
                    blocks[i] = d.copy(
                        conclusion = p.optString("result"),
                        done = true,
                        isError = p.optBoolean("is_error"),
                    )
                } else {
                    Log.w(TAG, "dispatchEnd 无配对卡 dispatch=$dispatchId")
                }
            }
            return
        }
        // 子会话事件（dispatch_id 非空）不进主时间线；toolCall/toolResult 归并进派发卡的
        // 子工具实时流（卡内展开可见子工具名/状态/耗时——桌面端「子会话标签页实时流」的
        // 安卓单页等价物），不再无声丢弃
        if (dispatchId.isNotEmpty()) {
            when (method) {
                "chat.toolCall" -> upsertChildTool(
                    dispatchId,
                    id = p.optString("id"),
                    name = p.optString("name"),
                    argsSummary = argsSummary(p.optString("arguments")),
                    running = true,
                    isError = false,
                    durationMs = 0,
                )
                "chat.toolResult" -> finishChildTool(dispatchId, p.optString("id"), p.optBoolean("is_error"))
            }
            return
        }
        val sid = p.optString("session_id", "")
        when (method) {
            "chat.userMessage" -> {
                if (sid != currentSessionId) return
                val msg = p.optJSONObject("message") ?: return
                val text = msg.optString("content")
                val last = blocks.lastOrNull()
                if (last !is ThreadBlock.User || last.text != text) {
                    blocks.add(ThreadBlock.User(text))
                    streamingIdx = -1
                }
            }

            "chat.delta" -> {
                if (sid != currentSessionId) return
                val kind = p.optString("kind", "text")
                val text = p.optString("text")
                if (text.isEmpty()) return
                val i = ensureStreamingBlock()
                val cur = blocks[i] as ThreadBlock.Assistant
                blocks[i] = if (kind == "reasoning") {
                    cur.copy(reasoning = (cur.reasoning ?: "") + text)
                } else {
                    cur.copy(content = cur.content + text)
                }
            }

            "chat.toolCall" -> {
                if (sid != currentSessionId) return
                streamingIdx = -1 // 工具轮开始：下一步 delta 开新 assistant 块
                val id = p.optString("id")
                val name = p.optString("name")
                val args = p.optString("arguments")
                blocks.add(
                    ThreadBlock.Tool(
                        id = id,
                        name = name,
                        title = toolTitle(name),
                        argsSummary = argsSummary(args),
                        command = argsCommand(args),
                        cwd = argsCwd(args),
                        output = null,
                        running = true,
                        isError = false,
                        durationMs = 0,
                    ),
                )
            }

            "chat.toolResult" -> {
                if (sid != currentSessionId) return
                val id = p.optString("id")
                val i = blocks.indexOfFirst { it is ThreadBlock.Tool && it.id == id }
                if (i >= 0) {
                    val t = blocks[i] as ThreadBlock.Tool
                    blocks[i] = t.copy(
                        output = p.optString("content").take(4_000),
                        running = false,
                        isError = p.optBoolean("is_error"),
                    )
                } else {
                    Log.w(TAG, "toolResult 无配对调用块 id=$id name=${p.optString("name")}")
                }
            }

            "chat.confirmRequest" -> {
                if (sid != currentSessionId) return
                confirm = PendingConfirm(
                    sessionId = sid,
                    id = p.optString("id"),
                    name = p.optString("name"),
                    arguments = p.optString("arguments"),
                    prompt = p.optString("prompt"),
                )
            }

            "chat.done" -> {
                if (sid != currentSessionId) return
                // 用最终 message 对齐流式块（修 delta 丢字的尾差）
                val msg = p.optJSONObject("message")
                if (msg != null) {
                    val i = lastAssistantIndex()
                    if (i >= 0) {
                        val cur = blocks[i] as ThreadBlock.Assistant
                        val content = msg.optString("content")
                        val reasoning = msg.optString("reasoning_content", "")
                        val model = msg.optString("model", cur.model)
                        val ftms = msg.optLong("first_token_ms", 0L)
                        val dur = msg.optLong("duration_ms", 0L)
                        val utok = msg.optInt("usage_tokens", 0)
                        blocks[i] = cur.copy(
                            content = content,
                            reasoning = reasoning.ifBlank { cur.reasoning },
                            reasoningMs = ftms,
                            model = model,
                            firstTokenMs = if (ftms > 0) ftms else null,
                            tokPerSec = if (utok > 0 && dur > 0) utok * 1000.0 / dur else null,
                        )
                    }
                }
                p.optJSONObject("context")?.let { contextUsage = parseContext(it) }
                p.optJSONObject("stats")?.let { sessionStats = parseStats(it) }
                Log.i(TAG, "done: ctx=$contextUsage stats=$sessionStats")
                msg?.optString("model", "")?.takeIf { it.isNotEmpty() }?.let { sessionModel = it }
                busy = false
                streamingIdx = -1
            }

            "chat.error" -> {
                if (sid != currentSessionId) return
                streamingIdx = -1
                val aborted = p.optBoolean("aborted")
                val message = p.optString("message")
                p.optJSONObject("partial")?.let { partial ->
                    val content = partial.optString("content")
                    if (content.isNotEmpty()) {
                        val i = lastAssistantIndex()
                        if (i >= 0) {
                            val cur = blocks[i] as ThreadBlock.Assistant
                            blocks[i] = cur.copy(content = content)
                        } else {
                            blocks.add(ThreadBlock.Assistant(content, null, 0, sessionModel))
                        }
                    }
                }
                blocks.add(ThreadBlock.Error(if (aborted) "已中断" else message))
                busy = false
            }

            "chat.busy" -> {
                if (sid != currentSessionId) return
                busy = p.optBoolean("busy")
                syncRunningDots()
            }

            "todo.updated" -> {
                if (sid != currentSessionId) return
                val items = p.optJSONArray("items") ?: return
                val list = (0 until items.length()).map { items.getJSONObject(it) }
                    .map { TodoItem(it.optString("content"), it.optString("status")) }
                val i = blocks.indexOfFirst { it is ThreadBlock.Todo }
                if (i >= 0) blocks[i] = ThreadBlock.Todo(list) else blocks.add(ThreadBlock.Todo(list))
            }

            "session.changed" -> refreshSessions()

            "chat.compacted" -> {
                if (sid != currentSessionId) return
                blocks.add(
                    ThreadBlock.Compaction(
                        summary = p.optString("summary"),
                        shadowed = p.optInt("shadowed"),
                        before = p.optInt("before"),
                        after = p.optInt("after"),
                        manual = p.optBoolean("manual"),
                    ),
                )
            }

            else -> Log.d(TAG, "忽略事件 $method")
        }
    }

    // ===== 历史装载 =====

    /** chat.history → 块（含 checkpoints → 压缩块、pending → 挂起确认）。 */
    private fun loadHistory(id: String) {
        client.call("chat.history", JSONObject().put("session_id", id)) { err, result ->
            if (err != null) {
                Log.w(TAG, "chat.history($id) 失败: ${err.optString("message")}")
                return@call
            }
            if (id != currentSessionId && currentSessionId.isNotEmpty()) return@call
            switchTo(id)
            applyHistory(result as? JSONObject)
        }
    }

    private fun switchTo(id: String) {
        if (currentSessionId == id) return
        currentSessionId = id
        blocks.clear()
        streamingIdx = -1
        confirm = null
        busy = false
        contextUsage = null
        sessionStats = null
        sessionModel = ""
    }

    private fun applyHistory(r: JSONObject?) {
        if (r == null) return
        val msgs = r.optJSONArray("messages") ?: return
        val checkpoints = r.optJSONArray("checkpoints")
            ?.let { cp -> (0 until cp.length()).map { cp.optInt(it) } }
            ?.toSet() ?: emptySet()
        // tool 消息先到调用块后到结果的配对表（历史里调用在前、结果紧随）
        val toolCallIdx = mutableMapOf<String, Int>()
        val newBlocks = mutableListOf<ThreadBlock>()
        for (i in 0 until msgs.length()) {
            val m = msgs.getJSONObject(i)
            val role = m.optString("role")
            if (i in checkpoints) {
                newBlocks.add(
                    ThreadBlock.Compaction(
                        summary = m.optString("content"),
                        shadowed = 0,
                        before = 0,
                        after = 0,
                        manual = false,
                    ),
                )
                continue
            }
            when (role) {
                "user" -> newBlocks.add(ThreadBlock.User(m.optString("content")))
                "assistant" -> {
                    val ftms = m.optLong("first_token_ms", 0L)
                    val dur = m.optLong("duration_ms", 0L)
                    val utok = m.optInt("usage_tokens", 0)
                    if (m.optString("content").isNotEmpty() || !m.optString("reasoning_content").isBlank()) {
                        newBlocks.add(
                            ThreadBlock.Assistant(
                                content = m.optString("content"),
                                reasoning = m.optString("reasoning_content").ifBlank { null },
                                reasoningMs = ftms,
                                model = m.optString("model", ""),
                                firstTokenMs = if (ftms > 0) ftms else null,
                                tokPerSec = if (utok > 0 && dur > 0) utok * 1000.0 / dur else null,
                            ),
                        )
                    }
                    val calls = m.optJSONArray("tool_calls")
                    if (calls != null) {
                        for (k in 0 until calls.length()) {
                            val c = calls.getJSONObject(k)
                            val fn = c.optJSONObject("function") ?: continue
                            val callId = c.optString("id")
                            val name = fn.optString("name")
                            val args = fn.optString("arguments")
                            toolCallIdx[callId] = newBlocks.size
                            newBlocks.add(
                                ThreadBlock.Tool(
                                    id = callId,
                                    name = name,
                                    title = toolTitle(name),
                                    argsSummary = argsSummary(args),
                                    command = argsCommand(args),
                                    cwd = argsCwd(args),
                                    output = null,
                                    running = false,
                                    isError = false,
                                    durationMs = m.optLong("duration_ms", 0L),
                                ),
                            )
                        }
                    }
                }

                "tool" -> {
                    val callId = m.optString("tool_call_id")
                    val idx = toolCallIdx[callId]
                    if (idx != null && idx < newBlocks.size) {
                        val t = newBlocks[idx] as ThreadBlock.Tool
                        newBlocks[idx] = t.copy(
                            output = m.optString("content").take(4_000),
                            isError = false,
                            durationMs = m.optLong("duration_ms", t.durationMs),
                        )
                    }
                }
            }
        }
        blocks.clear()
        blocks.addAll(newBlocks)
        streamingIdx = -1
        r.optJSONObject("context")?.let { contextUsage = parseContext(it) }
        r.optJSONObject("stats")?.let { sessionStats = parseStats(it) }
        sessionModel = r.optString("model", "")
        busy = r.optBoolean("busy")
        r.optJSONObject("pending")?.let { pend ->
            confirm = PendingConfirm(
                sessionId = pend.optString("session_id", currentSessionId),
                id = pend.optString("id"),
                name = pend.optString("name"),
                arguments = pend.optString("arguments"),
                prompt = pend.optString("prompt"),
            )
        }
        Log.i(TAG, "history($currentSessionId): ${msgs.length()} 条消息 → ${blocks.size} 块 busy=$busy")
    }

    private fun applySessionList(r: Any?) {
        // session.list 的 result 是**裸数组**（toProtocolSessionList 直接回 []SessionMeta）
        val arr: org.json.JSONArray? = when (r) {
            is org.json.JSONArray -> r
            is JSONObject -> r.optJSONArray("sessions")
            else -> null
        } ?: return
        val list = (0 until arr!!.length()).mapNotNull { idx ->
            val o = arr.getJSONObject(idx)
            val id = o.optString("id")
            if (id.isEmpty()) return@mapNotNull null
            SessionMeta(
                id = id,
                title = o.optString("title").ifBlank { "新对话" },
                updatedAt = relativeTime(o.optString("updated_at")),
                messages = o.optInt("messages"),
                archived = o.optBoolean("archived"),
                workspace = o.optString("workspace", ""),
                running = o.optString("id") == currentSessionId && busy,
            )
        }.filter { !it.archived }
        sessions.clear()
        sessions.addAll(list)
        Log.i(TAG, "session.list → ${list.size} 条")
    }

    /** 会话列表的运行中状态点随 busy 事件刷新（SessionMeta 是不可变值，重建条目）。 */
    private fun syncRunningDots() {
        for (i in sessions.indices) {
            val s = sessions[i]
            val running = s.id == currentSessionId && busy
            if (s.running != running) sessions[i] = s.copy(running = running)
        }
    }

    // ===== 工具函数 =====

    private fun ensureStreamingBlock(): Int {
        if (streamingIdx >= 0 && streamingIdx < blocks.size && blocks[streamingIdx] is ThreadBlock.Assistant) {
            return streamingIdx
        }
        blocks.add(ThreadBlock.Assistant(content = "", reasoning = null, reasoningMs = 0, model = sessionModel))
        streamingIdx = blocks.size - 1
        return streamingIdx
    }

    private fun lastAssistantIndex(): Int =
        blocks.indexOfLast { it is ThreadBlock.Assistant }

    /** 当前会话时间线里 dispatch_id 对应的派发卡下标（-1 = 无）。 */
    private fun dispatchCardIdx(dispatchId: String): Int =
        blocks.indexOfFirst { it is ThreadBlock.Dispatch && it.dispatchId == dispatchId }

    /** 子会话工具实时流：调用到达即插行/标记运行中（归属派发卡；卡不存在只记日志）。 */
    private fun upsertChildTool(
        dispatchId: String,
        id: String,
        name: String,
        argsSummary: String,
        running: Boolean,
        isError: Boolean,
        durationMs: Long,
    ) {
        val i = dispatchCardIdx(dispatchId)
        if (i < 0) {
            Log.w(TAG, "子会话工具事件无归属派发卡 dispatch=$dispatchId")
            return
        }
        val d = blocks[i] as ThreadBlock.Dispatch
        if (running) childStarts[id] = android.os.SystemClock.elapsedRealtime()
        val existing = d.childRuns.indexOfFirst { it.id == id }
        val entry = ChildTool(
            id = id,
            name = name,
            title = toolTitle(name),
            argsSummary = argsSummary,
            running = running,
            isError = isError,
            durationMs = durationMs,
        )
        val runs = if (existing >= 0) {
            d.childRuns.toMutableList().also { it[existing] = entry }
        } else {
            d.childRuns + entry
        }
        blocks[i] = d.copy(childRuns = runs, childTools = runs.size)
    }

    /**
     * 子会话工具结果到达：标记完成/出错，耗时按本地计时补齐（协议 toolResult 不带
     * duration_ms；[childStarts] 记录调用到达时刻，回放历史时才用消息里的耗时）。
     */
    private val childStarts = mutableMapOf<String, Long>()

    private fun finishChildTool(dispatchId: String, id: String, isError: Boolean) {
        val i = dispatchCardIdx(dispatchId)
        if (i < 0) {
            Log.w(TAG, "子会话工具结果无归属派发卡 dispatch=$dispatchId")
            return
        }
        val d = blocks[i] as ThreadBlock.Dispatch
        val idx = d.childRuns.indexOfFirst { it.id == id }
        if (idx < 0) {
            Log.w(TAG, "子会话工具结果无配对调用行 id=$id dispatch=$dispatchId")
            return
        }
        val start = childStarts.remove(id) ?: 0L
        val dur = if (start > 0) android.os.SystemClock.elapsedRealtime() - start else 0L
        val runs = d.childRuns.toMutableList()
        runs[idx] = runs[idx].copy(running = false, isError = isError, durationMs = dur)
        blocks[i] = d.copy(childRuns = runs, childTools = runs.size)
    }

    private fun parseContext(o: JSONObject): ContextUsage = ContextUsage(
        used = o.optInt("used"),
        window = o.optInt("window"),
        system = o.optInt("system"),
        tools = o.optInt("tools"),
        toolResults = o.optInt("tool_results"),
        messages = o.optInt("messages"),
        reasoning = o.optInt("reasoning"),
        estimated = o.optBoolean("estimated"),
    )

    private fun parseStats(o: JSONObject): SessionStats = SessionStats(
        turns = o.optInt("turns"),
        steps = o.optInt("steps"),
        llmMs = o.optLong("llm_ms"),
        toolMs = o.optLong("tool_ms"),
        ttftMs = o.optLong("ttft_ms"),
        ttftSteps = o.optInt("ttft_steps"),
        decodeMs = o.optLong("decode_ms"),
        decodeTokens = o.optInt("decode_tokens"),
        inputTokens = o.optInt("input_tokens"),
        cacheReadTokens = o.optInt("cache_read_tokens"),
        cacheWriteTokens = o.optInt("cache_write_tokens"),
        outputTokens = o.optInt("output_tokens"),
        legacyTokens = o.optInt("legacy_tokens"),
    )

    companion object {
        private const val TAG = "LxReal"

        /** 工具显示名（对齐桌面端工具卡标题；未知工具原样显示 id）。 */
        private val TOOL_TITLES = mapOf(
            "read_file" to "读文件",
            "search" to "搜索",
            "session_search" to "搜会话",
            "web_search" to "联网搜索",
            "web_fetch" to "抓网页",
            "job_output" to "后台任务输出",
            "job_list" to "后台任务列表",
            "job_kill" to "停止后台任务",
            "read_skill" to "读技能",
            "edit" to "编辑文件",
            "write_file" to "写文件",
            "bash" to "bash",
            "todo" to "清单",
            "agent_dispatch" to "派发子 Agent",
        )

        fun toolTitle(name: String): String = TOOL_TITLES[name] ?: name

        /** 参数摘要：取第一个标量值截断（照 mock 的 argsSummary 风格）。 */
        fun argsSummary(arguments: String): String = try {
            val o = JSONObject(arguments)
            val keys = o.keys()
            val first = if (keys.hasNext()) keys.next() else null
            when {
                first == null -> ""
                o.opt(first) is String -> o.getString(first).take(60)
                else -> o.opt(first).toString().take(60)
            }
        } catch (_: Exception) {
            arguments.take(60)
        }

        fun argsCommand(arguments: String): String = try {
            JSONObject(arguments).optString("command")
        } catch (_: Exception) {
            ""
        }

        fun argsCwd(arguments: String): String = try {
            JSONObject(arguments).optString("cwd")
        } catch (_: Exception) {
            ""
        }

        /** ISO / 「yyyy-MM-dd HH:mm[:ss]」时间戳 → 「N 分钟前」风格（对齐桌面端相对时间）。 */
        fun relativeTime(iso: String): String {
            val t = try {
                Instant.parse(iso)
            } catch (_: Exception) {
                null
            } ?: try {
                // 后端落库的 updated_at 可能是 "yyyy-MM-dd HH:mm[:ss]"（本地时区）
                val norm = if (iso.length == 16) "$iso:00" else iso
                java.time.LocalDateTime.parse(norm.replace(' ', 'T'))
                    .atZone(java.time.ZoneId.systemDefault()).toInstant()
            } catch (_: Exception) {
                return iso
            }
            val now = Instant.now()
            val mins = ChronoUnit.MINUTES.between(t, now)
            return when {
                mins < 1 -> "刚刚"
                mins < 60 -> "$mins 分钟前"
                mins < 60 * 24 -> "${mins / 60} 小时前"
                mins < 60 * 24 * 2 -> "昨天"
                mins < 60 * 24 * 7 -> "${mins / 60 / 24} 天前"
                else -> "${mins / 60 / 24 / 7} 周前"
            }
        }
    }
}
