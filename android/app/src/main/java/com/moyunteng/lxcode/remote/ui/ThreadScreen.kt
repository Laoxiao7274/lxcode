// 对话线程页（ThreadScreen）—— 照桌面端 thread/ 的块渲染器。
//
// 顶栏（连接药丸 + 会话标题 + 确认门触发入口）、中部消息流、底部输入区固定。
// 消息流逐个渲染八类块：用户气泡 / 助手正文 / 思考块 / 工具卡 / 子 Agent 派发卡 /
// 压缩检查点 / todo 清单 / 错误块。
package com.moyunteng.lxcode.remote.ui

import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.StartOffset
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.scrollBy
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.interaction.DragInteraction
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.ime
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.filled.Checklist
import androidx.compose.material.icons.filled.ErrorOutline
import androidx.compose.material.icons.filled.Info
import androidx.compose.material.icons.filled.KeyboardArrowDown
import androidx.compose.material.icons.filled.KeyboardArrowLeft
import androidx.compose.material.icons.filled.KeyboardArrowRight
import androidx.compose.material.icons.filled.KeyboardArrowUp
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
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.PathEffect
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.distinctUntilChanged
import com.moyunteng.lxcode.design.component.LxConfirmDialog
import com.moyunteng.lxcode.design.component.LxIconButton
import com.moyunteng.lxcode.design.component.LxStatus
import com.moyunteng.lxcode.design.component.LxStatusDot
import com.moyunteng.lxcode.design.motion.LxEnterSpec
import com.moyunteng.lxcode.design.motion.LocalLxMotionEnabled
import com.moyunteng.lxcode.design.motion.LxExpandable
import com.moyunteng.lxcode.design.motion.LxShimmerText
import com.moyunteng.lxcode.design.motion.LxStagger
import com.moyunteng.lxcode.design.motion.lxAnimateFloat
import com.moyunteng.lxcode.design.motion.lxCaret
import com.moyunteng.lxcode.design.motion.lxEnter
import com.moyunteng.lxcode.design.motion.lxPressScale
import com.moyunteng.lxcode.design.motion.lxStaggerEnter
import com.moyunteng.lxcode.design.motion.lxSweep
import com.moyunteng.lxcode.design.motion.rememberStreamReveal
import com.moyunteng.lxcode.design.token.Lx
import com.moyunteng.lxcode.design.token.LxColors
import com.moyunteng.lxcode.remote.model.ThreadBlock
import com.moyunteng.lxcode.remote.model.formatMs
import com.moyunteng.lxcode.remote.ui.md.MdBlock
import com.moyunteng.lxcode.remote.ui.md.MdSpan
import com.moyunteng.lxcode.remote.ui.md.parseMarkdown

