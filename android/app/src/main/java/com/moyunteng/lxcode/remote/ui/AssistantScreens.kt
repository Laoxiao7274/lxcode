// 助手模式页（助手域 UI，对齐桌面端 AssistantWorkspace 的两页式）：
//   AssistantScreen        = 助手会话列表（克隆 SessionsScreen 瘦身版：无项目分组/
//                            无搜索/无会话操作菜单——桌面端助手列表同样无这些）
//   AssistantThreadScreen  = 助手对话页（块渲染/确认门/派发卡/子会话临时视图全部
//                            复用 ThreadScreen 那套；Composer 是瘦身版——无斜杠
//                            面板、无排队发送，数据源按 assistant.* 换 asst 桶；
//                            2026-12 助手附件：图片入口照主域 Composer 接入——
//                            PickVisualMedia 暂存 + 预览行，随 assistant.send 上 wire）
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
import androidx.compose.material.icons.filled.KeyboardArrowDown
import androidx.compose.material.icons.filled.KeyboardArrowLeft
import androidx.compose.material.icons.filled.Stop
import androidx.compose.material.icons.filled.WarningAmber
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
import com.moyunteng.lxcode.design.component.LxIconButton
import com.moyunteng.lxcode.design.component.LxListItem
import com.moyunteng.lxcode.design.component.LxSectionHeader
import com.moyunteng.lxcode.design.component.LxStatus
import com.moyunteng.lxcode.design.component.LxStatusDot
import com.moyunteng.lxcode.design.motion.LxEnterSpec
import com.moyunteng.lxcode.design.motion.LxStagger
import com.moyunteng.lxcode.design.motion.lxEnter
import com.moyunteng.lxcode.design.motion.lxStaggerEnter
import com.moyunteng.lxcode.design.token.Lx
import com.moyunteng.lxcode.design.token.LxColors
import com.moyunteng.lxcode.remote.model.SessionMeta
import com.moyunteng.lxcode.remote.model.ThreadBlock
import com.moyunteng.lxcode.remote.model.effortLabel
import com.moyunteng.lxcode.remote.model.modelDisplayLabel
import com.moyunteng.lxcode.remote.model.EFFORT_IDS
import com.moyunteng.lxcode.remote.net.RealBackend

// ===== 助手会话列表页（Route.Assistant）=====

/** 助手会话列表页（SessionsScreen 的瘦身克隆：标题 + 新对话 + 会话行）。 */
@Composable
fun AssistantScreen(state: AppState) {
    val real = state.real
    LaunchedEffect(Unit) { real.refreshAssistantSessions() }

    Column(
        Modifier
            .fillMaxSize()
            .background(LxColors.Bg),
    ) {
        // 顶栏：返回 + 助手标题（会话列表页不需要连接药丸——连接态由顶栏上游表达）
        Row(
            Modifier
                .fillMaxWidth()
                .background(LxColors.Surface)
                .padding(horizontal = Lx.space.s6, vertical = Lx.space.s6),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(Lx.space.s6),
        ) {
            LxIconButton(onClick = { state.route = Route.Sessions }) {
                Icon(
                    imageVector = Icons.Filled.KeyboardArrowLeft,
                    contentDescription = "返回",
                    tint = Lx.colors.FgMuted,
                    modifier = Modifier.size(20.dp),
                )
            }
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
            // 导航项：新对话（assistant.new）——与会话列表页同款 NavRow 形态
            val navItems = listOf(
                NavEntry("新对话", Icons.Filled.AutoAwesome) {
                    real.assistantNew(onReady = { id -> state.route = Route.AssistantThread(id) })
                },
            )
            itemsIndexed(navItems) { i, entry ->
                NavRow(entry, Modifier.lxStaggerEnter(count = navItems.size, index = i))
            }

            // 助手会话行（assistant.list；running 位 = 快照 ∪ busy 投影）
            item { LxSectionHeader("对话", topPadding = Lx.space.s12) }
            val sessions = real.asst.sessions
            if (sessions.isEmpty()) {
                item { SmallEmpty("还没有助手对话——点上方「新对话」开始") }
            } else {
                itemsIndexed(sessions) { i, s ->
                    AssistantSessionRow(
                        s,
                        Modifier.lxStaggerEnter(count = sessions.size, index = i),
                        running = s.running || real.asst.isRunning(s.id),
                        onClick = { state.route = Route.AssistantThread(s.id) },
                    )
                }
            }
        }
    }
}

/** 助手会话行（SessionRowItem 瘦身版：状态点 + 标题 + 时间；无 ⋯ 菜单——
 *  助手域没有 session.rename/archive 的对应方法，不造）。 */
