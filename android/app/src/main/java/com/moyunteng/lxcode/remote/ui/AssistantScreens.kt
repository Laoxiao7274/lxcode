// 助手模式页（助手域 UI，对齐桌面端 AssistantWorkspace 的两页式）：
//   AssistantScreen        = 助手会话列表（克隆 SessionsScreen 瘦身版：无项目分组/
//                            无搜索/无会话操作菜单——桌面端助手列表同样无这些）
//   AssistantThreadScreen  = 助手对话页（块渲染/确认门/派发卡/子会话临时视图全部
//                            复用 ThreadScreen 那套；Composer 是瘦身版——无斜杠
//                            面板、无排队发送，数据源按 assistant.* 换 asst 桶；
//                            2026-12 助手附件：图片入口照主域 Composer 接入——
//                            PickVisualMedia 暂存 + 预览行，随 assistant.send 上 wire；
//                            批次 1 补齐文件入口——OpenMultipleDocuments 暂存 +
//                            PendingFileCard 预览，files 与 images 同款 base64 上 wire）
//
// 域纪律：本页一切数据/操作走 RealBackend.asst 与 assistant.* 方法，不碰主域
// 的 currentSessionId/blocks——两域的状态桶互不污染（见 RealBackend 助手域注释）。
package com.moyunteng.lxcode.remote.ui

import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.border
import androidx.compose.foundation.gestures.scrollBy
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsFocusedAsState
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.ime
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.AutoAwesome
import androidx.compose.material.icons.filled.ChatBubbleOutline
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Description
import androidx.compose.material.icons.filled.KeyboardArrowDown
import androidx.compose.material.icons.filled.KeyboardArrowLeft
import androidx.compose.material.icons.filled.Stop
import androidx.compose.material.icons.filled.WarningAmber
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.runtime.withFrameNanos
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.graphics.Color
import androidx.compose.foundation.gestures.scrollBy
import androidx.compose.foundation.interaction.DragInteraction
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.isActive
import com.moyunteng.lxcode.design.component.LxConfirmDialog
import com.moyunteng.lxcode.design.component.LxButton
import com.moyunteng.lxcode.design.component.LxButtonSize
import com.moyunteng.lxcode.design.component.LxButtonVariant
import com.moyunteng.lxcode.design.component.LxIconButton
import com.moyunteng.lxcode.design.component.LxListItem
import com.moyunteng.lxcode.design.component.LxSectionHeader
import com.moyunteng.lxcode.design.component.LxStatus
import com.moyunteng.lxcode.design.component.LxStatusDot
import com.moyunteng.lxcode.design.component.LxTextField
import com.moyunteng.lxcode.design.motion.LxEnterSpec
import com.moyunteng.lxcode.design.motion.LxStagger
import com.moyunteng.lxcode.design.motion.lxEnter
import com.moyunteng.lxcode.design.motion.lxStaggerEnter
import com.moyunteng.lxcode.design.token.Lx
import com.moyunteng.lxcode.design.token.LxVariant
import com.moyunteng.lxcode.remote.model.SessionMeta
import com.moyunteng.lxcode.remote.model.ThreadBlock
import com.moyunteng.lxcode.remote.model.effortLabel
import com.moyunteng.lxcode.remote.model.modelDisplayLabel
import com.moyunteng.lxcode.remote.model.EFFORT_IDS
import com.moyunteng.lxcode.remote.net.RealBackend

// ===== 助手会话列表页（Route.Assistant）=====

/** 问候语（按时段：早上好/下午好/晚上好——原型 `.hero .hi`）。 */
private fun heroGreeting(): String {
    val h = java.time.LocalTime.now().hour
    return when {
        h < 6 -> "夜深了"
        h < 12 -> "早上好"
        h < 18 -> "下午好"
        else -> "晚上好"
    }
}

/** hero 副行：日期 + 挂起会话数（运行中；对齐原型「今天是 12 月 26 日 · 有 2 件事还挂着」）。 */
private fun heroSub(runningCount: Int): String {
    val date = java.time.LocalDate.now().let {
        "${it.monthValue} 月 ${it.dayOfMonth} 日"
    }
    return if (runningCount > 0) "今天是 $date · 有 $runningCount 件事还挂着" else "今天是 $date"
}

/** 助手会话列表页（SessionsScreen 的瘦身克隆：标题 + 新对话 + 会话行）。
 *  2026-12 补齐行操作（对齐桌面端 AssistantSessionList 的 ⋯ 菜单）：行**长按**
 *  或行尾 ⋯ 点按弹 SessionOpsMenu（重命名/归档——session.rename/archive 不分
 *  space，走 RealBackend 的助手域封装）；归档即从列表收起（刷新已过滤）。 */
