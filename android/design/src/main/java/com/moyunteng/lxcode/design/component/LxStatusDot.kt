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
import androidx.compose.ui.draw.drawWithContent
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

    /** 运行中 —— 琥珀 `--turn-run: #d97706`，带光环扩散 */
    Run,

    /** 成功 —— `--success` */
    Success,

    /** 失败 —— `--danger` */
    Danger,
}

/**
 * lxcode 状态圆点 —— 后台任务/连接状态前面的那颗小圆点。
 *
 * 变体：`idle`（`--fg-faint`）/ `run`（琥珀 + 光环扩散）/ `success`（`--success`）/ `danger`（`--danger`）。
 *
 * 桌面端对应：`.row .st { width: 8px; height: 8px; border-radius: 50% }`，
 * 运行态 `.row .st.run { background: var(--warn); animation: pulse 1.8s ease-in-out infinite }`，
 * `@keyframes pulse { 0%,100% { box-shadow: 0 0 0 0 var(--ring) } 50% { box-shadow: 0 0 0 4px var(--ring) } }`
 * ——点**自身恒亮**，外圈光环（box-shadow 等价物）按 1.8s 周期扩散/收回：
 * 半径 0→4dp、alpha 随扩散渐隐。2026-12 扫尾条目 28 把旧的「点自身透明度呼吸」
 * 统一成这个形态（原型 `.navitem .badge` 同一条 pulse keyframes）。
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

    // 只有运行中态播光环：扩散是「还在跑」的信号，其余三态是静态结果，不该动。
    // 门控（reduced-motion）关闭时：连运行中态也不播 —— 静态实色点。
    // 环颜色 = 琥珀自身（原型 var(--ring) 是点色的低饱和等价物；安卓侧直接用
    // 点色 + alpha 渐隐，九套 token 组合下语义一致，不发明新 token）。
    val motion = LocalLxMotionEnabled.current
    val ringProgress = if (status == LxStatus.Run && motion) {
        val transition = rememberInfiniteTransition(label = "lx-status-dot")
        val p by transition.animateFloat(
            initialValue = 0f,
            targetValue = 1f,
            animationSpec = infiniteRepeatable(
                // pulse 1.8s ease-in-out infinite：0→50% 扩散到 4px，50→100% 收回
                animation = tween(durationMillis = LxMotion.DurationStatusRing, easing = Lx.motion.Easing),
                repeatMode = RepeatMode.Restart,
            ),
            label = "lx-status-dot-ring",
        )
        p
    } else {
        -1f // 无光环（< 0 = drawWithContent 里不画）
    }

    Box(
        modifier = modifier
            .size(size)
            .drawWithContent {
                if (ringProgress >= 0f) {
                    // 光环：半径从点缘扩到 +4dp，alpha 随扩散渐隐（box-shadow 的
                    // 4px blur 无 alpha 动画，这里 alpha 衰减等价「扩散即消散」观感）
                    val t = ringProgress
                    val r = this.size.minDimension / 2f + (4.dp.toPx() * t)
                    drawCircle(
                        color = color.copy(alpha = (1f - t).coerceIn(0f, 1f) * 0.35f),
                        radius = r,
                    )
                }
                drawContent()
            }
            .background(color = color, shape = CircleShape),
    )
}