@Composable
private fun AssistantSessionRow(
    session: SessionMeta,
    modifier: Modifier = Modifier,
    running: Boolean,
    onClick: () -> Unit,
) {
    LxListItem(
        title = session.title,
        modifier = modifier,
        leading = {
            LxStatusDot(
                status = if (running) LxStatus.Run else LxStatus.Idle,
                size = 7.dp,
            )
        },
        trailing = {
            Text(
                text = session.updatedAt,
                style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size10_5),
                color = Lx.colors.FgFaint,
            )
        },
        onClick = onClick,
    )
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

    fun lastBlockItemIndex(): Int = asst.blocks.size - 1

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
        if (count <= 0) return
        listState.scrollToItem(lastBlockItemIndex())
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

    Column(
        Modifier
            .fillMaxSize()
            .background(LxColors.Bg),
    ) {
        // 顶栏：返回 + 标题 + 确认门指示灯
        Row(
            Modifier
                .fillMaxWidth()
                .background(LxColors.Surface)
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
                tint = if (pendingConfirm != null) LxColors.Amber else Lx.colors.BorderStrong,
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
                val blocks: List<ThreadBlock> = asst.blocks
                itemsIndexed(blocks, key = { i, _ -> "$sessionId:$i" }) { i, block ->
                    Box(
                        Modifier.lxEnter(
                            spec = LxEnterSpec.BlockIn,
                            delayMillis = LxStagger.delayMillis(blocks.size, i),
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
                                0.24f to LxColors.Bg,
                                1f to LxColors.Bg,
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
                            tint = LxColors.Danger,
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
    // 确认门（assistant.confirmRequest → assistant.confirm 裁决；形态与主域同一只弹窗）
    if (pendingConfirm != null) {
        val c = pendingConfirm
        LxConfirmDialog(
            title = "执行此命令？",
            text = c.prompt.ifBlank { "${c.name} 工具申请执行，请确认。" },
            onApprove = { real.resolveAssistantConfirm(allow = true) },
            onDeny = { real.resolveAssistantConfirm(allow = false) },
            body = {
                TerminalBlock(
                    command = RealBackend.argsCommand(c.arguments).ifBlank { c.arguments },
                    cwd = RealBackend.argsCwd(c.arguments).ifBlank { null },
                )
            },
        )
    }
}


// ===== 助手输入区（Composer 瘦身版）=====

/**
 * 助手输入卡片：白卡形态与主域 Composer 同源（同一套 ring/圆角/内距），裁掉三块
 * 助手域用不上的能力：
 *  * 文件附件（桌面端 AssistantInput 同样不带——助手输入区是图片精简版）；
 *  * 斜杠面板 / 排队发送（桌面端 AssistantInput 同样没有——助手页是轻量输入）；
 *  * 发送缓冲区。
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

    var modelMenuOpen by remember { mutableStateOf(false) }
    var effortPanelOpen by remember { mutableStateOf(false) }
    // 附件（2026-12 助手附件）：图片走 PickVisualMedia（Photo Picker，免存储权限），
    // 读入暂存与预览复用主域同一套（real.pendingImages / attachNotice / PendingImageThumb），
    // 随 assistant.send 上 wire（见 RealBackend.assistantSend——base64 形态，手机与
    // 后端不同机，path 形态不适用）；不带文件入口（桌面端 AssistantInput 同款精简边界）。
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
    var permMenuOpen by remember { mutableStateOf(false) }
    var permConfirming by remember { mutableStateOf(false) }
    LaunchedEffect(permMenuOpen) { if (!permMenuOpen) permConfirming = false }

    // 返回键第一层：模型/权限菜单开着 → 返回先关弹层（与主域 Composer 同一条链）
    LaunchedEffect(modelMenuOpen, effortPanelOpen, permMenuOpen) {
        state.backInterceptor =
            if (modelMenuOpen || effortPanelOpen || permMenuOpen) {
                {
                    modelMenuOpen = false
                    effortPanelOpen = false
                    permMenuOpen = false
                    true
                }
            } else {
                null
            }
    }

    // 模型/effort 投影（与主域 Composer 同一条口径，数据源换 asst 桶）
    val asstModelId = asst.pickedModelId.ifBlank { real.defaultModelId }
    val currentModel = real.allModels.find { it.id == asstModelId }
    val effortOptions = currentModel?.efforts?.filter { it in EFFORT_IDS } ?: emptyList()
    var effort by remember { mutableStateOf("medium") }
    val effortId = if (effort in effortOptions) effort else effortOptions.lastOrNull() ?: ""
    val sendEffort = if (effortOptions.isNotEmpty()) effortId else ""
    val effortDim = if (effortOptions.isNotEmpty()) "· ${effortLabel(effortId)}" else null
    val approvalLabel = when (asst.approval) {
        "auto" -> "完全访问"
        "strict" -> "只读"
        else -> "默认"
    }

    Column(Modifier.fillMaxWidth().padding(bottom = 14.dp)) {
        // ===== 附件预览行（对齐主域 Composer 的 .att-preview 区：卡外、有暂存或有提示才渲染）=====
        if (real.pendingImages.isNotEmpty() || real.attachNotice != null) {
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
            }
        }
        val input = remember { MutableInteractionSource() }
        val focused by input.collectIsFocusedAsState()
        val ringWidth by androidx.compose.animation.core.animateDpAsState(
            targetValue = if (focused) 1.5.dp else 1.dp,
            animationSpec = androidx.compose.animation.core.tween(150),
            label = "assistant-ring-width",
        )
        val ringColor by com.moyunteng.lxcode.design.motion.lxAnimateColor(
            target = if (focused) Lx.colors.Fg else Lx.colors.BorderStrong,
            durationMillis = 150,
        )
        Column(
            Modifier
                .fillMaxWidth()
                .shadow(
                    elevation = 3.dp,
                    shape = ComposerCardShape,
                    clip = false,
                    spotColor = Color(0x0A000000),
                )
                .clip(ComposerCardShape)
                .background(Lx.colors.Surface)
                .border(width = ringWidth, color = ringColor, shape = ComposerCardShape),
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
                    // 「＋」添加图片（对齐主域 Composer 的 att-add：输入区左端图标；
                    // 只带图片入口——助手域不带文件，与桌面端 AssistantInput 同一精简边界）
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
                    Box {
                        ModelPill(
                            label = modelDisplayLabel(real.allModels, asstModelId),
                            effortDim = effortDim,
                            expanded = modelMenuOpen,
                            onToggle = {
                                modelMenuOpen = !modelMenuOpen
                                if (modelMenuOpen) effortPanelOpen = false
                            },
                        )
                        AnchorMenu(expanded = modelMenuOpen, onDismiss = { modelMenuOpen = false }) {
                            ModelMenuContent(
                                groups = real.modelGroups,
                                currentModelId = asstModelId,
                                effortDim = effortDim,
                                effortPanelOpen = effortPanelOpen,
                                onPickModel = { id ->
                                    val m = real.allModels.find { it.id == id }
                                    val supported = m?.efforts?.filter { it in EFFORT_IDS } ?: emptyList()
                                    if (supported.isNotEmpty() && effort !in supported) {
                                        effort = if ("medium" in supported) "medium" else supported.first()
                                    }
                                    real.changeAssistantModel(id) { ok, msg ->
                                        // 选中态由 assistant.modelChanged 广播对齐；失败 toast
                                        if (!ok) toast("切换模型失败${if (msg.isNotBlank()) "：$msg" else ""}")
                                    }
                                    modelMenuOpen = false
                                },
                                onToggleEffortPanel = { effortPanelOpen = !effortPanelOpen },
                            )
                        }
                        AnchorMenu(
                            expanded = modelMenuOpen && effortPanelOpen,
                            onDismiss = { effortPanelOpen = false },
                            focusable = false,
                            shiftXDp = 208.dp,
                            enter = mpSlideInEnter(),
                        ) {
                            EffortMenuContent(
                                effortIds = effortOptions,
                                currentEffort = effortId,
                                onPick = { id ->
                                    effort = id
                                    modelMenuOpen = false
                                },
                            )
                        }
                    }
                    Box {
                        ApprovalPill(
                            label = approvalLabel,
                            expanded = permMenuOpen,
                            onToggle = { permMenuOpen = !permMenuOpen },
                        )
                        AnchorMenu(expanded = permMenuOpen, onDismiss = { permMenuOpen = false }) {
                            PermMenuContent(
                                current = asst.approval ?: "confirm",
                                confirming = permConfirming,
                                onPick = { id ->
                                    if (id == "auto" && asst.approval != "auto") {
                                        if (!permConfirming) {
                                            permConfirming = true
                                            return@PermMenuContent
                                        }
                                    }
                                    permConfirming = false
                                    real.changeAssistantApproval(id)
                                    permMenuOpen = false
                                },
                            )
                        }
                    }
                    ContextRing(asst.contextUsage)
                    StatsCapsule(asst.sessionStats)
                }
                androidx.compose.foundation.layout.Spacer(Modifier.width(4.dp))
                SendButton(
                    busy = busy,
                    // 只附件没文本也允许发（与桌面端 AssistantInput 同语义——
                    // 后端以「[附件]」行充当正文）
                    enabled = text.trim().isNotEmpty() || real.pendingImages.isNotEmpty(),
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
}
