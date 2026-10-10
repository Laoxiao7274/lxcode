// lxcode 组件 · LxButton（按钮）
package com.moyunteng.lxcode.design.component

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsHoveredAsState
import androidx.compose.foundation.interaction.collectIsPressedAsState
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.LocalContentColor
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Shape
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.TextUnit
import androidx.compose.ui.unit.dp
import com.moyunteng.lxcode.design.motion.lxAnimateFloat
import com.moyunteng.lxcode.design.token.Lx
import com.moyunteng.lxcode.design.token.LxMotion
import com.moyunteng.lxcode.design.token.LxRadius
import com.moyunteng.lxcode.design.token.LxType

/**
 * 按钮的语义档。
 *
 * 四档与桌面端一致；颜色全部由 token 决定，调用方不能传裸色值。
 */
enum class LxButtonVariant {
    /** 黑底白字主按钮 —— 底 `--fg`，字 `#fff`（桌面端 `.fd-btn-p`） */
    Primary,

    /** 白底描边次按钮 —— 底 `--surface`，边 `--border-strong`，字 `--fg`（桌面端 `.fd-btn-g`） */
    Secondary,

    /** 无边框幽灵按钮 —— 透明底、无边框，hover/按压填 `--border-soft` */
    Ghost,

    /** 危险按钮 —— 底 `--danger`，字 `#fff`（桌面端 `.settings-confirm-auto-btn`） */
    Danger,
}

/**
 * 按钮尺寸档。
 *
 * 三档只改「内边距 / 字号 / 圆角 / 最小高度 / 图标尺寸」，不改颜色。
 */
enum class LxButtonSize {
    /** 小号 —— 11.5sp / 圆角 8 / 最小高 30dp（列表行内动作） */
    Small,

    /** 中号（默认） —— 12.5sp / 圆角 9 / 最小高 34dp（桌面端 `.fd-btn-p` 原尺寸） */
    Medium,

    /** 大号 —— 14sp / 圆角 10 / 最小高 44dp（主行动按钮，达到触摸目标下限） */
    Large,
}

/**
 * lxcode 按钮 —— 主 / 次 / 幽灵 / 危险四档语义按钮。
 *
 * 变体：[variant]（[LxButtonVariant]）× [size]（[LxButtonSize]）× [enabled] × [loading]。
 *  * `loading = true`：文字左侧换成 [LxSpinner]（白线或 token 色），同时屏蔽点击，外观亮度不变；
 *  * `enabled = false`：填充档降到 0.35、描边/幽灵档降到 0.6（对齐桌面端 `:disabled`）；
 *  * hover / 按压：填充档整体降亮度到 0.85 / 0.80（桌面端 `:hover { opacity: .85 }`），
 *    描边/幽灵档改边框色或底色，并按 `scale(.98)` 收缩。
 *
 * 桌面端对应：`.fd-btn-p { padding: 7px 14px; font-size: 12.5px; font-weight: 500; color: #fff;
 * background: var(--fg); border-radius: 9px }` + `:hover { opacity: .85 }` + `:disabled { opacity: .35 }`；
 * `.fd-btn-g { color: var(--fg-muted); border: 1px solid var(--border-strong); border-radius: 9px }` +
 * `:hover { color: var(--fg); border-color: #c9c9cf }`。
 *
 * @param text 按钮文字
 * @param onClick 点击回调
 * @param modifier 外部 Modifier
 * @param variant 语义档
 * @param size 尺寸档
 * @param enabled 是否可用
 * @param loading 是否加载中（显示 spinner 并屏蔽点击）
 * @param leadingIcon 可选前置图标插槽
 */
