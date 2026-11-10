// 应用根 —— 显式 Screen 状态机（不引 navigation 库：原型期只有少量屏幕，
// 引入 navigation-compose 只会多一层间接）。首页 = 会话列表。
// 纯远控壳：底部导航只有 会话/连接；配对与组件展示从连接页进入。
package com.moyunteng.lxcode.remote.ui

import android.util.Log
import androidx.activity.compose.BackHandler
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.windowInsetsPadding
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ChatBubbleOutline
import androidx.compose.material.icons.filled.Dns
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawWithContent
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.graphics.RectangleShape
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import com.moyunteng.lxcode.remote.BuildConfig
import com.moyunteng.lxcode.remote.ConnStore
import com.moyunteng.lxcode.design.motion.LxEnterSpec
import com.moyunteng.lxcode.design.motion.LxMotionGate
import com.moyunteng.lxcode.design.motion.LocalLxMotionEnabled
import com.moyunteng.lxcode.design.motion.lxAnimateColor
import com.moyunteng.lxcode.design.motion.lxEnter
import com.moyunteng.lxcode.design.motion.lxPressScale
import com.moyunteng.lxcode.design.token.Lx
import com.moyunteng.lxcode.design.token.LxElevation
import com.moyunteng.lxcode.design.token.LxIcons
import com.moyunteng.lxcode.design.token.lxShadow
import com.moyunteng.lxcode.design.token.LxTemp
import com.moyunteng.lxcode.design.token.LxTheme
import com.moyunteng.lxcode.design.token.LxVariant
import com.moyunteng.lxcode.remote.model.BackendConn
import com.moyunteng.lxcode.remote.model.LOCAL_ADDR
import com.moyunteng.lxcode.remote.model.SessionMeta
import java.util.UUID

/** 屏幕（路由）。 */
sealed interface Route {
    /** 顶部 tab 页（会话/助手/连接/设置）与从连接页进入的配对页显示底部导航栏；详情页（线程/表单）不显示。 */
    val showBottomBar: Boolean get() = true

    data object Sessions : Route
    data object Connections : Route
    data object Settings : Route
    data object Pairing : Route
    data object Screen : Route { override val showBottomBar get() = false }
    data class Thread(val sessionId: String) : Route { override val showBottomBar get() = false }
    data class ConnForm(val fromScan: Boolean) : Route { override val showBottomBar get() = false }

    /** 助手模式（两页式）：会话列表 + 对话页——数据/操作全走 assistant.* 域，
     *  与主域的 Thread 互不混用；列表页作为 tab 直达（底部导航可见）。 */
    data object Assistant : Route
    data class AssistantThread(val sessionId: String) : Route { override val showBottomBar get() = false }
}

/**
 * 应用级 bottom sheet 的种类（AppState.appSheet 单槽按 kind 渲染不同内容；
 * 2026-12 用户拍板：ComposerSheet 枚举提升为 AppSheet——弹层不再挂 Composer 内部，
 * 遮罩盖整个窗口含顶栏/底栏，任何页面都能唤起）。
 */
enum class AppSheet {
    /** 模型选择（chat.model · 会话级；主域 Composer 药丸） */
    Model,

    /** 思考力度（chat.send effort 四档） */
    Effort,

    /** 工具审批档（chat.approval 三档） */
    Approval,

    /** 上下文占用明细（五类拆分） */
    Context,

    /** 助手域模型选择（assistant.model · 助手域语义） */
    AssistantModel,

    /** 助手域思考力度（assistant.send effort） */
    AssistantEffort,

    /** 助手域工具审批档（assistant.approval） */
    AssistantApproval,

    /** 助手域上下文占用明细 */
    AssistantContext,

    /** 设置页「默认模型」（role.set · 全局 default 角色——新会话的默认模型） */
    SettingsModel,
}

/** 连接表单草稿（新建/编辑共用；[isNew] 决定保存是新增还是覆盖）。 */
data class ConnDraft(
    val id: String,
    val addr: String,
    val name: String,
    val token: String,
    val isNew: Boolean,
)

/**
 * 全应用状态 —— 页面共享的可变状态（连接列表、当前连接、断线开关、筛选）。
 *
 * 连接列表经 [ConnStore] 持久化（SharedPreferences JSON 数组；addr/token/name 逐条落盘，
 * App 重启保留）：保存/编辑/删除/选当前即写回；没有任何存档时只播本机条目。
 * 连接信息是 app 唯一持久化的业务状态（用户拍板的远端化原则）。
 */
class AppState(private val store: ConnStore? = null) {
    var route by mutableStateOf<Route>(Route.Sessions)

    // ===== 主题（变体 × 色温；P1 落存储与恢复，切换 UI 在 Settings 页下一批）=====

    /** 当前变体（ConnStore 落盘，启动恢复；默认 A）。 */
    var themeVariant by mutableStateOf(
        store?.loadThemePick()?.variant?.let { v ->
            LxVariant.entries.firstOrNull { it.name == v }
        } ?: LxVariant.A,
    )
        private set

    /** 当前色温（ConnStore 落盘，启动恢复；默认 Dark——原型暗色是主要对照形态）。 */
    var themeTemp by mutableStateOf(
        store?.loadThemePick()?.temp?.let { t ->
            LxTemp.entries.firstOrNull { it.name == t }
        } ?: LxTemp.Dark,
    )
        private set

    /** 切主题（唯一写口：写内存 + 落盘；Settings 页下一批接 UI）。 */
    fun setTheme(variant: LxVariant, temp: LxTemp) {
        themeVariant = variant
        themeTemp = temp
        store?.saveThemePick(variant, temp)
    }

    // ===== 动效开关（P2 设置页）：app 级覆盖 LxMotionGate 的门控 =====
    // null = 跟随系统动画缩放（无障碍）；true/false = 显式覆盖（LxMotionGate.override）。
    // Settings 页开关写这里（落 ConnStore）；AppRoot 的 LxMotionGate 读它。

    /** 动效覆盖（null = 跟随系统）。启动按存档恢复。 */
    var motionOverride by mutableStateOf(store?.loadMotionOverride())
        private set

    /**
     * 切动效开关（唯一写口：写内存 + 落盘）。命名避属性 setter：`setMotionOverride`
     * 与 `var motionOverride` 的 JVM setter 签名撞车（writeRealOn 同款前例）。
     */
    fun writeMotionOverride(override: Boolean?) {
        motionOverride = override
        store?.saveMotionOverride(override)
    }

