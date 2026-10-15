// 应用根 —— 显式 Screen 状态机（不引 navigation 库：原型期只有少量屏幕，
// 引入 navigation-compose 只会多一层间接）。首页 = 会话列表。
// 纯远控壳：底部导航只有 会话/连接；配对与组件展示从连接页进入。
package com.moyunteng.lxcode.remote.ui

import android.util.Log
import androidx.activity.compose.BackHandler
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
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.windowInsetsPadding
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
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import com.moyunteng.lxcode.remote.BuildConfig
import com.moyunteng.lxcode.remote.ConnStore
import com.moyunteng.lxcode.design.motion.LxEnterSpec
import com.moyunteng.lxcode.design.motion.LxMotionGate
import com.moyunteng.lxcode.design.motion.lxAnimateColor
import com.moyunteng.lxcode.design.motion.lxEnter
import com.moyunteng.lxcode.design.motion.lxPressScale
import com.moyunteng.lxcode.design.token.Lx
import com.moyunteng.lxcode.design.token.LxColors
import com.moyunteng.lxcode.design.token.LxTheme
import com.moyunteng.lxcode.remote.ShowcaseScreen
import com.moyunteng.lxcode.remote.model.BackendConn
import com.moyunteng.lxcode.remote.model.LOCAL_ADDR
import com.moyunteng.lxcode.remote.model.SessionMeta
import java.util.UUID

/** 屏幕（路由）。 */
sealed interface Route {
    /** 顶部 tab 页（会话/连接）与从连接页进入的配对/展示页显示底部导航栏；详情页（线程/表单）不显示。 */
    val showBottomBar: Boolean get() = true

    data object Sessions : Route
    data object Connections : Route
    data object Pairing : Route
    data object Showcase : Route
    data class Thread(val sessionId: String) : Route { override val showBottomBar get() = false }
    data class ConnForm(val fromScan: Boolean) : Route { override val showBottomBar get() = false }
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
 * App 重启保留）：保存/编辑/删除/选当前即写回；没有任何存档时按构建类型播种——
 * DEBUG 播 mock 列表（原型数据迁移，调试区控件同源），release 只播本机条目。
 * 连接信息是 app 唯一持久化的业务状态（用户拍板的远端化原则）；mock 数据已随
 * 原型退役（DEBUG 下播种的就是原 mock 连接条目）。
 */
class MockAppState(private val store: ConnStore? = null) {
    var route by mutableStateOf<Route>(Route.Sessions)

    /** 连接列表（含本机；可增删改；每次变更落盘）。 */
    val conns = mutableStateListOf<BackendConn>().apply {
        addAll(store?.loadConns() ?: seedConns())
    }

    /** 当前连接 id（真实模式「连接」到哪条；落盘，重启保留）。 */
    var activeConnId by mutableStateOf(store?.loadActiveId() ?: "local")

    /** 首次启动（无存档）的播种列表：只有本机一条——mock 假连接已随原型退役，
     *  连接列表是用户自己的存档（ConnStore 持久化），不播种编造的远程条目。 */
    private fun seedConns(): List<BackendConn> =
        listOf(BackendConn("local", "本机", LOCAL_ADDR, "", local = true))

    /** mock 断线开关：true = 在线（顶栏绿点），false = 离线（灰点 + 连接页错误条）。 */
    var online by mutableStateOf(true)

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
     * 应用一条配对载荷（真扫码 / mock 扫码 / deep-link 三条入口共用）。
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
     * 「动效」调试开关（原型用）。
     *
     * `true` = 跟随系统动画缩放；`false` = 强制静态（模拟系统「减少动态效果」），
     * 用来截取 reduced-motion 下的静态态做对照。真正的系统开关由
     * [com.moyunteng.lxcode.design.motion.LxMotionGate] 读
     * `Settings.Global.ANIMATOR_DURATION_SCALE`，两者取严。
     */
    var motionOn by mutableStateOf(true)