/** 对话线程页。 */
@Composable
fun ThreadScreen(state: MockAppState, sessionId: String) {
    // 子会话临时视图（点派发卡进入）：数据源切 child* 状态，返回回主会话——
    // 与桌面端「临时视图」同语义。整页替换渲染，主会话的滚动/输入态不共享。
    if (state.real.viewChildSessionId != null) {
        ChildThreadView(state)
        return
    }

    // 真实后端模式：标题/块/忙闲/确认全部来自 RealBackend（WS 事件归约）。
    // （mock 数据已随 P0 原型退役：未连接时会话页是连接引导态，到不了线程页。）
    val real = state.real
    val title = real.sessionTitle
    // 「运行中」的判定 = 会话忙闲（chat.busy 事件 / done-error 收敛）
    val running = real.busy
    // 真实模式的挂起确认（chat.confirmRequest）→ 复用 LxConfirmDialog
    val pendingConfirm = real.confirm

    // 从会话列表点进来时拉取历史（session.resume → chat.history；
    // 新建的会话已在 newSession 里切入，currentSessionId 相同则不重复拉）。
    LaunchedEffect(sessionId) {
        if (sessionId != real.currentSessionId) real.openSession(sessionId)
    }

    // ===== 滚到底 =====
    // 三个时机都要滚：
    //  ① 历史装载完成（openSession 异步回填 blocks 的那一刻）——对齐桌面端「进会话
    //     停在最底」；用 historyVersion（RealBackend 在 applyHistory 末尾递增）触发，
    //     **无条件**滚（装载 = 进入这个会话，用户预期就是看最新）。
    //  ② 消息流增长（delta 追加 / 工具行插入 / 用户发送）——只在「本来就在底部附近」
    //     时跟随（sticky-follow）：用户往上翻历史时每条 delta 把他拽回底部，历史就没法看了。
    //  ③ 键盘弹出（IME 可见性翻转）：lazyListState 是同一实例、消息数量不变，
    //     不监听它的话视口被压缩后最后一屏停在半截——imeTargetHeight 变化即再滚一次。
    val listState = rememberLazyListState()
    val historyVersion = real.historyVersion
    val imeHeight = WindowInsets.ime.getBottom(LocalDensity.current)

    // sticky-follow 的「跟随」状态：进入会话/拉底之后为 true，用户翻离底部后为 false。
    // 判定**只看用户拖拽**（interactionSource 的 DragInteraction）——程序化滚动
    // （snapToBottom / 跟随补滚）不产生拖拽事件，不会误伤；isScrollInProgress
    // 无法区分两者（程序滚也会置它，真机复测抓到过：补滚被当成翻历史、跟随被解除）。
    var sticky by remember { mutableStateOf(true) }

    /** 是否已在列表**真正的底部**：最后一块的**底边**进入视口可视区。
     *  不能用「最后一个条目可见」判定——最后一块超过一屏时它永远可见，
     *  用户看它的开头也会被判成在底部（流式新块一到就把人拽走）；
     *  补滚到真底部后该判定同样成立，两处语义统一。
     *  注意 viewportEndOffset **含**底部 contentPadding（输入区高度），
     *  真底 = viewportEndOffset - afterContentPadding，不减会把「还没到底」
     *  判成「已在底」（真机复测抓到过：流式跟随因此在块开头卡住）。 */
    /** 最后一个消息块在 LazyColumn 里的条目下标：头部固定有「分页状态行」item
     *  （恒在列表头；离线提示条打开时它占 index 0、header 退居 1）——消息块下标 =
     *  头部条目数 + 块下标。「真底」判定与 snapToBottom 共用这一份口径。 */
    fun lastBlockItemIndex(): Int {
        val headerCount = if (real.showingCached) 2 else 1
        return headerCount + real.blocks.size - 1
    }

    fun bottomAligned(): Boolean {
        val info = listState.layoutInfo
        if (info.visibleItemsInfo.isEmpty()) return true
        val last = info.visibleItemsInfo.lastOrNull { it.index == lastBlockItemIndex() }
            ?: return false
        return last.offset + last.size <= info.viewportEndOffset - info.afterContentPadding
    }

    // 用户拖拽流：开始拖拽时若不在底部 = 在翻历史，解除跟随；拖拽结束停在底部则恢复。
    LaunchedEffect(listState) {
        listState.interactionSource.interactions.collect { interaction ->
            when (interaction) {
                is DragInteraction.Start -> if (!bottomAligned()) sticky = false
                is DragInteraction.Stop, is DragInteraction.Cancel -> sticky = bottomAligned()
            }
        }
    }

    /** 滚到列表**真正的底部**。
     *  scrollToItem(index) 只把该条目的顶部对齐视口顶（LazyColumn 对超高条目就是
     *  这个行为）——最后一块超过一屏时，用户看到的是块的开头、最新内容在屏外。
     *  所以跳到最后一项之后，再按「条目底边 − 视口底」的实测差值补滚：
     *  逐帧收敛（最多 6 帧，防抖、不无限循环），期间图片/代码块异步量高导致的
     *  高度变化也会被后续帧补上；scrollBy 在可滚动范围尽头自动钳住，不会滚过头。
     *  [recheckDelayMs] > 0 时延迟后再收敛一次（异步内容在收敛帧之后才量完的兜底）。 */
    suspend fun snapToBottom(recheckDelayMs: Long = 0) {
        val count = real.blocks.size
        if (count <= 0) return
        listState.scrollToItem(lastBlockItemIndex())
        suspend fun converge() {
            repeat(6) {
                withFrameNanos { }
                if (!listState.canScrollForward) return
                val info = listState.layoutInfo
                val last = info.visibleItemsInfo.lastOrNull { it.index == lastBlockItemIndex() }
                // 真底 = viewportEndOffset - afterContentPadding（含底部输入区 padding，见 bottomAligned）
                val delta = last?.let {
                    it.offset + it.size - (info.viewportEndOffset - info.afterContentPadding)
                } ?: 0
                if (delta > 0) listState.scrollBy(delta.toFloat())
                else listState.scrollBy(1_000_000f) // 差值口径不符时兜底：直接滚到范围尽头
            }
        }
        converge()
        if (recheckDelayMs > 0) {
            delay(recheckDelayMs)
            if (sticky) converge()
        }
    }

    LaunchedEffect(historyVersion) {
        if (historyVersion > 0 && real.blocks.isNotEmpty()) {
            snapToBottom(recheckDelayMs = 400)
            sticky = true
        }
    }
    LaunchedEffect(real.blocks.size) {
        if (real.blocks.isNotEmpty() && sticky) {
            snapToBottom()
            sticky = true
        }
    }
    // 流式期间内容增长（delta 追加让最后一块变高、块数不变）：sticky 时跟随。
    // 用收敛式补滚（scrollBy 差值）而不是 scrollToItem——避免每条 delta 都把视口
    // 猛跳一次，也天然兼容超高块（scrollToItem 对它只做顶对齐）。
    // 先等一帧再量：effect 触发时新文本往往还没量高（layoutInfo 是旧的），
    // 直接量会得到 delta≈0 而漏滚（真机复测抓到过：流式停在新块开头）。
    LaunchedEffect(real.streamVersion) {
        if (real.streamVersion > 0 && sticky) {
            repeat(3) {
                withFrameNanos { }
                if (!listState.canScrollForward) return@LaunchedEffect
                val info = listState.layoutInfo
                val last = info.visibleItemsInfo.lastOrNull { it.index == lastBlockItemIndex() }
                // 真底 = viewportEndOffset - afterContentPadding（含底部输入区 padding，见 bottomAligned）
                val delta = last?.let {
                    it.offset + it.size - (info.viewportEndOffset - info.afterContentPadding)
                } ?: 0
                if (delta > 0) listState.scrollBy(delta.toFloat())
            }
        }
    }
    LaunchedEffect(imeHeight) {
        if (imeHeight > 0 && real.blocks.isNotEmpty() && sticky) {
            snapToBottom()
            sticky = true
        }
    }

    // ===== 向上翻页（before_seq 分页 + 不跳屏补偿，2026-12）=====
    //
    // 触发：第一个可见项接近列表头（index ≤ 1 且还没到头/没在装载）→ loadEarlier()。
    // 不跳屏：更早的块**插入头部**后，原第一项的下标整体后移 N——装载回调
    // （earlierVersion 变化）里按「插入前快照的第一可见项 (index, offset)」恢复视觉
    // 位置：插入后旧内容的新下标 = 旧下标 + 插入块数，逐帧收敛补滚（插入后同帧
    // layoutInfo 还是旧的，直接量会偏——复用 snapToBottom 的收敛纪律）。
    val earlierVersion = real.earlierVersion
    // 插入前快照：第一可见项的 (下标, 偏移) 与插入前的块数（插入数 = 前后差）
    var prePagingFirstIndex by remember { mutableStateOf(0) }
    var prePagingFirstOffset by remember { mutableStateOf(0) }
    var prePagingBlockCount by remember { mutableStateOf(0) }
    var pendingScrollCompensation by remember { mutableStateOf(false) }

    // 触发装载：滚到头（index ≤ 1）且还有更早的历史 → 快照位置 → loadEarlier()
    LaunchedEffect(listState) {
        snapshotFlow { listState.firstVisibleItemIndex to listState.firstVisibleItemScrollOffset }
            .distinctUntilChanged()
            .collect { (index, offset) ->
                if (index <= 1 && real.hasMoreHistory && !real.loadingEarlier &&
                    real.oldestLoadedSeq > 0 && real.blocks.isNotEmpty()
                ) {
                    prePagingFirstIndex = index
                    prePagingFirstOffset = offset
                    prePagingBlockCount = real.blocks.size
                    pendingScrollCompensation = true
                    real.loadEarlier()
                }
            }
    }

    // 装载完成：头部插入了 N 块 → 把旧的第一可见项滚回原 offset（不跳屏）。
    LaunchedEffect(earlierVersion) {
        if (!pendingScrollCompensation) return@LaunchedEffect
        pendingScrollCompensation = false
        val inserted = real.blocks.size - prePagingBlockCount
        if (inserted <= 0) return@LaunchedEffect
        val targetIndex = prePagingFirstIndex + inserted
        repeat(8) {
            withFrameNanos { }
            val info = listState.layoutInfo
            if (info.visibleItemsInfo.isEmpty()) return@LaunchedEffect
            val target = info.visibleItemsInfo.firstOrNull { it.index == targetIndex }
            if (target != null) {
                // 目标项（旧的第一可见内容）现在的实测顶部 − 快照 offset = 补滚量
                val delta = target.offset - prePagingFirstOffset
                if (delta != 0) listState.scrollBy(delta.toFloat())
                return@LaunchedEffect
            }
            // 目标项被挤出可视区（新块很高）：按目标下标硬滚到快照 offset
            listState.scrollToItem(targetIndex, prePagingFirstOffset)
        }
    }

    Column(
        Modifier
            .fillMaxSize()
            .background(LxColors.Bg),
    ) {
        // 顶栏：返回 + 连接药丸 + 会话标题 + 确认门触发入口
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
            ConnPill(
                online = state.pillOnline(),
                name = state.pillName(),
                onClick = { state.route = Route.Connections },
            )
            Text(
                text = title,
                style = Lx.type.ListTitle,
                color = Lx.colors.Fg,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f),
            )
            // 确认门指示灯（有挂起确认时常亮；裁决走下方的 LxConfirmDialog）
            Icon(
                imageVector = Icons.Filled.WarningAmber,
                contentDescription = "确认门",
                tint = if (pendingConfirm != null) LxColors.Amber else Lx.colors.BorderStrong,
                modifier = Modifier.size(18.dp),
            )
        }

        // 中部消息流 + 底部输入区（悬浮，对齐桌面端 composer-zone 机制）：
        // 消息列表铺满，底部 contentPadding = 输入区**整个叠层**的实测高度（onSizeChanged
        // 量 scrim Column，等价桌面端 --composer-h）——输入区多高都不会压住最后一条消息。
        // 注意量测对象必须是整个 scrim Column（40dp 渐隐带 + busy 指示条 + Composer 卡），
        // 不能只量 Composer 卡：渐隐带从 24% 起就是不透明底色，只量卡会让消息底部
        // 一行被它盖掉一半（真机反馈「输入框遮住消息一半」的根因）。
        // 输入卡片上方一层白色渐隐 scrim（linear-gradient to top, white 76% → transparent）。
        var composerHeightDp by remember { mutableStateOf(0.dp) }
        val density = LocalDensity.current
        // 输入区高度变化（busy 条出现/消失、斜杠面板弹出、附件预览增减）时，
        // sticky 跟随态下补滚一次——否则 padding 变了列表停在旧「真底」，最后一条被盖。
        LaunchedEffect(composerHeightDp) {
            if (composerHeightDp > 0.dp && real.blocks.isNotEmpty() && sticky) snapToBottom()
        }
        Box(Modifier.weight(1f).fillMaxWidth()) {
            LazyColumn(
                Modifier
                    .fillMaxSize()
                    .padding(horizontal = Lx.space.s12),
                state = listState,
                contentPadding = PaddingValues(top = Lx.space.s8, bottom = composerHeightDp),
                verticalArrangement = Arrangement.spacedBy(Lx.space.s10),
            ) {
                // 头部：离线提示（断网打开会话时显示缓存 + 提示条——缓存是视图加速，
                // 远端才是事实源；重连后远端 tail 装载会覆盖缓存段）。
                if (real.showingCached) {
                    item(key = "$sessionId:offline") {
                        Text(
                            text = "离线，显示缓存内容",
                            style = Lx.type.BodySmall.copy(
                                fontSize = Lx.type.Size11_5,
                                fontWeight = FontWeight.Medium,
                            ),
                            color = LxColors.Amber,
                            modifier = Modifier.fillMaxWidth().padding(vertical = Lx.space.s2),
                            textAlign = androidx.compose.ui.text.style.TextAlign.Center,
                        )
                    }
                }
                // 头部：分页状态行（spinner / 「已加载全部历史」/ 重试按钮）。
                // key 固定 "header"——插入更早块时它恒在 index 0，blocks 的 key 用
                // 下标（"$sessionId:$i"）会随插入整体平移（LazyColumn 以 key 复用，
                // 平移 = 所有块重组一次，但视觉位置由上面的补偿滚保持不变）。
                item(key = "$sessionId:header") {
                    when {
                        real.loadingEarlier -> Row(
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
                        real.loadEarlierError != null -> Row(
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
                                    textDecoration = TextDecoration.Underline,
                                ),
                                color = Lx.colors.Fg,
                                modifier = Modifier
                                    .padding(start = Lx.space.s4)
                                    .clickable { real.loadEarlier() },
                            )
                        }
                        !real.hasMoreHistory && real.oldestLoadedSeq > 0 -> Text(
                            text = "已加载全部历史",
                            style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size11_5),
                            color = Lx.colors.FgMuted,
                            modifier = Modifier.fillMaxWidth().padding(vertical = Lx.space.s2),
                            textAlign = androidx.compose.ui.text.style.TextAlign.Center,
                        )
                    }
                }
                // 消息块逐个入场：`block-in`（opacity 0→1 + translateY(4px)→0，320ms 标准曲线），
                // 延迟按 `staggerIn`（min(40ms, 360ms/条目数)）逐项错开。
                // key 用下标：流式 delta 会整块替换（data class copy），
                // 用块实例当 key 会让入场动画/打字机每个 delta 重播——下标才是稳定身份。
                val blocks: List<ThreadBlock> = real.blocks
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
                            // 进入子会话临时视图：标题 = Agent 名 + 任务摘要（卡内已知，
                            // DispatchCard 传原始值，这里拼标题）
                            onOpenChild = { childId, agentName, task ->
                                real.openChildSession(
                                    childId,
                                    title = "$agentName · ${task.take(24)}",
                                )
                            },
                        )
                    }
                }
                // 「生成中」打字指示（三点跳动）：流式期间（busy 且最后一块正在流式追加）
                // 显示在最后一个块下方——真机验证时确认「流式有内容追加但没有生成中标识」，
                // 这里补上与桌面端一致的 typing 点。历史回放/空闲不显示。
                if (running && real.streamVersion > 0) {
                    item(key = "$sessionId:typing") {
                        TypingIndicator(
                            modifier = Modifier.padding(
                                start = Lx.space.s4,
                                top = Lx.space.s2,
                            ),
                        )
                    }
                }
            }

            // 底部输入区：白色渐隐 scrim + 白卡（scrim 渐变盖住整个输入区高度，含卡片下缘空隙）。
            // onSizeChanged 量在**整个 scrim Column** 上（含 40dp 渐隐带 + busy 指示条 +
            // Composer 卡 + 卡下 14dp 空隙）——contentPadding 与 snapToBottom 的「真底」
            // 判定共用这一个数，输入区多高消息都不会被盖。
            Column(
                Modifier
                    .align(Alignment.BottomCenter)
                    .fillMaxWidth()
                    .onSizeChanged { composerHeightDp = with(density) { it.height.toDp() } }
                    .background(
                        Brush.verticalGradient(
                            colorStops = arrayOf(
                                0f to androidx.compose.ui.graphics.Color.Transparent,
                                0.24f to LxColors.Bg,
                                1f to LxColors.Bg,
                            ),
                        ),
                    ),
            ) {
                // scrim 上方先垫 40dp 渐隐带（盖住滚上来的消息）
                Spacer(Modifier.height(40.dp))
                // 「正在处理…」指示条：会话忙但无流式文本（工具执行中/等待模型首字）
                // 时显示在输入区上方——对齐桌面端 busy 指示（生成中时刻总有一个可见的
                // 「在干活」信号；流式文本自身的光标/打字点覆盖另一段）。
                if (running && real.streamVersion == 0) {
                    Row(
                        Modifier
                            .padding(horizontal = Lx.space.s12, vertical = Lx.space.s4)
                            .clip(Lx.radius.PillShape)
                            .background(Lx.colors.BorderSoft)
                            .padding(horizontal = Lx.space.s8, vertical = Lx.space.s4),
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.spacedBy(Lx.space.s6),
                    ) {
                        TypingDots(dotSize = 5.dp, gap = 3.dp)
                        Text(
                            text = "正在处理…",
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
                    Composer(state, real = real)
                }
            }
        }
    }

    // 确认门（真实模式）：内容来自后端 chat.confirmRequest（标题 = prompt，
    // 命令块 = arguments 里的 command/cwd）；批准/拒绝 → tool.confirm 裁决。
    if (pendingConfirm != null) {
        val c = pendingConfirm
        LxConfirmDialog(
            title = "执行此命令？",
            text = c.prompt.ifBlank { "${c.name} 工具申请执行，请确认。" },
            onApprove = { state.real.resolveConfirm(allow = true) },
            onDeny = { state.real.resolveConfirm(allow = false) },
            body = {
                TerminalBlock(
                    command = com.moyunteng.lxcode.remote.net.RealBackend.argsCommand(c.arguments)
                        .ifBlank { c.arguments },
                    cwd = com.moyunteng.lxcode.remote.net.RealBackend.argsCwd(c.arguments)
                        .ifBlank { null },
                )
            },
        )
    }
}

