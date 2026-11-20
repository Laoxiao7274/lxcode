// RealBackend —— 真实后端会话态（安卓端）。
//
// facade：投屏、助手域、附件、模型、连接与对外操作仍留在这里；事件/子会话归约见
// Reducers.kt，历史装载见 HistoryLoader.kt。后续步再评估投屏域与 AssistantDomain
// 内嵌类抽出；它们是有状态域，本轮不与纯逻辑段一起搬动。
//
// 职责：把 LxWsClient 的事件流归约成 UI 可观察状态（Compose mutableState），
// 类型复用 model 包里「逐字段对齐协议」的 SessionMeta / ThreadBlock / ContextUsage /
// SessionStats（Wire.kt 开头的纪律：接真后端时这一层换成 WS 解析结果，页面不动）。
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
import com.moyunteng.lxcode.remote.model.DISPATCH_INTERRUPTED
import com.moyunteng.lxcode.remote.model.AttachmentMeta
import com.moyunteng.lxcode.remote.model.ContextUsage
import com.moyunteng.lxcode.remote.model.EFFORT_IDS
import com.moyunteng.lxcode.remote.model.ModelGroup
import com.moyunteng.lxcode.remote.model.ModelMeta
import com.moyunteng.lxcode.remote.model.ProjectMeta
import com.moyunteng.lxcode.remote.model.RunningDispatchInfo
import com.moyunteng.lxcode.remote.model.SessionMeta
import com.moyunteng.lxcode.remote.model.SessionStats
import com.moyunteng.lxcode.remote.model.ThreadBlock
import com.moyunteng.lxcode.remote.model.TodoItem
import com.moyunteng.lxcode.remote.model.todoItemOf
import com.moyunteng.lxcode.remote.model.applyRunningDispatches
import com.moyunteng.lxcode.remote.model.isDispatchFinal
import com.moyunteng.lxcode.remote.model.isDispatchPlaceholder
import com.moyunteng.lxcode.remote.model.mergeChildLiveTail
import com.moyunteng.lxcode.remote.model.mergeChildTodoList
import com.moyunteng.lxcode.remote.model.mergeKnownChildIds
import com.moyunteng.lxcode.remote.model.parseModelList
import org.json.JSONArray
import org.json.JSONObject
import java.time.Instant
import java.time.temporal.ChronoUnit

/** 连接阶段（连接页/顶栏药丸的依据）。 */
enum class RealPhase { Offline, Connecting, Connected, Failed }

/** 挂起的工具确认（对齐 protocol.ConfirmRequest）。
 *  [kind] 空 = 高危工具确认（批准/拒绝）；"ask" = ask_user 提问（打字回答或点选）。
 *  [options] = 提问的预设答案（可空）——AskCard 渲染成可点选的选项按钮。 */
data class PendingConfirm(
    val sessionId: String,
    val id: String,
    val name: String,
    val arguments: String,
    val prompt: String,
    val kind: String = "",
    val options: List<String> = emptyList(),
) {
    /** 是否 ask_user 提问形态。 */
    val isAsk: Boolean get() = kind == "ask"
}

/**
 * 真实后端状态机。一个实例挂在 [com.moyunteng.lxcode.remote.ui.AppState] 上，
 * 「真实后端」开关打开时各页面从这里取数据；关闭时会话页是连接引导态。
 */
class RealBackend {

    internal val client = LxWsClient(
        eventListener = { m, p -> onEvent(m, p) },
        stateListener = { connected, msg -> onState(connected, msg) },
        unauthorized = { onPairingInvalid() },
        // 投屏二进制帧（screen.* 协议，internal/protocol/screen.go）：直投
        // ScreenDecoder.onFrame（未投屏时 decoder 为 null → 零开销直返）。
        frameListener = { bytes -> screenDecoder?.onFrame(bytes) },
    )

    // ===== 投屏（第一阶段：只看；internal/protocol/screen.go）=====

    /** 解码器（投屏页在时非 null；二进制帧直投它）。 */
    @Volatile var screenDecoder: com.moyunteng.lxcode.remote.ui.ScreenDecoder? = null

    /** 显示器枚举缓存（screen.monitors；壳不在线时空表）。 */
    var screenMonitors by mutableStateOf<List<com.moyunteng.lxcode.remote.ui.ScreenMonitorInfo>>(emptyList())
        internal set

    /** 壳侧捕屏模块是否在线（screen.monitors 的 shell_online）。 */
    var screenShellOnline by mutableStateOf(false)
        internal set

    /** 当前投屏状态（screen.state / screen.changed；null = 未知）。
     *  可写：ScreenScreen 的解码错误就地落错误态（active=false）。 */
    var screenState by mutableStateOf<com.moyunteng.lxcode.remote.ui.ScreenStateInfo?>(null)

    /**
     * 宿主 AppState 的弱引用（[applyScreenState] 置 screenWasActive 用——「曾经
     * active 过」是页面层状态，网络层不持有强引用防泄漏）。AppState 构造时注册。
     */
    @Volatile
    var appStateRef: java.lang.ref.WeakReference<com.moyunteng.lxcode.remote.ui.AppState>? = null

    /** screen.start/stop 在途（按钮防抖）。 */
    var screenBusy by mutableStateOf(false)
        internal set

    /**
     * 悬浮球菜单「横屏观看」的方向锁定（null = 跟随用户默认）。
     * requestedOrientation 是 Activity 级的（单 Activity 应用 = 全 app 跟着横），
     * 观看页内的其他页面此刻不可见，影响被限定在投屏观看态；但旋转触发 Activity
     * 重建后该设置会丢失——这里记一份：ScreenScreen 重建后重放、离开观看态恢复
     * FULL_USER（见 ScreenScreen 的方向 SideEffect/DisposableEffect）。
     */
    @Volatile
    var screenOrientationLock: Int? = null

    /** 启动 pending：start 应答成功到首帧到达之间有数秒空窗（壳 1s 轮询 +
     *  ffmpeg 启动），此间按钮禁用并显示「正在启动…」——真机用户在这段空窗
     *  里以为没点上又点（「要点很多次才能成功一次」的另一半：多次快速点击
     *  时序上无害，但每次点击都重置用户对状态的心理预期，报错 toast 互相覆盖）。
     *  置位 = start 应答成功且 active=false（壳还没上报 running）；复位 = 壳
     *  上报 running（screen.changed/state 的 active=true）或超时（见置位处）。 */
    var screenStartPending by mutableStateOf(false)
        internal set

    /** 拉显示器枚举 + 当前状态（投屏页进入时调）。 */
    fun refreshScreen() {
        client.call("screen.monitors", JSONObject()) { err, result ->
            if (err == null && result is JSONObject) {
                screenShellOnline = result.optBoolean("shell_online", false)
                val arr = result.optJSONArray("monitors")
                screenMonitors = if (arr == null) emptyList() else (0 until arr.length()).mapNotNull { i ->
                    val m = arr.optJSONObject(i) ?: return@mapNotNull null
                    com.moyunteng.lxcode.remote.ui.ScreenMonitorInfo(
                        id = m.optInt("id"), name = m.optString("name"),
                        primary = m.optBoolean("primary"), width = m.optInt("width"), height = m.optInt("height"),
                    )
                }
            }
        }
        client.call("screen.state", JSONObject()) { err, result ->
            if (err == null && result is JSONObject) applyScreenState(result)
        }
    }

    fun startScreen(monitor: Int, quality: String, onDone: (String?) -> Unit) {
        screenBusy = true
        client.call("screen.start", JSONObject().put("monitor", monitor).put("quality", quality)) { err, result ->
            screenBusy = false
            if (err != null) {
                screenStartPending = false
                onDone(err.optString("message"))
            } else {
                if (result is JSONObject) applyScreenState(result)
                // 应答成功但壳还没上报 running（active=false）：进入启动 pending
                //（15s 兜底超时自动复位——壳不在线/ffmpeg 起不来时按钮不能永久锁死）
                if (screenState?.active != true) {
                    screenStartPending = true
                    startPendingTimeout?.let { main.removeCallbacks(it) }
                    val r = Runnable { if (screenState?.active != true) screenStartPending = false }
                    startPendingTimeout = r
                    main.postDelayed(r, 15_000L)
                }
                onDone(null)
            }
        }
    }

    fun stopScreen(onDone: (String?) -> Unit) {
        screenBusy = true
        client.call("screen.stop", JSONObject()) { err, result ->
            screenBusy = false
            if (err != null) {
                onDone(err.optString("message"))
            } else {
                if (result is JSONObject) applyScreenState(result)
                onDone(null)
            }
        }
    }

    /** 运行中切清晰度（悬浮球菜单）：重发 screen.start 换 quality——后端命令电平
     *  变化会驱动壳重启 ffmpeg（monitor/quality 任一变化即重起），应答带新状态。 */
    fun setScreenQuality(quality: String, onDone: (String?) -> Unit) {
        val st = screenState ?: return onDone("当前没有投屏")
        client.call(
            "screen.start",
            JSONObject().put("monitor", st.monitor).put("quality", quality),
        ) { err, result ->
            if (err != null) {
                onDone(err.optString("message"))
            } else {
                if (result is JSONObject) applyScreenState(result)
                onDone(null)
            }
        }
    }

    /** 运行中切显示器（悬浮球菜单）：同 setScreenQuality 的机制，换 monitor。
     *  P1-1：quality 为 null（后端未上报当前档，壳太老）时不带 quality 参数——
     *  screen.start 的 quality 为空 = 后端 normalizeQuality 回 medium 并重启
     *  ffmpeg，会把用户已选的清晰度打回默认档；不带参同理（命令电平整体替换）。
     *  折衷：null 时仍以 medium 下发（与旧行为一致），但 UI 显示与选中态按
     *  「未知」渲染——不再有「明明显示高清、切个显示器变标准」的静默重置。 */
    fun setScreenMonitor(monitor: Int, onDone: (String?) -> Unit) {
        val st = screenState ?: return onDone("当前没有投屏")
        val q = st.quality ?: "medium"
        client.call(
            "screen.start",
            JSONObject().put("monitor", monitor).put("quality", q),
        ) { err, result ->
            if (err != null) {
                onDone(err.optString("message"))
            } else {
                if (result is JSONObject) applyScreenState(result)
                onDone(null)
            }
        }
    }

    // ===== 投屏控制（screen.input；internal/protocol/screen.go 的 ScreenInputParams）=====

    /** move 事件上次发送时刻（节流到 ~30Hz：手指滑动一次产生上百个 move，
     *  WS 请求与后端队列都吃不消；30Hz 与视频帧率对齐，视觉上已连续）。 */
    internal var lastMoveSentAt = 0L

    /**
     * 注入一条输入事件（fire-and-forget——控制事件高频，不逐条等应答；
     * 错误只对非 move 类弹 toast：move 失败通常是「控制未开启」，逐条弹会把
     * 用户 toast 刷爆，首次失败由 click/key 等显式动作兜住提示）。
     *
     * 后端安全门（server/screen.go screenInput）：非接收者 / 「允许控制」关 /
     * 壳不在线 → 错误应答。error toast 由调用方（ScreenScreen 的手势层）在
     * [onError] 里接（仅首个错误提示，避免连点刷屏）。
     */
    fun sendScreenInput(
        params: com.moyunteng.lxcode.remote.net.ScreenInputParams,
        onError: ((String) -> Unit)? = null,
    ) {
        if (params.type == "move") {
            val now = android.os.SystemClock.uptimeMillis()
            if (now - lastMoveSentAt < 33) return // 30Hz 节流
            lastMoveSentAt = now
        }
        val o = JSONObject()
        o.put("type", params.type)
        if (params.x != null) o.put("x", params.x)
        if (params.y != null) o.put("y", params.y)
        if (params.button != null) o.put("button", params.button)
        if (params.dx != null) o.put("dx", params.dx)
        if (params.dy != null) o.put("dy", params.dy)
        if (params.key != null) o.put("key", params.key)
        if (params.modifiers.isNotEmpty()) o.put("modifiers", JSONArray(params.modifiers))
        if (params.text != null) o.put("text", params.text)
        o.put("monitor", params.monitor)
        client.call("screen.input", o) { err, _ ->
            if (err != null) onError?.invoke(err.optString("message"))
        }
    }