    /**
     * 「真实后端」开关：开 = 各页面从 [real] 取真实数据（WS JSON-RPC 连后端）；
     * 关 = 会话页显示连接引导态（mock 假数据已随原型退役，无第三条路径）。
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

    /**
     * 启动自动检查发现的新版本（null = 无新版 / 还没查到 / 查询失败）。
     * 自动检查三态里只有「有新版」有 UI（会话页横幅，非阻断）；
     * 已最新与失败都静默——不弹 toast 打扰，手动检查入口在连接页。
     */
    var updateAvailable by mutableStateOf<com.moyunteng.lxcode.remote.update.ApkRelease?>(null)

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
    /** 顶栏连接药丸的在线位：跟 WS 实际连接态（未连接 = 灰点——不再有 mock 的
     *  「模拟在线」在线位）。 */
    fun pillOnline(): Boolean = realOn && real.connected

    /**
     * 返回键拦截器（BackHandler 链的第一层）：弹层/菜单开着时由各页面注册——
     * 返回键先关弹层（返回 true 消费），不导航；返回 null = 本页无弹层，交给路由回退。
     * 逐层纪律见 AppRoot 的 BackHandler：弹层 → 页面路由 → 首页交给系统（退出）。
     */
    var backInterceptor by mutableStateOf<(() -> Boolean)?>(null)

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
        if (activeConnId == id) setActiveConn("local")
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
        realOn = true
        // 上次连接的凭证也记进草稿：下次引导态/开关预填的就是这套地址
        saveRealDraft()
        Log.i(TAG, "连接条目 ${conn.name} → $realAddr")
        real.connect(realAddr, realToken)
    }

