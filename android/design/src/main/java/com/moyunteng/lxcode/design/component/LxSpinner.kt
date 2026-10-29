// lxcode 组件 · LxSpinner（加载态）
package com.moyunteng.lxcode.design.component

import androidx.compose.animation.core.Easing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.layout.size
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.moyunteng.lxcode.design.motion.LocalLxMotionEnabled
import com.moyunteng.lxcode.design.token.Lx
import com.moyunteng.lxcode.design.token.LxMotion

/**
 * lxcode 加载态 —— 一段细线圆弧匀速旋转。
 *
 * 变体：只有一档（细线 / 中性色）。尺寸由 [size] 决定：按钮内嵌用 14dp，独立加载用 20~24dp。
 *
 * 节奏：默认一圈 [LxMotion.DurationStandard] × 8（960ms）走标准曲线 —— 步进节奏是「细线」质感的来源。
 * 桌面端 keyframes 规格里「连接 spinner 一圈 800ms、小 spinner 一圈 700ms，匀速」由
 * [periodMillis] + [easing] 两个参数表达（`periodMillis = LxMotion.DurationSpinnerLarge` /
 * `DurationSpinnerSmall`，`easing = LxMotion.EasingLinear`），默认值保持本组件原有观感不变。
 *
 * 门控（reduced-motion）关闭时：不转 —— 停在 0°，静态圆弧。
 *
 * 颜色恒为 `--fg-muted`：加载不是语义状态，不占用 success/danger 两个语义色。
 * 需要别的颜色（如黑底主按钮内）请用 [LxSpinnerArc]。
 *
 * @param modifier 外部 Modifier
 * @param size 直径（默认 14dp，与 12.5sp 按钮文字同高）
 * @param periodMillis 一圈时长（ms）
 * @param easing 一圈的曲线
 */
@Composable
fun LxSpinner(
    modifier: Modifier = Modifier,
    size: Dp = 14.dp,
    periodMillis: Int = LxMotion.DurationStandard * 8,
    easing: Easing = LxMotion.Easing,
) {
    LxSpinnerArc(
        modifier = modifier,
        size = size,
        color = Lx.colors.FgMuted,
        periodMillis = periodMillis,
        easing = easing,
    )
}

/**
 * 带颜色覆写的旋转弧 —— 内部实现，供 [LxButton] 在黑底主按钮里换成白线。
 *
 * 刻意不公开：颜色是 token 决定的事，公开它等于给调用方开一个「随便填色」的口子。
 */
@Composable
internal fun LxSpinnerArc(
    modifier: Modifier = Modifier,
    size: Dp = 14.dp,
    color: Color,
    periodMillis: Int = LxMotion.DurationStandard * 8,
    easing: Easing = LxMotion.Easing,
) {
    // 门控关闭时不转：静态圆弧（停在 0°），不是「转得很慢」
    val motion = LocalLxMotionEnabled.current
    val angle = if (motion) {
        val transition = rememberInfiniteTransition(label = "lx-spinner")
        val a by transition.animateFloat(
            initialValue = 0f,
            targetValue = 360f,
            animationSpec = infiniteRepeatable(
                animation = tween(durationMillis = periodMillis, easing = easing),
                repeatMode = RepeatMode.Restart,
            ),
            label = "lx-spinner-angle",
        )
        a
    } else {
        0f
    }

    Canvas(modifier = modifier.size(size)) {
        val stroke = 1.5.dp.toPx()
        drawArc(
            color = color,
            startAngle = angle,
            sweepAngle = 270f,
            useCenter = false,
            topLeft = Offset(stroke / 2f, stroke / 2f),
            size = Size(this.size.width - stroke, this.size.height - stroke),
            style = Stroke(width = stroke, cap = StrokeCap.Round),
        )
    }
}