    /** 连接列表（含本机；可增删改；每次变更落盘）。 */
    val conns = mutableStateListOf<BackendConn>().apply {
        addAll(store?.loadConns() ?: seedConns())
    }

    /** 当前连接 id（真实模式「连接」到哪条；落盘，重启保留）。 */
    var activeConnId by mutableStateOf(store?.loadActiveId() ?: "local")

    /** 首次启动（无存档）的播种列表：只有本机一条——连接列表是用户自己的存档
     *  （ConnStore 持久化），不播种编造的远程条目。 */
    private fun seedConns(): List<BackendConn> =
        listOf(BackendConn("local", "本机", LOCAL_ADDR, "", local = true))

    /** 会话列表搜索串。 */
    var query by mutableStateOf("")

    /** 会话列表当前范围（项目 id / ""=未分组，对齐桌面端 Sidebar 的 filter）。 */
    var scope by mutableStateOf("")

    /** 待编辑的连接（null = 不在表单页）。 */
    var editing by mutableStateOf<ConnDraft?>(null)

    /**
     * 扫码页错误提示（非法载荷 / 相机不可用等）。
     *
     * 显示在扫码页（PairingScreen）；deep-link 带来的非法载荷也会路由到扫码页展示它。
     */
    var scanError by mutableStateOf<String?>(null)

    /**
     * 应用一条配对载荷（真扫码 / deep-link 两条入口共用）。
     *
     * 解析 `lxcode://pair?addr=…&token=…&name=…`：
     *  * 成功 → 预填连接表单（新建草稿）并跳表单页，返回 true；
     *  * 失败 → 记错误提示并回到扫码页（错误在扫码页展示），返回 false。不崩溃。
     */
    fun applyPairPayload(raw: String): Boolean {
        val payload = parsePairPayload(raw)
        if (payload == null) {
            scanError = "二维码内容不是有效的配对载荷" +
                "（应为 lxcode://pair?addr=…&token=…&name=…）"
            route = Route.Pairing
            return false
        }
        scanError = null
        editing = ConnDraft(
            id = UUID.randomUUID().toString(),
            addr = payload.addr,
            name = payload.name,
            token = payload.token,
            isNew = true,
        )
        route = Route.ConnForm(fromScan = true)
        return true
    }

    /**
     * 「真实后端」开关：开 = 各页面从 [real] 取真实数据（WS JSON-RPC 连后端）；
     * 关 = 会话页显示连接引导态。
     * 写入一律走 [writeRealOn]（开关即落盘）；启动恢复见 [restoreRealConnection]。
     */
    var realOn by mutableStateOf(false)

    /**
     * 真实后端地址/Token：预填上次输入（ConnStore 草稿），没有草稿回本机默认。
     * 「真实后端」开关的手填地址是引导态的输入口——不持久化的话每次杀进程
     * 重启都回 127.0.0.1:7789，用户每次重填（0.2.6 修复的 bug）。
     */
    var realAddr by mutableStateOf(store?.loadDraft()?.addr?.takeIf { it.isNotEmpty() } ?: "127.0.0.1:7789")

    /** 真实后端访问令牌（后端 remote.json Enabled 时所有 /rpc 连接必须携带——
     *  与地址同源的一对凭证；空 = 后端未开远程访问）。同样预填上次输入。 */
    var realToken by mutableStateOf(store?.loadDraft()?.token ?: "")

    /** 输入变化即落草稿（SharedPreferences.apply 异步；引导态没保存就重启也不丢输入）。
     *  name 字段归手动添加表单用，这里原样保留。 */
    fun saveRealDraft() {
        store?.saveDraft(realAddr, realToken, store?.loadDraft()?.name ?: "")
    }

    /** 手动添加表单：预填上次输入（没有草稿返回 null → 各字段回空/默认）。 */
    fun loadFormDraft(): ConnStore.Draft? = store?.loadDraft()

    /** 手动添加表单：输入变化即落草稿（与「真实后端」开关共用一份地址/Token）。 */
    fun saveFormDraft(addr: String, token: String, name: String) {
        store?.saveDraft(addr, token, name)
    }

    /** 真实后端状态机（net/ 包；开关打开时驱动连接与事件归约）。 */
    val real = com.moyunteng.lxcode.remote.net.RealBackend()

    init {
        // 网络层 → 页面层的单向回执通道（弱引用，防 AppState 泄漏）：
        // 壳上报 active=true 时置 screenWasActive（见 applyScreenState）。
        real.appStateRef = java.lang.ref.WeakReference(this)
    }

    /**
     * 启动自动检查发现的新版本（null = 无新版 / 还没查到 / 查询失败）。
     * 自动检查三态里只有「有新版」有 UI（会话页横幅，非阻断）；
     * 已最新与失败都静默——不弹 toast 打扰，手动检查入口在连接页。
     */
    var updateAvailable by mutableStateOf<com.moyunteng.lxcode.remote.update.ApkRelease?>(null)

    // ===== 投屏页（ScreenScreen）本地选择状态 =====

    /**
     * 投屏选中的显示器（未投屏时的选择；开始投屏时提交给 screen.start）。
     * 预填上次投屏的选择（ConnStore 持久化；没有记忆 = 0 主显示器）——
     * 0.3.30 拍板：连接页入口点击 = 直接按这里的值开始投屏，少一步。
     */
    var screenPickMonitor by mutableStateOf(store?.loadScreenPick()?.monitor ?: 0)

    /** 投屏清晰度档（low/medium/high）。预填上次选择，没有记忆 = medium。 */
    var screenPickQuality by mutableStateOf(
        store?.loadScreenPick()?.quality?.takeIf { it.isNotEmpty() } ?: "medium",
    )

    /** 投屏页的一次性提示（null/空 = 无；ScreenScreen 里几秒后自动清）。 */
    var screenToast by mutableStateOf<String?>(null)

    /**
     * 本进程内投屏曾经 active 过（active=true 的 screenState 到达即置 true，
     * [endScreenSession] 清）。用途有二：断开自动退回的判定源（从未 active 的
     * 启动失败留在投屏设置页，不打扰）+ 退出 toast 的「是否在投」判定。
     * 不持久化——进程重启后观看态由壳上报恢复，wasActive 在到达时重建即可。
     */
    var screenWasActive by mutableStateOf(false)

