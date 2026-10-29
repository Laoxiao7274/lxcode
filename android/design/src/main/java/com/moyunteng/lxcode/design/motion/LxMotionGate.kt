// lxcode 动效层 · 门控（LxMotionGate）
//
// 对齐桌面端的 `motionAllowed()`：**非测试模式 且 系统未开「减少动态效果」**。
// 安卓端没有「减少动态效果」这个开关，等价物是系统动画缩放
// `Settings.Global.ANIMATOR_DURATION_SCALE`（为 0 = 关闭动画）。
//
// 门控关闭时的语义（三条，与桌面端一致）：
//  * 入场动画直接跳到终态（不播）—— 见 [LxEnterSpec] 的动画规格被换成 snap；
//  * 循环动画停（呼吸 / 微光 / 扫光 / 光标 / spinner）；
//  * 退场直接完成（不等动画结束）。
//
// 额外提供 [LxMotionGate] 的 `override`：原型里的「动效」调试开关用它强制关闭，
// 用来截取 reduced-motion 下的静态态。`override = null` 时跟随系统。
package com.moyunteng.lxcode.design.motion

import android.content.Context
import android.database.ContentObserver
import android.os.Handler
import android.os.Looper
import android.provider.Settings
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.ReadOnlyComposable
import androidx.compose.runtime.compositionLocalOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalContext

/**
 * 动效是否允许 —— 全库唯一的门控事实源。
 *
 * 默认值 `true`：没有 [LxMotionGate] 包裹时（如组件库单测、preview）动效照常播，
 * 不静默变静态；真正的系统态由 [LxMotionGate] 读 [Settings.Global.ANIMATOR_DURATION_SCALE] 决定。
 */
val LocalLxMotionEnabled = compositionLocalOf { true }

/** 组件内读门控的统一入口：`if (lxMotionEnabled()) …`。 */
@Composable
@ReadOnlyComposable
fun lxMotionEnabled(): Boolean = LocalLxMotionEnabled.current

/**
 * 读系统动画缩放（`Settings.Global.ANIMATOR_DURATION_SCALE`）。
 *
 * 语义：缩放为 0 = 系统关闭动画（无障碍「移除动画」），此时返回 false。
 * 读不到（老设备 / 抛异常）按 1.0 处理（= 允许动效），不因为读不到就静默关掉全部动画。
 *
 * @param context 上下文（一般传 applicationContext）
 */
fun readSystemMotionEnabled(context: Context): Boolean = try {
    Settings.Global.getFloat(
        context.contentResolver,
        Settings.Global.ANIMATOR_DURATION_SCALE,
        1f,
    ) > 0f
} catch (_: Exception) {
    true
}

/**
 * 观察系统动画缩放的可组合值 —— 系统设置里改了立即生效（ContentObserver）。
 *
 * 用户从「开发者选项 → 动画程序时长缩放」改成 0 时，正在跑的原型立刻切到静态路径。
 */
@Composable
fun rememberSystemMotionEnabled(): Boolean {
    val context = LocalContext.current
    var enabled by remember(context) { mutableStateOf(readSystemMotionEnabled(context)) }
    DisposableEffect(context) {
        val resolver = context.contentResolver
        val observer = object : ContentObserver(Handler(Looper.getMainLooper())) {
            override fun onChange(selfChange: Boolean) {
                enabled = readSystemMotionEnabled(context)
            }
        }
        resolver.registerContentObserver(
            Settings.Global.getUriFor(Settings.Global.ANIMATOR_DURATION_SCALE),
            false,
            observer,
        )
        onDispose { resolver.unregisterContentObserver(observer) }
    }
    return enabled
}

/**
 * 动效门控提供者 —— 把「动效是否允许」下发给子树（[LocalLxMotionEnabled]）。
 *
 * @param override 调试开关：`null` = 跟随系统动画缩放；`false` = 强制静态（模拟 reduced-motion）；
 *   `true` = 强制开启动效（忽略系统设置，仅供截图对比用）
 * @param content 子树
 */
@Composable
fun LxMotionGate(
    override: Boolean? = null,
    content: @Composable () -> Unit,
) {
    val system = rememberSystemMotionEnabled()
    CompositionLocalProvider(
        LocalLxMotionEnabled provides (override ?: system),
        content = content,
    )
}
