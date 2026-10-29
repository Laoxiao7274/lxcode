// lxcode 组件 · LxStatusDot（状态圆点）
package com.moyunteng.lxcode.design.component

import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.moyunteng.lxcode.design.motion.LocalLxMotionEnabled
import com.moyunteng.lxcode.design.token.Lx
import com.moyunteng.lxcode.design.token.LxMotion

/**
 * 状态圆点的语义档。
 *
 * 四档与桌面端 `data-state` 的取值一一对应，颜色全部来自 token，不暴露给调用方。
 */
enum class LxStatus {
    /** 空闲/未知 —— `--fg-faint` */
    Idle,

    /** 运行中 —— 琥珀 `--turn-run: #d97706`，带呼吸闪烁 */
    Run,

    /** 成功 —— `--success` */
    Success,

    /** 失败 —— `--danger` */
    Danger,
}

/**
 * lxcode 状态圆点 —— 后台任务/连接状态前面的那颗小圆点。
 *
 * 变体：`idle`（`--fg-faint`）/ `run`（琥珀 + 呼吸）/ `success`（`--success`）/ `danger`（`--danger`）。
 *
 * 桌面端对应：`.job-dot { width: 8px; height: 8px; border-radius: 50%; background: var(--fg-faint) }`，
 * 运行态 `animation: job-pulse 1.6s ease-in-out infinite { 50% { opacity: .35 } }`。
 * 唯一偏差：桌面端运行态用 #3b82f6（蓝），本设计系统统一成琥珀 `#d97706`（见任务给定的色板）。
 *
 * @param status 语义档
 * @param modifier 外部 Modifier
 * @param size 直径（默认 8dp = 桌面端 8px）
 */
@Composable
fun LxStatusDot(
    status: LxStatus,
    modifier: Modifier = Modifier,
    size: Dp = 8.dp,
) {
    val color = when (status) {
        LxStatus.Idle -> Lx.colors.FgFaint
        LxStatus.Run -> Lx.colors.Amber
        LxStatus.Success -> Lx.colors.Success
        LxStatus.Danger -> Lx.colors.Danger
    }

    // 只有运行中态呼吸：闪烁是「还在跑」的信号，其余三态是静态结果，不该动。
    // 门控（reduced-motion）关闭时：连运行中态也不呼吸 —— 静态到终态（满透明度）。
    val motion = LocalLxMotionEnabled.current
    val alpha = if (status == LxStatus.Run && motion) {
        val transition = rememberInfiniteTransition(label = "lx-status-dot")
        val pulse by transition.animateFloat(
            initialValue = 1f,
            targetValue = 0.35f,
            animationSpec = infiniteRepeatable(
                // 一轮（1→0.35→1）= 1.6s（job-pulse 1.6s ease-in-out infinite）：
                // RepeatMode.Reverse 把一段动画来回播，所以单程取一半（800ms），整轮才是 1.6s
                animation = tween(durationMillis = LxMotion.DurationPulse / 2, easing = Lx.motion.Easing),
                repeatMode = RepeatMode.Reverse,
            ),
            label = "lx-status-dot-alpha",
        )
        pulse
    } else {
        1f
    }

    Box(
        modifier = modifier
            .size(size)
            .alpha(alpha)
            .background(color = color, shape = CircleShape),
    )
}
