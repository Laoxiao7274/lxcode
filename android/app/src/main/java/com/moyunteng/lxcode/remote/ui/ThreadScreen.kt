// 对话线程页（ThreadScreen）—— 照桌面端 thread/ 的块渲染器。
//
// 顶栏（连接药丸 + 会话标题 + 确认门触发入口）、中部消息流、底部输入区固定。
// 消息流逐个渲染八类块：用户气泡 / 助手正文 / 思考块 / 工具卡 / 子 Agent 派发卡 /
// 压缩检查点 / todo 清单 / 错误块。
package com.moyunteng.lxcode.remote.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.filled.ErrorOutline
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
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
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
import com.moyunteng.lxcode.remote.mock.MockData
import com.moyunteng.lxcode.remote.mock.SessionMeta
import com.moyunteng.lxcode.remote.mock.ThreadBlock
import com.moyunteng.lxcode.remote.mock.formatMs

/** 对话线程页。 */
@Composable
fun ThreadScreen(state: MockAppState, sessionId: String) {
    // 真实模式：标题/块/忙闲/确认全部来自 RealBackend（WS 事件归约）；
    // mock 模式：一切照旧（MockData 静态数据，行为零变化）。
    val real = if (state.realOn) state.real else null
    val session = MockData.sessions.firstOrNull { it.id == sessionId }
    val title = real?.sessionTitle ?: (session?.title ?: "子会话 · frontend-dev")
    var confirmOpen by remember { mutableStateOf(false) }
    // 「运行中」的判定：真实模式 = 会话忙闲（chat.busy）；mock = 线程里还有跑着的工具行
    val running = real?.busy ?: MockData.thread.any { it is ThreadBlock.Tool && it.running }
    // 真实模式的挂起确认（chat.confirmRequest）→ 复用 LxConfirmDialog
    val pendingConfirm = real?.confirm

    // 真实模式：从会话列表点进来时拉取历史（session.resume → chat.history；
    // 新建的会话已在 newSession 里切入，currentSessionId 相同则不重复拉）。
    if (real != null) {
        LaunchedEffect(sessionId) {
            if (sessionId != real.currentSessionId) real.openSession(sessionId)
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
            // 确认门触发入口（mock：点开「执行此命令？」弹层）
            LxIconButton(onClick = { confirmOpen = true }) {
                Icon(
                    imageVector = Icons.Filled.WarningAmber,
                    contentDescription = "确认门",
                    tint = LxColors.Amber,
                    modifier = Modifier.size(18.dp),
                )
            }
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
                contentPadding = PaddingValues(top = Lx.space.s8, bottom = composerHeightDp + 14.dp),
                verticalArrangement = Arrangement.spacedBy(Lx.space.s10),
            ) {
                // 消息块逐个入场：`block-in`（opacity 0→1 + translateY(4px)→0，320ms 标准曲线），
                // 延迟按 `staggerIn`（min(40ms, 360ms/条目数)）逐项错开。
                // 真实模式 key 用下标：流式 delta 会整块替换（data class copy），
                // 用块实例当 key 会让入场动画/打字机每个 delta 重播——下标才是稳定身份。
                val blocks: List<ThreadBlock> = real?.blocks ?: MockData.thread
                itemsIndexed(blocks) { i, block ->
                    Box(
                        Modifier.lxEnter(
                            spec = LxEnterSpec.BlockIn,
                            delayMillis = LxStagger.delayMillis(blocks.size, i),
                            key = if (real != null) i else block,
                        ),
                    ) {
                        ThreadBlockView(block, busy = running)
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

    // 确认门（mock 的「执行此命令？」弹层）
    if (confirmOpen) {
        LxConfirmDialog(
            title = "执行此命令？",
            text = "bash 工具申请执行以下命令（高危：会改动工作区）。",
            onApprove = { confirmOpen = false },
            onDeny = { confirmOpen = false },
            body = {
                TerminalBlock(
                    command = "rm -rf build && ./gradlew clean assembleDebug",
                    cwd = "C:\\Users\\xzy\\Desktop\\my\\lxcode\\android",
                )
            },
        )
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

/** 单个块的分发（对齐 Block.tsx 的 switch (block.kind)）。 */
@Composable
private fun ThreadBlockView(block: ThreadBlock, busy: Boolean) {
    when (block) {
        is ThreadBlock.User -> UserBubble(block.text)
        is ThreadBlock.Assistant -> AssistantBlock(block, busy)
        is ThreadBlock.Tool -> ToolCard(block)
        is ThreadBlock.Dispatch -> DispatchCard(block)
        is ThreadBlock.Compaction -> CompactionCard(block)
        is ThreadBlock.Todo -> TodoCard(block.items)
        is ThreadBlock.Error -> ErrorBlock(block.message)
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
private fun AssistantBlock(block: ThreadBlock.Assistant, busy: Boolean) {
    // 助手正文逐字揭示：桌面端 stream-reveal（REVEAL_* 常量），**不受 reduced-motion 门控**
    val reveal = rememberStreamReveal(block.content, key = block)

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
            output != null && block.isError -> Box(
                Modifier
                    .fillMaxWidth()
                    .clip(RoundedCornerShape(Lx.radius.r8))
                    .background(LxColors.DangerSoft)
                    .padding(Lx.space.s8),
            ) {
                Text(
                    text = output,
                    style = Lx.type.Mono12.copy(fontSize = Lx.type.Size11),
                    color = Lx.colors.Danger,
                )
            }

            output != null -> CodeBlock(output)
            else -> SmallEmpty("等待工具输出…")
        }
    }
}

/** 子 Agent 派发卡（一行摘要：状态点 + 已派发 → 名字 + 结论摘要 + 子会话 id，整行可点）。 */
@Composable
private fun DispatchCard(block: ThreadBlock.Dispatch) {
    Row(
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
            ) { /* 进子会话：原型里子会话内容与主会话一致，不另开页 */ }
            .padding(horizontal = Lx.space.s12, vertical = Lx.space.s10),
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
            text = block.conclusion,
            style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size11_5),
            color = Lx.colors.FgFaint,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f),
        )
        Text(
            text = block.sessionId,
            style = Lx.type.Mono12.copy(fontSize = Lx.type.Size10, color = LxColors.SuccessInk),
        )
        Icon(
            imageVector = Icons.Filled.KeyboardArrowRight,
            contentDescription = "打开子会话",
            tint = LxColors.SuccessInk,
            modifier = Modifier.size(14.dp),
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
private fun TodoCard(items: List<com.moyunteng.lxcode.remote.mock.TodoItem>) {
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