/** 单个块的分发（对齐 Block.tsx 的 switch (block.kind)）。
 *  [stableKey] 会话 id + 块下标：打字机的稳定身份——delta 整块替换（data class
 *  copy）时 key 不变，reveal 状态保持继续浇而不是重播。 */
@Composable
private fun ThreadBlockView(
    state: MockAppState,
    block: ThreadBlock,
    busy: Boolean,
    stableKey: String,
    onOpenChild: (String, String, String) -> Unit = { _, _, _ -> },
) {
    when (block) {
        is ThreadBlock.User -> UserBubble(state, block.text, block.attachments)
        is ThreadBlock.Assistant -> AssistantBlock(block, busy, stableKey)
        is ThreadBlock.Tool -> ToolCard(block)
        is ThreadBlock.Dispatch -> DispatchCard(block, onOpenChild)
        is ThreadBlock.Notice -> NoticeBar(block)
        is ThreadBlock.Compaction -> CompactionCard(block)
        is ThreadBlock.Todo -> TodoCard(block.items)
        is ThreadBlock.Error -> ErrorBlock(block.message)
    }
}

/**
 * 系统提示条（后台任务通告 / 重复调用提醒）：一行浅底提示，不是用户气泡。
 *
 * 对齐桌面端 NoticeBar.tsx（.job-notice：图标 + 标签 + 正文）——通告是 user 角色
 * 消息但不是用户说的话，按前缀识别后在这里以提示条形态出现。
 */
@Composable
private fun NoticeBar(block: ThreadBlock.Notice) {
    Row(
        Modifier
            .fillMaxWidth()
            .clip(Lx.radius.RowShape)
            .background(Lx.colors.BorderSoft)
            .padding(horizontal = Lx.space.s10, vertical = Lx.space.s8),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(Lx.space.s8),
    ) {
        SmallIcon(Icons.Filled.Info, Lx.colors.FgMuted, size = 14)
        Text(
            text = block.label,
            style = Lx.type.Pill.copy(fontSize = Lx.type.Size10_5, fontWeight = FontWeight.SemiBold),
            color = Lx.colors.FgMuted,
        )
        Text(
            text = block.text,
            style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size11_5),
            color = Lx.colors.FgMuted,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f),
        )
    }
}

/**
 * 用户气泡（底 #f1f1f3、右对齐、圆角 22、最大宽 70%）。
 *
 * 动效：回弹入场 —— 桌面端 `back.out(1.8)`（低阻尼 spring，等价 [LxEnterSpec.BubbleIn]）。
 */
