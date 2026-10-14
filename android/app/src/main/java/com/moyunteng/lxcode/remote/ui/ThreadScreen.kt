// 对话线程页（ThreadScreen）—— 照桌面端 thread/ 的块渲染器。
//
// 顶栏（连接药丸 + 会话标题 + 确认门触发入口）、中部消息流、底部输入区固定。
// 消息流逐个渲染八类块：用户气泡 / 助手正文 / 思考块 / 工具卡 / 子 Agent 派发卡 /
// 压缩检查点 / todo 清单 / 错误块。
package com.moyunteng.lxcode.remote.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.scrollBy
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
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.CheckCircle
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
import androidx.compose.runtime.withFrameNanos
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.delay
import com.moyunteng.lxcode.design.component.LxConfirmDialog
import com.moyunteng.lxcode.design.component.LxIconButton
import com.moyunteng.lxcode.design.component.LxStatus
import com.moyunteng.lxcode.design.component.LxStatusDot
import com.moyunteng.lxcode.design.motion.LxEnterSpec
import com.moyunteng.lxcode.design.motion.LxExpandable
import com.moyunteng.lxcode.design.motion.LxShimmerText
import com.moyunteng.lxcode.design.motion.LxStagger
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

/** 对话线程页。 */
@Composable
fun ThreadScreen(state: MockAppState, sessionId: String) {
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
    fun bottomAligned(): Boolean {
        val info = listState.layoutInfo
        if (info.visibleItemsInfo.isEmpty()) return true
        val last = info.visibleItemsInfo.lastOrNull { it.index == real.blocks.size - 1 }
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
        listState.scrollToItem(count - 1)
        suspend fun converge() {
            repeat(6) {
                withFrameNanos { }
                if (!listState.canScrollForward) return
                val info = listState.layoutInfo
                val last = info.visibleItemsInfo.lastOrNull { it.index == count - 1 }
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
                val last = info.visibleItemsInfo.lastOrNull { it.index == real.blocks.size - 1 }
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
        // 消息列表铺满，底部 contentPadding = 输入区实际高度 + 14dp（onSizeChanged 实测，
        // 等价桌面端 --composer-h）——输入区多高都不会压住最后一条消息；
        // 输入卡片上方一层白色渐隐 scrim（linear-gradient to top, white 76% → transparent）。
        var composerHeightDp by remember { mutableStateOf(0.dp) }
        val density = LocalDensity.current
        Box(Modifier.weight(1f).fillMaxWidth()) {
            LazyColumn(
                Modifier
                    .fillMaxSize()
                    .padding(horizontal = Lx.space.s12),
                state = listState,
                contentPadding = PaddingValues(top = Lx.space.s8, bottom = composerHeightDp + 14.dp),
                verticalArrangement = Arrangement.spacedBy(Lx.space.s10),
            ) {
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
                        ThreadBlockView(block, busy = running, stableKey = "$sessionId:$i")
                    }
                }
            }

            // 底部输入区：白色渐隐 scrim + 白卡（scrim 渐变盖住整个输入区高度，含卡片下缘空隙）
            Column(
                Modifier
                    .align(Alignment.BottomCenter)
                    .fillMaxWidth()
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
                Box(
                    Modifier
                        .fillMaxWidth()
                        .padding(horizontal = Lx.space.s12)
                        .onSizeChanged { composerHeightDp = with(density) { it.height.toDp() } },
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
private fun ThreadBlockView(block: ThreadBlock, busy: Boolean, stableKey: String) {
    when (block) {
        is ThreadBlock.User -> UserBubble(block.text)
        is ThreadBlock.Assistant -> AssistantBlock(block, busy, stableKey)
        is ThreadBlock.Tool -> ToolCard(block)
        is ThreadBlock.Dispatch -> DispatchCard(block)
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
private fun UserBubble(text: String) {
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
                Text(
                    text = reveal.text,
                    style = Lx.type.Body.copy(
                        fontSize = Lx.type.Size14,
                        lineHeight = (Lx.type.Size14.value * 1.71f).sp, // 行高 1.71 那档
                    ),
                    color = Lx.colors.Fg,
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
private fun DispatchCard(block: ThreadBlock.Dispatch) {
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

/** todo 清单块（active / 完成 两态任务行）。 */
@Composable
private fun TodoCard(items: List<com.moyunteng.lxcode.remote.model.TodoItem>) {
    val done = items.count { it.status == "done" }
    Column(
        Modifier
            .fillMaxWidth()
            .clip(Lx.radius.CardShape)
            .background(LxColors.Surface)
            .border(1.dp, Lx.colors.Border, Lx.radius.CardShape)
            .padding(Lx.space.s10),
        verticalArrangement = Arrangement.spacedBy(Lx.space.s6),
    ) {
        Text(
            text = "任务清单 $done/${items.size}",
            style = Lx.type.Pill.copy(fontSize = Lx.type.Size11, fontWeight = FontWeight.SemiBold),
            color = Lx.colors.FgMuted,
        )
        items.forEachIndexed { i, item ->
            val isDone = item.status == "done"
            val isActive = item.status == "active"
            Row(
                Modifier
                    .fillMaxWidth()
                    // todo 条目入场：translateY(-7dp) + 淡入，360ms 标准曲线，每项延迟 i*50ms
                    .lxEnter(spec = LxEnterSpec.TodoItemIn, delayMillis = i * 50),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(Lx.space.s8),
            ) {
                LxStatusDot(
                    status = when {
                        isDone -> LxStatus.Success
                        isActive -> LxStatus.Run
                        else -> LxStatus.Idle
                    },
                    size = 7.dp,
                )
                Text(
                    text = item.content,
                    style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size12_5),
                    color = if (isDone) Lx.colors.FgFaint else Lx.colors.Fg,
                    modifier = Modifier.weight(1f),
                )
                if (isActive) {
                    Text(
                        text = "进行中",
                        style = Lx.type.Pill.copy(fontSize = Lx.type.Size10, fontWeight = FontWeight.SemiBold),
                        color = LxColors.Amber,
                    )
                } else if (isDone) {
                    Text(
                        text = "完成",
                        style = Lx.type.Pill.copy(fontSize = Lx.type.Size10, fontWeight = FontWeight.SemiBold),
                        color = LxColors.SuccessInk,
                    )
                }
            }
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