@Composable
fun AssistantScreen(state: AppState) {
    val real = state.real
    LaunchedEffect(Unit) { real.refreshAssistantSessions() }

    // 行操作菜单 / 重命名弹窗（与 SessionsScreen 同款三件套）
    var menuFor by remember { mutableStateOf<SessionMeta?>(null) }
    var renaming by remember { mutableStateOf<SessionMeta?>(null) }
    // 返回键第一层：行菜单 / 重命名弹窗开着 → 返回先关弹层（AppRoot 的 BackHandler 链）
    LaunchedEffect(menuFor, renaming) {
        state.backInterceptor =
            if (menuFor != null || renaming != null) {
                {
                    menuFor = null
                    renaming = null
                    true
                }
            } else {
                null
            }
    }

    Column(
        Modifier
            .fillMaxSize()
            .background(Lx.colors.Bg),
    ) {
        // 顶栏：标题（tab 直达后列表页不是「从哪回来」的二级页——不摆返回键，
        // 切页走底部导航；标题独占左区，会话列表页不需要连接药丸——连接态由顶栏上游表达）
        Row(
            Modifier
                .fillMaxWidth()
                .background(Lx.colors.Surface)
                .padding(horizontal = Lx.space.s6, vertical = Lx.space.s6),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(Lx.space.s6),
        ) {
            Text(
                text = "助手",
                style = Lx.type.ListTitle.copy(fontWeight = FontWeight.SemiBold),
                color = Lx.colors.Fg,
                modifier = Modifier.weight(1f),
            )
        }

        // 断线/重连横幅（与会话列表页同一只横幅）
        if (real.phase == com.moyunteng.lxcode.remote.net.RealPhase.Failed || real.reconnecting) {
            Box(Modifier.padding(horizontal = Lx.space.s8)) {
                BackendDownBar(
                    down = real.phase == com.moyunteng.lxcode.remote.net.RealPhase.Failed,
                    reconnecting = real.reconnecting,
                    onRetry = { real.retry(state.realAddr, state.realToken) },
                    message = real.phaseMessage ?: "后端已断开：请检查电脑端 lxcode 是否在运行",
                    pairingInvalid = real.pairingInvalid,
                    onRepair = { state.route = Route.Pairing },
                )
            }
        }

        LazyColumn(
            Modifier
                .fillMaxSize()
                .padding(horizontal = Lx.space.s8),
        ) {
            // hero 头部（原型 `.hero`：问候 + 日期/挂起事项摘要；logo 圆标）
            item {
                HeroCard(
                    greeting = heroGreeting(),
                    sub = heroSub(real.asst.sessions.size),
                    // 渐变止点按变体（原型 `--grad-hero`：A=100%、B=58%、C=62%）
                    gradientStop = when (state.themeVariant) {
                        LxVariant.B -> 0.58f
                        LxVariant.C -> 0.62f
                        else -> 1f
                    },
                    modifier = Modifier.padding(horizontal = Lx.space.s4, vertical = Lx.space.s4),
                )
            }

            // 导航项：新对话（assistant.new）——与会话列表页同款 NavRow 形态
            val navItems = listOf(
                NavEntry("新对话", Icons.Filled.AutoAwesome) {
                    real.assistantNew(onReady = { id -> state.route = Route.AssistantThread(id) })
                },
            )
            itemsIndexed(navItems) { i, entry ->
                NavRow(entry, Modifier.lxStaggerEnter(count = navItems.size, index = i))
            }

            // 助手会话行（assistant.list；running 位 = 快照 ∪ busy 投影）——asstcard 形态
            item { LxSectionHeader("最近对话", topPadding = Lx.space.s12) }
            val sessions = real.asst.sessions
            if (sessions.isEmpty()) {
                item {
                    // 空态（原型 `.empty`：图标框 + 标题 + 副行）——抽到 Common.SmallEmpty 共用
                    SmallEmpty(
                        text = "随时开新对话",
                        icon = Icons.Filled.AutoAwesome,
                        title = "随时开新对话",
                        description = "日程、检索、写作、自动化——都是我的活",
                    )
                }
            } else {
                itemsIndexed(sessions) { i, s ->
                    Box {
                        AsstCard(
                            title = s.title,
                            sub = if (s.running || real.asst.isRunning(s.id)) "运行中…" else "轻点继续对话",
                            time = s.updatedAt,
                            running = s.running || real.asst.isRunning(s.id),
                            modifier = Modifier.lxStaggerEnter(count = sessions.size, index = i),
                            onClick = { state.route = Route.AssistantThread(s.id) },
                            onLongClick = { menuFor = s },
                            onMenu = { menuFor = s },
                        )
                        // 行操作菜单（对齐桌面端 AssistantSessionList 的 ⋯ 菜单：
                        // 重命名/归档；SessionOpsMenu 与 SessionsScreen 共用一只）
                        AnchorMenu(expanded = menuFor?.id == s.id, onDismiss = { menuFor = null }) {
                            SessionOpsMenu(
                                onRename = {
                                    menuFor = null
                                    renaming = s
                                },
                                onArchive = {
                                    menuFor = null
                                    real.archiveAssistantSession(s.id, archived = true)
                                },
                            )
                        }
                    }
                }
            }
            item { Box(Modifier.height(12.dp)) }
        }
    }

    // 重命名弹窗（session.rename 不分 space；成功后刷助手域列表）
    renaming?.let { target ->
        var draft by remember(target.id) { mutableStateOf(target.title) }
        AlertDialog(
            onDismissRequest = { renaming = null },
            containerColor = Lx.colors.Surface,
            title = {
                Text(
                    text = "重命名助手对话",
                    style = Lx.type.ListTitle.copy(fontWeight = FontWeight.SemiBold),
                    color = Lx.colors.Fg,
                )
            },
            text = {
                LxTextField(
                    value = draft,
                    onValueChange = { draft = it },
                    placeholder = "对话标题",
                )
            },
            confirmButton = {
                LxButton(
                    text = "保存",
                    onClick = {
                        val title = draft.trim()
                        if (title.isNotEmpty()) {
                            real.renameAssistantSession(target.id, title)
                        }
                        renaming = null
                    },
                    variant = LxButtonVariant.Primary,
                    size = LxButtonSize.Medium,
                )
            },
            dismissButton = {
                LxButton(
                    text = "取消",
                    onClick = { renaming = null },
                    variant = LxButtonVariant.Secondary,
                    size = LxButtonSize.Medium,
                )
            },
        )
    }
}