    /** 断开真实连接（条目「断开」按钮 / 调试开关关闭共用）：断 WS 并退出真实模式。 */
    fun disconnectReal() {
        real.disconnect()
        realOn = false
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

private const val TAG = "MockAppState"

// 纯远控壳：底部导航只留 会话 + 连接 两个 tab；「配对」改为连接页入口，
// 「展示」（组件库调试页）改为连接页调试区入口（Route 保留，导航不再直达）。
private val NAV_ITEMS = listOf(
    NavItem(Route.Sessions, "会话", Icons.Filled.ChatBubbleOutline),
    NavItem(Route.Connections, "连接", Icons.Filled.Dns),
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
    // 应用 Context 先注入更新器（MockAppState 构造时要建 ApkDownloader——
    // FileProvider URI 生成依赖它，见 ApkDownloader.AppContextHolder）
    com.moyunteng.lxcode.remote.update.AppContextHolder.init(context.applicationContext)
    val state = remember { MockAppState(ConnStore(context.applicationContext)) }

    // 下载器状态 → Compose 可观察（横幅进度/就绪/失败三态渲染）
    LaunchedEffect(state.updater) {
        state.updater.onState = { s -> state.downloadState = s }
    }

    // 应用内下载流程：Ready（下载完成）→ IO 线程 sha256 校验 → 通过自动拉起安装器
    LaunchedEffect(state.downloadState) {
        val ds = state.downloadState ?: return@LaunchedEffect
        val release = state.updateAvailable ?: return@LaunchedEffect
        if (ds is com.moyunteng.lxcode.remote.update.DownloadState.Ready) {
            val verified = kotlinx.coroutines.withContext(kotlinx.coroutines.Dispatchers.IO) {
                state.updater.verify(ds, release)
            }
            state.downloadState = verified
            if (verified is com.moyunteng.lxcode.remote.update.DownloadState.Ready) {
                state.updater.install(verified)
            }
        }
    }

    // 返回键路由栈（BackHandler 链）：① 弹层/菜单开着 → 先关弹层（各页面注册的
    // [MockAppState.backInterceptor]）；② 详情页回上级（线程→会话列表、表单/扫码/展示→连接）；
    // ③ 连接 tab → 会话 tab；④ 首页（会话列表）不拦截 → 交给系统（退出）。
    // 真实模式连接中返回只是退页面：不断开 WS（连接生命周期归「真实后端」开关管）。
    val backEnabled = state.route !is Route.Sessions || state.backInterceptor != null
    BackHandler(enabled = backEnabled) {
        if (state.backInterceptor?.invoke() == true) return@BackHandler
        when (val r = state.route) {
            is Route.Thread -> state.route = Route.Sessions
            is Route.ConnForm -> state.route = Route.Connections
            Route.Pairing -> state.route = Route.Connections
            Route.Showcase -> state.route = Route.Connections
            Route.Connections -> state.route = Route.Sessions
            Route.Sessions -> Unit // 首页语义：交给系统（activity 退出）
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

    // 亮屏/回前台即时重连（3.4）：掉线且非用户主动断开时立即 connectOnce——
    // 自动重连的指数退避最长 30s，不能让用户回手机看一眼等退避走完。
    // ON_START 在 activity onStart 触发；phase==Offline（从未连过/用户断开）no-op。
    val lifecycleOwner = androidx.compose.ui.platform.LocalLifecycleOwner.current
    androidx.compose.runtime.DisposableEffect(lifecycleOwner) {
        val obs = androidx.lifecycle.LifecycleEventObserver { _, event ->
            if (event == androidx.lifecycle.Lifecycle.Event.ON_START) {
                state.real.reconnectIfDown()
            }
        }
        lifecycleOwner.lifecycle.addObserver(obs)
        onDispose { lifecycleOwner.lifecycle.removeObserver(obs) }
    }

    // 启动自动检查更新：查询失败静默、已是最新静默、有新版 → 会话页横幅（非阻断，
    // 用户可关）。查询是阻塞 IO（UpdateChecker），放 Default 派发，不卡首帧。
    LaunchedEffect(Unit) {
        val result = kotlinx.coroutines.withContext(kotlinx.coroutines.Dispatchers.Default) {
            com.moyunteng.lxcode.remote.update.UpdateChecker.check(
                BuildConfig.VERSION_CODE.toLong(),
            )
        }
        if (result is com.moyunteng.lxcode.remote.update.UpdateCheckResult.Available) {
            state.updateAvailable = result.release
        }
    }

    LxTheme {
        // 动效门控：跟随系统动画缩放（ANIMATOR_DURATION_SCALE = 0 → 静态），
        // 原型里的「动效」开关（[MockAppState.motionOn]）关掉时强制静态。
        LxMotionGate(override = if (state.motionOn) null else false) {
            Box(
                Modifier
                    .fillMaxSize()
                    .background(LxColors.Bg)
                    .windowInsetsPadding(WindowInsets.safeDrawing),
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
                                Route.Pairing -> PairingScreen(state)
                                Route.Showcase -> ShowcaseScreen(Modifier.fillMaxSize())
                                is Route.Thread -> ThreadScreen(state, route.sessionId)
                                is Route.ConnForm -> ConnectionFormScreen(state, route.fromScan)
                            }
                        }
                    }
                    if (state.route.showBottomBar) {
                        BottomNav(state)
                    }
                }
            }
        }
    }
}

/** 底部导航栏（两个 tab：会话/连接；发丝上边框 + 当前项近黑字）。 */
@Composable
private fun BottomNav(state: MockAppState) {
    Row(
        Modifier
            .fillMaxWidth()
            .background(LxColors.Surface)
            .border(width = 1.dp, color = Lx.colors.Border)
            .padding(vertical = Lx.space.s6),
        horizontalArrangement = Arrangement.SpaceEvenly,
        verticalAlignment = Alignment.CenterVertically,
    ) {
        NAV_ITEMS.forEach { item ->
            val selected = state.route::class == item.route::class
            val interaction = remember { MutableInteractionSource() }
            // 选中态颜色过渡 120ms（门控关闭时瞬变）—— 桌面端 `.nav-item { transition: color .12s }`
            val tint by lxAnimateColor(target = if (selected) Lx.colors.Fg else Lx.colors.FgFaint)
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
                Icon(
                    imageVector = item.icon,
                    contentDescription = item.label,
                    tint = tint,
                    modifier = Modifier.size(20.dp),
                )
                Text(
                    text = item.label,
                    style = Lx.type.Pill.copy(fontSize = Lx.type.Size10),
                    color = tint,
                )
            }
        }
    }
}
