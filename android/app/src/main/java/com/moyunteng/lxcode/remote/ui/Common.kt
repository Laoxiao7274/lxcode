// 页面共用的小件 —— 由 :design 组件组合而来（不改 :design 公开 API）。
//
// 这里的每一件都对应桌面端的一条既有视觉规则，KDoc 里标注来源，便于追溯：
//  * [ConnPill]      ← Topbar.tsx 的 .conn-pill（脉冲圆点 + 连接名 + 下拉箭头）
//  * [ScreenHeader]  ← ag-doc-head（标题 + 关闭/返回）
//  * [TerminalBlock] ← ApprovalCard.module.css 的 .cmdBlock（深色终端块）
//  * [CodeBlock]     ← thread.css 的代码/工具输出底（--code-bg #f4f5f7）
//  * [MockSwitch]    ← 原型的 mock 开关（在线/离线、展开/折叠等）
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
import androidx.compose.material.icons.filled.KeyboardArrowDown
import androidx.compose.material.icons.filled.KeyboardArrowLeft
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.moyunteng.lxcode.design.component.LxIconButton
import com.moyunteng.lxcode.design.motion.LocalLxMotionEnabled
import com.moyunteng.lxcode.design.motion.lxAnimateColor
import com.moyunteng.lxcode.design.motion.lxPressScale
import com.moyunteng.lxcode.design.token.Lx
import com.moyunteng.lxcode.design.token.LxColors
import com.moyunteng.lxcode.design.token.LxMotion

/**
 * 顶栏连接药丸（对齐 Topbar.tsx `.conn-pill`）：脉冲圆点 + 连接名 + 下拉箭头。
 *
 * 两种态：在线 = `--success` 绿点 + 2.4s 呼吸；离线 = `--fg-faint` 灰点、无动画。
 */
@Composable
fun ConnPill(online: Boolean, name: String, onClick: () -> Unit, modifier: Modifier = Modifier) {
    val motion = LocalLxMotionEnabled.current
    val alpha = if (online && motion) {
        val transition = rememberInfiniteTransition(label = "conn-pill-dot")
        val a by transition.animateFloat(
            initialValue = 1f,
            targetValue = 0.45f,
            animationSpec = infiniteRepeatable(
                // 一轮 = 2.4s（RepeatMode.Reverse 下单程取一半）
                animation = tween(durationMillis = 1200, easing = Lx.motion.Easing),
                repeatMode = RepeatMode.Reverse,
            ),
            label = "conn-pill-dot-alpha",
        )
        a
    } else {
        1f
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
            .padding(horizontal = Lx.space.s8, vertical = Lx.space.s4),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(Lx.space.s6),
    ) {
        Box(
            Modifier
                .size(6.dp)
                .alpha(alpha)
                .background(dotColor, CircleShape),
        )
        Text(
            text = name,
            style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size11_5, fontWeight = FontWeight.Medium),
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
            .background(LxColors.Surface)
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
            .background(LxColors.TermBg)
            .padding(horizontal = Lx.space.s10, vertical = Lx.space.s8),
        verticalArrangement = Arrangement.spacedBy(Lx.space.s4),
    ) {
        if (!cwd.isNullOrBlank()) {
            Text(
                text = cwd,
                style = Lx.type.Mono12.copy(
                    fontSize = Lx.type.Size11,
                    color = LxColors.TermDim,
                ),
            )
        }
        Row(horizontalArrangement = Arrangement.spacedBy(Lx.space.s4)) {
            Text(text = "$", style = Lx.type.Mono12, color = Lx.colors.Success)
            Text(
                text = command,
                style = Lx.type.Mono12,
                color = LxColors.TermFg,
            )
        }
    }
}

/** 浅色代码块（对齐 thread.css `--code-bg #f4f5f7`）：等宽、可横向滚动。 */
@Composable
fun CodeBlock(text: String, modifier: Modifier = Modifier) {
    Box(
        modifier = modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(Lx.radius.r8))
            .background(LxColors.CodeBg)
            .padding(Lx.space.s8),
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

/** mock 开关（原型用）：左侧标签 + 右侧小滑块。 */
@Composable
fun MockSwitch(
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
                    .background(LxColors.Surface, CircleShape),
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

/** 空态占位（本地实现，避免依赖 :design 的空态样式差异）。 */
@Composable
fun SmallEmpty(text: String, modifier: Modifier = Modifier) {
    Box(modifier = modifier.fillMaxWidth().padding(Lx.space.s16), contentAlignment = Alignment.Center) {
        Text(text = text, style = Lx.type.BodySmall, color = Lx.colors.FgFaint)
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