// ===== 助手对话页（Route.AssistantThread）=====

/** 助手对话页：滚动逻辑照 ThreadScreen 的同一套（sticky-follow/逐帧补滚/滚到底），
 *  数据源换 asst 桶；块渲染/确认门/派发卡/子会话临时视图复用主域那套。 */
@Composable
fun AssistantThreadScreen(state: AppState, sessionId: String) {
    // 子会话临时视图：child* 桶是全局单开的（主域与助手域复用同一套），进入即整页替换
    if (state.real.viewChildSessionId != null) {
        ChildThreadView(state)
        return
    }

    val real = state.real
    val asst = real.asst
    val title = asst.sessions.firstOrNull { it.id == sessionId }?.title ?: "助手对话"
    val running = asst.busy
    val pendingConfirm = asst.confirm

    // 进入页面装载历史（已指向同一会话则不重复拉）
    LaunchedEffect(sessionId) {
        if (sessionId != asst.currentId) real.openAssistantSession(sessionId)
    }

    // ===== 滚到底（照 ThreadScreen 的三时机 + sticky-follow）=====
    val listState = rememberLazyListState()
    val historyVersion = asst.historyVersion
    val imeHeight = WindowInsets.ime.getBottom(LocalDensity.current)
    var sticky by remember { mutableStateOf(true) }

    fun lastBlockItemIndex(): Int {
        // 流内确认卡（pendingConfirm != null 时插在最后一块之后）：它是「真底」，
        // sticky 跟随与 snapToBottom 都要把它算进去，否则卡到不了可视区。
        // 块数取展示层同一份（daysep 展示层插入的条目要算上）。
        var i = threadBlocksWithDaySeps(asst.blocks).size - 1
        if (pendingConfirm != null) i++
        return i
    }

    fun bottomAligned(): Boolean {
        val info = listState.layoutInfo
        if (info.visibleItemsInfo.isEmpty()) return true
        val last = info.visibleItemsInfo.lastOrNull { it.index == lastBlockItemIndex() } ?: return false
        return last.offset + last.size <= info.viewportEndOffset - info.afterContentPadding
    }

    LaunchedEffect(listState) {
        listState.interactionSource.interactions.collect { interaction ->
            when (interaction) {
                is DragInteraction.Start -> if (!bottomAligned()) sticky = false
                is DragInteraction.Stop, is DragInteraction.Cancel -> sticky = bottomAligned()
            }
        }
    }

    suspend fun snapToBottom(recheckDelayMs: Long = 0) {
        val count = asst.blocks.size
        if (count <= 0) return        listState.scrollToItem(lastBlockItemIndex())
        suspend fun converge() {
            repeat(6) {
                withFrameNanos { }
                if (!listState.canScrollForward) return
                val info = listState.layoutInfo
                val last = info.visibleItemsInfo.lastOrNull { it.index == lastBlockItemIndex() }
                val delta = last?.let {
                    it.offset + it.size - (info.viewportEndOffset - info.afterContentPadding)
                } ?: 0
                if (delta > 0) listState.scrollBy(delta.toFloat())
                else listState.scrollBy(1_000_000f)
            }
        }
        converge()
        if (recheckDelayMs > 0) {
            delay(recheckDelayMs)
            if (sticky) converge()
        }
    }

    LaunchedEffect(historyVersion) {
        if (historyVersion > 0 && asst.blocks.isNotEmpty()) {
            snapToBottom(recheckDelayMs = 400)
            sticky = true
        }
    }
    LaunchedEffect(asst.blocks.size) {
        if (asst.blocks.isNotEmpty() && sticky) {
            snapToBottom()
            sticky = true
        }
    }
    // 确认卡入流/出流（pendingConfirm 翻转）也是流尾高度变化——sticky 时补滚一次，
    // 卡不悬在可视区外（历史在 scrim 时代由 composerHeightDp 承担这件事）。
    LaunchedEffect(pendingConfirm) {
        if (asst.blocks.isNotEmpty() && sticky) {
            snapToBottom(recheckDelayMs = 400)
            sticky = true
        }
    }
    LaunchedEffect(running) {
        if (!running) return@LaunchedEffect
        while (isActive) {
            withFrameNanos { }
            if (!sticky) continue
            if (!listState.canScrollForward) continue
            val info = listState.layoutInfo
            val last = info.visibleItemsInfo.lastOrNull { it.index == lastBlockItemIndex() }
            val delta = last?.let {
                it.offset + it.size - (info.viewportEndOffset - info.afterContentPadding)
            } ?: 0
            if (delta > 0) listState.scrollBy(delta.toFloat())
        }
    }
    LaunchedEffect(imeHeight) {
        if (imeHeight > 0 && asst.blocks.isNotEmpty() && sticky) {
            snapToBottom()
            sticky = true
        }
    }

    // 输入区高度（sticky 时 contentPadding 变化补滚一次——同 ThreadScreen）
    var composerHeightDp by remember { mutableStateOf(0.dp) }
    val density = LocalDensity.current

    // ===== 向上翻页（assistant.history 的 before_seq 分页——2026-12 P2 补齐）=====
    //
    // 触发/补偿与主域 ThreadScreen 同一套（index ≤ 1 且还有更早 → loadAssistantEarlier；
    // earlierVersion 变化 → 按插入前快照恢复视觉位置）。
    val earlierVersion = asst.earlierVersion
    var prePagingFirstIndex by remember { mutableStateOf(0) }
    var prePagingFirstOffset by remember { mutableStateOf(0) }
    var prePagingBlockCount by remember { mutableStateOf(0) }
    var pendingScrollCompensation by remember { mutableStateOf(false) }

    LaunchedEffect(listState) {
        snapshotFlow { listState.firstVisibleItemIndex to listState.firstVisibleItemScrollOffset }
            .distinctUntilChanged()
            .collect { (index, offset) ->
                if (index <= 1 && asst.hasMoreHistory && !asst.loadingEarlier &&
                    asst.oldestLoadedSeq > 0 && asst.blocks.isNotEmpty()
                ) {
                    prePagingFirstIndex = index
                    prePagingFirstOffset = offset
                    prePagingBlockCount = asst.blocks.size
                    pendingScrollCompensation = true
                    real.loadAssistantEarlier()
                }
            }
    }
    LaunchedEffect(earlierVersion) {
        if (!pendingScrollCompensation) return@LaunchedEffect
        pendingScrollCompensation = false
        val inserted = asst.blocks.size - prePagingBlockCount
        if (inserted <= 0) return@LaunchedEffect
        val targetIndex = prePagingFirstIndex + inserted
        repeat(8) {
            withFrameNanos { }
            val info = listState.layoutInfo
            if (info.visibleItemsInfo.isEmpty()) return@LaunchedEffect
            val target = info.visibleItemsInfo.firstOrNull { it.index == targetIndex }
            if (target != null) {
                val delta = target.offset - prePagingFirstOffset
                if (delta != 0) listState.scrollBy(delta.toFloat())
                return@LaunchedEffect
            }
            listState.scrollToItem(targetIndex, prePagingFirstOffset)
        }
    }

    Column(
        Modifier
            .fillMaxSize()
            .background(Lx.colors.Bg),
    ) {
        // 顶栏：返回 + 标题 + 确认门指示灯
        Row(
            Modifier
                .fillMaxWidth()
                .background(Lx.colors.Surface)
                .padding(horizontal = Lx.space.s6, vertical = Lx.space.s6),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(Lx.space.s6),
        ) {
            LxIconButton(onClick = { state.route = Route.Assistant }) {
                Icon(
                    imageVector = Icons.Filled.KeyboardArrowLeft,
                    contentDescription = "返回",
                    tint = Lx.colors.FgMuted,
                    modifier = Modifier.size(20.dp),
                )
            }
            Text(
                text = title,
                style = Lx.type.ListTitle,
                color = Lx.colors.Fg,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f),
            )
            Icon(
                imageVector = Icons.Filled.WarningAmber,
                contentDescription = "确认门",
                tint = if (pendingConfirm != null) Lx.colors.Amber else Lx.colors.BorderStrong,
                modifier = Modifier.size(18.dp),
            )
        }

        // 断线/重连横幅
        if (real.phase == com.moyunteng.lxcode.remote.net.RealPhase.Failed || real.reconnecting) {
            BackendDownBar(
                down = real.phase == com.moyunteng.lxcode.remote.net.RealPhase.Failed,
                reconnecting = real.reconnecting,
                onRetry = { real.retry(state.realAddr, state.realToken) },
                message = real.phaseMessage ?: "后端已断开：请检查电脑端 lxcode 是否在运行",
                pairingInvalid = real.pairingInvalid,
                onRepair = { state.route = Route.Pairing },
            )
        }

        // 消息流 + 底部输入区（scrim 叠层照 ThreadScreen 的同一套量测）
        Box(Modifier.weight(1f).fillMaxWidth()) {
            // 输入区高度变化（busy 条出现/消失）时 sticky 跟随态补滚一次
            LaunchedEffect(composerHeightDp) {
                if (composerHeightDp > 0.dp && asst.blocks.isNotEmpty() && sticky) snapToBottom()
            }
            LazyColumn(
                Modifier
                    .fillMaxSize()
                    .padding(horizontal = Lx.space.s16),
                state = listState,
                contentPadding = PaddingValues(top = Lx.space.s8, bottom = composerHeightDp),
                verticalArrangement = Arrangement.spacedBy(18.dp),
            ) {
                // 历史装载中（openAssistantSession 异步拉 assistant.history）
                if (real.asstHistoryLoading && asst.blocks.isEmpty()) {
                    item(key = "$sessionId:loading") {
                        Row(
                            Modifier
                                .fillMaxWidth()
                                .padding(vertical = Lx.space.s16),
                            horizontalArrangement = Arrangement.Center,
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            TypingDots(dotSize = 5.dp, gap = 3.dp)
                            Text(
                                text = "正在加载…",
                                style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size11_5),
                                color = Lx.colors.FgMuted,
                                modifier = Modifier.padding(start = Lx.space.s4),
                            )
                        }
                    }
                }
                // stagger 限首次装载路径（P3 性能，同 ThreadScreen：超上限不逐条延迟）
                // daysep 插入在展示层（不进 asst.blocks，理由同 ThreadScreen）
                // 头部：分页状态行（spinner / 重试 / 「已加载全部历史」）——主域
                // ThreadScreen 的 header 同款（key 固定，插入更早块时恒在 index 0）。
                item(key = "$sessionId:header") {
                    when {
                        asst.loadingEarlier -> Row(
                            Modifier.fillMaxWidth().padding(vertical = Lx.space.s4),
                            horizontalArrangement = Arrangement.Center,
                        ) {
                            TypingDots(dotSize = 5.dp, gap = 3.dp)
                            Text(
                                text = "加载更早的历史…",
                                style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size11_5),
                                color = Lx.colors.FgMuted,
                                modifier = Modifier.padding(start = Lx.space.s4),
                            )
                        }
                        asst.loadEarlierError != null -> Row(
                            Modifier.fillMaxWidth(),
                            horizontalArrangement = Arrangement.Center,
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            Text(
                                text = "更早的历史加载失败",
                                style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size11_5),
                                color = Lx.colors.FgMuted,
                            )
                            Text(
                                text = "重试",
                                style = Lx.type.BodySmall.copy(
                                    fontSize = Lx.type.Size11_5,
                                    fontWeight = FontWeight.Medium,
                                    textDecoration = androidx.compose.ui.text.style.TextDecoration.Underline,
                                ),
                                color = Lx.colors.Fg,
                                modifier = Modifier
                                    .padding(start = Lx.space.s4)
                                    .clickable { real.loadAssistantEarlier() },
                            )
                        }
                        !asst.hasMoreHistory && asst.oldestLoadedSeq > 0 -> Text(
                            text = "已加载全部历史",
                            style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size11_5),
                            color = Lx.colors.FgMuted,
                            modifier = Modifier.fillMaxWidth().padding(vertical = Lx.space.s2),
                            textAlign = androidx.compose.ui.text.style.TextAlign.Center,
                        )
                    }
                }
                val blocks: List<ThreadBlock> = threadBlocksWithDaySeps(asst.blocks)
                itemsIndexed(blocks, key = { i, _ -> "$sessionId:$i" }) { i, block ->
                    Box(
                        Modifier.lxEnter(
                            spec = LxEnterSpec.BlockIn,
                            delayMillis = if (blocks.size <= STAGGER_BLOCK_LIMIT) {
                                LxStagger.delayMillis(blocks.size, i)
                            } else {
                                0
                            },
                            key = i,
                        ),
                    ) {
                        ThreadBlockView(
                            state,
                            block,
                            busy = running,
                            stableKey = "$sessionId:$i",
                            onOpenChild = { childId, agentName, task ->
                                real.openAssistantChildSession(
                                    childId,
                                    title = "$agentName · ${task.take(24)}",
                                )
                            },
                        )
                    }
                }
                // 确认门（assistant.confirmRequest → assistant.confirm 裁决）——
                // **消息流内卡片**（2026-12 用户拍板：挂 scrim 里会「挤」且形态偏离
                // 原型；pending 是会话级状态，流内按「最后一条块之后」插卡，随消息
                // spacing 节奏）。ask 形态走 AskCard（点选/打字回答），工具确认走
                // ConfirmCard；scrim 里只留 busy 条。
                if (pendingConfirm != null) {
                    item(key = "$sessionId:confirm") {
                        val c = pendingConfirm!!
                        if (c.isAsk) {
                            AskCard(
                                confirm = c,
                                onAnswer = { answer ->
                                    // answer 非空 = 文本回答；空 = 跳过（allow=false，后端合成跳过结果）
                                    real.resolveAssistantConfirm(allow = answer.isNotEmpty(), answer = answer)
                                },
                            )
                        } else {
                            ConfirmCard(
                                confirm = c,
                                onAllow = { real.resolveAssistantConfirm(allow = true) },
                                onDeny = { real.resolveAssistantConfirm(allow = false) },
                                terminal = {
                                    TerminalBlock(
                                        command = RealBackend.argsCommand(c.arguments).ifBlank { c.arguments },
                                        cwd = RealBackend.argsCwd(c.arguments).ifBlank { null },
                                    )
                                },
                            )
                        }
                    }
                }
                if (running && asst.streamVersion > 0) {
                    item(key = "$sessionId:typing") {
                        TypingIndicator(
                            modifier = Modifier.padding(start = Lx.space.s4, top = Lx.space.s2),
                        )
                    }
                }
            }

            // 底部输入区（scrim + AssistantComposer）：onSizeChanged 量整个 scrim Column
            Column(
                Modifier
                    .align(Alignment.BottomCenter)
                    .fillMaxWidth()
                    .onSizeChanged { composerHeightDp = with(density) { it.height.toDp() } }
                    .background(
                        Brush.verticalGradient(
                            colorStops = arrayOf(
                                0f to Color.Transparent,
                                0.24f to Lx.colors.Bg,
                                1f to Lx.colors.Bg,
                            ),
                        ),
                    ),
            ) {
                androidx.compose.foundation.layout.Spacer(Modifier.size(40.dp))
                // 「生成中 · 点按停止」指示条（busy 且无流式文本；与主会话同一形态）
                if (running && asst.streamVersion == 0) {
                    val context = LocalContext.current
                    Row(
                        Modifier
                            .padding(horizontal = Lx.space.s12, vertical = Lx.space.s4)
                            .clip(Lx.radius.PillShape)
                            .background(Lx.colors.BorderSoft)
                            .clickable(
                                interactionSource = remember { MutableInteractionSource() },
                                indication = null,
                            ) {
                                real.assistantCancel { ok, msg ->
                                    if (!ok) {
                                        android.widget.Toast.makeText(
                                            context,
                                            "停止失败${if (msg.isNotBlank()) "：$msg" else ""}",
                                            android.widget.Toast.LENGTH_SHORT,
                                        ).show()
                                    }
                                }
                            }
                            .padding(horizontal = Lx.space.s8, vertical = Lx.space.s4),
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.spacedBy(Lx.space.s6),
                    ) {
                        Icon(
                            imageVector = Icons.Filled.Stop,
                            contentDescription = "停止",
                            tint = Lx.colors.Danger,
                            modifier = Modifier.size(12.dp),
                        )
                        TypingDots(dotSize = 5.dp, gap = 3.dp)
                        Text(
                            text = "生成中 · 点按停止",
                            style = Lx.type.BodySmall.copy(
                                fontSize = Lx.type.Size11_5,
                                fontWeight = FontWeight.Medium,
                            ),
                            color = Lx.colors.FgMuted,
                        )
                    }
                }
                // （确认卡已移进消息流 itemsIndexed 渲染——scrim 里只留 busy 条与输入区）
                Box(
                    Modifier
                        .fillMaxWidth()
                        .padding(horizontal = Lx.space.s12),
                ) {
                    AssistantComposer(state)
                }
            }
        }
    }
}


