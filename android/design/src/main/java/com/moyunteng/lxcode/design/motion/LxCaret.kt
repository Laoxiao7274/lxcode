// lxcode 动效层 · 光标（caret-blink）
//
// 桌面端 `caret-blink`：`@keyframes caret-blink { 0%,100% { opacity: 1 } 50% { opacity: 0 } }`
// + `animation: caret-blink 1s steps(1) infinite` —— `steps(1)` 是**硬切**，不是渐变。
// Compose 的等价物是 keyframes 里 0~499ms 恒 1、500~999ms 恒 0，`RepeatMode.Restart`。
package com.moyunteng.lxcode.design.motion

import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.keyframes
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import com.moyunteng.lxcode.design.token.LxMotion

/**
 * 光标闪烁修饰符 —— `caret-blink`：1s 一轮、`steps(1)` 硬切（不是渐变）。
 *
 * 门控关闭时（reduced-motion）：不闪，恒 1（光标该看得见，只是不动）。
 *
 * @param blinking 是否闪烁（默认 true；内容揭示完成后不再需要光标时由调用点条件渲染掉）
 */
@Composable
fun Modifier.lxCaret(blinking: Boolean = true): Modifier {
    val motion = LocalLxMotionEnabled.current
    if (!blinking || !motion) return this
    val transition = rememberInfiniteTransition(label = "lx-caret")
    val a by transition.animateFloat(
        initialValue = 1f,
        targetValue = 1f,
        animationSpec = infiniteRepeatable(
            animation = keyframes {
                durationMillis = LxMotion.DurationCaret
                1f at 0 // 0%   → opacity 1
                1f at 499 // 49.9% → 仍是 1（steps(1)：到下一个关键帧前保持）
                0f at 500 // 50%  → opacity 0
                0f at 999
            },
            repeatMode = RepeatMode.Restart,
        ),
        label = "lx-caret-alpha",
    )
    return this.alpha(a)
}