    internal fun applyScreenState(o: JSONObject) {
        // P1-1：quality 缺省改 null——后端壳未上报档位时（老壳/未知）不编造
        // "medium"，避免 setScreenMonitor/setScreenQuality 的「沿用当前档」把
        // 清晰度重置回 medium。UI 层显示 null 时按未知档处理（qualityLabel 兜底）。
        screenState = com.moyunteng.lxcode.remote.ui.ScreenStateInfo(
            active = o.optBoolean("active", false),
            monitor = o.optInt("monitor", 0),
            quality = o.optString("quality", "").ifEmpty { null },
            encoder = o.optString("encoder", ""),
            error = o.optString("error", "").ifEmpty { null },
        )
        // 壳上报 running（任意路径：start 应答 / screen.changed / screen.state 轮询）
        // 即退出启动 pending，并记「曾经 active 过」——断开自动退回（AppRoot 的
        // LaunchedEffect）与观看态返回键（endScreenSession）都据此判定；
        // 启动失败（从未 active）不置位，留在设置页。
        if (o.optBoolean("active", false)) {
            screenStartPending = false
            startPendingTimeout?.let { main.removeCallbacks(it) }
            startPendingTimeout = null
            appStateRef?.get()?.screenWasActive = true
        }
    }

    // ===== 可观察状态 =====

    var phase by mutableStateOf(RealPhase.Offline)
        internal set
    var phaseMessage by mutableStateOf<String?>(null)
        internal set

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
        internal set

    /** 当前会话时间线（真实块）。 */
    val blocks = mutableStateListOf<ThreadBlock>()

    /** 上下文占用（chat.done / chat.history 的 context 键；null = 未知 → UI 显示「—」）。 */
    var contextUsage by mutableStateOf<ContextUsage?>(null)
        internal set

    /** 整段会话统计（stats 键；null = 还没有任何一步 → 统计胶囊不渲染）。 */
    var sessionStats by mutableStateOf<SessionStats?>(null)
        internal set

    /** 会话此刻**实际使用**的模型（chat.history.model / chat.done.model）——后端
     *  解析后的结果（会话选择 > Agent 绑定 > default 角色）。消息气泡上的模型徽标
     *  读它；模型菜单的选中态读 [pickedModelId]（用户给这条会话选了什么）。 */
    var sessionModel by mutableStateOf("")
        internal set

    /** 模型菜单的选中态：本会话选中的模型优先，没选过（老会话/新会话）才回落
     *  全局 default 角色——与桌面端 ModelPicker 的 `model ?? settings.model` 同一条。 */
    val activeModelId: String
        get() = pickedModelId.ifEmpty { defaultModelId }

    /** 当前会话**此刻**的权限档（chat.history 的 approval 回读 / chat.approvalChanged
     *  广播；null = 后端没报——UI 回落「默认」，不猜）。会话级状态：switchTo 切会话
     *  即复位，切回时由历史快照重新对齐——两会话各持各的档，不串台。 */
    var approval by mutableStateOf<String?>(null)
        internal set

    /** 会话忙闲（chat.busy / done/error 收敛）。 */
    var busy by mutableStateOf(false)
        internal set

    /**
     * 自动重连进行中（真实开关开着、后端断线、后台自动重试在跑）：会话页/
     * 会话列表页「正在重连…」横幅的依据。断线其实一直在后台自动重连，把它
     * 表达出来——不显示的话用户以为必须手动去连接页重连（用户反馈的痛点）。
     * 置位 = onState(connected=false)（断线事件；自动重连随之排程）；
     * 复位 = 连接成功（onState(true)）/ 用户显式 disconnect() / 显式 connect()。
     */
    var reconnecting by mutableStateOf(false)
        internal set

    /**
     * 配对已失效（后端对升级请求回 HTTP 401 = token 错/被吊销）：重连必然再
     * 401，自动重连没有意义——置位后停掉重连排程，UI 显示「配对已失效」横幅 +
     * 「重新配对」按钮（跳扫码页）。复位 = 用户显式 connect()/disconnect()
     * （重新配对成功后保存/连接即走 connect）。
     */
    var pairingInvalid by mutableStateOf(false)
        internal set

    /**
     * 历史**装载版本号**（applyHistory 每次完成 +1）：ThreadScreen 据此在历史装载
     * 完成的那一刻滚动到底部（「进会话停在最底」，对齐桌面端行为）。数量变化
     * （blocks.size）不足以区分「装载完成」与「流式追加」——两者的滚动语义不同
     *（装载后必滚，流式期间跟随），但首个装载事件里两者都会发生，这里只服务 ①。
     */
    var historyVersion by mutableStateOf(0)
        internal set

    // ===== 向上翻页（chat.history 的 before_seq 分页，2026-12）=====

    /** 已加载窗口最旧一条消息的 seq（下一次 before_seq 的 anchor；0 = 还没装载过）。
     *  取值来源：tail 装载 = tail 段首条消息的 seq；分页装载 = min(旧值, 新窗口首条)。 */
    var oldestLoadedSeq by mutableStateOf(0L)
        internal set

    /** anchor 之前还有没有更早的历史（远端 has_more；false = 列表头显示「已加载全部历史」）。 */
    var hasMoreHistory by mutableStateOf(true)
        internal set

    /** 分页装载中（LazyColumn 顶部 spinner）。 */
    var loadingEarlier by mutableStateOf(false)
        internal set

    /** 上一次分页装载失败的原因（非空 = 顶部显示重试；成功装载即清）。 */
    var loadEarlierError by mutableStateOf<String?>(null)
        internal set

    /** 分页装载版本号（更早的块插入头部时 +1；ThreadScreen 据此做不跳屏补偿）。 */
    var earlierVersion by mutableStateOf(0)
        internal set

    /** 会话本地缓存（视图加速；与 ConnStore 分开存储——cache/sessions/<会话 id>.json）。 */
    internal val sessionCache = SessionCache(com.moyunteng.lxcode.remote.update.AppContextHolder.context)

    /** 当前会话是否在展示缓存（断网打开会话时置位；远端装载成功即清）。 */
    var showingCached by mutableStateOf(false)
        internal set

    /**
     * 流式**内容版本号**（当前会话的流式块每次被 delta 改写 +1）：ThreadScreen 据此在
     * sticky-follow 时跟随内容增长——最后一块超过一屏时，delta 只让它变高、块数不变
     * （blocks.size 不触发），不监听它的话视口会停在该块开头、最新内容在屏外。
     */
    var streamVersion by mutableStateOf(0)
        internal set

    // ===== 助手域（assistant.*，协议见 internal/protocol/assistant.go）=====
    //
    // 助手是**另一个域**（internal/server/assistant.go 的硬约束）：assistant.* 只
    // 寻址 space='assistant' 的会话，主域 chat.* 传助手会话 id 报错，反之亦然——
    // 本域的每个请求都显式带 session_id，绝不混用主域的连接焦点（对齐桌面端
    // frontend/src/agent/ws/assistant.ts 的域隔离注释）。
    //
    // 事件归约（assistantRouteEvent）：assistant.* 的 payload 与 chat.* 逐字段同构、
    // 只换事件名——归一成 chat.* 等价名后走同一份块语义（八类块 + 派发卡 + 确认门
    // 先行 + 子事件归并），数据桶换助手域自己的。块构建逻辑与主 reducer 逐条同源；
    // 子会话临时视图（viewChildSessionId / child* 桶）**复用主域那套**：任何时刻
    // 只有一个打开的子会话视图（UI 单页面），主域与助手域不可能同时开——子会话
    // 的归属判定按 session_id（全局唯一），与域无关；带 dispatch_id 的子事件归属
    // 判定按卡反查（两域的卡各查各的，见 assistantRouteEvent 的子事件分支）。

    /** 助手域状态桶（会话列表/当前会话/时间线/忙闲/确认/模型/权限档——与主域
     *  同名成员一一对应，独立实例防串台）。 */
    class AssistantDomain {
        /** 助手域会话列表（assistant.list；wire 形状与 session.list 同源）。 */
        val sessions = mutableStateListOf<SessionMeta>()

        /** 助手域会话级忙闲投影（assistant.busy 广播维护；列表 running 位的并集来源）。 */
        val busyMap = androidx.compose.runtime.mutableStateMapOf<String, Boolean>()

        /** 当前打开的助手会话（assistant.new / 点会话行；空 = 没打开）。 */
        var currentId by mutableStateOf("")
            internal set

        /** 助手会话时间线（八类块 + 派发卡）。 */
        val blocks = mutableStateListOf<ThreadBlock>()

        /** 忙闲（assistant.busy / done/error 收敛——当前打开的助手会话）。 */
        var busy by mutableStateOf(false)
            internal set

        /** 挂起确认（assistant.confirmRequest；裁决走 assistant.confirm）。 */
        var confirm by mutableStateOf<PendingConfirm?>(null)
            internal set

        /** 上下文占用（assistant.done / assistant.history 的 context 键）。 */
        var contextUsage by mutableStateOf<ContextUsage?>(null)
            internal set

        /** 整段会话统计（stats 键；null = 无）。 */
        var sessionStats by mutableStateOf<SessionStats?>(null)
            internal set

        /** 当前会话模型（assistant.history 的 model / assistant.modelChanged）。 */
        var sessionModel by mutableStateOf("")
            internal set

        /** 模型菜单选中态（会话选择 → 回落 default 角色，同主域 activeModelId 口径）。 */
        var pickedModelId by mutableStateOf("")
            internal set

        /** 权限档（assistant.history 的 approval 回读 / assistant.approvalChanged）。 */
        var approval by mutableStateOf<String?>(null)
            internal set

        /** 历史装载版本号（ThreadScreen 据此滚到底）。 */
        var historyVersion by mutableStateOf(0)
            internal set

        /** 流式内容版本号（sticky-follow 的逐帧补滚数据源）。 */
        var streamVersion by mutableStateOf(0)
            internal set

        /** 当前流式 assistant 块下标（-1 = 无）。 */
        var streamingIdx = -1

        /** 已收尾派发集合（迟到的子事件不许把卡翻回运行中；生命周期 = 当前时间线）。 */
        val endedDispatches = mutableSetOf<String>()

        /** 某助手会话此刻是否在生成（快照 running 位 ∪ busy 投影——与主域
         *  isSessionRunning 同一条并集口径）。 */
        fun isRunning(id: String): Boolean = busyMap[id] == true

        /** 切入助手会话：清当前时间线与域内状态（对齐主域 switchTo 的同一套清账）。 */
        fun switchTo(id: String) {
            if (currentId == id) return
            currentId = id
            blocks.clear()
            streamingIdx = -1
            endedDispatches.clear()
            confirm = null
            busy = false
            contextUsage = null
            sessionStats = null
            sessionModel = ""
            pickedModelId = ""
            approval = null
            historyVersion = 0
            streamVersion = 0
        }
    }

    /** 助手域状态（UI 页面与 Composer 瘦身版的数据源）。 */
    val asst = AssistantDomain()

    /** 刷新助手域会话列表（assistant.list；打开助手页时调）。 */
    fun refreshAssistantSessions() {
        client.call("assistant.list", null) { err, result ->
            if (err != null) {
                Log.w(TAG, "assistant.list 失败: ${err.optString("message")}")
                return@call
            }
            applyAssistantSessionList(result)
        }
    }

