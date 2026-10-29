// lxcode 动效层 · 思考微光（think-shimmer）
//
// 桌面端：`background: linear-gradient(90deg, var(--fg-faint), var(--fg-muted), var(--fg-faint));
// background-size: 300%; animation: think-shimmer 2.25s linear infinite`（文字渐变扫过）。
// Compose 的等价物 = 把同一段渐变当作 Text 的 brush，并让渐变带横向扫过。
//
// 颜色不在规格里（规格只给了时长与 background-size），所以这里取 token 派生：
// 底 = 调用点给（默认 `--fg-muted`），高光 = 调用点给（默认 `--fg`），不新造色值。
package com.moyunteng.lxcode.design.motion

import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.text.TextStyle
import com.moyunteng.lxcode.design.token.Lx
import com.moyunteng.lxcode.design.token.LxMotion

/**
 * 思考微光文本 —— `think-shimmer`：一条渐变带 2.25s 扫过文字，`background-size: 300%`。
 *
 * 门控关闭时（reduced-motion）：不扫，直接以 [baseColor] 平色显示。
 *
 * @param text 文本
 * @param style 文本样式（字号 / 字重沿用调用点，本组件只接管颜色）
 * @param modifier 外部 Modifier
 * @param baseColor 底色（默认 `--fg-muted`）
 * @param highlightColor 高光色（默认 `--fg`）
 * @param enabled 是否扫光（思考块「运行中」时为 true）
 */
@Composable
fun LxShimmerText(
    text: String,
    style: TextStyle,
    modifier: Modifier = Modifier,
    baseColor: Color = Lx.colors.FgMuted,
    highlightColor: Color = Lx.colors.Fg,
    enabled: Boolean = true,
) {
    val motion = LocalLxMotionEnabled.current
    var widthPx by remember { mutableFloatStateOf(0f) }

    val shimmer = enabled && motion
    val progress = if (shimmer) {
        val transition = rememberInfiniteTransition(label = "lx-shimmer")
        val p by transition.animateFloat(
            initialValue = 0f,
            targetValue = 1f,
            animationSpec = infiniteRepeatable(
                animation = tween(LxMotion.DurationShimmer, easing = LxMotion.EasingLinear),
                repeatMode = RepeatMode.Restart,
            ),
            label = "lx-shimmer-progress",
        )
        p
    } else {
        0f
    }

    // background-size: 300% —— 渐变带宽度 = 文字宽度的 3 倍
    val band = widthPx * 3f
    val textStyle = if (shimmer && band > 0f) {
        val startX = -band + (widthPx + band) * progress
        style.copy(
            brush = Brush.linearGradient(
                colors = listOf(baseColor, highlightColor, baseColor),
                start = Offset(startX, 0f),
                end = Offset(startX + band, 0f),
            ),
        )
    } else {
        style.copy(color = baseColor)
    }

    Text(
        text = text,
        style = textStyle,
        modifier = modifier.onSizeChanged { widthPx = it.width.toFloat() },
    )
}
