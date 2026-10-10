// lxcode 组件 · LxIconButton（小图标按钮）
package com.moyunteng.lxcode.design.component

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsHoveredAsState
import androidx.compose.foundation.interaction.collectIsPressedAsState
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.size
import androidx.compose.material3.LocalContentColor
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.moyunteng.lxcode.design.motion.lxAnimateColor
import com.moyunteng.lxcode.design.motion.lxPressScale
import com.moyunteng.lxcode.design.token.Lx
import com.moyunteng.lxcode.design.token.LxMotion

/**
 * 小图标按钮的语义档。
 *
 * 三档只决定「图标/填充的颜色」，不改变尺寸与圆角 —— 尺寸由 [LxIconButton] 的 [size] 决定。
 */
enum class LxIconButtonVariant {
    /** 中性 —— 图标 `--fg-muted`，hover 变 `--fg` + `--border-soft` 填充 */
    Neutral,

    /** 危险 —— 图标 `--danger`，hover 变 `--danger` + `--border-soft` 填充 */
    Danger,
}

/**
 * lxcode 小图标按钮 —— 列表行尾部、工具条里的紧凑图标动作。
 *
 * 变体：`neutral` / `danger`（见 [LxIconButtonVariant]）。
 * 状态：`enabled`（禁用降透明度）、hover/按压（`--border-soft` 填充）、尺寸档。
 *
 * 桌面端对应：`.catalog .cg-icon-btn { width: 24px; height: 24px; color: var(--fg-faint);
 * background: transparent; border: none; border-radius: 6px }` +
 * `.ag-mini-btn:hover { color: var(--fg); background: var(--border-soft) }`。
 * 圆角取 7dp 档（任务规定的「小图标按钮（圆角 7）」）。
 *
 * @param onClick 点击回调
 * @param modifier 外部 Modifier
 * @param variant 语义档
 * @param size 触控边长（默认 32dp；桌面端 24px，手指目标上浮一档）
 * @param enabled 是否可用
 * @param content 图标内容（建议 16dp 的线描图标）
 */
@Composable
fun LxIconButton(
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    variant: LxIconButtonVariant = LxIconButtonVariant.Neutral,
    size: Dp = 32.dp,
    enabled: Boolean = true,
    content: @Composable () -> Unit,
) {
    val interaction = remember { MutableInteractionSource() }
    val hovered by interaction.collectIsHoveredAsState()
    val pressed by interaction.collectIsPressedAsState()

    val ink = when {
        variant == LxIconButtonVariant.Danger -> Lx.colors.Danger
        hovered -> Lx.colors.Fg
        else -> Lx.colors.FgMuted
    }
    val container: Color = if (hovered || pressed) Lx.colors.BorderSoft else Color.Transparent
    // hover / 按压填充过渡 120ms（门控关闭时瞬变）+ 按压缩放 0.98
    val fill by lxAnimateColor(target = container)

    Box(
        modifier = modifier
            .lxPressScale(interaction, enabled = enabled)
            .size(size)
            .alpha(if (enabled) 1f else LxMotion.DisabledAlphaSoft)
            .clip(Lx.radius.IconButtonShape)
            .background(fill)
            .clickable(
                enabled = enabled,
                interactionSource = interaction,
                indication = null,
                onClick = onClick,
            ),
        contentAlignment = Alignment.Center,
    ) {
        // 图标颜色经 CompositionLocal 下发：图标由调用方传入（组件库不带图标资产）
        CompositionLocalProvider(LocalContentColor provides ink) {
            content()
        }
    }
}