    /** 新建助手会话并切入（assistant.new；助手列表「新对话」/ 发送前无会话时也走这里）。 */
    fun assistantNew(onReady: (String) -> Unit = {}) {
        client.call("assistant.new", null) { err, result ->
            if (err != null) {
                Log.w(TAG, "assistant.new 失败: ${err.optString("message")}")
                asst.blocks.add(ThreadBlock.Error("新建助手会话失败: ${err.optString("message")}"))
                return@call
            }
            val id = (result as? JSONObject)?.optString("session_id").orEmpty()
            Log.i(TAG, "assistant.new → $id")
            if (id.isNotEmpty()) {
                asst.switchTo(id)
                onReady(id)
            }
        }
    }

    /** 打开既有助手会话：assistant.history 装载（不发 session.resume——助手域没有
     *  连接焦点语义，切换就是换 currentId + 拉历史）。 */
    fun openAssistantSession(id: String) {
        if (id != asst.currentId) asst.switchTo(id)
        asstHistoryLoading = true
        client.call("assistant.history", JSONObject().put("session_id", id).put("tail", TAIL_WINDOW)) { err, result ->
            asstHistoryLoading = false
            if (id != asst.currentId) return@call
            if (err != null) {
                Log.w(TAG, "assistant.history($id) 失败: ${err.optString("message")}")
                asst.blocks.clear()
                asst.blocks.add(ThreadBlock.Error("历史加载失败: ${err.optString("message")}（返回后重新进入可重试）"))
                asst.historyVersion++
                return@call
            }
            applyAssistantHistory(result as? JSONObject)
        }
    }

    /** 助手历史装载中（助手页的「正在加载…」态；openAssistantSession 发出到应答之间）。 */
    var asstHistoryLoading by mutableStateOf(false)
        internal set

    /** 给当前助手会话发一条消息（assistant.send）。
     *  effort 是**请求级参数**（protocol.ChatSendParams.Effort——与桌面端同语义）。
     *  附件（2026-12 助手附件，本次补齐文件入口）：pendingImages/pendingFiles
     *  非空时随消息带上（images/files base64 形态——手机与后端不同机，path 形态
     *  不适用；wire 形状与主域 send 同款），发送受理即清暂存。 */
    fun assistantSend(text: String, effort: String = "") {
        val t = text.trim()
        if (t.isEmpty() && pendingImages.isEmpty() && pendingFiles.isEmpty()) return
        if (asst.currentId.isEmpty()) {
            assistantNew(onReady = { assistantSend(t, effort) })
            return
        }
        asst.busy = true
        val images = pendingImages.toList()
        val files = pendingFiles.toList()
        Log.i(TAG, "assistant.send session=${asst.currentId} text=${t.take(80)} effort=$effort img=${images.size} file=${files.size}")
        val params = JSONObject().put("session_id", asst.currentId).put("text", t)
        // 空串不带 effort 键（wire 纪律：值缺省整键缺席）
        if (effort.isNotBlank()) params.put("effort", effort)
        if (images.isNotEmpty()) {
            params.put(
                "images",
                org.json.JSONArray().apply {
                    images.forEach {
                        put(
                            JSONObject()
                                .put("mime", it.mime)
                                .put("data", it.data)
                                .put("name", it.name),
                        )
                    }
                },
            )
        }
        // 文件附件（2026-12 批次 1）：与主域 sendMsg 的 files base64 组装同款
        //（name + data；mime 后端按 name 推断，wire 不带）。
        if (files.isNotEmpty()) {
            params.put(
                "files",
                org.json.JSONArray().apply {
                    files.forEach {
                        put(
                            JSONObject()
                                .put("name", it.name)
                                .put("data", it.data),
                        )
                    }
                },
            )
        }
        client.call("assistant.send", params) { err, _ ->
            if (err != null) {
                asst.busy = false
                val msg = err.optString("message")
                Log.w(TAG, "assistant.send 被拒: code=${err.optInt("code")} $msg")
                asst.blocks.add(ThreadBlock.Error("发送失败: $msg"))
            } else {
                // 发送受理才清暂存（被拒时保留——用户可改完重发，不丢已选附件）
                pendingImages.clear()
                pendingFiles.clear()
                attachNotice = null
                Log.i(TAG, "assistant.send accepted")
            }
        }
    }

    /** 取消当前助手会话的轮（assistant.cancel）。 */
    fun assistantCancel(onDone: ((ok: Boolean, msg: String) -> Unit)? = null) {
        if (asst.currentId.isEmpty()) {
            onDone?.invoke(false, "还没有打开助手会话")
            return
        }
        Log.i(TAG, "assistant.cancel session=${asst.currentId}")
        client.call("assistant.cancel", JSONObject().put("session_id", asst.currentId)) { err, _ ->
            if (err != null) {
                val msg = err.optString("message").ifBlank { "后端未连接" }
                onDone?.invoke(false, msg)
            } else {
                onDone?.invoke(true, "")
            }
        }
    }

    /** 裁决助手域确认门（assistant.confirm；allow=false = 拒绝；answer 非空 = ask_user 的文本回答）。 */
    fun resolveAssistantConfirm(allow: Boolean, answer: String = "") {
        val c = asst.confirm ?: return
        Log.i(TAG, "assistant.confirm id=${c.id} allow=$allow answer=${answer.isNotEmpty()}")
        val params = JSONObject()
            .put("session_id", c.sessionId)
            .put("id", c.id)
            .put("allow", allow)
        if (answer.isNotEmpty()) params.put("answer", answer)
        client.call(
            "assistant.confirm",
            params,
        ) { err, _ ->
            if (err != null) {
                Log.w(TAG, "assistant.confirm 失败: ${err.optString("message")}")
                asst.blocks.add(ThreadBlock.Error("确认裁决失败: ${err.optString("message")}"))
            }
            asst.confirm = null
        }
    }

    /** 手动压缩当前助手会话（assistant.compact）。结果经 onDone 回调；压缩成功本身
     *  由 assistant.compacted 事件插「已压缩历史」块。 */
    fun assistantCompact(onDone: ((compacted: Boolean, reason: String) -> Unit)? = null) {
        if (asst.currentId.isEmpty()) return
        Log.i(TAG, "assistant.compact session=${asst.currentId}")
        client.call("assistant.compact", JSONObject().put("session_id", asst.currentId)) { err, result ->
            if (err != null) {
                val msg = err.optString("message")
                Log.w(TAG, "assistant.compact 失败: $msg")
                asst.blocks.add(ThreadBlock.Error("压缩失败: $msg"))
                onDone?.invoke(false, msg)
                return@call
            }
            val compacted = (result as? JSONObject)?.optBoolean("compacted") ?: false
            val reason = (result as? JSONObject)?.optString("reason").orEmpty()
            if (!compacted && reason.isNotEmpty()) {
                asst.blocks.add(ThreadBlock.Error("压缩未执行：$reason"))
            }
            onDone?.invoke(compacted, reason)
        }
    }

    /** 改当前助手会话的权限档（assistant.approval；乐观置位，广播回来再对齐）。 */
    fun changeAssistantApproval(mode: String) {
        val sid = asst.currentId
        if (sid.isEmpty()) return
        asst.approval = mode
        client.call(
            "assistant.approval",
            JSONObject().put("session_id", sid).put("approval", mode),
        ) { err, _ ->
            if (err != null) Log.w(TAG, "assistant.approval 失败: ${err.optString("message")}")
        }
    }

    /** 切当前助手会话的模型（assistant.model；选中态由 assistant.modelChanged 对齐）。 */
    fun changeAssistantModel(id: String, onDone: ((ok: Boolean, msg: String) -> Unit)? = null) {
        val sid = asst.currentId
        if (sid.isEmpty()) {
            onDone?.invoke(false, "还没有打开助手会话")
            return
        }
        client.call(
            "assistant.model",
            JSONObject().put("session_id", sid).put("model_id", id),
        ) { err, _ ->
            if (err != null) {
                val msg = err.optString("message")
                Log.w(TAG, "assistant.model 失败: $msg")
                onDone?.invoke(false, msg)
            } else {
                onDone?.invoke(true, "")
            }
        }
    }

    /** 打开助手域的子会话临时视图：与主域 openChildSession 同语义，历史走
     *  **assistant.history** 域内寻址（子会话库行继承父 space='assistant'）——
     *  chat.history 主域寻址助手子会话会报错。child* 状态桶复用主域那套。 */
    fun openAssistantChildSession(childId: String, title: String) {
        if (viewChildSessionId == childId) return
        viewChildSessionId = childId
        childViewTitle = title
        childBlocks.clear()
        childStreamingIdx = -1
        childConfirm = null
        childBusy = false
        childHistoryVersion = 0
        childStreamVersion = 0
        childModelId = ""
        childHistoryLoading = true
        client.call("assistant.history", JSONObject().put("session_id", childId)) { err, result ->
            childHistoryLoading = false
            if (viewChildSessionId != childId) return@call
            if (err != null) {
                Log.w(TAG, "assistant.history(child $childId) 失败: ${err.optString("message")}")
                childBlocks.add(ThreadBlock.Error("子会话历史加载失败: ${err.optString("message")}"))
                return@call
            }
            historyLoader.applyChildHistory(result as? JSONObject)
        }
    }

    /** assistant.list / assistant.tasks 的应答解析（wire 与 session.list 同源——
     *  复用 historyLoader.applySessionList 的逐字段口径，落进助手域列表；running 位并入 busy 投影）。 */
    internal fun applyAssistantSessionList(r: Any?) {
        val arr: org.json.JSONArray = when (r) {
            is org.json.JSONArray -> r
            is JSONObject -> r.optJSONArray("sessions") ?: return
            else -> return
        }
        val list = (0 until arr.length()).mapNotNull { idx ->
            val o = arr.getJSONObject(idx)
            val id = o.optString("id")
            if (id.isEmpty()) return@mapNotNull null
            if (o.optBoolean("running")) asst.busyMap[id] = true
            SessionMeta(
                id = id,
                title = o.optString("title").ifBlank { "新对话" },
                updatedAt = relativeTime(o.optString("updated_at")),
                messages = o.optInt("messages"),
                archived = o.optBoolean("archived"),
                workspace = "",
                running = o.optBoolean("running") || asst.busyMap[id] == true,
            )
        }.filter { !it.archived }
        asst.sessions.clear()
        asst.sessions.addAll(list)
        Log.i(TAG, "assistant.list → ${list.size} 条")
    }

    /** assistant.history → 块（与主域 applyHistory 同一份解析器 historyLoader.parseHistoryBlocks；
     *  无分页/缓存薄记——助手会话通常较短，TAIL_WINDOW 内一次装完）。 */
    internal fun applyAssistantHistory(r: JSONObject?) {
        if (r == null) return
        val (parsed, todosArr, _, _) = historyLoader.parseHistoryBlocks(r)
        asst.endedDispatches.clear()
        asst.blocks.clear()
        asst.blocks.addAll(parsed)
        asst.streamingIdx = -1
        todosArr?.let { arr ->
            val list = (0 until arr.length()).map { arr.getJSONObject(it) }
                .map { todoItemOf(it.optString("content"), it.optString("status")) }
            asst.blocks.add(ThreadBlock.Todo(list))
        }
        r.optJSONObject("context")?.let { asst.contextUsage = reducers.parseContext(it) }
        r.optJSONObject("stats")?.let { asst.sessionStats = parseStats(it) }
        asst.sessionModel = r.optString("model", "")
        asst.pickedModelId = asst.sessionModel
        asst.busy = r.optBoolean("busy")
        if (asst.currentId.isNotEmpty()) asst.busyMap[asst.currentId] = asst.busy
        r.optString("approval", "").takeIf { it.isNotEmpty() }?.let { asst.approval = it }
        r.optJSONObject("pending")?.let { pend ->
            asst.confirm = PendingConfirm(
                sessionId = pend.optString("session_id", asst.currentId),
                id = pend.optString("id"),
                name = pend.optString("name"),
                arguments = pend.optString("arguments"),
                prompt = pend.optString("prompt"),
            kind = pend.optString("kind"),
            options = pend.optJSONArray("options")?.let { a ->
                (0 until a.length()).map { a.optString(it) }
            } ?: emptyList(),
            )
        }
        Log.i(TAG, "assistant.history(${asst.currentId}): ${parsed.size} 条消息 → ${asst.blocks.size} 块 busy=${asst.busy}")
        asst.historyVersion++
    }