    /**
     * 投屏页的全屏观看位（页面级 UI 态，纯客户端、不进协议）：投屏进行中
     * （screenState.active==true）时 true = 全屏观看态（整页铺满 + 悬浮球，
     * AppRoot 豁免 safeDrawing），false = 缩小态（设置页形态 + 视频卡继续播，
     * 投屏继续跑）。未投屏时的设置页「双击进全屏」仍用 ScreenScreen 本地的
     * fullscreen rememberSaveable——两者语义不同（本位只在 active 时生效）。
     * 返回键语义（AppRoot BackHandler）读它：全屏 → 缩小；缩小 → 离开页面。
     */
    var screenFullscreen by mutableStateOf(false)

    /** 脱离页面上下文的一次性系统 toast（退出投屏发生在非组合上下文，screenToast 不适用）。 */
    fun showToast(msg: String) {
        android.widget.Toast.makeText(
            com.moyunteng.lxcode.remote.update.AppContextHolder.context,
            msg,
            android.widget.Toast.LENGTH_SHORT,
        ).show()
    }

    /**
     * 退出投屏的唯一收口（显式停止动作：悬浮球「停止投屏」）：
     * 投屏在跑（或启动 pending 挂着）则 stopScreen——WS 已断线等失败静默（会话已经
     * 结束，不再打扰）；回连接页；曾在投屏则 toast「已停止投屏」。
     * 缩小/放大（[screenFullscreen]）是纯 UI 态切换，不经过本函数。
     * 断开自动退回（AppRoot 的 LaunchedEffect）负责非用户主动的断开（screen.changed
     * active=false），与本函数互斥：本函数先清 [screenWasActive]，那条路径不再触发。
     */
    fun endScreenSession() {
        val wasActive = screenWasActive
        screenWasActive = false
        if (real.screenState?.active == true || real.screenStartPending) {
            real.stopScreen { err ->
                if (err != null && real.screenState?.active == true) screenToast = err
            }
        }
        route = Route.Connections
        if (wasActive) showToast("已停止投屏")
    }

    /** 用户是否成功投屏过（持久化；连接页入口据此直接开始而非落设置页）。 */
    fun hasCastBefore(): Boolean = store?.loadScreenPick()?.hasCast == true

    /** 投屏成功后记下选择 + hasCast（下次入口点击直接开始）。 */
    fun rememberScreenCast(monitor: Int, quality: String) {
        screenPickMonitor = monitor
        screenPickQuality = quality
        store?.saveScreenPick(monitor, quality)
    }

    /**
     * 应用内更新下载器（DownloadManager + sha256 校验 + 拉起系统安装器）。
     * downloadState 是横幅的渲染源（Running=进度 / Ready=可安装 / Failed=提示重试）。
     * [AppContextHolder] 由 AppRoot 在构造本状态前 init（上下文时序见 AppRoot）。
     */
    val updater = com.moyunteng.lxcode.remote.update.ApkDownloader(
        com.moyunteng.lxcode.remote.update.AppContextHolder.context,
    )

    /** 下载器状态投影（ApkDownloader.onState 回调 → Compose 可观察）。 */
    var downloadState by mutableStateOf<com.moyunteng.lxcode.remote.update.DownloadState?>(null)

    /**
     * 拉起安装器失败的人话提示（install() 返回 false 时置上，横幅副行展示）。
     * 国产 ROM（MIUI/ColorOS 等）无包安装器或「未知来源」被拦时 ActivityNotFoundException
     * ——不展示就是「点了安装没反应」的静默失败（对齐桌面更新脚本教训）。
     */
    var installHint by mutableStateOf<String?>(null)

    /**
     * 「安装未知应用」权限引导对话框开关（Android 8+ 未授权时点「安装」置上）。
     * 见 SessionsScreen.InstallPermissionGuideDialog——预检不发 ACTION_VIEW，
     * 弹我们自己的对话框引导去系统设置授权页。
     */
    var showInstallPermissionGuide by mutableStateOf(false)

    /**
     * 启动自动检查发现新版本的确认弹窗（待展示的 release；null = 无待弹）。
     * 弹窗批准才置 [updateAvailable] 出横幅（统一弹窗形态：与连接页「检查更新」
     * 同款 LxConfirmDialog，横幅不再承担「发现新版」的第一呈现）。
     * 冷启动找回的进行中下载（downloadState 非空）不弹——用户已表达过下载意图。
     */
    var pendingUpdatePrompt by mutableStateOf<com.moyunteng.lxcode.remote.update.ApkRelease?>(null)

    /**
     * 更新弹窗内闭环进行中（2026-12 用户拍板）：确认下载后弹窗不关，转进度/就绪/
     * 失败态（数据源同横幅的 downloadState）。关闭弹窗（后台/以后再说）置 false——
     * 下载继续，横幅仍是兜底入口。渲染条件见 SessionsScreen.UpdateFoundDialog。
     */
    var updateDialogActive by mutableStateOf(false)

    /** 顶栏连接药丸的在线位：跟 WS 实际连接态（未连接 = 灰点）。 */
    fun pillOnline(): Boolean = realOn && real.connected

    /**
     * 返回键拦截器（BackHandler 链的第一层）：弹层/菜单开着时由各页面注册——
     * 返回键先关弹层（返回 true 消费），不导航；返回 null = 本页无弹层，交给路由回退。
     * 逐层纪律见 AppRoot 的 BackHandler：弹层 → 页面路由 → 首页交给系统（退出）。
     */
    var backInterceptor by mutableStateOf<(() -> Boolean)?>(null)

    // ===== 应用级 bottom sheet 单槽（2026-12 用户拍板，从 Composer 内部提升）=====
    // 非空 = 开着一个 sheet（同一时刻只开一个——单槽位写法天然保证；置 null 关闭）。
    // 挂载点唯一在 AppRoot（fillMaxSize 覆盖整窗，遮罩含顶栏/底栏，对齐原型 .sheet-wrap）；
    // 返回键收口在 AppRoot BackHandler 链的第一层（先于各页面注册的 backInterceptor）。

    /** 当前打开的 sheet（null = 关闭）。写口走 [openAppSheet]/[closeAppSheet]。 */
    var appSheet by mutableStateOf<AppSheet?>(null)
        private set