@Composable
private fun UserBubble(
    state: MockAppState,
    text: String,
    attachments: List<com.moyunteng.lxcode.remote.model.AttachmentMeta> = emptyList(),
) {
    // 灯箱（全屏图片查看）状态：集合 = 本条消息的全部图片、下标按点击处对齐
    var lightbox by remember { mutableStateOf<Pair<List<String>, Int>?>(null) }
    val real = state.real
    Column(
        Modifier.fillMaxWidth(),
        horizontalAlignment = Alignment.End,
    ) {
        // 附件区在气泡上方、气泡外（对齐桌面端 .user-atts 语义：图片 gallery +
        // 文件卡与文字气泡视觉分离）；文本非空才画气泡（纯图片消息不画空泡）
        if (attachments.isNotEmpty()) {
            UserAttachmentSection(
                atts = attachments,
                urlOf = { real.attachmentURL(it) },
                onOpenImage = { urls, i -> lightbox = urls to i },
            )
        }
        if (text.isNotEmpty()) {
            Row(
                Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.End,
            ) {
                Box(
                    Modifier
                        .lxEnter(spec = LxEnterSpec.BubbleIn, key = text)
                        .fillMaxWidth(0.78f)
                        .clip(RoundedCornerShape(22.dp))
                        .background(LxColors.BubbleUser)
                        .padding(horizontal = Lx.space.s16, vertical = Lx.space.s10),
                ) {
                    Text(
                        text = text,
                        style = Lx.type.Body.copy(lineHeight = (Lx.type.Size14.value * 1.6f).sp),
                        color = Lx.colors.Fg,
                    )
                }
            }
        }
    }
    lightbox?.let { (urls, i) ->
        ImageLightbox(urls = urls, initialIndex = i, onDismiss = { lightbox = null })
    }
}

/** 助手块（思考块 + 正文 + 轮末统计行）。 */
@Composable
private fun AssistantBlock(block: ThreadBlock.Assistant, busy: Boolean, stableKey: String) {
    // 助手正文逐字揭示：桌面端 stream-reveal（REVEAL_* 常量），**不受 reduced-motion 门控**。
    // 打字机只用于**实时流式增量**（block.streaming = true）：历史回放的 assistant 块
    // 直接整块呈现全文（对齐桌面端 AnswerBody 的 committed/tail 语义——历史 = snap
    // 全量落下，只有实时 delta 才 reveal），否则打开会话时所有旧消息重播一遍打字。
    // key 用**稳定身份**（会话 id + 块下标）：流式 delta 每次整块替换（data class copy），
    // key 用块对象会让 remember(key) 每 delta 重置 revealed → 每条 delta 从头重播打字。
    // key 稳定时 text 增长只推进循环（revealed 续浇），块定格（streaming=false）整块呈现。
    val reveal = rememberStreamReveal(block.content, enabled = block.streaming, key = stableKey)

    Column(verticalArrangement = Arrangement.spacedBy(Lx.space.s6)) {
        if (!block.reasoning.isNullOrBlank()) {
            ReasoningRow(text = block.reasoning, elapsedMs = block.reasoningMs, shimmer = busy)
        }
        if (block.content.isNotEmpty()) {
            Row(verticalAlignment = Alignment.Bottom) {
                // Markdown 渲染（自写子集，见 md/MarkdownParser.kt）：解析在**已显示
                // 文本**（reveal.text）上进行——打字机揭示到哪，markdown 就渲染到哪，
                // 两者天然对齐；流式期间每帧重解析整块的已显示前缀（纯字符串扫描，
                // 微秒级，实测瓶颈在 layout 不在解析）。历史块（streaming=false）
                // reveal.text = 全文，同样路径一次渲染。
                MarkdownBody(
                    text = reveal.text,
                    modifier = Modifier.weight(1f),
                )
                if (reveal.revealing) {
                    // 光标：caret-blink 1s steps(1)（硬切）
                    Box(
                        Modifier
                            .padding(start = 1.dp, bottom = 3.dp)
                            .size(width = 2.dp, height = 14.dp)
                            .lxCaret()
                            .background(Lx.colors.Fg),
                    )
                }
            }
        }
        if (!block.content.isNullOrBlank()) {
            // 署名行：真实模式用实测值（first_token_ms / usage_tokens÷duration_ms），
            // 未知段省略；mock 数据没填这两个字段 → 走旧硬编码（回归保障）。
            val footer = if (block.firstTokenMs != null || block.tokPerSec != null) {
                listOfNotNull(
                    block.model.takeIf { it.isNotBlank() },
                    block.firstTokenMs?.let { "首字 ${formatMs(it)}" },
                    block.tokPerSec?.let { String.format("%.1f tok/s", it) },
                ).joinToString(" · ")
            } else {
                "${block.model} · 首字 812ms · 13.7 tok/s"
            }
            Text(
                text = footer,
                style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size10_5),
                color = Lx.colors.FgFaint,
            )
        }
    }
}

/** 思考/推理块（折叠态一行「思考」+ 耗时；展开显示推理文本）。 */
@Composable
private fun ReasoningRow(text: String, elapsedMs: Long, shimmer: Boolean) {
    var open by remember { mutableStateOf(false) }
    val interaction = remember { MutableInteractionSource() }

    Column(Modifier.fillMaxWidth()) {
        Row(
            Modifier
                .fillMaxWidth()
                .lxPressScale(interaction)
                .clip(RoundedCornerShape(Lx.radius.r8))
                .clickable(
                    interactionSource = interaction,
                    indication = null,
                ) { open = !open }
                .padding(vertical = Lx.space.s4),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(Lx.space.s6),
        ) {
            SmallIcon(Icons.Filled.CheckCircle, Lx.colors.FgFaint, size = 14)
            if (shimmer) {
                // 思考微光：think-shimmer 2.25s（渐变扫过文字，background-size 300%）——「运行中」才扫
                LxShimmerText(
                    text = "思考",
                    style = Lx.type.BodySmall.copy(
                        fontSize = Lx.type.Size12_5,
                        fontWeight = FontWeight.Medium,
                    ),
                )
            } else {
                Text(
                    text = "思考",
                    style = Lx.type.BodySmall.copy(
                        fontSize = Lx.type.Size12_5,
                        fontWeight = FontWeight.Medium,
                    ),
                    color = Lx.colors.FgMuted,
                )
            }
            Text(
                text = "思考了 ${formatMs(elapsedMs)}",
                style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size11),
                color = Lx.colors.FgFaint,
            )
            Icon(
                imageVector = if (open) Icons.Filled.KeyboardArrowUp else Icons.Filled.KeyboardArrowDown,
                contentDescription = if (open) "收起思考" else "展开思考",
                tint = Lx.colors.FgFaint,
                modifier = Modifier.size(12.dp),
            )
        }
        // 展开 / 折叠：高度 + 淡入淡出，220ms 标准曲线（不是瞬现）
        LxExpandable(visible = open) {
            Box(
                Modifier
                    .fillMaxWidth()
                    .padding(start = Lx.space.s16, top = Lx.space.s4),
            ) {
                Text(
                    text = text,
                    style = Lx.type.BodySmall.copy(
                        fontSize = Lx.type.Size12_5,
                        lineHeight = (Lx.type.Size12_5.value * 1.6f).sp,
                    ),
                    color = Lx.colors.FgMuted,
                )
            }
        }
    }
}

/** 工具调用卡（工具名 + 参数摘要 + 输出块 + 耗时 + 状态点）。 */
@Composable
private fun ToolCard(block: ThreadBlock.Tool) {
    val status = when {
        block.running -> LxStatus.Run
        block.isError -> LxStatus.Danger
        else -> LxStatus.Success
    }
    val statusText = when {
        block.running -> "执行中…"
        block.isError -> "失败"
        else -> "成功"
    }

    Column(verticalArrangement = Arrangement.spacedBy(Lx.space.s6)) {
        Row(
            Modifier
                .fillMaxWidth()
                .clip(RoundedCornerShape(Lx.radius.r8))
                // 「运行中」扫光：tool-sweep —— 300dp 宽渐变带从左扫到右，2.6s ease-out infinite
                .lxSweep(enabled = block.running),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(Lx.space.s8),
        ) {
            LxStatusDot(status = status, size = 8.dp)
            Text(
                text = block.title,
                style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size12_5, fontWeight = FontWeight.Medium),
                color = Lx.colors.Fg,
            )
            Text(
                text = block.argsSummary,
                style = Lx.type.Mono12.copy(fontSize = Lx.type.Size11, color = Lx.colors.FgFaint),
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f),
            )
            Text(
                text = statusText,
                style = Lx.type.Pill.copy(fontSize = Lx.type.Size10, fontWeight = FontWeight.SemiBold),
                color = when (status) {
                    LxStatus.Run -> LxColors.Amber
                    LxStatus.Danger -> Lx.colors.Danger
                    else -> LxColors.SuccessInk
                },
            )
            if (!block.running) {
                Text(
                    text = formatMs(block.durationMs),
                    style = Lx.type.Mono12.copy(fontSize = Lx.type.Size10, color = Lx.colors.FgFaint),
                )
            }
        }

        val output = block.output
        when {
            output != null && block.isError -> FoldableOutput(output, danger = true)
            output != null -> FoldableOutput(output, danger = false)
            else -> SmallEmpty("等待工具输出…")
        }
    }
}

