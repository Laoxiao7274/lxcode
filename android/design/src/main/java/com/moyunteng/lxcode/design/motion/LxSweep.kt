// lxcode 动效层 · 工具行扫光（tool-sweep）
//
// 桌面端：一条 300px 宽的渐变从左 -300px 扫到 100%，2.6s ease-out infinite，用在「运行中」的工具行。
// Compose 的等价物 = 在内容之上画一条横向渐变带，位置按 2.6s 循环从左侧外推到右侧外。
//
// 颜色不在规格里（规格只给几何与时长），所以取 token 派生的中性高光：`--fg` 低透明度。
package com.moyunteng.lxcode.design.motion

import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.drawWithContent
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.moyunteng.lxcode.design.token.Lx
import com.moyunteng.lxcode.design.token.LxMotion

/**
 * 扫光修饰符 —— `tool-sweep`：一条 300dp 宽的渐变带从左扫到右，2.6s `ease-out` infinite。
 *
 * 门控关闭时（reduced-motion）：不扫（连一条静态高光也不留 —— 静态态就是没有扫光）。
 *
 * @param enabled 是否扫光（「运行中」的工具行为 true）
 * @param bandWidth 渐变带宽度（默认 300dp = 桌面端 300px）
 * @param durationMillis 一轮时长（默认 2600ms）
 * @param peakAlpha 高光峰值透明度（默认 0.10：`--fg` 10%，白底上肉眼可见但不抢眼）
 */
@Composable
fun Modifier.lxSweep(
    enabled: Boolean = true,
    bandWidth: Dp = 300.dp,
    durationMillis: Int = LxMotion.DurationToolSweep,
    peakAlpha: Float = 0.10f,
): Modifier {
    val motion = LocalLxMotionEnabled.current
    if (!enabled || !motion) return this

    val transition = rememberInfiniteTransition(label = "lx-sweep")
    val progress by transition.animateFloat(
        initialValue = 0f,
        targetValue = 1f,
        animationSpec = infiniteRepeatable(
            animation = tween(durationMillis, easing = LxMotion.Easing),
            repeatMode = RepeatMode.Restart,
        ),
        label = "lx-sweep-progress",
    )
    val highlight = Lx.colors.Fg.copy(alpha = peakAlpha)

    return this.drawWithContent {
        drawContent()
        val band = bandWidth.toPx()
        val x = -band + (size.width + band) * progress
        drawRect(
            brush = Brush.horizontalGradient(
                colors = listOf(Color.Transparent, highlight, Color.Transparent),
                startX = x,
                endX = x + band,
            ),
            topLeft = Offset.Zero,
            size = size,
        )
    }
}