    /** 「自动执行」两步确认态——Approval/AssistantApproval sheet 用；
     *  开非审批档 sheet 或关 sheet 时复位（原 Composer 局部 remember 提升于此）。
     *  写口走 [writeAppSheetPermConfirming]。 */
    var appSheetPermConfirming by mutableStateOf(false)

    /**
     * effort 本地视图（内存态，不落盘）：协议里 effort 只是 chat.send 的请求级参数
     * （后端没有会话级 effort 方法——远端无读写通道），发送时带上当前视图。
     * 主域与助手域各一份（原 Composer/AssistantComposer 的局部 remember 提升于此——
     * sheet body 挂在 AppRoot 作用域，写口要能跨出输入区组合）。
     * App 重启回落模型默认（与「远端化」原则一致）。
     */
    var composerEffort by mutableStateOf("medium")

    /** 助手域 effort 视图（语义见 [composerEffort]）。 */
    var assistantEffort by mutableStateOf("medium")

    /** 打开应用级 sheet（已开着其它 sheet 时直接替换——同一时刻只开一个不变式）；
     *  开非审批档 sheet 时复位两步确认态。 */
    fun openAppSheet(kind: AppSheet) {
        appSheet = kind
        if (kind != AppSheet.Approval && kind != AppSheet.AssistantApproval) {
            appSheetPermConfirming = false
        }
    }

    /** 关闭应用级 sheet（两步确认态一并复位）。 */
    fun closeAppSheet() {
        appSheet = null
        appSheetPermConfirming = false
    }

    /** 顶栏连接药丸的显示名：已连接标「真实后端」，未连接标当前选中条目名。 */
    fun pillName(): String = if (realOn) "真实后端" else activeName

    val activeConn: BackendConn
        get() = conns.firstOrNull { it.id == activeConnId } ?: conns.first()

    /** 顶栏连接药丸显示名（本机显示「本机」，远程显示其名字）。 */
    val activeName: String get() = activeConn.name

    fun addConn(draft: ConnDraft) {
        conns.add(BackendConn(draft.id, draft.name, draft.addr, draft.token))
        store?.saveConns(conns.toList())
    }

    fun updateConn(draft: ConnDraft) {
        val i = conns.indexOfFirst { it.id == draft.id }
        if (i >= 0) conns[i] = BackendConn(draft.id, draft.name, draft.addr, draft.token)
        store?.saveConns(conns.toList())
    }

    fun removeConn(id: String) {
        conns.removeAll { it.id == id }
        if (activeConnId == id) {
            // 删的是活跃条目：真实连接还跑在它读的凭证上，先断 WS + 退真实模式，
            // 再把「当前」指向剩余第一条（没有了就空串——不再盲目写 "local"，
            // 那会让 restoreRealConnection 以为条目还在而命中失配路径）。
            disconnectReal()
            setActiveConn(conns.firstOrNull()?.id ?: "")
        }
        store?.saveConns(conns.toList())
    }

    /** 设置当前连接 id（落盘，重启后连接列表与「当前」态原样恢复）。 */
    fun setActiveConn(id: String) {
        activeConnId = id
        store?.saveActiveId(id)
    }

    // ===== 连接条目 → 真实连接（连接管理 = 真实连接的收口）=====

    /**
     * 「连接」一条条目：把它的 addr/token 设为真实模式凭证并立即连接
     * （等价于调试区「真实后端」开关 + 手填地址，但入口在连接列表）。
     * 断线重连沿用现有 retry 机制——它读 realAddr/realToken，即本条目的凭证。
     */
    fun connectTo(conn: BackendConn) {
        setActiveConn(conn.id)
        realAddr = if (conn.local) LOCAL_ADDR else conn.addr
        realToken = conn.token
        writeRealOn(true)
        // 上次连接的凭证也记进草稿：下次引导态/开关预填的就是这套地址
        saveRealDraft()
        Log.i(TAG, "连接条目 ${conn.name} → $realAddr")
        real.connect(realAddr, realToken)
    }

    /** 断开真实连接（条目「断开」按钮 / 调试开关关闭共用）：断 WS 并退出真实模式。 */
    fun disconnectReal() {
        real.disconnect()
        writeRealOn(false)
    }

    /**
     * 「真实后端」开关的内存态写入收口（连接页手动开关用）：开关即落盘。
     * 命名避属性 setter：`setRealOn` 与 `var realOn` 的 JVM setter 签名撞车
     * （Platform declaration clash），落盘收口叫 writeRealOn。
     * store 是 private（持久化细节不出状态类），外部一律经此方法写 realOn——
     * connectTo/disconnectReal 内部写内存态时也走这里，三处落盘同一份逻辑。
     */
    fun writeRealOn(on: Boolean) {
        realOn = on
        store?.saveRealOn(on)
    }

    /**
     * 启动恢复连接（AppRoot 冷启动 LaunchedEffect 调一次）：上次退出处于真实模式
     * （realOn 落盘 true）且当前连接 id 命中现存条目 → 直接 connectTo 恢复连接。
     * 放这里而非构造函数：构造时发网络连接与 AppContextHolder.init 时序耦合（AppRoot）。
     * 条目失配（已被删/activeConnId 与现存列表对不上）→ 回落连接草稿凭证
     * （draft.addr 非空 → 按草稿连；这是上次退出时真实连接在用的那套），
     * 两者都没有 → 不动作，保持 Offline 引导态（不编造）。连接失败不回写
     * realOn=false（Failed 态持续退避 + 亮屏即拉），只有用户显式断开才清。
     */
    fun restoreRealConnection() {
        if (store?.loadRealOn() != true) return
        // 守卫：连接还活着（非 Offline/Failed）就不动它。曾经 Activity 重建会
        // 重跑这里的 LaunchedEffect(Unit)，connectTo 把活着的 WS 拆了重连——
        // 用户感知「投屏断开」。configChanges 后旋转不再重建 Activity，这条是
        // 纵深防御：任何未来触发重建的路径都拆不了活连接。
        if (real.phase != com.moyunteng.lxcode.remote.net.RealPhase.Offline &&
            real.phase != com.moyunteng.lxcode.remote.net.RealPhase.Failed
        ) return
        conns.firstOrNull { it.id == activeConnId }?.let {
            connectTo(it)
            return
        }
        // 兜底：activeConnId 失配但凭证还在草稿里 → 用草稿恢复（不再静默放弃）。
        val draft = store?.loadDraft()
        if (draft != null && draft.addr.isNotEmpty()) {
            Log.w(TAG, "restoreRealConnection: activeConnId 失配（'$activeConnId'），回落连接草稿凭证 ${draft.addr}")
            realAddr = draft.addr
            realToken = draft.token
            writeRealOn(true)
            real.connect(realAddr, realToken)
        } else {
            Log.w(TAG, "restoreRealConnection: realOn=true 但 activeConnId 失配且无草稿凭证，保持引导态")
        }
    }