/**
 * 工具输出折叠兜底：超过 8 行或 600 字符默认只显首 2 行 + 「展开」开关。
 *
 * 4_000 字符的截断上限（RealBackend.toolResult.take）意味着最坏情形仍是一大段
 * 等宽文本铺满屏幕——折叠是读侧兜底，写侧截断照旧。错误输出同样适用（首 2 行
 * 已足够判断「失败了」，全文点开看）。
 */
@Composable
private fun FoldableOutput(text: String, danger: Boolean) {
    val lines = text.split("\n")
    val foldable = lines.size > 8 || text.length > 600
    var open by remember(text) { mutableStateOf(false) }
    Column(verticalArrangement = Arrangement.spacedBy(Lx.space.s4)) {
        val shown = if (foldable && !open) lines.take(2).joinToString("\n") + "\n…" else text
        if (danger) {
            Box(
                Modifier
                    .fillMaxWidth()
                    .clip(RoundedCornerShape(Lx.radius.r8))
                    .background(LxColors.DangerSoft)
                    .padding(Lx.space.s8),
            ) {
                Text(
                    text = shown,
                    style = Lx.type.Mono12.copy(fontSize = Lx.type.Size11),
                    color = Lx.colors.Danger,
                )
            }
        } else {
            CodeBlock(shown)
        }
        if (foldable) {
            Text(
                text = if (open) "收起" else "展开",
                style = Lx.type.Pill.copy(fontSize = Lx.type.Size10_5, fontWeight = FontWeight.SemiBold),
                color = Lx.colors.FgMuted,
                modifier = Modifier.clickable(
                    interactionSource = remember { MutableInteractionSource() },
                    indication = null,
                ) { open = !open },
            )
        }
    }
}

/**
 * 子 Agent 派发卡：一行摘要（状态点 + 已派发 → 名字 + 结论摘要 + 子会话 id），
 * 点按展开**子工具实时流**——子会话的 chat.toolCall/toolResult（带 dispatch_id）
 * 归并进卡，实时看到子工具名/状态/耗时（桌面端「子会话标签页实时流」的安卓单页等价物）。
 */
@Composable
private fun DispatchCard(
    block: ThreadBlock.Dispatch,
    onOpenChild: (String, String, String) -> Unit,
) {
    // 展开态按 dispatchId 记忆：同一张卡流式更新（copy 重建）不重置展开
    var open by remember(block.dispatchId) { mutableStateOf(false) }
    Column(
        Modifier
            .fillMaxWidth()
            .clip(Lx.radius.CardShape)
            .background(LxColors.Surface)
            .border(
                width = 1.dp,
                color = if (block.isError) Lx.colors.Danger.copy(alpha = 0.35f)
                else Lx.colors.Success.copy(alpha = 0.3f),
                shape = Lx.radius.CardShape,
            )
            .clickable(
                interactionSource = remember { MutableInteractionSource() },
                indication = null,
            ) { open = !open }
            .padding(horizontal = Lx.space.s12, vertical = Lx.space.s10),
        verticalArrangement = Arrangement.spacedBy(Lx.space.s8),
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(Lx.space.s8),
        ) {
            LxStatusDot(
                status = if (block.isError) LxStatus.Danger else if (block.done) LxStatus.Success else LxStatus.Run,
                size = 8.dp,
            )
            Text(
                text = "已派发 → ${block.agentName}",
                style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size12_5, fontWeight = FontWeight.SemiBold),
                color = Lx.colors.Fg,
            )
            Text(
                text = if (!block.done && block.childRuns.isNotEmpty()) {
                    // 子会话实时态：运行中的子工具数（实时流行的计数）
                    "子会话执行中 · ${block.childRuns.count { it.running }} 个工具运行中"
                } else if (!block.done && block.childTools > 0) {
                    "子会话执行中 · ${block.childTools} 个工具调用"
                } else {
                    block.conclusion
                },
                style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size11_5),
                color = Lx.colors.FgFaint,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f),
            )
            // 子 Agent 清单进度（todo.updated 带 dispatch_id 的广播归并；-1 = 没有清单）：
            // 手机上看派发进度的轻量形态——不必展开卡就知道子 Agent 跑到哪一步
            if (block.childTodosDone >= 0 && block.childTodosTotal > 0) {
                Text(
                    text = "清单 ${block.childTodosDone}/${block.childTodosTotal}",
                    style = Lx.type.Pill.copy(fontSize = Lx.type.Size10, fontWeight = FontWeight.SemiBold),
                    color = if (block.childTodosDone == block.childTodosTotal) LxColors.SuccessInk else LxColors.Amber,
                )
            }
            Text(
                text = block.sessionId,
                style = Lx.type.Mono12.copy(fontSize = Lx.type.Size10, color = LxColors.SuccessInk),
            )
            Icon(
                imageVector = Icons.Filled.KeyboardArrowDown,
                contentDescription = if (open) "收起子工具" else "展开子工具",
                tint = LxColors.SuccessInk,
                modifier = Modifier
                    .size(14.dp)
                    .graphicsLayer {
                        rotationZ = if (open) 180f else 0f
                    },
            )
        }
        if (open) {
            if (block.childRuns.isEmpty()) {
                Text(
                    text = "子会话还没有工具调用",
                    style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size11),
                    color = Lx.colors.FgFaint,
                )
            } else {
                Column(verticalArrangement = Arrangement.spacedBy(Lx.space.s4)) {
                    block.childRuns.forEach { run -> ChildToolRow(run) }
                }
            }
        }
        // 「查看 ›」进入子会话（临时视图；sessionId 空 = 历史重建没拿到子会话 id，
        // 不显示入口——不给一个点了必失败的按钮）
        if (block.sessionId.isNotEmpty()) {
            Row(
                Modifier
                    .fillMaxWidth()
                    .clip(RoundedCornerShape(Lx.radius.r6))
                    .clickable(
                        interactionSource = remember { MutableInteractionSource() },
                        indication = null,
                    ) { onOpenChild(block.sessionId, block.agentName, block.task) }
                    .padding(horizontal = Lx.space.s4, vertical = Lx.space.s4),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(Lx.space.s4),
            ) {
                Text(
                    text = "查看子会话",
                    style = Lx.type.Pill.copy(fontSize = Lx.type.Size11, fontWeight = FontWeight.SemiBold),
                    color = LxColors.SuccessInk,
                )
                Icon(
                    imageVector = Icons.Filled.KeyboardArrowRight,
                    contentDescription = "进入子会话",
                    tint = LxColors.SuccessInk,
                    modifier = Modifier.size(12.dp),
                )
            }
        }
    }
}

/** 派发卡内的一行子工具实时态（状态点 + 工具名 + 参数摘要 + 耗时/运行中）。 */
@Composable
private fun ChildToolRow(run: com.moyunteng.lxcode.remote.model.ChildTool) {
    Row(
        Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(Lx.radius.r6))
            .background(Lx.colors.BorderSoft)
            .padding(horizontal = Lx.space.s8, vertical = Lx.space.s4),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(Lx.space.s6),
    ) {
        LxStatusDot(
            status = when {
                run.running -> LxStatus.Run
                run.isError -> LxStatus.Danger
                else -> LxStatus.Success
            },
            size = 6.dp,
        )
        Text(
            text = run.title,
            style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size11, fontWeight = FontWeight.Medium),
            color = Lx.colors.Fg,
        )
        Text(
            text = run.argsSummary,
            style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size10_5),
            color = Lx.colors.FgFaint,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f),
        )
        Text(
            text = when {
                run.running -> "运行中"
                run.durationMs > 0 -> formatMs(run.durationMs)
                else -> ""
            },
            style = TextStyle(fontFamily = Lx.type.Mono, fontSize = 10.sp, color = Lx.colors.FgFaint),
        )
    }
}

