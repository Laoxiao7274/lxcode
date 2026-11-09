// 页面共用的小件 —— 由 :design 组件组合而来（不改 :design 公开 API）。
//
// 这里的每一件都对应桌面端的一条既有视觉规则，KDoc 里标注来源，便于追溯：
//  * [ConnPill]      ← Topbar.tsx 的 .conn-pill（脉冲圆点 + 连接名 + 下拉箭头）
//  * [ScreenHeader]  ← ag-doc-head（标题 + 关闭/返回）
//  * [TerminalBlock] ← ApprovalCard.module.css 的 .cmdBlock（深色终端块）
//  * [CodeBlock]     ← thread.css 的代码/工具输出底（--code-bg #f4f5f7）
//  * [LabelSwitch]   ← 原型的开关（在线/离线、展开/折叠等）
package com.moyunteng.lxcode.remote.ui

import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ErrorOutline
import androidx.compose.material.icons.filled.KeyboardArrowDown
import androidx.compose.material.icons.filled.KeyboardArrowLeft
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.draw.drawWithContent
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.geometry.RoundRect
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.PathEffect
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.moyunteng.lxcode.design.component.LxButton
import com.moyunteng.lxcode.design.component.LxButtonSize
import com.moyunteng.lxcode.design.component.LxButtonVariant
import com.moyunteng.lxcode.design.component.LxIconButton
import com.moyunteng.lxcode.design.component.LxSpinner
import com.moyunteng.lxcode.design.motion.LxEnterSpec
import com.moyunteng.lxcode.design.motion.LocalLxMotionEnabled
import com.moyunteng.lxcode.design.motion.lxAnimateColor
import com.moyunteng.lxcode.design.motion.lxEnter
import com.moyunteng.lxcode.design.motion.lxPressScale
import com.moyunteng.lxcode.design.token.Lx
import com.moyunteng.lxcode.design.token.LxMotion

/**
 * 顶栏连接药丸（对齐 Topbar.tsx `.conn-pill`）：脉冲圆点 + 连接名 + 下拉箭头。
 *
 * 两种态：在线 = `--success` 绿点 + 2.4s 呼吸；离线 = `--fg-faint` 灰点、无动画。
 */
@Composable
fun ConnPill(online: Boolean, name: String, onClick: () -> Unit, modifier: Modifier = Modifier) {
    val motion = LocalLxMotionEnabled.current
    // 光环扩散进度（原型 `.pill .dot.pulse { animation: pulse 2s ease-in-out
    // infinite }`：点**自身恒亮**，外圈 box-shadow 0→4px→0 扩散 alpha 渐隐——
    // 2026-12 扫尾条目 28 与 LxStatusDot 统一成光环形态，不再点自身透明度呼吸）。
    val ringProgress = if (online && motion) {
        val transition = rememberInfiniteTransition(label = "conn-pill-dot")
        val p by transition.animateFloat(
            initialValue = 0f,
            targetValue = 1f,
            animationSpec = infiniteRepeatable(
                // 一轮 = 2s（RepeatMode.Restart：0→50% 扩散、50→100% 收回，
                // 与 pulse keyframes 的 0%,100%/50% 三停靠同形）
                animation = tween(durationMillis = 1000, easing = Lx.motion.Easing),
                repeatMode = RepeatMode.Restart,
            ),
            label = "conn-pill-dot-ring",
        )
        p
    } else {
        -1f // 无光环
    }
    // 状态切换时圆点颜色 160ms 过渡（不是瞬变）—— 桌面端 `transition: background .16s`
    val dotColor by lxAnimateColor(
        target = if (online) Lx.colors.Success else Lx.colors.FgFaint,
        durationMillis = LxMotion.DurationPopIn,
    )
    val interaction = remember { MutableInteractionSource() }

    Row(
        modifier = modifier
            .lxPressScale(interaction)
            .clip(Lx.radius.PillShape)
            .clickable(
                interactionSource = interaction,
                indication = null,
                onClick = onClick,
            )
            // 原型 `.pill`：1px --line2 边 + --surface 底（2026-12 规格修正——
            // 原先裸排无边框，与顶栏品牌行的分隔感不够）
            .border(1.dp, Lx.colors.BorderStrong, Lx.radius.PillShape)
            .background(Lx.colors.Surface)
            .padding(horizontal = Lx.space.s10, vertical = Lx.space.s4),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(Lx.space.s6),
    ) {
        Box(
            Modifier
                .size(6.dp)
                .drawWithContent {
                    // 光环扩散（原型 `.pill .dot.pulse` 的 box-shadow 等价物）：
                    // 半径从点缘扩到 +4dp、alpha 随扩散渐隐；点内容恒亮叠在其上
                    if (ringProgress >= 0f) {
                        val t = ringProgress
                        drawCircle(
                            color = dotColor.copy(alpha = (1f - t).coerceIn(0f, 1f) * 0.35f),
                            radius = size.minDimension / 2f + 4.dp.toPx() * t,
                        )
                    }
                    drawContent()
                }
                .background(dotColor, CircleShape),
        )
        Text(
            text = name,
            // 原型 `.pill`：font-size var(--t-sm) = 12.5px / 600
            style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size12_5, fontWeight = FontWeight.SemiBold),
            color = Lx.colors.FgMuted,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        Icon(
            imageVector = Icons.Filled.KeyboardArrowDown,
            contentDescription = null,
            tint = Lx.colors.FgFaint,
            modifier = Modifier.size(12.dp),
        )
    }
}

