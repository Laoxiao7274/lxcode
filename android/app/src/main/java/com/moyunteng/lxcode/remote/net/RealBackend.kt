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
//   chat.approvalChanged → 会话权限档（药丸；chat.history 的 approval 键回读同源）。
//   dispatch_id 非空的事件属于子会话：confirmRequest 必带 dispatch_id（协议契约），先于
//   过滤器处理并弹确认门；toolCall 计入派发卡的子工具计数；其余丢弃（不进主时间线）。
package com.moyunteng.lxcode.remote.net

import android.util.Log
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.moyunteng.lxcode.remote.model.ChildTool
import com.moyunteng.lxcode.remote.model.ContextUsage
import com.moyunteng.lxcode.remote.model.EFFORT_IDS
import com.moyunteng.lxcode.remote.model.ModelGroup
import com.moyunteng.lxcode.remote.model.ModelMeta
import com.moyunteng.lxcode.remote.model.ProjectMeta
import com.moyunteng.lxcode.remote.model.SessionMeta
import com.moyunteng.lxcode.remote.model.SessionStats
import com.moyunteng.lxcode.remote.model.ThreadBlock
import com.moyunteng.lxcode.remote.model.TodoItem
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
 * 「真实后端」开关打开时各页面从这里取数据；关闭时会话页是连接引导态（mock 假数据
 * 已随原型退役）。
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

    /** 项目列表（project.list；对齐 protocol.ProjectMeta：id/name/path）。
     *  会话的 workspace 字段存的是**项目 id**（空 = 未分组），显示时按 id 取名——
     *  语义对照桌面端 Sidebar.tsx（projectById.get(s.workspace)?.name）与
     *  internal/store/sessions.go 的 ListProjects/WorkspaceOf。 */
    val projects = mutableStateListOf<ProjectMeta>()

    /** 当前会话（session.new / session.resume / 点会话行）。 */
    var currentSessionId by mutableStateOf("")
        private set

    /** 当前会话时间线（真实块）。 */
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

    /** 当前会话**此刻**的权限档（chat.history 的 approval 回读 / chat.approvalChanged
     *  广播；null = 后端没报——UI 回落「默认」，不猜）。会话级状态：switchTo 切会话
     *  即复位，切回时由历史快照重新对齐——两会话各持各的档，不串台。 */
    var approval by mutableStateOf<String?>(null)
        private set

    /** 会话忙闲（chat.busy / done/error 收敛）。 */
    var busy by mutableStateOf(false)
        private set

    /**
     * 历史**装载版本号**（applyHistory 每次完成 +1）：ThreadScreen 据此在历史装载
     * 完成的那一刻滚动到底部（「进会话停在最底」，对齐桌面端行为）。数量变化
     * （blocks.size）不足以区分「装载完成」与「流式追加」——两者的滚动语义不同
     *（装载后必滚，流式期间跟随），但首个装载事件里两者都会发生，这里只服务 ①。
     */
    var historyVersion by mutableStateOf(0)
        private set

    /**
     * 各会话的实时忙闲（chat.busy 广播；多端共享进度的列表侧投影）：
     * 任意客户端（手机/桌面）上某会话开始/结束生成，这里都收到同一份广播。
     * 会话列表的「运行中」状态点读它——不只是当前打开的会话。
     */
    private val sessionBusy = mutableMapOf<String, Boolean>()

    /** 挂起的工具确认（chat.confirmRequest；null = 无）。 */
    var confirm by mutableStateOf<PendingConfirm?>(null)
        private set

    /** 当前会话标题（会话列表条目；未找到 = 新对话占位）。 */
    val sessionTitle: String
        get() = sessions.firstOrNull { it.id == currentSessionId }?.title ?: "新对话"

    val connected: Boolean get() = phase == RealPhase.Connected

    /** 当前连接凭证（remote.json Enabled 的后端要求 query token；空 = 未开远程）。 */
    var token by mutableStateOf("")
        private set

    // ===== 模型注册表（model.list；composer 模型菜单数据源）=====
    //
    // 对齐桌面端 settings-models.ts 的 mapModels：按 base_url（完整端点）分组投影，
    // efforts 派生自 capabilities.reasoning（未声明 reasoning 的模型整个强度入口隐藏
    // ——对非推理端点传 effort 参数会 400）。模型是**角色绑定**不是客户端本地态：
    // 选中态 = roles.default（role.set 改，model.changed 广播回来对齐）。

    /** 注册表快照（enabled 条目按端点分组；model.changed 时重拉）。 */
    var modelGroups by mutableStateOf<List<ModelGroup>>(emptyList())
        private set

    /** 当前 default 角色绑定的模型 id（后端是唯一事实源；空 = 未绑定）。 */
    var defaultModelId by mutableStateOf("")
        private set

    /** 平铺的全部模型（按 id 查找用）。 */
    val allModels: List<ModelMeta>
        get() = modelGroups.flatMap { it.models }

    /** 刷新模型注册表（model.list）。 */
    fun refreshModels() {
        client.call("model.list", null) { err, result ->
            if (err != null) {
                Log.w(TAG, "model.list 失败: ${err.optString("message")}")
                return@call
            }
            applyModelList(result as? JSONObject)
        }
    }

    /** 切换 default 角色绑定（role.set；model.changed 广播回来对齐）。
     *  [onDone] 回调结果（ok=false 时 msg 是错误信息——调用方给用户反馈；
     *  选中态不本地回退：它从 model.list 的 roles.default 推导，后端没改绑
     *  就不会广播，显示自动停在旧模型上）。 */
    fun setDefaultModel(id: String, onDone: ((ok: Boolean, msg: String) -> Unit)? = null) {
        Log.i(TAG, "role.set default → $id")
        client.call("role.set", JSONObject().put("role", "default").put("model_id", id)) { err, _ ->
            if (err != null) {
                val msg = err.optString("message")
                Log.w(TAG, "role.set 失败: $msg")
                onDone?.invoke(false, msg)
            } else {
                onDone?.invoke(true, "")
            }
        }
    }

    private fun applyModelList(r: JSONObject?) {
        if (r == null) return
        val models = r.optJSONArray("models")
        val roles = r.optJSONObject("roles")
        if (models == null) return
        val groups = linkedMapOf<String, MutableList<ModelMeta>>()
        for (i in 0 until models.length()) {
            val m = models.getJSONObject(i)
            if (!m.optBoolean("enabled")) continue
            val id = m.optString("id")
            if (id.isEmpty()) continue
            val baseUrl = m.optString("base_url")
            val meta = ModelMeta(
                id = id,
                name = m.optString("display_name").ifBlank { m.optString("model", id) },
                desc = if (m.optString("format") == "anthropic") "Anthropic 格式" else "OpenAI 兼容",
                // 档位派生自能力声明（对齐桌面端 settings-models.ts：未声明 reasoning
                // 的模型整个强度入口隐藏）
                efforts = if (m.optJSONObject("capabilities")?.optBoolean("reasoning") == true) EFFORT_IDS else emptyList(),
            )
            groups.getOrPut(baseUrl) { mutableListOf() }.add(meta)
        }
        modelGroups = groups.map { (url, ms) ->
            val host = try {
                java.net.URI("https://" + url.substringAfter("://")).host ?: url
            } catch (_: Exception) {
                url
            }
            ModelGroup(name = host, models = ms)
        }
        // default 角色绑定是唯一事实源（对齐桌面端 settings.tsx：派生选中态，不猜）
        defaultModelId = roles?.optString("default").orEmpty()
        Log.i(TAG, "model.list → ${allModels.size} 个模型 / ${modelGroups.size} 组 default=$defaultModelId")
    }

    // ===== 连接生命周期 =====

    /** 连接（真实开关打开 / 重试按钮）。token 与 addr 同源传入——后端
     *  remote.json Enabled 时缺它直接 401。 */
    fun connect(addr: String = this.addr, token: String = this.token) {
        this.addr = addr
        this.token = token
        phase = RealPhase.Connecting
        phaseMessage = null
        client.connect(addr, token = token)
    }

    /** 断开（真实开关关闭）。 */
    fun disconnect() {
        client.close()
        phase = RealPhase.Offline
        phaseMessage = null
    }

    /** 亮屏即时重连（MainActivity onStart/onResume 调）：掉线且非用户主动断开时
     *  立即重连，不等自动重连的退避定时器。未进真实模式（从未连过）不动作。 */
    fun reconnectIfDown() {
        if (phase == RealPhase.Offline) return
        client.reconnectIfDown()
    }

    /** 重试（断线 UI 的按钮；addr 传当前输入框地址——改了地址点重试应连新地址）。 */
    fun retry(addr: String = this.addr, token: String = this.token) {
        Log.i(TAG, "手动重试连接 $addr")
        connect(addr, token)
    }

    private fun onState(connected: Boolean, message: String?) {
        if (connected) {
            phase = RealPhase.Connected
            phaseMessage = null
            // 重连后忙闲以服务端事实为准（切入既有会话时 chat.history 会带回真实
            // busy 键重设）：留着断线前的 busy=true 会让「发送」一直当「停止」用，
            // 而停止的收敛依赖后端事件——假后端/断线场景会卡死在生成中。
            busy = false
            // 会话级忙闲投影同样清账：断线期间的 busy 广播收不到，留下的旧值
            // 会让列表的「运行中」状态点卡住（后端 hello 不带逐会话 busy——
            // 未知就显示空闲，等下一个 busy 广播修正，不编状态）。
            sessionBusy.clear()
            refreshSessions()
            refreshProjects()
            // 模型注册表：composer 模型菜单的数据源（桌面端是握手并行组的一步）
            refreshModels()
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

    /** 刷新项目列表（project.list；hello 成功后与 session.list 同批拉取，
     *  project.changed 广播时重拉——对齐桌面端 Sidebar 的 projectsTick 机制）。 */
    fun refreshProjects() {
        client.call("project.list", null) { err, result ->
            if (err != null) {
                Log.w(TAG, "project.list 失败: ${err.optString("message")}")
                return@call
            }
            applyProjectList(result)
        }
    }

    /** 新建会话并切入（会话列表「新对话」；发送前无会话时也走这里）。
     *  [workspace] 归属项目 id（session.new 的 SessionNewParams.Workspace——
     *  项目组内新建时透传 scope；空 = 未分组）。 */
    fun newSession(onReady: (String) -> Unit = {}, workspace: String = "") {
        val params = if (workspace.isNotBlank()) {
            JSONObject().put("workspace", workspace)
        } else {
            null
        }
        client.call("session.new", params) { err, result ->
            if (err != null) {
                Log.w(TAG, "session.new 失败: ${err.optString("message")}")
                blocks.add(ThreadBlock.Error("新建会话失败: ${err.optString("message")}"))
                return@call
            }
            val id = (result as? JSONObject)?.optString("session_id").orEmpty()
            Log.i(TAG, "session.new → $id workspace=$workspace")
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

    /** 发送一条消息（chat.send；内容经事件流到达）。
     *  effort 是**请求级参数**（protocol.ChatSendParams.Effort——与桌面端同语义：
    *   每条消息携带；后端没有会话级 effort 方法、chat.history 也不回它，故无
    *   远端读写通道，本端不持久化选择——只是发送时带上当前内存视图的值）。
     *  effort 为空 = 不带键（老后端/未选档优雅降级）。 */
    fun send(text: String, effort: String = "") {
        val t = text.trim()
        if (t.isEmpty()) return
        if (currentSessionId.isEmpty()) {
            newSession(onReady = { send(t, effort) })
            return
        }
        busy = true
        Log.i(TAG, "chat.send session=$currentSessionId text=${t.take(80)} effort=$effort")
        val params = JSONObject().put("session_id", currentSessionId).put("text", t)
        // 空串不带 effort 键（wire 纪律：值缺省整键缺席，不发明「空档位」语义）
        if (effort.isNotBlank()) params.put("effort", effort)
        client.call(
            "chat.send",
            params,
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

    /** 中途改当前会话的权限档（chat.approval：后端落库 + 广播，立刻生效于
     *  运行中的一轮）。本地先乐观置位（药丸即时反馈），chat.approvalChanged
     *  广播回来再对齐一次（幂等）；失败仅记日志——真实档位以广播为准。
     *  名字不带 set 前缀：`var approval` 的合成 setter 会占掉 setApproval 签名。 */
    fun changeApproval(mode: String) {
        val sid = currentSessionId
        if (sid.isEmpty()) return
        approval = mode
        Log.i(TAG, "chat.approval session=$sid approval=$mode")
        client.call(
            "chat.approval",
            JSONObject().put("session_id", sid).put("approval", mode),
        ) { err, _ ->
            if (err != null) Log.w(TAG, "chat.approval 失败: ${err.optString("message")}")
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
                // todos 非空 = 主 Agent 预拆的清单初稿（todo 契约——卡上第一秒显示计划）
                val todos = p.optJSONArray("todos")
                val (tDone, tTotal) = if (todos != null) {
                    val list = (0 until todos.length()).map { todos.getJSONObject(it) }
                    list.count { it.optString("status") == "done" } to list.size
                } else {
                    -1 to 0
                }
                blocks.add(
                    ThreadBlock.Dispatch(
                        agentName = p.optString("agent_name"),
                        task = p.optString("task"),
                        conclusion = "",
                        sessionId = p.optString("session_id", ""),
                        done = false,
                        isError = false,
                        dispatchId = p.optString("dispatch_id", ""),
                        childTodosDone = tDone,
                        childTodosTotal = tTotal,
                    ),
                )
            } else {
                // 先按 dispatch_id 精确配对（协议契约：dispatchStart/End 的
                // dispatch_id 同源）；找不到（历史重建后的卡没有 dispatch_id、
                // 或跨会话的旧广播）才回落「最后一张未完结卡」并记日志。
                val exact = dispatchCardIdx(dispatchId)
                val i = if (exact >= 0) {
                    exact
                } else {
                    Log.w(TAG, "dispatchEnd 无 dispatch_id 配对卡，回落未完结卡 dispatch=$dispatchId")
                    blocks.indexOfLast { it is ThreadBlock.Dispatch && it.done.not() }
                }
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
        // 安卓单页等价物）。todo.updated（带 dispatch_id = 子 Agent 的清单更新）：
        // 归并进**归属派发卡**（轻量派发进度——手机上看子 Agent 跑到哪一步，
        // 与桌面端 TodoUpdatedParams.DispatchID 的归属契约一致；卡不存在只记日志）。
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
                "todo.updated" -> mergeChildTodos(dispatchId, p.optJSONArray("items"))
            }
            return
        }
        val sid = p.optString("session_id", "")
        when (method) {
            "chat.userMessage" -> {
                if (sid != currentSessionId) return
                val msg = p.optJSONObject("message") ?: return
                val text = msg.optString("content")
                // 通告前缀识别（对齐桌面端 shared/notices.ts 的前缀表）：user 角色 ≠
                // 用户说的话——后台任务完成通告渲染成提示条，不进用户气泡。
                // hidden 前缀（[系统通告] ）是写给模型的注记：不产生任何块。
                if (text.startsWith(HIDDEN_NOTICE_PREFIX)) return
                val notice = noticeKind(text)
                if (notice != null) {
                    blocks.add(ThreadBlock.Notice(label = notice.second, text = text.removePrefix(notice.first).trim()))
                    streamingIdx = -1
                    return
                }
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
                    cur.copy(reasoning = (cur.reasoning ?: "") + text, streaming = true)
                } else {
                    cur.copy(content = cur.content + text, streaming = true)
                }
            }

            "chat.toolCall" -> {
                if (sid != currentSessionId) return
                streamingIdx = -1 // 工具轮开始：下一步 delta 开新 assistant 块
                val name = p.optString("name")
                // 调度工具不建工具行（对齐桌面端 reduce.ts）：它的渲染形态就是派发卡
                //（dispatchStart 已建卡）——两处都建 = 同一个调度渲染两遍，且
                // toolResult 会把子会话结论全文塞进 output（4000 字符）。
                if (name == "agent_dispatch") return
                val id = p.optString("id")
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
                // 调度结果不进主时间线（对齐桌面端）：全文是子会话结论，由
                // dispatchEnd 回填派发卡的 conclusion 行（卡上截一行显示）。
                if (p.optString("name") == "agent_dispatch") return
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
                // 用最终 message 对齐流式块（修 delta 丢字的尾差）。
                // 覆盖语义对齐桌面端 reduce.ts 的 reduceDone：**content 非空才覆盖**
                //——中间步 done（带 tool_calls、content 空）不得把已流式的正文
                // 清成空串；空 content 时只关 streaming + 填元数据。
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
                            content = content.ifBlank { cur.content },
                            reasoning = reasoning.ifBlank { cur.reasoning },
                            reasoningMs = ftms,
                            model = model,
                            firstTokenMs = if (ftms > 0) ftms else null,
                            tokPerSec = if (utok > 0 && dur > 0) utok * 1000.0 / dur else null,
                            // 轮结束 = 打字机收尾：整块呈现（streaming=false）
                            streaming = false,
                        )
                    }
                }
                p.optJSONObject("context")?.let { contextUsage = parseContext(it) }
                p.optJSONObject("stats")?.let { sessionStats = parseStats(it) }
                Log.i(TAG, "done: ctx=$contextUsage stats=$sessionStats")
                msg?.optString("model", "")?.takeIf { it.isNotEmpty() }?.let { sessionModel = it }
                busy = false
                // 主轮结束重拉会话列表（对齐桌面端 ws/index.ts 的 chat.done →
                // refreshSessionList）：新对话的首条消息落库后侧栏才出得来
                //（后端语义：未发过消息的空会话不进 session.list；而 session.new
                // 建的会话首条消息不发 SessionStarted 事件——唯一刷新信号就是 done）
                refreshSessions()
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
                            blocks[i] = cur.copy(content = content, streaming = false)
                        } else {
                            blocks.add(ThreadBlock.Assistant(content, null, 0, sessionModel))
                        }
                    }
                }
                blocks.add(ThreadBlock.Error(if (aborted) "已中断" else message))
                busy = false
            }

            "chat.busy" -> {
                // 多端共享进度：忙闲广播**先记进会话级投影**（列表运行中状态点，
                // 所有会话都记——不只是当前打开的），当前会话再同步本页的 busy。
                if (sid.isNotEmpty()) {
                    sessionBusy[sid] = p.optBoolean("busy")
                    syncRunningDots()
                }
                if (sid != currentSessionId) return
                busy = p.optBoolean("busy")
            }

            "chat.approvalChanged" -> {
                // 权限档变更广播（多端同步——本端改档后的回执也走这条）：按会话
                // 归属，别的会话的变更不改本会话药丸
                if (sid != currentSessionId) return
                p.optString("approval", "").takeIf { it.isNotEmpty() }?.let { approval = it }
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

            "project.changed" -> refreshProjects()

            // 模型注册表变更（多端同步：桌面端加了模型/改了绑定，手机端重拉对齐）
            "model.changed" -> refreshModels()

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
        // 先切再拉：currentSessionId 必须在**发起请求前**就指向目标会话——否则
        // 回调里的过期响应守卫（id != currentSessionId）会把这次响应整个丢掉，
        // 从 A 切 B 永远切不动（A 开着时点 B，resume 成功但 history 被丢弃，
        // 界面停在 A——本批 e2e 实测抓到）。
        if (id != currentSessionId) switchTo(id)
        client.call("chat.history", JSONObject().put("session_id", id)) { err, result ->
            if (err != null) {
                Log.w(TAG, "chat.history($id) 失败: ${err.optString("message")}")
                return@call
            }
            // 请求在途时用户又点了别的会话：currentSessionId 已指向新目标，
            // 这份旧响应作废（守卫语义保留，判定对象不变）
            if (id != currentSessionId) return@call
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
        // 权限档是会话级状态：切会话即清（切回时由 chat.history 的 approval 重新
        // 对齐）——沿用上一会话的档位显示是串台
        approval = null
        // 历史装载版本也清零：新会话的历史还没装载，滚动触发条件（historyVersion>0）
        // 必须等下一次 applyHistory 才成立——沿用上一会话的版本号会让 ThreadScreen
        // 在 blocks 还是空的时候去滚
        historyVersion = 0
    }

    private fun applyHistory(r: JSONObject?) {
        if (r == null) return
        val msgs = r.optJSONArray("messages") ?: return
        val checkpoints = r.optJSONArray("checkpoints")
            ?.let { cp -> (0 until cp.length()).map { cp.optInt(it) } }
            ?.toSet() ?: emptySet()
        // tool 消息先到调用块后到结果的配对表（历史里调用在前、结果紧随）
        val toolCallIdx = mutableMapOf<String, Int>()
        val dispatchCallIdx = mutableMapOf<String, Int>()
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
                "user" -> {
                    // 通告前缀识别（对齐桌面端 history.ts 的 notices 分支）：
                    // hidden 注记不产生任何块；通告渲染成提示条，其余才是用户气泡。
                    val content = m.optString("content")
                    if (content.startsWith(HIDDEN_NOTICE_PREFIX)) continue
                    val notice = noticeKind(content)
                    if (notice != null) {
                        newBlocks.add(ThreadBlock.Notice(label = notice.second, text = content.removePrefix(notice.first).trim()))
                    } else {
                        newBlocks.add(ThreadBlock.User(content))
                    }
                }
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
                            // 调度调用重建的是**派发卡**，不是工具行（对齐桌面端 history.ts
                            // 的 DISPATCH_TOOL_NAME 分支）——回放建工具行的后果：每次刷新
                            // 派发都退化成一张 4000 字 output 的全文工具卡，卡、Agent 名、
                            // 子会话 id 全没了。先按"中断"定格，配对结果到了再回填。
                            if (name == "agent_dispatch") {
                                val parsed = try {
                                    JSONObject(args)
                                } catch (_: Exception) {
                                    null
                                }
                                dispatchCallIdx[callId] = newBlocks.size
                                newBlocks.add(
                                    ThreadBlock.Dispatch(
                                        agentName = parsed?.optString("agent").orEmpty().ifBlank { "子 Agent" },
                                        task = parsed?.optString("task").orEmpty(),
                                        // 先按"中断"定格：历史里没有配对结果 = 那次派发在
                                        // 结束前就断了，不是"正在跑"（不许假装 running）。
                                        // 结果到了（tool 分支）再回填成 done + 正文。
                                        conclusion = "这次派发在结束前中断了——历史里没有结果记录（重启前它没跑完）。",
                                        sessionId = parsed?.optString("session").orEmpty(),
                                        done = true,
                                        isError = true,
                                    ),
                                )
                                continue
                            }
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
                    // ① 调度卡回填：结果正文剥掉「[子会话 id: …]」提示行（给模型看的
                    //    续跑线索），子会话 id 从提示行取（权威值）；正文空 = 没有文本结论。
                    val dIdx = dispatchCallIdx[callId]
                    if (dIdx != null && dIdx < newBlocks.size) {
                        val d = newBlocks[dIdx] as ThreadBlock.Dispatch
                        val (body, childSid) = splitDispatchResult(m.optString("content"))
                        newBlocks[dIdx] = d.copy(
                            conclusion = body.ifBlank { "（子 Agent 没有产出文本结论）" },
                            sessionId = childSid.ifBlank { d.sessionId },
                            done = true,
                            isError = false,
                        )
                    } else {
                        // ② 普通工具行：调用在前、结果紧随，按 id 配对回填
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
        }
        blocks.clear()
        blocks.addAll(newBlocks)
        streamingIdx = -1
        // 历史快照的任务清单（ChatHistoryResult.todos——对齐桌面端 history-snapshot
        // 的 todos 装载）：非空插一块 Todo（当前清单的定格视图，随后的 todo.updated
        // 会找到并替换它）；缺席/空 = 不插（老后端或没建过清单——不编空块）。
        r.optJSONArray("todos")?.let { arr ->
            if (arr.length() > 0) {
                val list = (0 until arr.length()).map { arr.getJSONObject(it) }
                    .map { TodoItem(it.optString("content"), it.optString("status")) }
                blocks.add(ThreadBlock.Todo(list))
            }
        }
        r.optJSONObject("context")?.let { contextUsage = parseContext(it) }
        r.optJSONObject("stats")?.let { sessionStats = parseStats(it) }
        sessionModel = r.optString("model", "")
        busy = r.optBoolean("busy")
        // 历史的 busy 键是服务端事实：同步进会话级忙闲投影（列表状态点对齐）
        if (currentSessionId.isNotEmpty()) {
            sessionBusy[currentSessionId] = busy
            syncRunningDots()
        }
        // 该会话此刻的权限档（后端 LiveApproval——会话级状态持久化的显示依据）；
        // 空串 = 老后端没报，保持 null（UI 回落「默认」，不猜）
        r.optString("approval", "").takeIf { it.isNotEmpty() }?.let { approval = it }
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
        // 历史装载完成：ThreadScreen 据此滚动到底（进会话停在最底，对齐桌面端）
        historyVersion++
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
                // 运行中状态点 = chat.busy 广播的会话级投影（多端共享进度）：
                // 任何客户端上该会话在生成，这里都亮着——不再只看当前打开的会话
                running = sessionBusy[id] == true,
            )
        }.filter { !it.archived }
        sessions.clear()
        sessions.addAll(list)
        Log.i(TAG, "session.list → ${list.size} 条")
    }

    private fun applyProjectList(r: Any?) {
        // project.list 的 result 是**裸数组**（dispatchProjects 直接回 []ProjectMeta：
        // {id,name,path}；workspace 字段是项目 id 而不是路径——见 SessionMeta.workspace）
        val arr: org.json.JSONArray? = when (r) {
            is org.json.JSONArray -> r
            is JSONObject -> r.optJSONArray("projects")
            else -> null
        } ?: return
        val list = (0 until arr!!.length()).mapNotNull { idx ->
            val o = arr.getJSONObject(idx)
            val id = o.optString("id")
            if (id.isEmpty()) return@mapNotNull null
            ProjectMeta(id = id, name = o.optString("name"), path = o.optString("path"))
        }
        projects.clear()
        projects.addAll(list)
        Log.i(TAG, "project.list → ${list.size} 个: ${list.joinToString { it.name }}")
    }

    /** 会话列表的运行中状态点随 busy 事件刷新（SessionMeta 是不可变值，重建条目）。 */
    private fun syncRunningDots() {
        for (i in sessions.indices) {
            val s = sessions[i]
            val running = sessionBusy[s.id] == true
            if (s.running != running) sessions[i] = s.copy(running = running)
        }
    }

    // ===== 工具函数 =====

    private fun ensureStreamingBlock(): Int {
        if (streamingIdx >= 0 && streamingIdx < blocks.size && blocks[streamingIdx] is ThreadBlock.Assistant) {
            return streamingIdx
        }
        // 新流式块 streaming=true：打字机只对实时增量揭示（历史回放的块是 false）
        blocks.add(ThreadBlock.Assistant(content = "", reasoning = null, reasoningMs = 0, model = sessionModel, streaming = true))
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

    /**
     * 子 Agent 的清单进度归并（todo.updated 带 dispatch_id 的广播，多端共享进度）：
     * 完成数/总数归进归属派发卡（卡上「清单 2/4」一行的数据源）；卡不存在只记日志
     * （跨会话的旧广播、历史里没有卡的派发——丢弃是正确行为，不建孤儿卡）。
     */
    private fun mergeChildTodos(dispatchId: String, items: org.json.JSONArray?) {
        if (items == null) return
        val i = dispatchCardIdx(dispatchId)
        if (i < 0) {
            Log.w(TAG, "子会话清单事件无归属派发卡 dispatch=$dispatchId")
            return
        }
        val d = blocks[i] as ThreadBlock.Dispatch
        val list = (0 until items.length()).map { items.getJSONObject(it) }
            .map { TodoItem(it.optString("content"), it.optString("status")) }
        blocks[i] = d.copy(
            childTodosDone = list.count { it.status == "done" },
            childTodosTotal = list.size,
        )
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

        /**
         * 通告前缀表（对齐桌面端 shared/notices.ts 的 NOTICE_KINDS，逐字含尾空格）。
         *
         * 这类消息是**真实 user 角色消息**（模型必须当用户回合回应），但不是用户说的
         * 话——按文本前缀识别，渲染成提示条。命中取第一个前缀（顺序敏感：更长的排前）。
         */
        private val NOTICE_KINDS = listOf(
            "[后台任务通告] " to "后台任务通告",
            "[重复调用提醒] " to "重复调用提醒",
        )

        /** hidden 注记前缀（写给模型的系统注记，界面上不产生任何块）。 */
        private const val HIDDEN_NOTICE_PREFIX = "[系统通告] "

        /** 命中的通告种类（label, 剩余正文）；没命中 = null（普通用户消息）。 */
        private fun noticeKind(text: String): Pair<String, String>? =
            NOTICE_KINDS.firstOrNull { text.startsWith(it.first) }

        /** 子会话 id 提示行（后端 internal/agent/tools.go 在 dispatch 结果末尾追加）：
         *  `\n\n[子会话 id: <id> —— 只在这次没做完时填进 session 参数续跑]`。
         *  给模型看的续跑线索，回放时剥掉；id 从这里取是**权威**值（续跑时
         *  arguments.session 可能为空）。对齐桌面端 history.ts 的 SUB_SESSION_HINT。 */
        private val SUB_SESSION_HINT = Regex("\\[子会话 id:\\s*([^\\]\\s]+)[^\\]]*\\]")

        /** 拆分 dispatch 结果：结论正文 + 子会话 id（没有提示行时 id 为空串）。 */
        fun splitDispatchResult(raw: String): Pair<String, String> {
            val m = SUB_SESSION_HINT.find(raw) ?: return raw.trimEnd() to ""
            val body = (raw.substring(0, m.range.first) + raw.substring(m.range.last + 1)).trimEnd()
            return body to m.groupValues[1]
        }

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