/** 压缩检查点块（「已压缩历史」标签 + 摘要正文）。 */
@Composable
private fun CompactionCard(block: ThreadBlock.Compaction) {
    Column(
        Modifier
            .fillMaxWidth()
            .clip(Lx.radius.CardShape)
            .background(Lx.colors.BorderSoft)
            .padding(Lx.space.s10),
        verticalArrangement = Arrangement.spacedBy(Lx.space.s6),
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(Lx.space.s6),
        ) {
            Text(
                text = if (block.manual) "已压缩历史（手动）" else "已压缩历史",
                style = Lx.type.Pill.copy(fontSize = Lx.type.Size11, fontWeight = FontWeight.SemiBold),
                color = Lx.colors.FgMuted,
            )
            Text(
                text = "${block.shadowed} 条 · ${block.before} → ${block.after}",
                style = Lx.type.Mono12.copy(fontSize = Lx.type.Size10, color = Lx.colors.FgFaint),
            )
        }
        Text(
            text = block.summary,
            style = Lx.type.BodySmall.copy(
                fontSize = Lx.type.Size12,
                lineHeight = (Lx.type.Size12.value * 1.6f).sp,
            ),
            color = Lx.colors.Fg,
        )
    }
}

/**
 * todo 清单块（对齐桌面端 aicss/TodoList.tsx + TodoList.module.css）：
 *
 *  - 头部一行可点（折叠/展开）：状态图标（全 done = 实心绿勾 / 有 active = 进度环
 *    （conic 进度扇 + 虚线外环，determinate = done/total）/ 其余 = 清单图标）+
 *    「任务清单」标题 + 右侧三段计数「✓N · ▶M · ○K」（done 段绿、active 段强调、
 *    pending 段 muted；三段恒在——为 0 的段也显示，一眼看到「还有什么没做」）；
 *    全 done 时整头显示「完成 N 项」（绿）。
 *  - 自动展开/收起（todo 契约批②）：有 active 项自动展开一次；全 done 自动收起；
 *    折叠态用户可随时手动覆盖。
 *  - 条目行：状态图标（done 绿勾 / active 箭头 / pending 虚线圆）+ 内容文字
 *    （done = muted + 删除线）。
 */
@Composable
private fun TodoCard(items: List<com.moyunteng.lxcode.remote.model.TodoItem>) {
    val done = items.count { it.status == "done" }
    val active = items.count { it.status == "active" }
    val pending = items.size - done - active
    val running = active > 0
    val allDone = items.isNotEmpty() && done == items.size
    val pct = if (items.isEmpty()) 0f else done.toFloat() / items.size
    // 自动展开/收起（对齐桌面端 useEffect([running, allDone])）
    var collapsed by remember { mutableStateOf(allDone) }
    LaunchedEffect(running, allDone) {
        if (running) collapsed = false
        else if (allDone) collapsed = true
    }

    Column(
        Modifier
            .fillMaxWidth()
            .clip(Lx.radius.CardShape)
            .background(LxColors.Surface)
            .border(1.dp, Lx.colors.Border, Lx.radius.CardShape)
            .padding(horizontal = Lx.space.s12, vertical = Lx.space.s6),
    ) {
        // 头部（可点折叠）
        Row(
            Modifier
                .fillMaxWidth()
                .clickable(
                    interactionSource = remember { MutableInteractionSource() },
                    indication = null,
                ) { collapsed = !collapsed }
                .padding(vertical = Lx.space.s6),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(Lx.space.s8),
        ) {
            // 状态图标位：16dp 槽位里三态切换
            Box(Modifier.size(16.dp), contentAlignment = Alignment.Center) {
                when {
                    allDone -> Icon(
                        imageVector = Icons.Filled.CheckCircle,
                        contentDescription = null,
                        tint = LxColors.SuccessInk,
                        modifier = Modifier.size(15.dp),
                    )
                    running -> TodoProgressRing(progress = pct, size = 13.dp)
                    else -> SmallIcon(Icons.Filled.Checklist, Lx.colors.FgFaint, size = 13)
                }
            }
            Text(
                text = "任务清单",
                style = Lx.type.BodySmall.copy(
                    fontSize = Lx.type.Size13,
                    fontWeight = FontWeight.Medium,
                ),
                color = Lx.colors.Fg,
            )
            Spacer(Modifier.weight(1f))
            if (allDone) {
                Text(
                    text = "完成 ${items.size} 项",
                    style = Lx.type.BodySmall.copy(
                        fontSize = Lx.type.Size12,
                        fontWeight = FontWeight.Medium,
                    ),
                    color = LxColors.SuccessInk,
                )
            } else {
                // 三段计数：✓ N · ▶ M · ○ K（段间点分隔；为 0 的段仍显示）
                TodoCountSegment("✓", done.toString(), LxColors.SuccessInk)
                Text(text = "·", style = Lx.type.BodySmall, color = Lx.colors.FgFaint)
                TodoCountSegment("▶", active.toString(), Lx.colors.Fg)
                Text(text = "·", style = Lx.type.BodySmall, color = Lx.colors.FgFaint)
                TodoCountSegment("○", pending.toString(), Lx.colors.FgFaint)
            }
            Icon(
                imageVector = if (collapsed) Icons.Filled.KeyboardArrowDown else Icons.Filled.KeyboardArrowUp,
                contentDescription = if (collapsed) "展开任务清单" else "折叠任务清单",
                tint = Lx.colors.FgFaint,
                modifier = Modifier.size(12.dp),
            )
        }
        // 条目列表（折叠：高度 + 淡入淡出）
        LxExpandable(visible = !collapsed) {
            Column(
                Modifier
                    .fillMaxWidth()
                    .padding(top = Lx.space.s4, bottom = Lx.space.s6),
                verticalArrangement = Arrangement.spacedBy(Lx.space.s8),
            ) {
                items.forEachIndexed { i, item ->
                    val isDone = item.status == "done"
                    val isActive = item.status == "active"
                    Row(
                        Modifier
                            .fillMaxWidth()
                            // todo 条目入场：translateY(-7dp) + 淡入，360ms 标准曲线，每项延迟 i*50ms
                            .lxEnter(spec = LxEnterSpec.TodoItemIn, delayMillis = i * 50),
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.spacedBy(9.dp),
                    ) {
                        // 状态图标：done 绿勾 / active 箭头（强调）/ pending 虚线圆
                        when {
                            isDone -> Icon(
                                imageVector = Icons.Filled.CheckCircle,
                                contentDescription = null,
                                tint = LxColors.SuccessInk,
                                modifier = Modifier.size(15.dp),
                            )
                            isActive -> SmallIcon(Icons.Filled.KeyboardArrowRight, Lx.colors.Fg, size = 15)
                            else -> Box(
                                Modifier
                                    .size(13.dp)
                                    .border(1.2.dp, Lx.colors.FgFaint, CircleShape),
                            )
                        }
                        Text(
                            text = item.content,
                            style = Lx.type.BodySmall.copy(
                                fontSize = Lx.type.Size12_5,
                                textDecoration = if (isDone) TextDecoration.LineThrough else null,
                            ),
                            color = when {
                                isDone -> Lx.colors.FgFaint
                                isActive -> Lx.colors.Fg
                                else -> Lx.colors.FgMuted
                            },
                            modifier = Modifier.weight(1f),
                        )
                    }
                }
            }
        }
    }
}

/** 三段计数的一段（图形字符 + 数字；对齐桌面端 CountSegment 的着色语义）。 */
@Composable
private fun TodoCountSegment(glyph: String, value: String, color: androidx.compose.ui.graphics.Color) {
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(2.dp)) {
        Text(
            text = glyph,
            style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size11_5, fontWeight = FontWeight.SemiBold),
            color = color,
        )
        Text(
            text = value,
            style = Lx.type.Badge.copy(fontSize = Lx.type.Size11_5),
            color = color,
        )
    }
}

/**
 * 头部进度环（桌面端 .todoHeadPie：conic 进度扇 + 虚线外环，13dp）。
 * 进度 = done/total（determinate fill）；进度弧走 400ms 过渡（--todo-pie transition）。
 */
