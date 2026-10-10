// lxcode 动效层 · 打字机（stream-reveal）
//
// 逐字揭示的常量与算法**逐条**照桌面端复刻（frontend 的 stream-reveal）：
//   REVEAL_MIN_CPS = 30、REVEAL_MAX_CPS = 1000、REVEAL_POUR_SECONDS = 0.35、REVEAL_MAX_LAG = 4000
// 算法：每帧按「积压量 / 0.35s」算速度并夹在 30~1000 字符/秒之间；积压超 4000 直接跳到全文。
//
// **纪律：打字机不受 reduced-motion 门控**（桌面端注释明说 reduced-motion 不关闭它）。
// 理由：打字机是「内容出现的节奏」，不是装饰动效；关掉它等于让用户看不到内容在长出来。
package com.moyunteng.lxcode.design.motion

import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.withFrameNanos
import kotlin.math.min

/** 打字机常量 —— 与桌面端 stream-reveal 的四个常量同名同值。 */
object LxReveal {
    /** 最小速度（字符/秒）—— 桌面端 `REVEAL_MIN_CPS` */
    const val MinCps = 30f

    /** 最大速度（字符/秒）—— 桌面端 `REVEAL_MAX_CPS` */
    const val MaxCps = 1000f

    /** 浇注时长（秒）—— 桌面端 `REVEAL_POUR_SECONDS`：每段积压按 0.35s 浇完 */
    const val PourSeconds = 0.35f

    /** 积压上限（字符）—— 桌面端 `REVEAL_MAX_LAG`：超过就直接对齐全文 */
    const val MaxLagChars = 4000
}

/**
 * 逐字揭示的当前状态。
 *
 * @param text 当前应当显示的文本（已揭示前缀）
 * @param revealing 是否还在揭示（光标据此显示/消失）
 */
@Stable
class LxRevealState internal constructor(
    val text: String,
    val revealing: Boolean,
)

/**
 * 逐字揭示 —— 把 [text] 按桌面端 stream-reveal 的节奏逐字长出来。
 *
 * 用法：`val r = rememberStreamReveal(block.content); Text(r.text)`；`r.revealing` 为真时
 * 在末尾画光标（[Modifier.lxCaret]）。
 *
 * @param text 目标全文
 * @param enabled 是否揭示（false = 直接给全文，用于「已经结束」的历史块）
 * @param key 变化即重新揭示（换会话 / 换消息时传会话 id 或块下标）
 */
@Composable
fun rememberStreamReveal(
    text: String,
    enabled: Boolean = true,
    key: Any? = null,
): LxRevealState {
    var revealed by remember(key) { mutableIntStateOf(if (enabled) 0 else text.length) }
    var revealing by remember(key) { mutableStateOf(false) }

    LaunchedEffect(text, enabled, key) {
        if (!enabled || text.isEmpty()) {
            revealed = text.length
            revealing = false
            return@LaunchedEffect
        }
        if (revealed > text.length) revealed = 0
        revealing = true
        var carry = 0f
        var lastFrame = 0L
        while (revealed < text.length) {
            withFrameNanos { now ->
                val dtSeconds = if (lastFrame == 0L) 0f else (now - lastFrame) / 1_000_000_000f
                lastFrame = now
                val lag = text.length - revealed
                if (lag > LxReveal.MaxLagChars) {
                    // 积压超上限：直接对齐全文（不假装还能浇完）
                    revealed = text.length
                } else {
                    val cps = (lag / LxReveal.PourSeconds).coerceIn(LxReveal.MinCps, LxReveal.MaxCps)
                    carry += cps * dtSeconds
                    val step = carry.toInt()
                    if (step > 0) {
                        carry -= step
                        revealed = min(text.length, revealed + step)
                    }
                }
            }
        }
        revealing = false
    }

    return LxRevealState(text = text.take(revealed), revealing = revealing)
}