    /**
     * 助手域事件归约：[chat] = 归一后的 chat.* 等价事件名（ASSISTANT_TO_CHAT 反查），
     * [p] = 原始载荷（与 chat.* 同构）。块构建逻辑与主 reducer（onEvent）逐条同源，
     * 数据桶全部落 [asst]——两域的 currentSessionId 过滤互不污染。
     */
    internal fun assistantRouteEvent(chat: String, p: JSONObject) {
        val dispatchId = p.optString("dispatch_id", "")
        // 确认门先行（重放主 reducer onEvent 同一条纪律）：子 Agent 的确认请求必带
        // dispatch_id，不能进下面的子会话丢弃分支——助手域的高危确认同样全来自
        // 子派发（助手 Agent 内置 run_task/委派），漏了这条确认弹窗永不出现。
        if (chat == "chat.confirmRequest") {
            if (dispatchId.isNotEmpty()) {
                // 归属判定两域各查各的卡（主域在跑的派发与助手域的派发互不可见）
                val idx = reducers.dispatchCardIdx(dispatchId)
                val aidx = reducers.dispatchCardIdx(dispatchId, asst.blocks)
                if (idx < 0 && aidx < 0) {
                    Log.w(TAG, "assistant.confirmRequest 无归属派发卡 dispatch=$dispatchId")
                    return
                }
            } else if (p.optString("session_id", "") != asst.currentId) {
                return
            }
            val pc = PendingConfirm(
                sessionId = p.optString("session_id", ""),
                id = p.optString("id"),
                name = p.optString("name"),
                arguments = p.optString("arguments"),
                prompt = p.optString("prompt"),
            kind = p.optString("kind"),
            options = p.optJSONArray("options")?.let { a ->
                (0 until a.length()).map { a.optString(it) }
            } ?: emptyList(),
            )
            asst.confirm = pc
            // 子会话视图双投（对齐主 reducer：卡上的子会话 id 与 viewChildSessionId 相等）
            val ownerChild = reducers.dispatchChildSessionOf(dispatchId)
                .ifBlank { reducers.dispatchChildSessionOf(dispatchId, asst.blocks) }
            if (dispatchId.isNotEmpty() && ownerChild == viewChildSessionId) {
                childConfirm = pc
            }
            return
        }
        // 派发卡事件：归属键是 owner_session_id——先于子会话过滤器处理
        if (chat == "chat.dispatchStart" || chat == "chat.dispatchEnd") {
            if (p.optString("owner_session_id") != asst.currentId) return
            if (chat == "chat.dispatchStart") {
                val todos = p.optJSONArray("todos")
                val (tDone, tTotal) = if (todos != null) {
                    val list = (0 until todos.length()).map { todos.getJSONObject(it) }
                    list.count { it.optString("status") == "done" } to list.size
                } else {
                    -1 to 0
                }
                val cardId = p.optString("dispatch_id", "")
                val idx = if (cardId.isNotEmpty()) {
                    reducers.dispatchCardIdx(cardId, asst.blocks)
                } else {
                    asst.blocks.indexOfLast { it is ThreadBlock.Dispatch && it.done.not() }
                }
                val fresh = ThreadBlock.Dispatch(
                    agentName = p.optString("agent_name"),
                    task = p.optString("task"),
                    conclusion = "",
                    sessionId = p.optString("session_id", ""),
                    done = false,
                    isError = false,
                    dispatchId = cardId,
                    childTodosDone = tDone,
                    childTodosTotal = tTotal,
                )
                if (idx >= 0) {
                    val prev = asst.blocks[idx] as ThreadBlock.Dispatch
                    asst.blocks[idx] = prev.copy(
                        agentName = fresh.agentName.ifBlank { prev.agentName },
                        task = fresh.task.ifBlank { prev.task },
                        sessionId = fresh.sessionId.ifBlank { prev.sessionId },
                        done = false,
                        isError = false,
                        conclusion = "",
                        dispatchId = cardId.ifBlank { prev.dispatchId },
                        childTodosDone = if (tDone >= 0) tDone else prev.childTodosDone,
                        childTodosTotal = if (tTotal > 0) tTotal else prev.childTodosTotal,
                    )
                } else {
                    asst.blocks.add(fresh)
                }
            } else {
                val exact = reducers.dispatchCardIdx(dispatchId, asst.blocks)
                val i = if (exact >= 0) {
                    exact
                } else {
                    Log.w(TAG, "assistant.dispatchEnd 无 dispatch_id 配对卡 dispatch=$dispatchId")
                    asst.blocks.indexOfLast { it is ThreadBlock.Dispatch && it.done.not() }
                }
                if (i >= 0) {
                    val d = asst.blocks[i] as ThreadBlock.Dispatch
                    val finalTodos = p.optJSONArray("final_todos")
                    val (fDone, fTotal) = if (finalTodos != null && finalTodos.length() > 0) {
                        val list = (0 until finalTodos.length()).map { finalTodos.getJSONObject(it) }
                        list.count { it.optString("status") == "done" } to list.size
                    } else {
                        d.childTodosDone to d.childTodosTotal
                    }
                    asst.endedDispatches.add(dispatchId)
                    asst.blocks[i] = d.copy(
                        // 空 result = 子 Agent 没产出文本结论（对齐历史路径的既有兜底）
                        conclusion = p.optString("result").ifBlank { "（子 Agent 没有产出文本结论）" },
                        sessionId = p.optString("session_id", "").ifBlank { d.sessionId },
                        done = true,
                        isError = p.optBoolean("is_error"),
                        childTodosDone = fDone,
                        childTodosTotal = fTotal,
                    )
                } else {
                    Log.w(TAG, "assistant.dispatchEnd 无配对卡 dispatch=$dispatchId")
                }
            }
            return
        }
        // 子会话事件（dispatch_id 非空）不进助手时间线；toolCall/toolResult 归并进
        // **助手域**派发卡（asst.blocks——主域的卡查不到它）。todo.updated 归并清单进度。
        if (dispatchId.isNotEmpty()) {
            reducers.markDispatchRunning(dispatchId, asst.blocks)
            // 子会话视图双投：归属判定两域各查各的卡（与确认门同一条口径）
            val ownerChild = reducers.dispatchChildSessionOf(dispatchId)
                .ifBlank { reducers.dispatchChildSessionOf(dispatchId, asst.blocks) }
            val routeToChild = viewChildSessionId != null &&
                (ownerChild == viewChildSessionId || dispatchId == viewChildSessionId)
            if (routeToChild) reducers.childRouteEvent(chat, p)
            when (chat) {
                "chat.toolCall" -> {
                    reducers.upsertChildTool(
                        asst.blocks,
                        dispatchId,
                        id = p.optString("id"),
                        name = p.optString("name"),
                        argsSummary = argsSummary(p.optString("arguments")),
                        running = true,
                        isError = false,
                        durationMs = 0,
                    )
                }
                "chat.toolResult" -> {
                    reducers.finishChildTool(asst.blocks, dispatchId, p.optString("id"), p.optBoolean("is_error"))
                }
                "todo.updated" -> {
                    reducers.mergeChildTodos(asst.blocks, dispatchId, p.optJSONArray("items"))
                }
            }
            return
        }
        val sid = p.optString("session_id", "")
        // 子会话视图双投：子会话自己的事件（无 dispatch_id、session_id == 子会话 id）
        // 投进 child 状态（与域无关——子会话 id 全局唯一）。
        if (viewChildSessionId != null && sid == viewChildSessionId) {
            reducers.childRouteEvent(chat, p)
            return
        }
        when (chat) {
            "chat.userMessage" -> {
                if (sid != asst.currentId) return
                val msg = p.optJSONObject("message") ?: return
                val text = msg.optString("content")
                if (text.startsWith(HIDDEN_NOTICE_PREFIX)) return
                val notice = noticeKind(text)
                if (notice != null) {
                    asst.blocks.add(ThreadBlock.Notice(label = notice.second, text = text.removePrefix(notice.first).trim()))
                    asst.streamingIdx = -1
                    return
                }
                val last = asst.blocks.lastOrNull()
                if (last !is ThreadBlock.User || last.text != text) {
                    asst.blocks.add(ThreadBlock.User(text, emptyList(), localEpochMs = System.currentTimeMillis()))
                    asst.streamingIdx = -1
                }
            }

            "chat.delta" -> {
                if (sid != asst.currentId) return
                val kind = p.optString("kind", "text")
                val text = p.optString("text")
                if (text.isEmpty()) return
                val i = asstEnsureStreamingBlock()
                val cur = asst.blocks[i] as ThreadBlock.Assistant
                asst.blocks[i] = if (kind == "reasoning") {
                    cur.copy(reasoning = (cur.reasoning ?: "") + text, streaming = true)
                } else {
                    cur.copy(content = cur.content + text, streaming = true)
                }
                asst.streamVersion++
            }

            "chat.toolCall" -> {
                if (sid != asst.currentId) return
                asst.streamingIdx = -1
                val name = p.optString("name")
                if (name == "agent_dispatch") return
                val args = p.optString("arguments")
                asst.blocks.add(
                    ThreadBlock.Tool(
                        id = p.optString("id"),
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
                if (sid != asst.currentId) return
                if (p.optString("name") == "agent_dispatch") {
                    // 调度结果：剥子会话 id 回填卡（与主 reducer 同一条口径）；
                    // 附件回流同主 reducer——挂到派发卡渲染成果条。
                    val (body, childSid) = splitDispatchResult(p.optString("content"))
                    val atts = parseAttachments(p.optJSONArray("attachments"))
                    val i = reducers.dispatchCardIdx(p.optString("id"), asst.blocks)
                    if (i >= 0) {
                        val d = asst.blocks[i] as ThreadBlock.Dispatch
                        asst.blocks[i] = d.copy(
                            // 空 result = 子 Agent 没产出文本结论（dispatchEnd 同款兜底；
                            // toolResult 先到，不兜则卡上结论行在 dispatchEnd 前空白）
                            conclusion = d.conclusion.ifBlank { body.ifBlank { "（子 Agent 没有产出文本结论）" } },
                            sessionId = childSid.ifBlank { d.sessionId },
                            attachments = if (atts.isNotEmpty()) atts else d.attachments,
                        )
                    }
                    return
                }
                val id = p.optString("id")
                val i = asst.blocks.indexOfFirst { it is ThreadBlock.Tool && it.id == id }
                if (i >= 0) {
                    val t = asst.blocks[i] as ThreadBlock.Tool
                    val atts = parseAttachments(p.optJSONArray("attachments"))
                    asst.blocks[i] = t.copy(
                        output = p.optString("content").take(4_000),
                        running = false,
                        isError = p.optBoolean("is_error"),
                        attachments = if (atts.isNotEmpty()) atts else t.attachments,
                    )
                } else {
                    Log.w(TAG, "assistant.toolResult 无配对调用块 id=$id")
                }
            }

            "chat.done" -> {
                if (sid != asst.currentId) return
                val msg = p.optJSONObject("message")
                if (msg != null) {
                    val i = asst.blocks.indexOfLast { it is ThreadBlock.Assistant }
                    if (i >= 0) {
                        val cur = asst.blocks[i] as ThreadBlock.Assistant
                        val content = msg.optString("content")
                        val reasoning = msg.optString("reasoning_content", "")
                        val model = msg.optString("model", cur.model)
                        val ftms = msg.optLong("first_token_ms", 0L)
                        val dur = msg.optLong("duration_ms", 0L)
                        val utok = msg.optInt("usage_tokens", 0)
                        asst.blocks[i] = cur.copy(
                            content = content.ifBlank { cur.content },
                            reasoning = reasoning.ifBlank { cur.reasoning },
                            reasoningMs = ftms,
                            model = model,
                            firstTokenMs = if (ftms > 0) ftms else null,
                            tokPerSec = if (utok > 0 && dur > 0) utok * 1000.0 / dur else null,
                            streaming = false,
                        )
                    }
                }
                p.optJSONObject("context")?.let { asst.contextUsage = reducers.parseContext(it) }
                p.optJSONObject("stats")?.let { asst.sessionStats = parseStats(it) }
                msg?.optString("model", "")?.takeIf { it.isNotEmpty() }?.let { asst.sessionModel = it }
                asst.busy = false
                if (sid.isNotEmpty()) {
                    asst.busyMap[sid] = false
                }
                asst.streamingIdx = -1
            }

            "chat.error" -> {
                if (sid != asst.currentId) return
                asst.streamingIdx = -1
                val aborted = p.optBoolean("aborted")
                val message = p.optString("message")
                p.optJSONObject("partial")?.let { partial ->
                    val content = partial.optString("content")
                    if (content.isNotEmpty()) {
                        val i = asst.blocks.indexOfLast { it is ThreadBlock.Assistant }
                        if (i >= 0) {
                            val cur = asst.blocks[i] as ThreadBlock.Assistant
                            asst.blocks[i] = cur.copy(content = content, streaming = false)
                        } else {
                            asst.blocks.add(ThreadBlock.Assistant(content, null, 0, asst.sessionModel))
                        }
                    }
                }
                asst.blocks.add(ThreadBlock.Error(if (aborted) "已中断" else message))
                asst.busy = false
                if (sid.isNotEmpty()) {
                    asst.busyMap[sid] = false
                }
            }

            "chat.busy" -> {
                if (sid.isNotEmpty()) {
                    asst.busyMap[sid] = p.optBoolean("busy")
                }
                if (sid != asst.currentId) return
                asst.busy = p.optBoolean("busy")
            }

            "chat.modelChanged" -> {
                if (sid != asst.currentId) return
                p.optString("model_id", "").takeIf { it.isNotEmpty() }?.let { asst.pickedModelId = it }
            }

            "chat.approvalChanged" -> {
                if (sid != asst.currentId) return
                p.optString("approval", "").takeIf { it.isNotEmpty() }?.let { asst.approval = it }
            }

            "todo.updated" -> {
                if (sid != asst.currentId) return
                val items = p.optJSONArray("items") ?: return
                val list = (0 until items.length()).map { items.getJSONObject(it) }
                    .map { todoItemOf(it.optString("content"), it.optString("status")) }
                val i = asst.blocks.indexOfFirst { it is ThreadBlock.Todo }
                when {
                    // 空清单 = 清空：移除既有块（与主域 reducer 同一条口径）
                    list.isEmpty() -> if (i >= 0) asst.blocks.removeAt(i)
                    i >= 0 -> asst.blocks[i] = ThreadBlock.Todo(list)
                    else -> asst.blocks.add(ThreadBlock.Todo(list))
                }
            }

            "chat.compacted" -> {
                if (sid != asst.currentId) return
                asst.blocks.add(
                    ThreadBlock.Compaction(
                        summary = p.optString("summary"),
                        shadowed = p.optInt("shadowed"),
                        before = p.optInt("before"),
                        after = p.optInt("after"),
                        manual = p.optBoolean("manual"),
                    ),
                )
            }

            else -> Log.d(TAG, "助手域忽略事件 $chat")
        }
    }

    internal fun asstEnsureStreamingBlock(): Int {
        if (asst.streamingIdx >= 0 && asst.streamingIdx < asst.blocks.size &&
            asst.blocks[asst.streamingIdx] is ThreadBlock.Assistant
        ) {
            return asst.streamingIdx
        }
        asst.blocks.add(ThreadBlock.Assistant(content = "", reasoning = null, reasoningMs = 0, model = asst.sessionModel, streaming = true))
        asst.streamingIdx = asst.blocks.size - 1
        return asst.streamingIdx
    }

    // ===== 子会话临时视图（对齐桌面端「临时视图」语义）=====
    //
    // 点派发卡进入子会话：数据源切到 child* 状态，主会话 blocks/busy/confirm
    // **原封不动**——返回即恢复，重开数据不丢。live 事件双投：子会话的事件
    // （session_id == viewChildSessionId，或 dispatch_id 归属该子会话）在归并进
    // 主时间线派发卡的同时投进 child 状态（对齐桌面端 reduceSessionStates 双投）。

    /** 正在查看的子会话 id（null = 不在子会话视图；返回主会话时置空）。 */
    var viewChildSessionId by mutableStateOf<String?>(null)

    /** 子会话视图的时间线。 */
    val childBlocks = mutableStateListOf<ThreadBlock>()

    /** 子会话视图的忙闲（chat.busy / done/error 收敛——该子会话自己的）。 */
    var childBusy by mutableStateOf(false)
        internal set

    /** 子会话视图的挂起确认（confirm 带 dispatch_id 的子确认，正常弹）。 */
    var childConfirm by mutableStateOf<PendingConfirm?>(null)
        internal set

    /** 子会话视图的历史装载版本（ThreadScreen 据此滚到底）。 */
    var childHistoryVersion by mutableStateOf(0)
        internal set

    /** 子会话历史**装载中**（openChildSession 发出 chat.history 到应答/失败之间）：
     *  ChildThreadView 的加载态依据——视图打开后异步拉历史，装载期间 childBlocks
     *  为空，没有这条 UI 会渲染成全空白页（问题：打开子会话没有 loading 指示）。
     *  失败（错误块已插入）与装载完成即复位。 */
    var childHistoryLoading by mutableStateOf(false)
        internal set

    /** 子会话视图的流式内容版本（同 streamVersion 的语义，child 块追加时 +1）。 */
    var childStreamVersion by mutableStateOf(0)
        internal set

    /** 子会话视图自己的流式块下标（-1 = 无）。 */
    internal var childStreamingIdx = -1

    /** childTodos 当前归属的子会话 id（沿用口径按它收窄——切子会话不清屏但清单不串台）。 */
    internal var childTodoOwner = ""

    /** 子会话视图的标题（Agent 名 + 任务摘要，由派发卡带入）。 */
    var childViewTitle by mutableStateOf("")

    /** 子会话**当前清单**（置底清单条的数据源）：todo.updated / chat.history 的
     *  todos 快照落这里，**不再往时间线插 Todo 块**（对齐桌面纪律——时间线是
     *  消息流，清单是会话级状态）。null = 没有清单（不渲染清单条）。 */
    var childTodos by mutableStateOf<List<TodoItem>?>(null)
        internal set

    /** 子会话**选中**的模型 id（chat.history 顶层 model 键回读 / chat.modelChanged
     *  按 session_id 归属维护；空 = 未知（老后端/未装载）→ 显示回落 default 角色，
     *  与主会话 activeModelId 同一条口径）。子会话页的模型菜单读它、
     *  changeSessionModelFor(childId) 切它（切了会同步更新 Agent 绑定）。 */
    var childModelId by mutableStateOf("")
        internal set

    /**
     * 各会话的实时忙闲（chat.busy 广播；多端共享进度的列表侧投影）：
     * 任意客户端（手机/桌面）上某会话开始/结束生成，这里都收到同一份广播。
     * 会话列表的「运行中」状态点读它——不只是当前打开的会话。
     */
    // Compose 可观察的 map（mutableStateMapOf）：投影本身就是 UI 状态的一部分，
    // 任何在组合里读它的地方（列表状态点/项目行计数）都随 busy 事件自动重组——
    // 用户报的「必须点进去再退出才亮」就是这条链断在"投影只是普通 map"上。
    internal val sessionBusy = androidx.compose.runtime.mutableStateMapOf<String, Boolean>()

    /**
     * 某会话此刻是否在生成（**投影口径**，与列表状态点同源）。
     *
     * UI 读它而不是只读 `SessionMeta.running`：`SessionMeta` 是不可变值，只有
     * `historyLoader.syncRunningDots()` 重建条目时才变；投影是 Compose 可观察的 state map，
     * 任何在组合里读它的地方都会随 busy 事件重组。两条都读（`session.running ||
     * isSessionRunning(id)`）= 服务端快照与实时广播取并集，任一来源更新都能点亮。
     */
    fun isSessionRunning(id: String): Boolean = sessionBusy[id] == true

    /** 挂起的工具确认（chat.confirmRequest；null = 无）。 */
    var confirm by mutableStateOf<PendingConfirm?>(null)
        internal set

    /** 当前会话标题（会话列表条目；未找到 = 新对话占位）。 */
    val sessionTitle: String
        get() = sessions.firstOrNull { it.id == currentSessionId }?.title ?: "新对话"

    val connected: Boolean get() = phase == RealPhase.Connected

    /** 当前连接凭证（remote.json Enabled 的后端要求 query token；空 = 未开远程）。 */
    var token by mutableStateOf("")
        internal set

    // ===== 附件（图片/文件上传 + 渲染元数据；对齐 docs/attachments.md）=====

    /** Composer 暂存的一张图片：data 为纯 base64（不带 data: 前缀——发送直接用）。
     *  [uri] 是内容选择器给的 content:// Uri（预览渲染 + 降级重读用），不进 wire。 */
    data class PendingImage(
        val id: String,
        val name: String,
        val mime: String,
        val data: String,
        val size: Long,
        val uri: android.net.Uri,
    )

    /** Composer 暂存的一个文件附件（base64 形态——手机与后端不同机，path 形态不适用）。 */
    data class PendingFile(
        val id: String,
        val name: String,
        val size: Long,
        val mime: String,
        val data: String,
        val uri: android.net.Uri,
    )

    /** 单图上限 5MB / 单文件 20MB / 各最多 4 个（与后端 server 校验同一套常量，
     *  常量本体在文件尾的 companion object 里）。 */

    /** 图片暂存（Composer 预览与发送的数据源）。 */
    val pendingImages = mutableStateListOf<PendingImage>()

    /** 文件暂存（同上）。 */
    val pendingFiles = mutableStateListOf<PendingFile>()

    /** 一次性提示（超限拒绝等）——Composer 预览区显示，随下一条消息清掉。 */
    var attachNotice by mutableStateOf<String?>(null)

    /** 暂存图片（base64 读盘 + 上限校验；任何一项非法拒整批并给原因——
     *  半收半拒会让「哪几张进了」变成猜谜，对齐桌面端 addPendingImages）。
     *  [items] = (content Uri, mime) 对（mime 由系统选择器解析）。 */
    fun addPendingImages(items: List<Pair<android.net.Uri, String>>) {
        if (items.isEmpty()) return
        val context = com.moyunteng.lxcode.remote.update.AppContextHolder.context
        val decoded = mutableListOf<PendingImage>()
        for ((uri, mime) in items) {
            val name = queryDisplayName(context, uri) ?: "图片"
            val bytes = try {
                context.contentResolver.openInputStream(uri)?.use { it.readBytes() }
            } catch (_: Exception) {
                null
            }
            if (bytes == null) {
                attachNotice = "图片 $name 读取失败"
                return
            }
            if (mime !in IMAGE_MIMES) {
                attachNotice = "不支持的图片类型：$name（支持 png/jpeg/webp/gif）"
                return
            }
            if (bytes.size > MAX_IMAGE_BYTES) {
                attachNotice = "图片 $name 超过 5MB 上限"
                return
            }
            decoded.add(
                PendingImage(
                    id = "img-${System.nanoTime()}-$name",
                    name = name,
                    mime = mime,
                    data = android.util.Base64.encodeToString(bytes, android.util.Base64.NO_WRAP),
                    size = bytes.size.toLong(),
                    uri = uri,
                ),
            )
        }
        if (pendingImages.size + decoded.size > MAX_IMAGE_COUNT) {
            attachNotice = "一条消息最多 $MAX_IMAGE_COUNT 张图片"
            return
        }
        pendingImages.addAll(decoded)
        attachNotice = null
    }

    /** 暂存一个文件（文件不做类型白名单——Agent 什么都要能收）。 */
    fun addPendingFiles(items: List<android.net.Uri>) {
        if (items.isEmpty()) return
        val context = com.moyunteng.lxcode.remote.update.AppContextHolder.context
        val decoded = mutableListOf<PendingFile>()
        for (uri in items) {
            val name = queryDisplayName(context, uri) ?: "文件"
            val mime = context.contentResolver.getType(uri) ?: "application/octet-stream"
            val bytes = try {
                context.contentResolver.openInputStream(uri)?.use { it.readBytes() }
            } catch (_: Exception) {
                null
            }
            if (bytes == null) {
                attachNotice = "文件 $name 读取失败"
                return
            }
            if (bytes.size > MAX_FILE_BYTES) {
                attachNotice = "文件 $name 超过 20MB 上限（当前 ${formatBytes(bytes.size.toLong())}）"
                return
            }
            decoded.add(
                PendingFile(
                    id = "file-${System.nanoTime()}-$name",
                    name = name,
                    size = bytes.size.toLong(),
                    mime = mime,
                    data = android.util.Base64.encodeToString(bytes, android.util.Base64.NO_WRAP),
                    uri = uri,
                ),
            )
        }
        if (pendingFiles.size + decoded.size > MAX_FILE_COUNT) {
            attachNotice = "一条消息最多 $MAX_FILE_COUNT 个文件"
            return
        }
        pendingFiles.addAll(decoded)
        attachNotice = null
    }

    fun removePendingImage(id: String) {
        pendingImages.removeAll { it.id == id }
    }

    fun removePendingFile(id: String) {
        pendingFiles.removeAll { it.id == id }
    }

    /** content:// Uri 的显示名（OpenableColumns.DISPLAY_NAME；取不到回落 null）。 */
    internal fun queryDisplayName(context: android.content.Context, uri: android.net.Uri): String? {
        try {
            context.contentResolver.query(uri, null, null, null, null)?.use { c ->
                val idx = c.getColumnIndex(android.provider.OpenableColumns.DISPLAY_NAME)
                if (idx >= 0 && c.moveToFirst()) return c.getString(idx)
            }
        } catch (_: Exception) {
        }
        return null
    }

    /** 附件取件 URL（渲染缩略图/下载用）：`http://<addr>/attachments/<path>?token=...`。
     *  引用形态校验与桌面端 attachmentURL 同一份：只认两段、拒绝穿越。
     *  addr 可能带 wss://（樱花隧道扫码配对）/ws:// 前缀——剥掉保持 http://<host:port>
     *  原形态（配对前行为不变；经隧道的附件下载是明文 http 会被节点 501，属已知限制）。 */
    fun attachmentURL(path: String): String? {
        if (path.isEmpty() || path.contains("..") || path.startsWith("/") || path.contains("\\")) return null
        val segs = path.split("/").filter { it.isNotEmpty() }
        if (segs.size != 2) return null
        val q = if (token.isNotBlank()) "?token=${java.net.URLEncoder.encode(token, "UTF-8")}" else ""
        return "http://${addr.trim().substringAfter("://", addr.trim()).trimEnd('/')}/attachments/${segs[0]}/${java.net.URLEncoder.encode(segs[1], "UTF-8")}$q"
    }

    /** llm.Message 的 attachments JSON → AttachmentMeta 列表（缺键 = 空表）。 */
    internal fun parseAttachments(arr: org.json.JSONArray?): List<AttachmentMeta> {
        if (arr == null) return emptyList()
        return (0 until arr.length()).mapNotNull { i ->
            val a = arr.optJSONObject(i) ?: return@mapNotNull null
            val p = a.optString("path")
            if (p.isEmpty()) return@mapNotNull null
            AttachmentMeta(
                path = p,
                name = a.optString("name"),
                size = a.optLong("size", 0),
                mime = a.optString("mime", ""),
                kind = if (a.optString("kind") == "image") "image" else "file",
                caption = a.optString("caption", ""),
            )
        }
    }

    // ===== 模型注册表（model.list；composer 模型菜单数据源）=====
    //
    // 对齐桌面端 settings-models.ts 的 mapModels：按 base_url（完整端点）分组投影，
    // efforts 派生自 capabilities.reasoning（未声明 reasoning 的模型整个强度入口隐藏
    // ——对非推理端点传 effort 参数会 400）。模型是**角色绑定**不是客户端本地态：
    // 选中态 = roles.default（role.set 改，model.changed 广播回来对齐）。

    /** 注册表快照（enabled 条目按端点分组；model.changed 时重拉）。 */
    var modelGroups by mutableStateOf<List<ModelGroup>>(emptyList())
        internal set

    /** 全局 default 角色绑定的模型 id（后端是唯一事实源；空 = 未绑定）。
     *  语义 = **新会话的默认模型**（设置页管），不是当前会话的选中态——当前会话
     *  看 [sessionModel]（chat.history.model / chat.modelChanged）。 */
    var defaultModelId by mutableStateOf("")
        internal set

    /** 当前会话**选中**的模型 id（会话级状态：chat.history 的 model 键回读 /
     *  chat.modelChanged 广播）。空 = 未知（刚建会话/老后端）→ 显示回落
     *  [defaultModelId]。与 defaultModelId 的分工见上——混用正是「一个会话切
     *  模型所有会话跟着变」那个 bug。 */
    internal var pickedModelId by mutableStateOf("")

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

    /** 切换**当前会话**的模型（chat.model：后端落库 + 广播，只改这一条会话）。
     *  与 role.set 的分工：role.set 改的是全局 default 角色（= 新会话的默认模型，
     *  设置页语义），拿它切模型会让所有会话跟着变——这正是用户报的那个 bug。
     *  [onDone] 回调结果（ok=false 时 msg 是错误信息——调用方给用户反馈；
     *  选中态不本地乐观改：后端成功才广播 chat.modelChanged，归约层按它对齐，
     *  失败时显示自然停在旧模型上，不假装成功）。 */
    fun changeSessionModel(id: String, onDone: ((ok: Boolean, msg: String) -> Unit)? = null) {
        changeSessionModelFor(currentSessionId, id, onDone)
    }

    /** 设置**全局 default 角色**的模型（role.set：新会话的默认模型——设置页语义；
     *  与 [changeSessionModel] 的会话级切换分工明确，混用正是「一个会话切模型所有
     *  会话跟着变」那个 bug）。成功后 model.list 快照刷新回读对齐 defaultModelId。 */
    fun setDefaultModel(id: String, onDone: ((ok: Boolean, msg: String) -> Unit)? = null) {
        Log.i(TAG, "role.set role=default model=$id")
        client.call(
            "role.set",
            JSONObject().put("role", "default").put("model_id", id),
        ) { err, _ ->
            if (err != null) {
                val msg = err.optString("message")
                Log.w(TAG, "role.set 失败: $msg")
                onDone?.invoke(false, msg)
            } else {
                // 选中态以后端为准：role.set 成功后端即广播 model.changed，
                // 归约层 refreshModels 重拉快照对齐 defaultModelId，本地不乐观改
                onDone?.invoke(true, "")
            }
        }
    }

    /** 切换**指定会话**（含子会话）的模型：changeSessionModel 的带 id 变体。
     *  子会话页的模型菜单走它——2026-12 语义升级：chat.model 带子会话 id 时后端
     *  除落库 sessions.model 外同步更新该子会话 Agent 的 agents.model 绑定，
     *  即「给这个子 Agent 换模型」——主 Agent 之后**新派发**的子会话也用新模型
     *  （不只是这一条子会话的续跑）。主会话切模型不受影响（仍是会话级状态）。 */
    fun changeSessionModelFor(sessionId: String, id: String, onDone: ((ok: Boolean, msg: String) -> Unit)? = null) {
        if (sessionId.isEmpty()) {
            onDone?.invoke(false, "还没有打开会话")
            return
        }
        Log.i(TAG, "chat.model session=$sessionId → $id")
        client.call(
            "chat.model",
            JSONObject().put("session_id", sessionId).put("model_id", id),
        ) { err, _ ->
            if (err != null) {
                val msg = err.optString("message")
                Log.w(TAG, "chat.model 失败: $msg")
                onDone?.invoke(false, msg)
            } else {
                onDone?.invoke(true, "")
            }
        }
    }

    internal fun applyModelList(r: JSONObject?) {
        if (r == null) return
        // 投影口径（组合键主键 / 显示名 / 选中态比较）在纯函数里，JVM 单测钉住：
        // model.list 的 `id` 是**条目自己的 model id**，注册表主键是
        // `Key() = provider::id`（无 provider 时纯 id）——只读纯 id 的后果：填了
        // provider 标签后选中态不高亮（后端回的 chat.history.model /
        // chat.modelChanged.model_id 是组合键），药丸还会把组合键原样显示出来。
        val snapshot = parseModelList(r)
        modelGroups = snapshot.groups
        // default 角色绑定是唯一事实源（对齐桌面端 settings.tsx：派生选中态，不猜）
        defaultModelId = snapshot.defaultModelId
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
        reconnecting = false // 用户显式发起 = 不在自动重连态（成功或失败由 onState 重述）
        pairingInvalid = false // 手动 connect 重置配对失效态（重新配对后凭证已换）
        // 连接层语义：connect 以 force 作废旧在途 socket 并立即发起新尝试
        //（epoch 守卫保证旧回调只清账、不反杀新连接）——并发触发源各自 connect
        // 不会出现「无在途尝试且无重连排程」的保障空窗（重连不变式见 LxWsClient.connect）。
        client.connect(addr, token = token)
    }

    /** 断开（真实开关关闭）。 */
    fun disconnect() {
        client.disconnect() // 显式断开 = userClosed，不再自动重连
        phase = RealPhase.Offline
        phaseMessage = null
        reconnecting = false
        pairingInvalid = false
    }

    /** 亮屏即时重连（MainActivity onStart/onResume 调）：掉线且非用户主动断开时
     *  立即重连，不等自动重连的退避定时器。未进真实模式（从未连过）/已连接
     *  （守卫：回前台不拆活连接——connect 会 closeQuietly 重建，白扔一次握手）
     *  /配对已失效（401 后重连必再 401）时不动作。
     *  走 [connect] 完整语义（重置 userClosed/退避 + newWebSocket 重建）。
     *  连接层以 force 作废旧在途 socket 并发起新尝试（epoch 守卫防旧回调
     *  反杀，重连保障不变式见 LxWsClient.connect）：这里的 phase 检查挡住
     *  Connected/Offline，握手在途是 Connecting 之外的 Failed 态反复回调
     * （自动重连尝试中），放行到连接层由 epoch 守卫裁决。 */
    fun reconnectIfDown() {
        if (phase == RealPhase.Offline || phase == RealPhase.Connected || pairingInvalid) return
        connect(addr, token)
    }

    /** 回前台（ON_START）统一入口：掉线立即重连；已连接做 ping 体检（3s 超时
     *  无应答 = 死连接——TCP 没断但后端不回帧，主动断开走自动重连）。 */
    fun onForeground() {
        reconnectIfDown()
        if (phase == RealPhase.Connected) client.healthCheck()
    }

    /** 重试（断线 UI 的按钮；addr 传当前输入框地址——改了地址点重试应连新地址）。 */
    fun retry(addr: String = this.addr, token: String = this.token) {
        Log.i(TAG, "手动重试连接 $addr")
        connect(addr, token)
    }

    internal fun onState(connected: Boolean, message: String?) {
        if (connected) {
            phase = RealPhase.Connected
            phaseMessage = null
            reconnecting = false
            // 重连后忙闲以服务端事实为准（切入既有会话时 chat.history 会带回真实
            // busy 键重设）：留着断线前的 busy=true 会让「发送」一直当「停止」用，
            // 而停止的收敛依赖后端事件——假后端/断线场景会卡死在生成中。
            busy = false
            // 会话级忙闲投影同样清账：断线期间的 busy 广播收不到，留下的旧值
            // 会让列表的「运行中」状态点卡住。
            sessionBusy.clear()
            // 但清空不能留下「不知道」：hello 的 busy_sessions 就是**此刻在跑**
            // 的会话 id 快照（protocol.HelloResult，与 session.list 的 running 位
            // 同源，见 internal/server/emit.go 的 busySessions）——握手即建投影，
            // 手机一连上就能看到电脑端正在跑的会话（否则要等下一次 busy 广播，
            // 而 busy 只在轮次边界各发一次，错过了就永远不知道在跑）。
            client.helloResult?.optJSONArray("busy_sessions")?.let { arr ->
                for (i in 0 until arr.length()) {
                    val id = arr.optString(i, "")
                    if (id.isNotEmpty()) sessionBusy[id] = true
                }
            }
            historyLoader.syncRunningDots()
            refreshSessions()
            refreshProjects()
            // P1-2：投屏状态恢复链——断线期间 screen.changed 事件一次性丢失，
            // 手机端 screenState 停在断线前快照；若投屏实际还在跑，观看页拿到
            // 的 active=false 是旧值（黑屏卡住）；若实际已停，旧 active=true
            // 让断开自动退回（AppRoot LaunchedEffect）不触发。refreshScreen
            // 重拉 screen.state 得到真实 active（与助手域恢复链同款模式），
            // applyScreenState 更新的是同一个 screenState 源，页面据此自然刷新。
            refreshScreen()
            // 模型注册表：composer 模型菜单的数据源（桌面端是握手并行组的一步）
            refreshModels()
            // 断线期间错过的 chat.dispatchStart 不重放（事件一次性），而回放扫不出
            // 正在跑的派发（结果提示行要派发结束才落库）——重连即重拉当前会话的
            // chat.history，让 running_dispatches（新后端 0.3.6+）按此刻的运行中登记
            // 把卡补上子会话 id；老后端无该键时回放占位卡维持原样（不编 id）。
            if (currentSessionId.isNotEmpty()) {
                Log.i(TAG, "重连后重拉当前会话历史: $currentSessionId")
                loadHistory(currentSessionId)
            }
            // 助手域同款恢复链（对标上面的主域动作）：断线期间 assistant.* 事件
            // 同样一次性丢失，重连后无人重拉的话时间线停在断线前快照。三步：
            //   ① 清助手域忙闲投影——与主域 sessionBusy.clear() 同一条清账纪律
            //     （断线期间的 assistant.busy 收不到，旧值会让助手会话卡在「生成中」）；
            //   ② 列表 running 位以服务端事实重建（assistant.list 应答经
            //     applyAssistantSessionList 逐条补 busyMap）；
            //   ③ currentId 非空则**强制**重拉 assistant.history——与主域恢复链
            //     同理，断线期间错过的 assistant.dispatchStart 不重放，只有
            //     assistant.history 能按此刻的运行中登记把卡补回来。注意不走
            //     openAssistantSession 的「id != currentId 才拉」守卫（那是用户
            //     切换语义；恢复链即使 id 相同也要拉）——直接发请求，应答由
            //     applyAssistantHistory 按此刻 busy 键重建 asst.busy/busyMap
            //     （applyAssistantHistory 内已有该修正），投影正确归位。
            asst.busyMap.clear()
            asst.busy = false
            refreshAssistantSessions()
            if (asst.currentId.isNotEmpty()) {
                Log.i(TAG, "重连后重拉助手会话历史: ${asst.currentId}")
                openAssistantSession(asst.currentId)
            }
        } else if (phase != RealPhase.Offline) {
            // 用户主动 disconnect 后的迟到回调不再标失败
            if (pairingInvalid) return // 401 后的补发失败回调不覆盖失效态
            val wasConnected = phase == RealPhase.Connected
            phase = RealPhase.Failed
            phaseMessage = message
            // 自动重连已在后台排程（LxWsClient.scheduleReconnect）——UI 把
            // 「正在重连…」表达出来，用户不用去连接页手动重连。
            // ** Connecting 阶段除外**：刚点开开关/重试的首次连接失败后，用户
            // 看到的是失败态 + 重试按钮（显式 connect 已把 reconnecting 复位）；
            // 若此刻置 reconnecting=true，横幅说「正在重连」而失败态把按钮顶掉，
            // 用户没有任何可操作入口。只有「已连过又断了」（断线事件，此前是
            // Connected）或本来就在自动重连态（断线后的后续尝试又失败）才是
            // 自动重连语义。
            if (wasConnected || reconnecting) reconnecting = true
        }
    }

    /** WS 层上报「升级请求被 401 拒」（token 错/被吊销）：停重连 + UI 失效态。 */
    internal fun onPairingInvalid() {
        pairingInvalid = true
        reconnecting = false
        phase = RealPhase.Failed
        phaseMessage = "配对已失效：请在电脑端重新生成配对码"
        client.disconnect() // 显式断开 = userClosed，不再 scheduleReconnect
    }

    // ===== 对外操作 =====

    /** 刷新会话列表（session.list）。 */
    fun refreshSessions() {
        client.call("session.list", null) { err, result ->
            if (err != null) {
                Log.w(TAG, "session.list 失败: ${err.optString("message")}")
                return@call
            }
            historyLoader.applySessionList(result)
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
            historyLoader.applyProjectList(result)
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
                historyLoader.switchTo(id)
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

    /**
     * 打开子会话临时视图（点派发卡；对齐桌面端「临时视图」语义）。
     *
     * 只拉子会话历史进 child* 状态，**不动** currentSessionId / blocks / busy /
     * confirm——主会话状态原封保留，返回即恢复，重开数据不丢。
     * `chat.history` 支持按子会话 id 寻址（桌面端临时视图同款通道），
     * 不发 session.resume（子会话不是「当前会话」，切焦点是另一回事）。
     */
    fun openChildSession(childId: String, title: String) {
        if (viewChildSessionId == childId) return
        viewChildSessionId = childId
        childViewTitle = title
        childBlocks.clear()
        childStreamingIdx = -1
        childConfirm = null
        childBusy = false
        childHistoryVersion = 0
        childStreamVersion = 0
        childModelId = ""
        childHistoryLoading = true
        client.call("chat.history", JSONObject().put("session_id", childId)) { err, result ->
            childHistoryLoading = false
            if (err != null) {
                Log.w(TAG, "chat.history(child $childId) 失败: ${err.optString("message")}")
                childBlocks.add(ThreadBlock.Error("子会话历史加载失败: ${err.optString("message")}"))
                return@call
            }
            // 请求在途时已切走/关闭视图：过期响应作废
            if (viewChildSessionId != childId) return@call
            historyLoader.applyChildHistory(result as? JSONObject)
        }
    }

    /** 关闭子会话视图（返回主会话）：清 child 状态，主会话状态从未动过。 */
    fun closeChildSession() {
        viewChildSessionId = null
        childBlocks.clear()
        childStreamingIdx = -1
        childConfirm = null
        childBusy = false
        childHistoryVersion = 0
        childStreamVersion = 0
        childHistoryLoading = false
        childModelId = ""
        childTodos = null
        childTodoOwner = ""
        childViewTitle = ""
    }

    /** 裁决子会话视图里挂起的确认（与主会话 resolveConfirm 同款，session_id 取载荷）。 */
    fun resolveChildConfirm(allow: Boolean) {
        val c = childConfirm ?: return
        Log.i(TAG, "tool.confirm(child) id=${c.id} allow=$allow")
        client.call(
            "tool.confirm",
            JSONObject().put("session_id", c.sessionId).put("id", c.id).put("allow", allow),
        ) { err, _ ->
            if (err != null) {
                Log.w(TAG, "tool.confirm(child) 失败: ${err.optString("message")}")
                childBlocks.add(ThreadBlock.Error("确认裁决失败: ${err.optString("message")}"))
            }
            childConfirm = null
        }
    }

    /** 发送一条消息（chat.send；内容经事件流到达）。
     *  effort 是**请求级参数**（protocol.ChatSendParams.Effort——与桌面端同语义：
    *   每条消息携带；后端没有会话级 effort 方法、chat.history 也不回它，故无
    *   远端读写通道，本端不持久化选择——只是发送时带上当前内存视图的值）。
     *  effort 为空 = 不带键（老后端/未选档优雅降级）。
     *  暂存附件非空时随消息带上（images/files base64 形态——手机与后端不同机，
     *  path 形态不适用），发送成功即清暂存。 */
    fun send(text: String, effort: String = "") {
        val t = text.trim()
        if (t.isEmpty() && pendingImages.isEmpty() && pendingFiles.isEmpty()) return
        if (currentSessionId.isEmpty()) {
            newSession(onReady = { send(t, effort) })
            return
        }
        busy = true
        attachNotice = null
        val images = pendingImages.toList()
        val files = pendingFiles.toList()
        Log.i(TAG, "chat.send session=$currentSessionId text=${t.take(80)} effort=$effort img=${images.size} file=${files.size}")
        val params = JSONObject().put("session_id", currentSessionId).put("text", t)
        // 空串不带 effort 键（wire 纪律：值缺省整键缺席，不发明「空档位」语义）
        if (effort.isNotBlank()) params.put("effort", effort)
        if (images.isNotEmpty()) {
            params.put(
                "images",
                org.json.JSONArray().apply {
                    images.forEach {
                        put(
                            JSONObject()
                                .put("mime", it.mime)
                                .put("data", it.data)
                                .put("name", it.name),
                        )
                    }
                },
            )
        }
        if (files.isNotEmpty()) {
            params.put(
                "files",
                org.json.JSONArray().apply {
                    files.forEach {
                        put(
                            JSONObject()
                                .put("name", it.name)
                                .put("data", it.data),
                        )
                    }
                },
            )
        }
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
                // 发送受理才清暂存（被拒时保留——用户可改完重发，不丢已选附件）
                pendingImages.clear()
                pendingFiles.clear()
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

    // ===== 输入草稿（chat.draft：per-session 落库，多端共享）=====

    /** per-session 草稿（内存记账层，对齐桌面端 SessionDrafts 模式）：Composer 切
     *  会话按 sessionId 取初值/写回。落库经 chat.draft 防抖（[noteDraft]）——后端
     *  只存 text，附件等富状态仍在本端内存。 */
    internal val sessionDrafts = mutableMapOf<String, String>()

    /** 当前会话的草稿（Composer 初值/读写口；无会话 = 空串）。 */
    fun draftOf(sessionId: String): String = sessionDrafts[sessionId] ?: ""

    /** 草稿写入口：更新内存记账 + 防抖 800ms 落库（chat.draft）。防抖窗口内的
     *  连续打字只发一次请求；切走会话由 [flushDraft] 立即补发本会话的最终值。 */
    fun noteDraft(sessionId: String, text: String) {
        if (sessionId.isEmpty()) return
        sessionDrafts[sessionId] = text
        draftFlushPending = true
        draftHandler.removeCallbacks(draftFlushRunnable)
        draftHandler.postDelayed(draftFlushRunnable, DRAFT_FLUSH_DELAY_MS)
    }

    /** 立即把挂起的草稿落库（切会话/断连前调用——防抖窗口里的最后状态不能丢）。 */
    fun flushDraft() {
        if (!draftFlushPending) return
        draftFlushPending = false
        draftHandler.removeCallbacks(draftFlushRunnable)
        val sid = currentSessionId
        if (sid.isEmpty()) return
        sendDraft(sid, sessionDrafts[sid] ?: "")
    }

    internal val draftHandler = android.os.Handler(android.os.Looper.getMainLooper())
    internal var draftFlushPending = false
    internal val draftFlushRunnable = Runnable { flushDraft() }

    internal fun sendDraft(sid: String, text: String) {
        if (phase != RealPhase.Connected) return // 断线时草稿留在内存，重连装载历史即回填
        Log.d(TAG, "chat.draft session=$sid len=${text.length}")
        client.call(
            "chat.draft",
            JSONObject().put("session_id", sid).put("text", text),
        ) { err, _ ->
            if (err != null) Log.w(TAG, "chat.draft 失败: ${err.optString("message")}")
        }
    }

    /** 裁决确认门（tool.confirm；allow=false = 拒绝，后端合成「已取消，未执行」；
     *  answer 非空 = ask_user 提问的文本回答——走会话的 Answer 路径）。 */
    fun resolveConfirm(allow: Boolean, answer: String = "") {
        val c = confirm ?: return
        Log.i(TAG, "tool.confirm id=${c.id} allow=$allow answer=${answer.isNotEmpty()}")
        val params = JSONObject()
            .put("session_id", c.sessionId)
            .put("id", c.id)
            .put("allow", allow)
        if (answer.isNotEmpty()) params.put("answer", answer)
        client.call(
            "tool.confirm",
            params,
        ) { err, _ ->
            if (err != null) {
                Log.w(TAG, "tool.confirm 失败: ${err.optString("message")}")
                blocks.add(ThreadBlock.Error("确认裁决失败: ${err.optString("message")}"))
            }
            confirm = null
        }
    }

    /** 取消当前轮（chat.cancel）。结果经 [onDone] 回调——err = 没送到（典型：
     *  断线，`后端未连接`）：调用方必须给用户反馈（toast），不能静默——
     *  「点了停止像没点」就是这条错误被吞掉的直接后果。 */
    fun cancel(onDone: ((ok: Boolean, msg: String) -> Unit)? = null) {
        if (currentSessionId.isEmpty()) {
            onDone?.invoke(false, "还没有打开会话")
            return
        }
        Log.i(TAG, "chat.cancel session=$currentSessionId")
        client.call("chat.cancel", JSONObject().put("session_id", currentSessionId)) { err, _ ->
            if (err != null) {
                val msg = err.optString("message").ifBlank { "后端未连接" }
                Log.w(TAG, "chat.cancel 失败: $msg")
                onDone?.invoke(false, msg)
            } else {
                onDone?.invoke(true, "")
            }
        }
    }

    /** 取消**正在跑的子会话**（chat.cancel + 子会话 id）：后端 cancelChildAnywhere
     *  按 children 登记寻址真运行时（跑完即注销的子会话走普通会话路径兜底）。
     *  子会话页忙时顶栏的停止钮走它。 */
    fun cancelChild(childId: String) {
        if (childId.isEmpty()) return
        Log.i(TAG, "chat.cancel child=$childId")
        client.call("chat.cancel", JSONObject().put("session_id", childId)) { _, _ -> }
    }

    /**
     * 给**子会话**发一条续跑消息（chat.send + 子会话 id）：子会话页空闲时的
     * 输入框走它——对子 Agent 直发补指令（后端现建 runtime 续跑）。
     * 派发运行中后端会以 CodeBusy 拒绝（「该子会话正在派发中」），错误照主
     * 会话 send 的口径插错误块。
     */
    fun sendChild(childId: String, text: String) {
        val t = text.trim()
        if (childId.isEmpty() || t.isEmpty()) return
        Log.i(TAG, "chat.send child=$childId text=${t.take(80)}")
        childBusy = true
        client.call(
            "chat.send",
            JSONObject().put("session_id", childId).put("text", t),
        ) { err, _ ->
            if (err != null) {
                childBusy = false
                val msg = err.optString("message")
                Log.w(TAG, "chat.send(child) 被拒: code=${err.optInt("code")} $msg")
                childBlocks.add(ThreadBlock.Error("发送失败: $msg"))
            }
        }
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

    // ===== 事件归约（facade；实现见 Reducers.kt）=====

    internal val reducers = Reducers(this)

    internal fun onEvent(method: String, p: JSONObject) = reducers.onEvent(method, p)

    // ===== 历史装载（facade；实现见 HistoryLoader.kt）=====

    internal val historyLoader = HistoryLoader(this)

    internal fun loadHistory(id: String) = historyLoader.loadHistory(id)

    fun loadEarlier() = historyLoader.loadEarlier()

    // ===== 网络可用性监听（缺口5：掉线期间网络恢复 → 立即重连，不等退避）=====
    //
    // 断线常因网络本身不可用（Wi-Fi↔蜂窝切换/隧道抖动）：此时自动重连的指数退避
    // 会在「网络还没回来」上空转并一路涨到 30s 封顶——网络恢复时用户还得等退避
    // 走完。onAvailable 回调补上这个缺口：网络一恢复立即 reconnectIfDown。

    internal var netCallback: android.net.ConnectivityManager.NetworkCallback? = null

    /** 主线程 Handler（网络回调在系统 binder 线程到达，重连须切主线程）。 */
    internal val main = android.os.Handler(android.os.Looper.getMainLooper())

    /** 启动 pending 的兜底超时回调（重新 start 时先撤旧的，防多条并存）。 */
    internal var startPendingTimeout: Runnable? = null

    /** 注册网络可用回调（进程内一次；AppRoot 构造 AppState 后调）。
     *  ACCESS_NETWORK_STATE 权限已在 Manifest。注册失败（无系统服务）静默：
     *  缺它只是退避变慢，不是功能失效。 */
    fun startNetworkWatch() {
        if (netCallback != null) return
        val cm = com.moyunteng.lxcode.remote.update.AppContextHolder.context
            .getSystemService(android.content.Context.CONNECTIVITY_SERVICE)
            as? android.net.ConnectivityManager ?: return
        val cb = object : android.net.ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: android.net.Network) {
                Log.i(TAG, "网络恢复 onAvailable → 立即重连检查")
                main.post { reconnectIfDown() }
            }
        }
        try {
            cm.registerDefaultNetworkCallback(cb)
            netCallback = cb
        } catch (e: Exception) {
            Log.w(TAG, "registerDefaultNetworkCallback 失败: $e")
        }
    }

    internal fun parseStats(o: JSONObject): SessionStats = SessionStats(
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
        internal const val TAG = "LxReal"

        /** 草稿落库的防抖窗口（ms）：打字高频写，800ms 静默后发一次。 */
        internal const val DRAFT_FLUSH_DELAY_MS = 800L

        /**
         * assistant.* 事件 → chat.* 等价事件名（与 internal/server/emit.go 的
         * assistantEventName **互为逆映射**，对齐桌面端 frontend/src/agent/ws/events.ts
         * 的 ASSISTANT_TO_CHAT——表里没有的方法名不猜归属，忽略）。
         * assistant.filesChanged / assistant.configChanged 本批不消费（第三批）。
         */
        internal val ASSISTANT_TO_CHAT = mapOf(
            "assistant.userMessage" to "chat.userMessage",
            "assistant.delta" to "chat.delta",
            "assistant.toolCall" to "chat.toolCall",
            "assistant.toolResult" to "chat.toolResult",
            "assistant.confirmRequest" to "chat.confirmRequest",
            "assistant.done" to "chat.done",
            "assistant.error" to "chat.error",
            "assistant.busy" to "chat.busy",
            "assistant.dispatchStart" to "chat.dispatchStart",
            "assistant.dispatchEnd" to "chat.dispatchEnd",
            "assistant.compacted" to "chat.compacted",
            "assistant.todoUpdated" to "todo.updated",
            "assistant.approvalChanged" to "chat.approvalChanged",
            "assistant.modelChanged" to "chat.modelChanged",
        )

        /** 历史尾部窗口（chat.history 的 tail 参数）：进会话只拉最近 N 条（慢链路
         *  分页——公网隧道带宽有限，全量大会话历史秒级传输；桌面局域网无感）。
         *  50 条 ≈ 一次长对话的工具往返量，覆盖「进会话看最新」的绝大多数场景。 */
        internal const val TAIL_WINDOW = 50

        // ===== 附件上限（与后端 server 侧校验同一套常量，见 docs/attachments.md）=====

        const val MAX_IMAGE_BYTES = 5L * 1024 * 1024
        const val MAX_FILE_BYTES = 20L * 1024 * 1024
        const val MAX_IMAGE_COUNT = 4
        const val MAX_FILE_COUNT = 4
        val IMAGE_MIMES = setOf("image/png", "image/jpeg", "image/webp", "image/gif")

        /** 字节数 → 人话大小（附件卡 meta 行；与桌面端 formatBytes 同口径）。 */
        fun formatBytes(n: Long): String = when {
            n < 1024 -> "$n B"
            n < 1024 * 1024 -> "${n / 1024} KB"
            else -> String.format(java.util.Locale.ROOT, "%.1f MB", n / (1024.0 * 1024.0))
        }

        /**
         * 通告前缀表（对齐桌面端 shared/notices.ts 的 NOTICE_KINDS，逐字含尾空格）。
         *
         * 这类消息是**真实 user 角色消息**（模型必须当用户回合回应），但不是用户说的
         * 话——按文本前缀识别，渲染成提示条。命中取第一个前缀（顺序敏感：更长的排前）。
         */
        internal val NOTICE_KINDS = listOf(
            "[后台任务通告] " to "后台任务通告",
            "[重复调用提醒] " to "重复调用提醒",
        )

        /** hidden 注记前缀（写给模型的系统注记，界面上不产生任何块）。 */
        internal const val HIDDEN_NOTICE_PREFIX = "[系统通告] "

        /** 命中的通告种类（label, 剩余正文）；没命中 = null（普通用户消息）。 */
        internal fun noticeKind(text: String): Pair<String, String>? =
            NOTICE_KINDS.firstOrNull { text.startsWith(it.first) }

        /** 子会话 id 提示行（后端 internal/agent/tools.go 在 dispatch 结果末尾追加）：
         *  `\n\n[子会话 id: <id> —— 只在这次没做完时填进 session 参数续跑]`。
         *  给模型看的续跑线索，回放时剥掉；id 从这里取是**权威**值（续跑时
         *  arguments.session 可能为空）。对齐桌面端 history.ts 的 SUB_SESSION_HINT。
         *  解析已下沉到 model 层（DispatchMerge.kt——纯函数可单测），这里只委托。 */

        /** 拆分 dispatch 结果：结论正文 + 子会话 id（没有提示行时 id 为空串）。 */
        fun splitDispatchResult(raw: String): Pair<String, String> =
            com.moyunteng.lxcode.remote.model.splitDispatchResult(raw)

        /** 工具显示名（对齐桌面端工具卡标题；未知工具原样显示 id）。 */
        internal val TOOL_TITLES = mapOf(
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

        /** 参数摘要：取第一个标量值截断（对齐原型 argsSummary 的风格）。 */
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