@Composable
private fun TodoProgressRing(progress: Float, size: androidx.compose.ui.unit.Dp) {
    val animated by lxAnimateFloat(target = progress)
    // token 读取在 composable 上下文（draw lambda 里读不了 CompositionLocal）
    val track = Lx.colors.FgFaint
    val pie = Lx.colors.Fg
    Canvas(Modifier.size(size)) {
        val ringPx = 1.6.dp.toPx()
        val r = this.size.minDimension / 2 - ringPx / 2
        // 虚线外环（对齐 pending 条目的虚线圆语义——.todoHeadPieRing）
        drawCircle(
            color = track,
            radius = r,
            style = Stroke(width = ringPx, pathEffect = PathEffect.dashPathEffect(floatArrayOf(3f, 4f))),
        )
        // 进度扇：从 12 点方向顺时针（conic-gradient 同语义），扇心留虚线环可见的空腔
        val pieR = r - ringPx
        if (pieR > 0) {
            drawArc(
                color = pie,
                startAngle = -90f,
                sweepAngle = 360f * animated,
                useCenter = true,
                topLeft = Offset(this.size.width / 2 - pieR, this.size.height / 2 - pieR),
                size = Size(pieR * 2, pieR * 2),
            )
        }
    }
}

/** 错误块（--danger 色 + 错误文案）。 */
@Composable
private fun ErrorBlock(message: String) {
    Row(
        Modifier
            .fillMaxWidth()
            .clip(Lx.radius.RowShape)
            .background(LxColors.DangerSoft)
            .padding(horizontal = Lx.space.s10, vertical = Lx.space.s8),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(Lx.space.s8),
    ) {
        SmallIcon(Icons.Filled.ErrorOutline, Lx.colors.Danger, size = 14)
        Text(
            text = message,
            style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size12),
            color = Lx.colors.Danger,
            modifier = Modifier.weight(1f),
        )
    }
}

// ===== Markdown 渲染（自写子集 → Compose，见 md/MarkdownParser.kt）=====

/** Markdown 正文样式（行高 1.71 那档，原 AssistantBlock 正文的基线）。 */
@Composable
private fun mdBodyStyle(): TextStyle = Lx.type.Body.copy(
    fontSize = Lx.type.Size14,
    lineHeight = (Lx.type.Size14.value * 1.71f).sp,
)

/**
 * Markdown 块渲染：把 [parseMarkdown] 的块列表映射成 Compose 组件。
 *
 * 视觉对齐 design token：代码块底 = --code-bg（LxColors.CodeBg，Common.CodeBlock 同源）、
 * 行内 code 底 = --border-soft 派生、引用 = --border-soft 底、标题按级别加粗缩放。
 * 链接（行内 [text](url) 与裸 URL）走 LinkAnnotation.Url——点击 ACTION_VIEW，
 * 无浏览器时系统弹「没有应用」（与桌面端容错一致，不额外拦截）。
 *
 * 与打字机协同：[text] 是**已显示文本**（reveal.text），流式期间每帧重解析
 * （remember(text)——文本没变不重算；delta 驱动的帧率下是纯字符串扫描，开销微秒级）。
 */
@Composable
private fun MarkdownBody(text: String, modifier: Modifier = Modifier) {
    val blocks = remember(text) { parseMarkdown(text) }
    val body = mdBodyStyle()
    Column(modifier = modifier, verticalArrangement = Arrangement.spacedBy(Lx.space.s6)) {
        blocks.forEach { block ->
            when (block) {
                is MdBlock.Paragraph -> MdText(
                    block.spans,
                    style = body,
                    color = Lx.colors.Fg,
                    // 空行后的段落给段距（桌面端 .md-para { margin-top: 1em }——
                    // 块间呼吸感是 DSH markdown 规范「好看」的核心）
                    modifier = if (block.gap) Modifier.padding(top = Lx.space.s10) else Modifier,
                )
                is MdBlock.Heading -> MdText(
                    spans = block.spans,
                    style = body.copy(
                        fontWeight = FontWeight.Bold,
                        // 标题缩放：token 只有 9.5~14 一档（桌面端 h1/h2 同样 14~16 区间），
                        // 安卓取 14sp 加粗为最大、13sp 次级——不发明新 token
                        fontSize = if (block.level <= 2) Lx.type.Size14 else Lx.type.Size13,
                    ),
                    color = Lx.colors.Fg,
                )
                is MdBlock.CodeBlock -> Box(
                    Modifier
                        .fillMaxWidth()
                        .clip(RoundedCornerShape(Lx.radius.r8))
                        .background(LxColors.CodeBg)
                        .padding(Lx.space.s8),
                ) {
                    Text(
                        text = block.code,
                        style = Lx.type.Mono12.copy(
                            fontSize = Lx.type.Size11,
                            lineHeight = (Lx.type.Size11.value * 1.5f).sp,
                        ),
                        color = Lx.colors.FgMuted,
                        modifier = Modifier.horizontalScroll(rememberScrollState()),
                    )
                }
                is MdBlock.Hr -> Box(
                    // .md-hr：0.5px hairline、--border-strong、上下 24px margin
                    Modifier
                        .fillMaxWidth()
                        .padding(vertical = Lx.space.s10)
                        .height(0.5.dp)
                        .background(Lx.colors.BorderStrong),
                )
                is MdBlock.Listing -> Column(verticalArrangement = Arrangement.spacedBy(Lx.space.s2)) {
                    block.items.forEachIndexed { idx, item ->
                        Row(
                            // 嵌套缩进：每级 14dp（桌面端 .md-li { padding-left: level*14 }）
                            modifier = Modifier.padding(start = (item.level * 14).dp),
                            horizontalArrangement = Arrangement.spacedBy(Lx.space.s6),
                        ) {
                            Text(
                                // 真实 marker：有序 = 「N.」（块内递增），无序 = 「·」
                                text = if (block.ordered) "${idx + 1}." else "·",
                                style = Lx.type.Mono12.copy(
                                    fontSize = Lx.type.Size12_5,
                                    lineHeight = (Lx.type.Size12_5.value * 1.71f).sp,
                                ),
                                color = Lx.colors.FgMuted,
                            )
                            MdText(item.spans, style = body, color = Lx.colors.Fg, modifier = Modifier.weight(1f))
                        }
                    }
                }
                is MdBlock.Quote -> {
                    // token 读取必须在 composable 上下文（drawBehind lambda 里读不了）
                    val quoteBar = Lx.colors.FgFaint
                    Column(
                        // .md-quote：左侧 2px 边条（--fg-faint）+ 左 14px 内距 + muted 文字
                        //——不用整块底色（桌面端引用语言是边条不是色块）
                        Modifier
                            .fillMaxWidth()
                            .drawBehind {
                                drawRect(
                                    color = quoteBar,
                                    topLeft = Offset.Zero,
                                    size = Size(2.dp.toPx(), size.height),
                                )
                            }
                            .padding(start = Lx.space.s10),
                        verticalArrangement = Arrangement.spacedBy(Lx.space.s2),
                    ) {
                        block.paragraphs.forEach { p ->
                            MdText(
                                p,
                                style = Lx.type.Body.copy(
                                    fontSize = Lx.type.Size14,
                                    lineHeight = (Lx.type.Size14.value * 1.71f).sp,
                                ),
                                color = Lx.colors.FgMuted,
                            )
                        }
                    }
                }
            }
        }
    }
}

/** 一个块的 AnnotatedString 渲染（粗/斜/code 样式 + 链接点击）。 */
@Composable
private fun MdText(
    spans: List<MdSpan>,
    style: TextStyle,
    modifier: Modifier = Modifier,
    color: androidx.compose.ui.graphics.Color,
) {
    // token 读取在 composable 上下文，字符串组装是纯函数（remember 记住成品）
    val mono = Lx.type.Mono
    val codeBg = Lx.colors.BorderSoft
    val annotated = remember(spans, mono, codeBg) { spansToAnnotatedString(spans, mono, codeBg) }
    Text(text = annotated, style = style, color = color, modifier = modifier)
}

/**
 * span 列表 → AnnotatedString（纯函数；token 由调用方传入）。
 * 链接用 LinkAnnotation.Url（compose 1.7 Text 原生处理点击 + 无障碍语义）；
 * 行内 code 底色用 --border-soft（token 低饱和填充）。
 */