@Composable
fun LxButton(
    text: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    variant: LxButtonVariant = LxButtonVariant.Primary,
    size: LxButtonSize = LxButtonSize.Medium,
    enabled: Boolean = true,
    loading: Boolean = false,
    leadingIcon: (@Composable () -> Unit)? = null,
) {
    val interaction = remember { MutableInteractionSource() }
    val hovered by interaction.collectIsHoveredAsState()
    val pressed by interaction.collectIsPressedAsState()

    val look = buttonLook(variant = variant, hovered = hovered, pressed = pressed)
    val metrics = sizeMetrics(size)
    val clickable = enabled && !loading

    // 禁用透明度：填充档 0.35（.fd-btn-p:disabled），描边/幽灵档 0.6（.fd-trigger:disabled）
    val disabledAlpha = when (variant) {
        LxButtonVariant.Primary, LxButtonVariant.Danger -> LxMotion.DisabledAlpha
        LxButtonVariant.Secondary, LxButtonVariant.Ghost -> LxMotion.DisabledAlphaSoft
    }
    // 填充档的 hover 走整体透明度（桌面端 opacity: .85），其余档已经在 look 里改过颜色了
    val alpha = when {
        !enabled -> disabledAlpha
        variant == LxButtonVariant.Primary || variant == LxButtonVariant.Danger ->
            if (pressed) 0.80f else if (hovered) 0.85f else 1f

        else -> 1f
    }
    val scale = if (pressed && clickable) LxMotion.PressedScale else 1f
    // 按压微交互：缩放与透明度都走 120ms 标准曲线（门控关闭时瞬变）—— 桌面端 `transition: .12s`
    val animatedScale by lxAnimateFloat(target = scale, durationMillis = LxMotion.DurationStandard)
    val animatedAlpha by lxAnimateFloat(target = alpha, durationMillis = LxMotion.DurationStandard)

    Row(
        modifier = modifier
            .graphicsLayer {
                this.alpha = animatedAlpha
                scaleX = animatedScale
                scaleY = animatedScale
            }
            .heightIn(min = metrics.minHeight)
            .clip(metrics.shape)
            .background(look.container)
            .then(
                if (look.border != null) Modifier.border(1.dp, look.border, metrics.shape) else Modifier,
            )
            .clickable(
                enabled = clickable,
                interactionSource = interaction,
                indication = null,
                onClick = onClick,
            )
            .padding(metrics.padding),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(Lx.space.s6, Alignment.CenterHorizontally), // 桌面端 gap: 6~7px
    ) {
        CompositionLocalProvider(LocalContentColor provides look.content) {
            when {
                loading -> LxSpinnerArc(size = metrics.iconSize, color = look.content)
                leadingIcon != null -> leadingIcon()
            }
            if (text.isNotEmpty()) {
                Text(
                    text = text,
                    style = Lx.type.Button,
                    fontSize = metrics.fontSize,
                    color = look.content,
                )
            }
        }
    }
}

/** 按钮尺寸档的度量。 */
private data class ButtonMetrics(
    val padding: PaddingValues,
    val fontSize: TextUnit,
    val shape: Shape,
    val minHeight: Dp,
    val iconSize: Dp,
)

/** 尺寸档 → 度量。桌面端只有一档（7px 14px / 12.5px / 9px），其余两档按刻度外推。 */
private fun sizeMetrics(size: LxButtonSize): ButtonMetrics = when (size) {
    LxButtonSize.Small -> ButtonMetrics(
        padding = PaddingValues(horizontal = 10.dp, vertical = 5.dp),
        fontSize = LxType.Size11_5,
        shape = LxRadius.RowShape,
        minHeight = 30.dp,
        iconSize = 12.dp,
    )

    LxButtonSize.Medium -> ButtonMetrics(
        padding = PaddingValues(horizontal = 14.dp, vertical = 7.dp), // 桌面端 `.fd-btn-p { padding: 7px 14px }`
        fontSize = LxType.Size12_5,
        shape = LxRadius.FieldShape,
        minHeight = 34.dp,
        iconSize = 14.dp,
    )

    LxButtonSize.Large -> ButtonMetrics(
        padding = PaddingValues(horizontal = 18.dp, vertical = 10.dp),
        fontSize = LxType.Size14,
        shape = RoundedCornerShape(LxRadius.r10),
        minHeight = 44.dp, // 触摸目标下限
        iconSize = 16.dp,
    )
}

/** 按钮外观（底 / 边 / 字色），四档 × hover × 按压。 */
private data class ButtonLook(
    val container: Color,
    val border: Color?,
    val content: Color,
)

/** 语义档 → 外观。填充档 hover 不改色（由外层透明度负责），描边/幽灵档改边框或底色。 */
@Composable
private fun buttonLook(
    variant: LxButtonVariant,
    hovered: Boolean,
    pressed: Boolean,
): ButtonLook = when (variant) {
    LxButtonVariant.Primary -> ButtonLook(
        container = Lx.colors.Fg,
        border = null,
        content = Lx.colors.Bg, // #fff
    )

    LxButtonVariant.Secondary -> ButtonLook(
        container = if (hovered || pressed) Lx.colors.BorderSoft else Lx.colors.Surface,
        border = if (hovered) Lx.colors.BorderGhostHover else Lx.colors.BorderStrong,
        content = Lx.colors.Fg,
    )

    LxButtonVariant.Ghost -> ButtonLook(
        container = if (hovered || pressed) Lx.colors.BorderSoft else Color.Transparent,
        border = null,
        content = if (hovered) Lx.colors.Fg else Lx.colors.FgMuted,
    )

    LxButtonVariant.Danger -> ButtonLook(
        container = Lx.colors.Danger,
        border = null,
        content = Lx.colors.Bg,
    )
}