    /** 这条条目是不是「当前」连接：真实模式 targeting 它才算（调试开关手填地址
     *  不属于任何条目 → setActiveConn("") 清掉标记，避免错标）。 */
    fun isCurrentConn(id: String): Boolean = realOn && activeConnId == id

    // ===== 会话列表的真实投影（连接后才拉得到数据；未连接 = 引导态，不渲染假列表）=====

    /** 真实会话列表按范围过滤（workspace 与桌面端同语义：""=未分组）。 */
    fun realSessionsInScope(): List<SessionMeta> =
        real.sessions.filter { it.workspace == scope }

    fun realProjectSessionCount(projectId: String): Int =
        real.sessions.count { it.workspace == projectId }

    fun realLooseSessionCount(): Int =
        real.sessions.count { it.workspace.isEmpty() }

    /** 项目/未分组下运行中的会话数（sessionBusy 投影聚合）：侧栏项目行的运行指示
     *  数据源——对齐桌面端 Sidebar 项目行的 spinner（项目内任一会话忙就亮）。
     *  计数复用 SessionMeta.running（RealBackend 已把 chat.busy 广播投影进去），
     *  不再单独开一份口径。 */
    fun realProjectRunningCount(projectId: String): Int =
        real.sessions.count { it.workspace == projectId && it.running }

    fun realLooseRunningCount(): Int =
        real.sessions.count { it.workspace.isEmpty() && it.running }
}

/** 底部导航项。 */
private data class NavItem(val route: Route, val label: String, val icon: ImageVector)

private const val TAG = "AppState"

/** AppState 的进程级持有（见 AppRoot 内注释：Activity 重建不再重建状态机）。 */
@Volatile
private var applicationScopedAppState: AppState? = null

// 底部导航三 tab（0.4.x 安卓 UI 重做 P1：会话/助手/连接）；
// 「设置」从连接页入口改为 Route（本批先留路由，Settings 页 UI 下一批）；
// 「配对」改为连接页入口，「展示」（组件库调试页）改为连接页调试区入口。
private val NAV_ITEMS = listOf(
    NavItem(Route.Sessions, "会话", LxIcons.Chat),
    NavItem(Route.Assistant, "助手", LxIcons.Bulb),
    NavItem(Route.Connections, "连接", LxIcons.Server),
)

/**
 * 应用根：底部导航 + 页面切换。
 *
 * @param pairUri 待处理的 deep-link（`lxcode://pair?...`；MainActivity 从 VIEW intent 带入，
 *                处理完经 [onPairUriConsumed] 置空防重组重复消费）
 * @param onPairUriConsumed pairUri 消费完的回调
 */