/**
 * 页面顶栏（对齐 ag-doc-head）：返回/关闭 + 标题 + 尾部插槽。
 *
 * @param title 标题（如「连接」「配对」）
 * @param onBack 返回回调
 * @param trailing 尾部插槽（动作按钮）
 * @param leading 前插槽（默认返回箭头；传了则替换）
 */
@Composable
fun ScreenHeader(
    title: String,
    onBack: () -> Unit,
    modifier: Modifier = Modifier,
    leading: (@Composable () -> Unit)? = null,
    trailing: (@Composable () -> Unit)? = null,
) {
    Row(
        modifier = modifier
            .fillMaxWidth()
            .background(Lx.colors.Surface)
            .padding(horizontal = Lx.space.s8, vertical = Lx.space.s6),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(Lx.space.s6),
    ) {
        if (leading != null) {
            leading()
        } else {
            LxIconButton(onClick = onBack) {
                Icon(
                    imageVector = Icons.Filled.KeyboardArrowLeft,
                    contentDescription = "返回",
                    tint = Lx.colors.FgMuted,
                    modifier = Modifier.size(20.dp),
                )
            }
        }
        Text(
            text = title,
            style = Lx.type.ListTitle.copy(fontSize = Lx.type.Size14),
            color = Lx.colors.Fg,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f),
        )
        if (trailing != null) trailing()
    }
}

/** 深色终端块（对齐 ApprovalCard.module.css `.cmdBlock`）：`$` 提示符绿 + `--term-fg` 正文。 */
@Composable
fun TerminalBlock(command: String, modifier: Modifier = Modifier, cwd: String? = null) {
    Column(
        modifier = modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(Lx.radius.r10))
            .background(Lx.colors.TermBg)
            .padding(horizontal = Lx.space.s10, vertical = Lx.space.s8),
        verticalArrangement = Arrangement.spacedBy(Lx.space.s4),
    ) {
        if (!cwd.isNullOrBlank()) {
            Text(
                text = cwd,
                style = Lx.type.Mono12.copy(
                    fontSize = Lx.type.Size11,
                    color = Lx.colors.TermDim,
                ),
            )
        }
        Row(horizontalArrangement = Arrangement.spacedBy(Lx.space.s4)) {
            Text(text = "$", style = Lx.type.Mono12, color = Lx.colors.Success)
            Text(
                text = command,
                style = Lx.type.Mono12,
                color = Lx.colors.TermFg,
            )
        }
    }
}

/** 浅色代码块（对齐 thread.css `--code-bg #f4f5f7`）：等宽、可横向滚动。
 *  只服务 Markdown 围栏块（```）——工具输出走 [ToolOut]（深色终端形态）。 */
@Composable
fun CodeBlock(text: String, modifier: Modifier = Modifier) {
    Box(
        modifier = modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(Lx.radius.r10))
            .background(Lx.colors.CodeBg)
            // 对齐 thread.css 代码围栏（.tool-result 同款）：padding 10px 14px
            .padding(horizontal = Lx.space.s12, vertical = Lx.space.s10),
    ) {
        Text(
            text = text,
            style = Lx.type.Mono12.copy(
                color = Lx.colors.FgMuted,
                fontSize = Lx.type.Size11,
            ),
            modifier = Modifier.horizontalScroll(rememberScrollState()),
        )
    }
}

