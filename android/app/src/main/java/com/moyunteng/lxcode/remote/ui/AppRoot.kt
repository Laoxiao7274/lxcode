// 应用根 —— 显式 Screen 状态机（不引 navigation 库：原型期只有少量屏幕，
// 引入 navigation-compose 只会多一层间接）。首页 = 会话列表。
// 纯远控壳：底部导航只有 会话/连接；配对与组件展示从连接页进入。
package com.moyunteng.lxcode.remote.ui

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
import androidx.compose.ui.unit.dp
import com.moyunteng.lxcode.design.motion.LxEnterSpec
import com.moyunteng.lxcode.design.motion.LxMotionGate
import com.moyunteng.lxcode.design.motion.lxAnimateColor
import com.moyunteng.lxcode.design.motion.lxEnter
import com.moyunteng.lxcode.design.motion.lxPressScale
import com.moyunteng.lxcode.design.token.Lx
import com.moyunteng.lxcode.design.token.LxColors
import com.moyunteng.lxcode.design.token.LxTheme
import com.moyunteng.lxcode.remote.ShowcaseScreen
import com.moyunteng.lxcode.remote.mock.BackendConn
import com.moyunteng.lxcode.remote.mock.MockData
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
 * 全应用 mock 状态 —— 页面共享的可变状态（连接列表、当前连接、断线开关、筛选）。
 *
 * 全部内存态、无网络：原型只为「把页面画出来」服务。
 */
class MockAppState {
    var route by mutableStateOf<Route>(Route.Sessions)

    /** 连接列表（含本机；可增删改）。 */
    val conns = mutableStateListOf<BackendConn>().apply { addAll(MockData.connections) }

    /** 当前连接 id。 */
    var activeConnId by mutableStateOf(MockData.activeConnId)

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
     * 「真实后端」开关（本批新增）：开 = 各页面从 [real] 取真实数据
     * （WS JSON-RPC 连 7789）；关 = 完全回到 mock 路径（一行不改 mock 行为）。
     */
    var realOn by mutableStateOf(false)

    /** 真实后端地址（连接页可改；默认本机 7789，测试时经 adb reverse 映射）。 */
    var realAddr by mutableStateOf("127.0.0.1:7789")

    /** 真实后端状态机（net/ 包；开关打开时驱动连接与事件归约）。 */
    val real = com.moyunteng.lxcode.remote.net.RealBackend()

    /** 顶栏连接药丸的在线位：真实模式跟 WS 实际连接态，mock 模式跟断线开关。 */
    fun pillOnline(): Boolean = if (realOn) real.connected else online

    /** 顶栏连接药丸的显示名：真实模式标「真实后端」，mock 模式沿用连接名。 */
    fun pillName(): String = if (realOn) "真实后端" else activeName

    val activeConn: BackendConn
        get() = conns.firstOrNull { it.id == activeConnId } ?: conns.first()

    /** 顶栏连接药丸显示名（本机显示「本机」，远程显示其名字）。 */
    val activeName: String get() = activeConn.name

    fun addConn(draft: ConnDraft) {
        conns.add(BackendConn(draft.id, draft.name, draft.addr, draft.token))
    }

    fun updateConn(draft: ConnDraft) {
        val i = conns.indexOfFirst { it.id == draft.id }
        if (i >= 0) conns[i] = BackendConn(draft.id, draft.name, draft.addr, draft.token)
    }

    fun removeConn(id: String) {
        conns.removeAll { it.id == id }
        if (activeConnId == id) activeConnId = "local"
    }

    fun sessionsInScope(): List<com.moyunteng.lxcode.remote.mock.SessionMeta> {
        val all = MockData.sessions.filter { !it.archived }
        return if (scope.isEmpty()) all.filter { it.workspace.isEmpty() }
        else all.filter { it.workspace == scope }
    }

    // ===== 真实模式的数据投影（realOn=true 时会话列表页读这里）=====

    /** 真实会话列表按范围过滤（workspace 与 mock 同语义：""=未分组）。 */
    fun realSessionsInScope(): List<com.moyunteng.lxcode.remote.mock.SessionMeta> =
        real.sessions.filter { it.workspace == scope }

    fun realProjectSessionCount(projectId: String): Int =
        real.sessions.count { it.workspace == projectId }

    fun realLooseSessionCount(): Int =
        real.sessions.count { it.workspace.isEmpty() }

    fun projectSessionCount(projectId: String): Int =
        MockData.sessions.count { it.workspace == projectId }

    fun looseSessionCount(): Int =
        MockData.sessions.count { it.workspace.isEmpty() }
}

/** 底部导航项。 */
private data class NavItem(val route: Route, val label: String, val icon: ImageVector)

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
    val state = remember { MockAppState() }

    // deep-link：`lxcode://pair?...`（系统相机扫码 / adb VIEW intent 唤起）
    // → 解析 → 预填连接表单；非法载荷 → 扫码页展示错误提示。
    LaunchedEffect(pairUri) {
        if (pairUri != null) {
            state.applyPairPayload(pairUri)
            onPairUriConsumed()
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