// ===== 助手输入区（Composer 瘦身版）=====

/**
 * 助手输入卡片：白卡形态与主域 Composer 同源（同一套 ring/圆角/内距），裁掉两块
 * 助手域用不上的能力：
 *  * 斜杠面板 / 排队发送（桌面端 AssistantInput 同样没有——助手页是轻量输入）；
 *  * 发送缓冲区。
 * 附件（批次 1 补齐）：图片 + 文件两个入口与主域 Composer 同款；文件暂存
 * （real.pendingFiles）与预览（PendingFileCard）复用主域同一套，随 assistant.send
 * 上 wire（files 与 images 同款 base64 形态）。
 * 保留：模型药丸（assistant.model 会话级切）/ effort 二级面板 / 审批档药丸
 * （assistant.approval）/ 上下文环 / 统计胶囊 / 发送钮（busy = 停止语义）。
 */
@Composable
private fun AssistantComposer(state: AppState) {
    val real = state.real
    val asst = real.asst
    val busy = asst.busy
    var text by remember { mutableStateOf("") }
    val context = LocalContext.current
    fun toast(msg: String) = android.widget.Toast.makeText(context, msg, android.widget.Toast.LENGTH_SHORT).show()

    // Composer 弹层统一收口成**应用级 bottom sheet 单槽**（2026-12 用户拍板：
    // 与主域 Composer 同一条提升——本组合只写 state.openAppSheet；挂载点与返回键
    // 收口都在 AppRoot）。effort 视图 = state.assistantEffort（提升到 AppState）。
    // 附件（2026-12 助手附件，本次补齐文件入口）：图片走 PickVisualMedia（Photo
    // Picker，免存储权限）、文件走 OpenMultipleDocuments（SAF，免权限）——与主域
    // Composer 同一套入口；读入暂存与预览复用主域同一套（real.pendingImages /
    // real.pendingFiles / attachNotice / PendingImageThumb / PendingFileCard），
    // 随 assistant.send 上 wire（见 RealBackend.assistantSend——base64 形态，手机与
    // 后端不同机，path 形态不适用）。
    val imagePicker = rememberLauncherForActivityResult(ActivityResultContracts.PickMultipleVisualMedia(maxItems = 4)) { uris ->
        if (uris.isNotEmpty()) {
            real.addPendingImages(
                uris.map { uri ->
                    val mime = context.contentResolver.getType(uri) ?: "image/jpeg"
                    uri to mime
                },
            )
        }
    }
    val filePicker = rememberLauncherForActivityResult(ActivityResultContracts.OpenMultipleDocuments()) { uris ->
        if (uris.isNotEmpty()) real.addPendingFiles(uris)
    }

    // 模型/effort 投影（与主域 Composer 同一条口径，数据源换 asst 桶）
    val asstModelId = asst.pickedModelId.ifBlank { real.defaultModelId }
    val currentModel = real.allModels.find { it.id == asstModelId }
    val effortOptions = currentModel?.efforts?.filter { it in EFFORT_IDS } ?: emptyList()
    val effort = state.assistantEffort
    val effortId = if (effort in effortOptions) effort else effortOptions.lastOrNull() ?: ""
    val sendEffort = if (effortOptions.isNotEmpty()) effortId else ""
    val effortDim = if (effortOptions.isNotEmpty()) "· ${effortLabel(effortId)}" else null
    val approvalLabel = when (asst.approval) {
        "auto" -> "自动"
        "strict" -> "严格"
        else -> "确认"
    }

    Column(Modifier.fillMaxWidth().padding(bottom = 14.dp)) {
        // ===== 附件预览行（对齐主域 Composer 的 .att-preview 区：卡外、有暂存或有提示才渲染）=====
        if (real.pendingImages.isNotEmpty() || real.pendingFiles.isNotEmpty() || real.attachNotice != null) {
            Column(
                Modifier
                    .fillMaxWidth()
                    .padding(start = 16.dp, end = 16.dp, bottom = 8.dp),
                verticalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                real.attachNotice?.let { notice ->
                    Text(
                        text = notice,
                        style = TextStyle(fontFamily = Lx.type.Sans, fontSize = 11.5.sp),
                        color = Lx.colors.FgMuted,
                    )
                }
                if (real.pendingImages.isNotEmpty()) {
                    Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                        real.pendingImages.forEach { im ->
                            PendingImageThumb(
                                image = im,
                                urlOf = { real.attachmentURL(it) },
                                onRemove = { real.removePendingImage(im.id) },
                            )
                        }
                    }
                }
                // 文件暂存卡（主域 Composer 的 pendingFiles 同款——240×64 卡 + 移除钮）
                if (real.pendingFiles.isNotEmpty()) {
                    Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                        real.pendingFiles.forEach { f ->
                            PendingFileCard(
                                name = f.name,
                                sub = fileSub(f.size, f.mime),
                                onRemove = { real.removePendingFile(f.id) },
                            )
                        }
                    }
                }
            }
        }
        val input = remember { MutableInteractionSource() }
        val focused by input.collectIsFocusedAsState()
        // 助手输入卡与主域同规格：r22 卡 + 聚焦 ring 光环（`.inputcard:focus-within`）
        val ringColor by com.moyunteng.lxcode.design.motion.lxAnimateColor(
            target = if (focused) Lx.colors.FgFaint else Lx.colors.BorderStrong,
            durationMillis = 180,
        )
        val haloAlpha by androidx.compose.animation.core.animateFloatAsState(
            targetValue = if (focused) 0.28f else 0f,
            animationSpec = androidx.compose.animation.core.tween(180),
            label = "assistant-halo",
        )
        val haloColor = Lx.colors.Ring
        Column(
            Modifier
                .fillMaxWidth()
                .drawHalo(haloAlpha, haloColor, 22.dp)
                .shadow(
                    elevation = 3.dp,
                    shape = ComposerCardShape,
                    clip = false,
                    spotColor = Lx.shadow.Card.copy(alpha = 0.06f),
                )
                .clip(ComposerCardShape)
                .background(Lx.colors.Surface)
                .border(width = 1.dp, color = ringColor, shape = ComposerCardShape),
        ) {
            ComposerTextField(
                value = text,
                onValueChange = { text = it },
                busy = busy,
                interactionSource = input,
                modifier = Modifier.fillMaxWidth(),
            )
            Row(
                Modifier
                    .fillMaxWidth()
                    .padding(start = 12.dp, end = 12.dp, top = 8.dp, bottom = 10.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                Row(
                    Modifier
                        .weight(1f)
                        .horizontalScroll(rememberScrollState()),
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    // 「＋」添加图片（对齐主域 Composer 的 att-add：输入区左端图标）
                    // ＋ 文件入口（主域 Composer 的文件图标同款——批次 1 补齐助手域
                    // 文件附件；上限口径与主域同源：≤4 个、单个 20MB，无类型白名单）
                    LxIconButton(onClick = {
                        imagePicker.launch(
                            androidx.activity.result.PickVisualMediaRequest(
                                ActivityResultContracts.PickVisualMedia.ImageOnly,
                            ),
                        )
                    }) {
                        Icon(
                            imageVector = Icons.Filled.Add,
                            contentDescription = "添加图片",
                            tint = Lx.colors.FgMuted,
                            modifier = Modifier.size(16.dp),
                        )
                    }
                    LxIconButton(onClick = { filePicker.launch(arrayOf("*/*")) }) {
                        Icon(
                            imageVector = Icons.Filled.Description,
                            contentDescription = "添加文件",
                            tint = Lx.colors.FgMuted,
                            modifier = Modifier.size(16.dp),
                        )
                    }
                    // ===== 药丸组（P2：点按开 bottom sheet，与主域同款）=====
                    ModelPill(
                        label = modelDisplayLabel(real.allModels, asstModelId),
                        effortDim = effortDim,
                        expanded = state.appSheet == AppSheet.AssistantModel,
                        onToggle = {
                            if (state.appSheet == AppSheet.AssistantModel) state.closeAppSheet() else state.openAppSheet(AppSheet.AssistantModel)
                        },
                    )
                    if (effortOptions.isNotEmpty()) {
                        ComposerGhostPill(
                            label = effortLabel(effortId),
                            onClick = {
                                if (state.appSheet == AppSheet.AssistantEffort) state.closeAppSheet() else state.openAppSheet(AppSheet.AssistantEffort)
                            },
                        )
                    }
                    ApprovalPill(
                        label = approvalLabel,
                        expanded = state.appSheet == AppSheet.AssistantApproval,
                        onToggle = {
                            if (state.appSheet == AppSheet.AssistantApproval) state.closeAppSheet() else state.openAppSheet(AppSheet.AssistantApproval)
                        },
                    )
                    ContextRing(
                        usage = asst.contextUsage,
                        onClick = { state.openAppSheet(AppSheet.AssistantContext) },
                    )
                    StatsCapsule(asst.sessionStats)
                }
                androidx.compose.foundation.layout.Spacer(Modifier.width(4.dp))
                SendButton(
                    busy = busy,
                    // 只附件没文本也允许发（与桌面端 AssistantInput 同语义——
                    // 后端以「[附件]」行充当正文）
                    enabled = text.trim().isNotEmpty() || real.pendingImages.isNotEmpty() || real.pendingFiles.isNotEmpty(),
                    onClick = {
                        if (busy) {
                            real.assistantCancel { ok, msg ->
                                if (!ok) toast("停止失败${if (msg.isNotBlank()) "：$msg" else ""}")
                            }
                        } else {
                            real.assistantSend(text, sendEffort)
                            text = ""
                        }
                    },
                )
            }
        }
    }

    // （Composer 弹层已提升为应用级 bottom sheet——AppSheetHost 在 AppRoot 唯一挂载，
    // assistant.* 域语义在 AppSheet.Assistant* 四个条目；本组合只负责药丸的写口。）
}