/**
 * 工具输出深色终端块（原型 `.toolout`）：TermBg 底 + TermFg 等宽字 + r12 圆角。
 *
 * 原型把工具输出画成终端（`background: var(--code); color: var(--code-fg)`），
 * 与 Markdown 围栏块的浅色底（--code-bg）刻意分两套——前者是「机器吐的原文」，
 * 后者是「正文里的代码」。pad = 10/12、mono 11.5sp、行高 1.6、错误输出整块
 * 危险色染（TermBg 上 danger 提亮保证可读）。
 */
@Composable
fun ToolOut(
    text: String,
    modifier: Modifier = Modifier,
    danger: Boolean = false,
) {
    Box(
        modifier = modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(Lx.radius.r12))
            .background(Lx.colors.TermBg)
            // 原型 `.toolout`：padding 10px 12px；文本在卡内横滚（长行不折行）
            .padding(horizontal = Lx.space.s12, vertical = Lx.space.s10),
    ) {
        Text(
            text = text,
            style = Lx.type.Mono12.copy(
                fontSize = Lx.type.Size11_5,
                lineHeight = (Lx.type.Size11_5.value * 1.6f).sp,
                color = if (danger) Lx.colors.Danger else Lx.colors.TermFg,
            ),
            modifier = Modifier.horizontalScroll(rememberScrollState()),
        )
    }
}

/** 带标签的开关：左侧标签 + 右侧小滑块。 */
@Composable
fun LabelSwitch(
    label: String,
    checked: Boolean,
    onCheckedChange: (Boolean) -> Unit,
    modifier: Modifier = Modifier,
) {
    val interaction = remember { MutableInteractionSource() }
    // 轨道颜色 120ms 过渡（门控关闭时瞬变）—— 桌面端开关 `transition: background .12s`
    val track by lxAnimateColor(target = if (checked) Lx.colors.Success else Lx.colors.BorderStrong)

    Row(
        modifier = modifier
            .lxPressScale(interaction)
            .clip(Lx.radius.RowShape)
            .clickable(
                interactionSource = interaction,
                indication = null,
            ) { onCheckedChange(!checked) }
            .padding(horizontal = Lx.space.s8, vertical = Lx.space.s6),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(Lx.space.s8),
    ) {
        Text(
            text = label,
            style = Lx.type.ListSubtitle.copy(fontSize = Lx.type.Size11_5),
            color = Lx.colors.FgMuted,
        )
        Box(
            Modifier
                .size(width = 34.dp, height = 18.dp)
                .clip(Lx.radius.PillShape)
                .background(track)
                .padding(2.dp),
            contentAlignment = if (checked) Alignment.CenterEnd else Alignment.CenterStart,
        ) {
            Box(
                Modifier
                    .size(14.dp)
                    .background(Lx.colors.Surface, CircleShape),
            )
        }
    }
}

/** 小标签行（标签 + 值；用于连接页的「地址」「Token」两行）。 */
@Composable
fun LabeledRow(
    label: String,
    value: String,
    mono: Boolean,
    modifier: Modifier = Modifier,
    trailing: (@Composable () -> Unit)? = null,
) {
    Row(
        modifier = modifier.fillMaxWidth(),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(Lx.space.s8),
    ) {
        Text(
            text = label,
            style = Lx.type.Pill.copy(fontSize = Lx.type.Size11, fontWeight = FontWeight.SemiBold),
            color = Lx.colors.FgFaint,
            modifier = Modifier.width(52.dp),
        )
        Text(
            text = value,
            style = if (mono) {
                Lx.type.BodySmall.copy(fontSize = Lx.type.Size12)
            } else {
                Lx.type.BodySmall.copy(fontSize = Lx.type.Size12)
            },
            color = Lx.colors.Fg,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f),
        )
        if (trailing != null) trailing()
    }
}

/**
 * 空态占位（原型 `.empty` 形态，抽共用——助手页/会话页同一套）：
 *
 *   * 图标框 52dp / r16 / --soft 底 + 1px --line 边 + 22dp 图标（`--fg3`）；
 *   * 主行 14px/700（--fg2）、副行 12.5px（--fg3，`margin-top 4px`）；
 *   * 整块居中、padding 44/30。
 *
 * [title]/[description] 双行都给时是助手页那套全量形态；只给 text 时回落为
 * 单行居中灰字（原 SmallEmpty 语义，工具输出等待位这类小场景）。
 */