private fun spansToAnnotatedString(
    spans: List<MdSpan>,
    mono: androidx.compose.ui.text.font.FontFamily,
    codeBg: androidx.compose.ui.graphics.Color,
): androidx.compose.ui.text.AnnotatedString {
    val linkInk = LxColors.SuccessInk
    val builder = androidx.compose.ui.text.AnnotatedString.Builder()
    for (s in spans) {
        val start = builder.length
        builder.append(s.text)
        val end = builder.length
        if (s.link != null) {
            // LinkAnnotation.Url：Text 自动处理点按（ACTION_VIEW 由系统解析）
            builder.addLink(
                androidx.compose.ui.text.LinkAnnotation.Url(
                    s.link,
                    androidx.compose.ui.text.TextLinkStyles(
                        style = androidx.compose.ui.text.SpanStyle(
                            color = linkInk,
                            textDecoration = androidx.compose.ui.text.style.TextDecoration.Underline,
                        ),
                    ),
                ),
                start,
                end,
            )
            continue
        }
        if (!s.bold && !s.italic && !s.code && !s.strike) continue
        builder.addStyle(
            androidx.compose.ui.text.SpanStyle(
                fontWeight = if (s.bold) FontWeight.Bold else null,
                fontStyle = if (s.italic) androidx.compose.ui.text.font.FontStyle.Italic else null,
                background = if (s.code) codeBg else androidx.compose.ui.graphics.Color.Unspecified,
                fontFamily = if (s.code) mono else null,
                textDecoration = if (s.strike) androidx.compose.ui.text.style.TextDecoration.LineThrough else null,
            ),
            start,
            end,
        )
    }
    return builder.toAnnotatedString()
}

// ===== 生成中指示（三点跳动；design 动效门控复用）=====

/** 「生成中」打字指示：三个 6dp 圆点波动（跳字机节奏），motionAllowed 门控。 */
@Composable
private fun TypingIndicator(modifier: Modifier = Modifier) {
    Row(modifier = modifier, horizontalArrangement = Arrangement.spacedBy(4.dp)) {
        TypingDots(dotSize = 6.dp, gap = 4.dp)
    }
}

/**
 * 三点跳动圆点（typing dots）：每点按 150ms 相位差做透明度 + 位移波动。
 * 门控关闭（reduced-motion / 调试开关）时三点恒亮静态——不消失（空闲≠在动，
 * 但「动效关了指示也该看得见」与 lxCaret 的 reduced-motion 纪律同源）。
 */
@Composable
private fun TypingDots(dotSize: androidx.compose.ui.unit.Dp, gap: androidx.compose.ui.unit.Dp) {
    val motion = LocalLxMotionEnabled.current
    Row(horizontalArrangement = Arrangement.spacedBy(gap)) {
        repeat(3) { i ->
            val alpha: Float
            val dy: Float
            if (motion) {
                val transition = rememberInfiniteTransition(label = "lx-typing-$i")
                val a by transition.animateFloat(
                    initialValue = 0.3f,
                    targetValue = 1f,
                    animationSpec = infiniteRepeatable(
                        animation = tween(durationMillis = 450, easing = Lx.motion.Easing),
                        repeatMode = RepeatMode.Reverse,
                        // 相位差：起始延迟按 i 递增（Compose 的 infiniteRepeatable 无
                        // startDelay，用 keyframes 偏移实现——每点前 150ms 静止）
                        initialStartOffset = androidx.compose.animation.core.StartOffset(i * 150),
                    ),
                    label = "lx-typing-alpha-$i",
                )
                alpha = a
                dy = (1f - a) * 2f // 亮 = 上浮 2dp，暗 = 落回
            } else {
                alpha = 0.6f
                dy = 0f
            }
            Box(
                Modifier
                    .offset(y = dy.dp)
                    .size(dotSize)
                    .alpha(alpha)
                    .background(Lx.colors.FgMuted, CircleShape),
            )
        }
    }
}

// ===== 子会话临时视图（点派发卡进入；桌面端「临时视图」的安卓等价物）=====

/**
 * 子会话视图页：标题 = Agent 名 + 任务摘要，块流 = childBlocks（实时双投），
 * 返回回主会话（主会话状态全程未动，重开数据不丢）。
 *
 * 结构是 ThreadScreen 的精简投影：无输入区（子会话由主 Agent 驱动，用户不直接
 * 发言——对齐桌面端临时视图只读）、无统计胶囊/上下文环；确认门用同一只
 * LxConfirmDialog（confirm 带 dispatch_id 的子确认正常弹，裁决走 resolveChildConfirm）。
 */
@Composable
private fun ChildThreadView(state: MockAppState) {
    val real = state.real
    val childId = real.viewChildSessionId ?: return

    // 返回键：先回主会话（消费），不退会话页
    BackHandler { real.closeChildSession() }

    // 历史装载完成滚到底
    val listState = rememberLazyListState()
    LaunchedEffect(real.childHistoryVersion) {
        if (real.childHistoryVersion > 0 && real.childBlocks.isNotEmpty()) {
            listState.scrollToItem(real.childBlocks.size - 1)
        }
    }
    // 流式跟随（同主会话的收敛式补滚，简化版）
    LaunchedEffect(real.childStreamVersion) {
        if (real.childStreamVersion > 0 && real.childBlocks.isNotEmpty()) {
            withFrameNanos { }
            if (listState.canScrollForward) listState.scrollBy(1_000_000f)
        }
    }

    Column(
        Modifier
            .fillMaxSize()
            .background(LxColors.Bg),
    ) {
        // 顶栏：返回主会话 + 「子会话」标签 + Agent 名/任务摘要
        Row(
            Modifier
                .fillMaxWidth()
                .background(LxColors.Surface)
                .padding(horizontal = Lx.space.s6, vertical = Lx.space.s6),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(Lx.space.s6),
        ) {
            LxIconButton(onClick = { real.closeChildSession() }) {
                Icon(
                    imageVector = Icons.Filled.KeyboardArrowLeft,
                    contentDescription = "返回主会话",
                    tint = Lx.colors.FgMuted,
                    modifier = Modifier.size(20.dp),
                )
            }
            Column(Modifier.weight(1f)) {
                Text(
                    text = real.childViewTitle.ifBlank { "子会话" },
                    style = Lx.type.ListTitle,
                    color = Lx.colors.Fg,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                Text(
                    text = "子会话 · " + childId.take(8),
                    style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size10_5),
                    color = Lx.colors.FgFaint,
                )
            }
            // 忙闲点（子会话自己的）
            LxStatusDot(
                status = if (real.childBusy) LxStatus.Run else LxStatus.Idle,
                size = 8.dp,
            )
        }

        // 块流（只读；无输入区）
        LazyColumn(
            Modifier
                .fillMaxSize()
                .padding(horizontal = Lx.space.s12),
            state = listState,
            contentPadding = PaddingValues(top = Lx.space.s8, bottom = Lx.space.s16),
            verticalArrangement = Arrangement.spacedBy(Lx.space.s10),
        ) {
            val blocks: List<ThreadBlock> = real.childBlocks
            itemsIndexed(blocks, key = { i, _ -> "$childId:$i" }) { i, block ->
                Box(Modifier.lxEnter(spec = LxEnterSpec.BlockIn, delayMillis = 0, key = i)) {
                    ThreadBlockView(state, block, busy = real.childBusy, stableKey = "$childId:$i")
                }
            }
            // 生成中打字点（子会话流式期间）
            if (real.childBusy && real.childStreamVersion > 0) {
                item(key = "$childId:typing") {
                    TypingIndicator(modifier = Modifier.padding(start = Lx.space.s4, top = Lx.space.s2))
                }
            }
            if (real.childBusy && real.childStreamVersion == 0) {
                item(key = "$childId:processing") {
                    Text(
                        text = "正在处理…",
                        style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size11_5),
                        color = Lx.colors.FgFaint,
                        modifier = Modifier.padding(start = Lx.space.s4),
                    )
                }
            }
        }
    }

    // 子会话的确认请求（confirm 带 dispatch_id）正常弹
    real.childConfirm?.let { c ->
        LxConfirmDialog(
            title = "执行此命令？",
            text = c.prompt.ifBlank { "${c.name} 工具申请执行，请确认。" },
            onApprove = { real.resolveChildConfirm(allow = true) },
            onDeny = { real.resolveChildConfirm(allow = false) },
            body = {
                TerminalBlock(
                    command = com.moyunteng.lxcode.remote.net.RealBackend.argsCommand(c.arguments)
                        .ifBlank { c.arguments },
                    cwd = com.moyunteng.lxcode.remote.net.RealBackend.argsCwd(c.arguments)
                        .ifBlank { null },
                )
            },
        )
    }
}
