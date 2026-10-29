// lxcode 设计主题 · LxTheme
//
// 装配方式（刻意不套 Material3 的配色）：
//   1. 五个 token 单例经 CompositionLocal 下发 —— 组件只读 `Lx.colors` / `Lx.space` / …，
//      不直接引用单例。今天只有一套默认值，但接缝留着：将来加暗色/换肤只改这里。
//   2. 不调用 `MaterialTheme(colorScheme = lightColorScheme(...))` —— `lightColorScheme()`
//      未显式覆盖的槽位会保留 Material 基线紫，套上去等于埋一颗破坏中性风的雷。
//      本设计系统的组件全部显式取 Lx token，不需要 Material 主题兜底；
//      Material3 只当底层实现用（涟漪指示、文本排版引擎）。
//   3. 涟漪指示换成 --fg 的低透明度，与桌面端 hover 填充 rgba(26,26,26,6%~10%) 同源。
package com.moyunteng.lxcode.design.token

import androidx.compose.foundation.LocalIndication
import androidx.compose.material3.ripple
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.ReadOnlyComposable
import androidx.compose.runtime.staticCompositionLocalOf

/** 颜色 token 下发点（默认 [LxColors]）。 */
val LocalLxColors = staticCompositionLocalOf { LxColors }

/** 间距 token 下发点（默认 [LxSpace]）。 */
val LocalLxSpace = staticCompositionLocalOf { LxSpace }

/** 圆角 token 下发点（默认 [LxRadius]）。 */
val LocalLxRadius = staticCompositionLocalOf { LxRadius }

/** 字体 token 下发点（默认 [LxType]）。 */
val LocalLxType = staticCompositionLocalOf { LxType }

/** 动效 token 下发点（默认 [LxMotion]）。 */
val LocalLxMotion = staticCompositionLocalOf { LxMotion }

/**
 * 组件内读 token 的统一入口。
 *
 * 组件写 `Lx.colors.Fg` 而不是 `LxColors.Fg` —— 走 CompositionLocal 才换得了主题。
 */
object Lx {
    /** 颜色 token */
    val colors: LxColors
        @Composable @ReadOnlyComposable get() = LocalLxColors.current

    /** 间距 token */
    val space: LxSpace
        @Composable @ReadOnlyComposable get() = LocalLxSpace.current

    /** 圆角 token */
    val radius: LxRadius
        @Composable @ReadOnlyComposable get() = LocalLxRadius.current

    /** 字体 token */
    val type: LxType
        @Composable @ReadOnlyComposable get() = LocalLxType.current

    /** 动效 token */
    val motion: LxMotion
        @Composable @ReadOnlyComposable get() = LocalLxMotion.current
}

/**
 * lxcode 安卓端设计主题。
 *
 * 用法：`LxTheme { ShowcaseScreen() }`。它只做两件事：下发 Lx token、把按压涟漪
 * 换成中性色。它**不**提供 Material3 的默认配色（理由见文件头注释）。
 */
@Composable
fun LxTheme(content: @Composable () -> Unit) {
    CompositionLocalProvider(
        LocalLxColors provides LxColors,
        LocalLxSpace provides LxSpace,
        LocalLxRadius provides LxRadius,
        LocalLxType provides LxType,
        LocalLxMotion provides LxMotion,
        LocalIndication provides ripple(color = LxColors.Ripple),
        content = content,
    )
}