@Composable
fun SmallEmpty(
    text: String,
    modifier: Modifier = Modifier,
    icon: androidx.compose.ui.graphics.vector.ImageVector? = null,
    title: String? = null,
    description: String? = null,
) {
    if (icon == null && title == null && description == null) {
        Box(modifier = modifier.fillMaxWidth().padding(Lx.space.s16), contentAlignment = Alignment.Center) {
            Text(text = text, style = Lx.type.BodySmall, color = Lx.colors.FgFaint)
        }
        return
    }
    Column(
        modifier = modifier
            .fillMaxWidth()
            .padding(vertical = 44.dp, horizontal = 30.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(4.dp),
    ) {
        if (icon != null) {
            Box(
                Modifier
                    .size(52.dp)
                    .clip(RoundedCornerShape(16.dp))
                    .background(Lx.colors.BorderSoft)
                    .border(1.dp, Lx.colors.Border, RoundedCornerShape(16.dp)),
                contentAlignment = Alignment.Center,
            ) {
                SmallIcon(icon, Lx.colors.FgFaint, size = 22)
            }
        }
        Text(
            text = title ?: text,
            style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size14, fontWeight = FontWeight.Bold),
            color = Lx.colors.FgMuted,
            modifier = Modifier.padding(top = 8.dp),
        )
        if (description != null) {
            Text(
                text = description,
                style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size12_5),
                color = Lx.colors.FgFaint,
            )
        }
    }
}

/** 图标（用于页内小图标位，统一 16dp 与颜色）。 */
@Composable
fun SmallIcon(icon: ImageVector, tint: androidx.compose.ui.graphics.Color, size: Int = 16) {
    Icon(
        imageVector = icon,
        contentDescription = null,
        tint = tint,
        modifier = Modifier.size(size.dp),
    )
}

/** 一点纵向留白。 */
@Composable
fun VSpace(dp: androidx.compose.ui.unit.Dp) {
    Box(Modifier.height(dp))
}

/**
 * 虚线边框（原型 `.compactblock` / `.notice`：`border: 1px dashed`）。
 *
 * Compose 的 border 没有 dash 样式——Canvas 按 [dashLen]/[gapLen] 手画四段圆角
 * 描边（PathEffect.dashPathEffect）。挂 clip 之后画在内容层下面，颜色/圆角由
 * 调用方给（随主题）。
 */
fun Modifier.lxDashBorder(
    color: androidx.compose.ui.graphics.Color,
    shape: androidx.compose.ui.graphics.Shape,
    dashLen: Float = 5f,
    gapLen: Float = 4f,
    width: Float = 1f,
): Modifier = drawBehind {
    val outline = shape.createOutline(size, layoutDirection, this)
    val path = Path().apply {
        // Outline.Rounded → Path（通用路径），直线/椭圆回落也支持
        when (outline) {
            is androidx.compose.ui.graphics.Outline.Rounded -> addRoundRect(outline.roundRect)
            is androidx.compose.ui.graphics.Outline.Generic -> addPath(outline.path)
            else -> addRect(Rect(0f, 0f, size.width, size.height))
        }
    }
    drawPath(
        path = path,
        color = color,
        style = Stroke(
            width = width * density,
            pathEffect = PathEffect.dashPathEffect(floatArrayOf(dashLen * density, gapLen * density)),
        ),
    )
}

/** 文本片段（可指定字号/字重/颜色）。 */
@Composable
fun Txt(
    text: String,
    color: androidx.compose.ui.graphics.Color = Lx.colors.Fg,
    size: androidx.compose.ui.unit.TextUnit = Lx.type.Size13,
    weight: FontWeight = FontWeight.Normal,
    modifier: Modifier = Modifier,
    mono: Boolean = false,
    maxLines: Int = Int.MAX_VALUE,
) {
    Text(
        text = text,
        style = if (mono) {
            Lx.type.Mono12.copy(fontSize = size, color = color)
        } else {
            Lx.type.Body.copy(fontSize = size, lineHeight = (size.value * 1.5f).sp, color = color)
        },
        fontWeight = weight,
        maxLines = maxLines,
        modifier = modifier,
    )
}