@Composable
fun AppRoot(pairUri: String? = null, onPairUriConsumed: () -> Unit = {}) {
    val context = LocalContext.current
    // 应用 Context 先注入更新器（AppState 构造时要建 ApkDownloader——
    // FileProvider URI 生成依赖它，见 ApkDownloader.AppContextHolder）
    com.moyunteng.lxcode.remote.update.AppContextHolder.init(context.applicationContext)
    // AppState 是进程级单例（rememberScreenState 的思路落到这个项目最核心的
    // 状态类上）：旋转（requestedOrientation / 传感器）触发 Activity 重建，
    // remember {} 会把 WS 连接、route、screenState 全部丢掉——用户实测点
    // 「横屏观看」后整 app 横屏 + 投屏误判「断开」退回主页，根因即此。
    // 提升到 Application 级：重建后同一份状态机原样接上，页面不闪、不断线。
    // 单 Activity + 单用户远控壳，无配置变体，进程级 = 生命周期正确域。
    val state = remember {
        applicationScopedAppState ?: AppState(ConnStore(context.applicationContext))
            .also { applicationScopedAppState = it }
    }

    // 下载器状态 → Compose 可观察（横幅进度/就绪/失败三态渲染）
    LaunchedEffect(state.updater) {
        state.updater.onState = { s -> state.downloadState = s }
    }

    // 冷启动找回：DownloadManager 的任务是系统级持久的——进程被杀/后台回收后
    // 下载仍在跑。启动时按持久化的 download id 查回状态，横幅回到进度/就绪/失败，
    // 而不是静默回「空闲」（对齐桌面「更新脚本静默失败无人拉起」的教训）。
    LaunchedEffect(Unit) {
        state.updater.resumePendingDownload()
    }

    // 连接持久化（恢复）：realOn 落盘 + 凭证/列表/activeConnId 早已落盘，重启后
    // 这里按存档自动恢复真实模式——重进 app 不用每次手动点连接。放 LaunchedEffect
    // 而非 AppState 构造函数：构造时发网络连接与 AppContextHolder.init 时序耦合。
    // 恢复条件与失败语义见 AppState.restoreRealConnection。
    LaunchedEffect(Unit) {
        state.restoreRealConnection()
        // 网络可用性监听（缺口5）：掉线期间网络恢复 → 立即重连，不等退避走完。
        state.real.startNetworkWatch()
    }

    // 应用内下载流程：Ready（下载完成）→ installVerified（**校验+安装单入口**：
    // sha256 失败即短路不安装，F-Droid 纪律「不可能绕过验证提交安装」）。
    // 拉起安装器失败（国产 ROM 无安装器/未知来源被拦）→ 人话提示走横幅，不静默。
    // **自动流只做提示，不做安装编排**：拉安装器这个动作只归 SessionsScreen 横幅的
    // 「安装」按钮（用户显式手势）——曾经这里也自动 installVerified，与手动按钮
    // 是两条都会拉安装器的路径（冷启动找回的 Ready 还会不打招呼直接弹安装器，
    // 用户视角「没点安装却弹了」）。Ready 到达时只把横幅置成「可以安装」等手势。
    LaunchedEffect(state.downloadState) {
        val ds = state.downloadState ?: return@LaunchedEffect
        if (ds is com.moyunteng.lxcode.remote.update.DownloadState.Idle) {
            // 安装完成广播（PACKAGE_REPLACED）或防御式比对收尾：横幅回空闲；
            // 弹窗内闭环态（updateDialogActive）一并收口关弹窗
            state.installHint = null
            state.downloadState = null
            state.updateAvailable = null
            state.updateDialogActive = false
            state.pendingUpdatePrompt = null
            return@LaunchedEffect
        }
        if (ds is com.moyunteng.lxcode.remote.update.DownloadState.Ready &&
            state.installHint != null
        ) {
            // 上一次「安装」拉起失败留了指引；用户此刻环境已修好（比如刚开了
            // 「未知来源」），横幅回到 Ready 的正常文案让「安装」按钮可再试
            //（拉起失败不消耗单飞位，见 ApkDownloader.launchInstaller）。
            state.installHint = null
        }
    }

    // 防御式横幅收尾（缺口4 兜底）：横幅是否显示由 update 元数据的 versionCode 比对
    // 驱动而非只靠 PACKAGE_REPLACED 广播——广播丢失（进程被杀/注册时序）时，已装新版
    // 的用户回到 app 也会因「远端不再比本机新」自动摘掉横幅。
    LaunchedEffect(state.updateAvailable) {
        val release = state.updateAvailable ?: return@LaunchedEffect
        if (!com.moyunteng.lxcode.remote.update.isRemoteNewer(
                release.versionCode,
                BuildConfig.VERSION_CODE.toLong(),
            )
        ) {
            state.updateAvailable = null
            state.installHint = null
            state.pendingUpdatePrompt = null
            state.updateDialogActive = false
            if (state.downloadState !is com.moyunteng.lxcode.remote.update.DownloadState.Running) {
                state.downloadState = null
            }
        }
    }

    // 返回键路由栈（BackHandler 链）：① 应用级 bottom sheet 开着 → 先关它（唯一挂载点
    // 在 AppRoot，返回键收口也在这——原 Composer 局部 sheetKind 的 backInterceptor 注册
    // 已随应用级提升删除）；② 弹层/菜单开着 → 先关弹层（各页面注册的
    // [AppState.backInterceptor]）；③ 详情页回上级（线程→会话列表、表单/扫码→连接）；
    // ③ 连接 tab → 会话 tab；④ 首页（会话列表）不拦截 → 交给系统（退出）。
    // 真实模式连接中返回只是退页面：不断开 WS（连接生命周期归「真实后端」开关管）。
    // 投屏页例外：观看态（正在投屏）返回键 = 分形态——全屏观看先缩小回设置页形态
    //（投屏继续，不停止）；已是缩小态则离开投屏页回连接页，投屏**继续跑**
    //（挂起语义：恢复 0.3.31 废除前的行为——用户从连接页/入口再进投屏页看到
    // 「投屏中」，可再放大观看）。停止投屏是显式动作（停止按钮/悬浮球）。
    // 设置态（未在投）仍只是回连接页。
    val backEnabled = state.appSheet != null ||
        state.backInterceptor != null ||
        (state.route !is Route.Sessions && state.route !is Route.Assistant)
    BackHandler(enabled = backEnabled) {
        if (state.appSheet != null) {
            state.closeAppSheet()
            return@BackHandler
        }
        if (state.backInterceptor?.invoke() == true) return@BackHandler
        when (val r = state.route) {
            is Route.Thread -> state.route = Route.Sessions
            is Route.AssistantThread -> state.route = Route.Assistant
            Route.Assistant -> state.route = Route.Sessions
            Route.Settings -> state.route = Route.Sessions
            is Route.ConnForm -> state.route = Route.Connections
            Route.Pairing -> state.route = Route.Connections
            Route.Screen ->
                if (state.real.screenState?.active == true && state.screenFullscreen) {
                    state.screenFullscreen = false // 全屏 → 缩小（投屏继续）
                } else {
                    state.route = Route.Connections // 缩小态/设置态 → 离开页面（投屏继续）
                }
            Route.Connections -> state.route = Route.Sessions
            Route.Settings -> state.route = Route.Sessions
            Route.Sessions -> Unit // 首页语义：交给系统（activity 退出）
        }
    }

    // 路由离开投屏页即复位全屏观看位（根因 B 收口点）：screenFullscreen 是页面级
    // UI 态，所有离开路径（返回键 BackHandler、endScreenSession、断开自动退回、
    // 任何其它 route 跳转）最终都表现为 route 变化——在这里统一复位，不再依赖
    // ScreenScreen 子树内的 effect：路由同步切换时子树直接卸载，子树里的
    // LaunchedEffect(watching) 被跳过、复位从未执行；配合壳侧 ffmpeg 崩溃自愈
    //（screen-restart.ts 1s 内 active=true 回来）→ 下次点入口直接落全屏。
    // endScreenSession 与断开退回处**不单独复位**：本 effect 已覆盖——route 变化
    // 的同一次重组里 watching（route is Route.Screen && ...）已为 false，safeDrawing
    // 与系统条外观同步还原，不会闪一帧全屏布局。
    LaunchedEffect(state.route) {
        if (state.route !is Route.Screen && state.screenFullscreen) {
            state.screenFullscreen = false
        }
    }

    // 投屏断开自动退回：曾在观看态（screenWasActive）后 active 变 false——
    // 停止/解码错误/断线（screen.changed active=false 上报）→ 自动回连接页 +
    // toast「投屏已断开」。启动失败（从未 active）留在投屏设置页看错误，不退。
    // endScreenSession 先清 wasActive 再 stop，用户主动退不触发本路径。
    LaunchedEffect(state.real.screenState?.active, state.screenWasActive, state.route) {
        if (state.screenWasActive &&
            state.real.screenState?.active == false &&
            state.route is Route.Screen
        ) {
            state.screenWasActive = false
            state.route = Route.Connections
            state.showToast("投屏已断开")
        }
    }

    // deep-link：`lxcode://pair?...`（系统相机扫码 / adb VIEW intent 唤起）
    // → 解析 → 预填连接表单；非法载荷 → 扫码页展示错误提示。
    LaunchedEffect(pairUri) {
        if (pairUri != null) {
            state.applyPairPayload(pairUri)
            onPairUriConsumed()
        }
    }

    // 亮屏/回前台即时重连（3.4）：掉线立即 connect；已连接做 ping 体检（3s 超时
    // 的死连接主动断开走自动重连）——自动重连的指数退避最长 30s，不能让用户回
    // 手机看一眼等退避走完。
    // ON_START 在 activity onStart 触发；phase==Offline（从未连过/用户断开）no-op。
    val lifecycleOwner = androidx.compose.ui.platform.LocalLifecycleOwner.current
    androidx.compose.runtime.DisposableEffect(lifecycleOwner) {
        val obs = androidx.lifecycle.LifecycleEventObserver { _, event ->
            if (event == androidx.lifecycle.Lifecycle.Event.ON_START) {
                state.real.onForeground()
            }
        }
        lifecycleOwner.lifecycle.addObserver(obs)
        onDispose { lifecycleOwner.lifecycle.removeObserver(obs) }
    }

    // 启动自动检查更新：查询失败静默、已是最新静默、有新版 → 与连接页「检查更新」
    // 同一个确认弹窗（2026-12 用户拍板统一形态，不再以横幅作「发现新版」的第一
    // 呈现）。冷启动找回的进行中下载（resumePendingDownload 恢复了 downloadState）
    // 不弹——用户此前已表达过下载意图，横幅接管即可。查询是阻塞 IO
    // （UpdateChecker），放 Default 派发，不卡首帧。
    LaunchedEffect(Unit) {
        val result = kotlinx.coroutines.withContext(kotlinx.coroutines.Dispatchers.Default) {
            com.moyunteng.lxcode.remote.update.UpdateChecker.check(
                BuildConfig.VERSION_CODE.toLong(),
            )
        }
        if (result is com.moyunteng.lxcode.remote.update.UpdateCheckResult.Available) {
            // 双重保险：downloadState 投影与下载器 active 位任一非空都视为
            // 「冷启动找回在进行中/已完成任务」——不弹确认弹窗（用户已表达过下载意图）
            if (state.downloadState == null && state.updater.idle()) {
                state.pendingUpdatePrompt = result.release
            }
        }
    }

    // 投屏页的「全屏观看」判定（safeDrawing 豁免与深色系统条的唯一依据）：
    // 在投屏页 + 投屏进行中 + 全屏观看位。缩小观看态（投屏继续、设置页形态）
    // 与未投屏设置页一样走正常 safeDrawing——视频卡只是页面内一块 16:9 卡片，
    // 不需要画面铺到系统条之下。
    val watching = state.route is Route.Screen &&
        state.real.screenState?.active == true &&
        state.screenFullscreen

    // 全屏观看的系统条外观（豁免 safeDrawing 的配套）：窗口背景是投屏画面（深色），
    // 状态栏/导航栏图标须转白色（isAppearanceLight=false）；离开全屏观看（缩小/停止/
    // 断开退回）还原浅底深字（窗口背景回 Lx.colors.Bg 白）。改动只经
    // WindowInsetsController，不动 themes.xml 与 Activity 级配置；
    // watching 驱动的 LaunchedEffect 天然覆盖所有退出路径（watching 翻转即还原）。
    val window = (context as? android.app.Activity)?.window
    LaunchedEffect(watching) {
        window ?: return@LaunchedEffect
        androidx.core.view.WindowCompat.getInsetsController(window, window.decorView)
            .isAppearanceLightStatusBars = !watching
        androidx.core.view.WindowCompat.getInsetsController(window, window.decorView)
            .isAppearanceLightNavigationBars = !watching
    }

    LxTheme(variant = state.themeVariant, temp = state.themeTemp) {
        // 动效门控：app 级开关（设置页）覆盖 > 跟随系统动画缩放（ANIMATOR_DURATION_SCALE
        // = 0 → 静态）。override = null 时 LxMotionGate 内部读系统。
        LxMotionGate(override = state.motionOverride) {
            Box(
                Modifier
                    .fillMaxSize()
                    .background(if (watching) Lx.colors.TermBg else Lx.colors.Bg)
                    // 全屏观看态豁免 safeDrawing：投屏画面绘制到状态栏/导航栏之下
                    //（「整个页面全是投屏画面，四周零边框」，2026-12 拍板）。
                    // 全屏观看 = 显式空 insets、零 padding——顶栏与悬浮球的系统条
                    // 间距由 WatchSurface/ScreenFloatingBall 在页面内部自补偿
                    //（statusBarsPadding / rootWindowInsets 钳制）。缩小观看态与
                    // 未投屏设置页走正常 safeDrawing（页面内自补了系统条预留）。
                    // WindowInsets(0,0,0,0) = 显式空 insets（不用条件跳过修饰符：
                    // 两个分支走同一条修饰符链，切换态不会丢布局参数）。
                    .windowInsetsPadding(
                        if (watching) WindowInsets(0, 0, 0, 0) else WindowInsets.safeDrawing,
                    )
            ) {
                Column(Modifier.fillMaxSize()) {
                    Box(Modifier.weight(1f)) {
                        val route = state.route
                        // 页面切换：内容淡入 + 轻微上浮，200ms 标准曲线（换路由即重播）
                        Box(
                            Modifier
                                .fillMaxSize()
                                .lxEnter(spec = LxEnterSpec.PageIn, key = route),
                        ) {
                            when (route) {
                                Route.Sessions -> SessionsScreen(state)
                                Route.Connections -> ConnectionsScreen(state)
                                // 设置页（P2 新建）：外观（变体/色温分段 + 动效开关）、
                                // 通知占位、模型/连接入口、版本与检查更新
                                Route.Settings -> SettingsScreen(state)
                                Route.Pairing -> PairingScreen(state)
                                Route.Screen -> ScreenScreen(state)
                                is Route.Thread -> ThreadScreen(state, route.sessionId)
                                is Route.ConnForm -> ConnectionFormScreen(state, route.fromScan)
                                // 助手模式（assistant.* 域）：列表 + 对话两页
                                Route.Assistant -> AssistantScreen(state)
                                is Route.AssistantThread -> AssistantThreadScreen(state, route.sessionId)
                            }
                        }
                    }
                    if (state.route.showBottomBar) {
                        BottomNav(state)
                    }
                }

                // ===== 应用级 bottom sheet（唯一挂载点，2026-12 从 Composer 内部提升）=====
                // 最外层 Box 内挂载：fillMaxSize 覆盖整个窗口（遮罩含顶栏/底栏，对齐原型
                // .sheet-wrap——遮罩只盖消息区是用户报的形态偏差）。内容按 AppSheet 分发，
                // 数据源在闭包里直读 state.real（真实后端对象替换时重组自动跟随）。
                AppSheetHost(state)
            }
        }
    }
}

/**
 * 底部导航栏（三 tab：会话/助手/连接）。
 * 2026-12 形态修正（对齐原型 `.bottomnav`）：整条 = Bg 86% 半透明底
 * （原型还有 backdrop-filter blur——安卓不复刻，见 rememberFrostedNavBg 注释）
 * + shadow-float 上投；
 * 选中项 = 40×30 accent-soft 药丸底 + float 上投阴影；运行中 badge = 7dp 琥珀
 * （`--warn`）呼吸脉冲（`pulse 1.8s ease-in-out infinite`），不再用红点
 * （Danger 只给失败语义）。
 */
@Composable
private fun BottomNav(state: AppState) {
    val frosted = rememberFrostedNavBg()
    Row(
        Modifier
            .fillMaxWidth()
            .then(frosted)
            .shadow(
                elevation = LxElevation.Menu.elevation,
                shape = RectangleShape,
                clip = false,
                ambientColor = Lx.shadow.Float,
                spotColor = Lx.shadow.Float,
            )
            .padding(vertical = Lx.space.s4),
        horizontalArrangement = Arrangement.SpaceEvenly,
        verticalAlignment = Alignment.CenterVertically,
    ) {
        NAV_ITEMS.forEach { item ->
            val selected = state.route::class == item.route::class
            val interaction = remember { MutableInteractionSource() }
            // 选中态颜色过渡 120ms（门控关闭时瞬变）—— 桌面端 `.nav-item { transition: color .12s }`
            // 图标色走 accent 档；label 色对齐原型 `.navitem.on { color: var(--fg) }`
            // ——accent 只给图标底，label 用主文字色（原型 2026-12 扫尾条目 27）。
            val tint by lxAnimateColor(target = if (selected) Lx.colors.Accent else Lx.colors.FgFaint)
            val labelTint by lxAnimateColor(target = if (selected) Lx.colors.Fg else Lx.colors.FgFaint)
            // 会话 tab 的运行中 badge：任一会话在跑就亮（快照 ∪ busy 投影同一口径）
            val anyRunning = item.route::class == Route.Sessions::class &&
                state.real.sessions.any { it.running || state.real.isSessionRunning(it.id) }
            Column(
                Modifier
                    .weight(1f)
                    .lxPressScale(interaction)
                    .clickable(
                        interactionSource = interaction,
                        indication = null,
                    ) { state.route = item.route },
                horizontalAlignment = Alignment.CenterHorizontally,
                verticalArrangement = Arrangement.spacedBy(Lx.space.s2),
            ) {
                // 选中项 = 40×30 accent-soft 药丸底 + float 上投阴影；未选中 = 裸图标
                Row(
                    Modifier
                        .lxShadow(LxElevation.Menu, Lx.radius.PillShape, Lx.shadow.Float.copy(alpha = 0.12f))
                        .clip(Lx.radius.PillShape)
                        .background(if (selected) Lx.colors.AccentSoft else androidx.compose.ui.graphics.Color.Transparent)
                        // 触摸目标：图标区 40×30 药丸 + 内边距，点击热区不小于 44dp 高
                        .padding(horizontal = 10.dp, vertical = 5.dp),
                    horizontalArrangement = Arrangement.spacedBy(0.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Box(contentAlignment = Alignment.Center) {
                        Icon(
                            imageVector = item.icon,
                            contentDescription = item.label,
                            tint = tint,
                            modifier = Modifier.size(20.dp),
                        )
                        if (anyRunning) {
                            // 运行中 badge（原型 `.navitem .badge`：7dp 圆点、`--warn`
                            // 琥珀 + `pulse 1.8s ease-in-out infinite` 呼吸环——运行是
                            // 「进行中」语义，红点是错误色）
                            val pulse = if (LocalLxMotionEnabled.current) {
                                val transition = rememberInfiniteTransition(label = "nav-badge")
                                val ring by transition.animateFloat(
                                    initialValue = 0f,
                                    targetValue = 4.dp.value,
                                    animationSpec = infiniteRepeatable(
                                        animation = tween(900, easing = Lx.motion.Easing),
                                        repeatMode = RepeatMode.Restart,
                                    ),
                                    label = "nav-badge-ring",
                                )
                                ring
                            } else {
                                0f
                            }
                            Box(
                                Modifier
                                    .align(Alignment.TopEnd)
                                    .offset(x = 6.dp, y = (-2).dp)
                                    .size(7.dp)
                                    .lxPulseRing(color = Lx.colors.Amber, alphaScale = pulse)
                                    .background(Lx.colors.Amber, CircleShape),
                            )
                        }
                    }
                }
                Text(
                    text = item.label,
                    // 原型 `.navitem`：font-size 10.5px / 600；选中色 var(--fg)
                    style = Lx.type.Pill.copy(fontSize = Lx.type.Size10_5),
                    color = labelTint,
                )
            }
        }
    }
}

/**
 * 导航底（原型 `.bottomnav`：`background: color-mix(in srgb, var(--bg) 86%,
 * transparent); backdrop-filter: blur(14px)`）。
 *
 * 安卓实现：只铺 `Bg.copy(alpha = 0.86)` 半透明底，**不复刻 blur**——
 * 本应用 Column 布局下导航底下没有内容延伸，只有纯色背景，blur 纯色零收益，
 * 挂在本层反而把图标/文字一起模糊（2026-12 定案移除）；真毛玻璃需要内容
 * 延伸到导航之下 + backdrop 方案，属独立改造，需要时再立项。
 */
@Composable
private fun rememberFrostedNavBg(): Modifier =
    Modifier.background(Lx.colors.Bg.copy(alpha = 0.86f))

/** badge 呼吸环：`pulse` keyframes（0% → 50% 时 box-shadow 扩到 4px）。
 *  颜色组合期读好传入（DrawScope 里读不了 CompositionLocal）。 */
private fun Modifier.lxPulseRing(color: androidx.compose.ui.graphics.Color, alphaScale: Float): Modifier =
    drawWithContent {
        drawContent()
        if (alphaScale > 0.01f) {
            drawCircle(
                color = color.copy(alpha = ((1f - alphaScale / 4f).coerceIn(0f, 1f)) * 0.35f),
                radius = size.minDimension / 2f + alphaScale.dp.toPx(),
            )
        }
    }
