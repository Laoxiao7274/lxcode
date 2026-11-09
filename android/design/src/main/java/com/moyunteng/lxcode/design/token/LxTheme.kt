// lxcode 设计主题 · LxTheme
//
// 装配方式（刻意不套 Material3 的配色）：
//   1. 颜色 token 按 (variant, temp) 从 [LxColorSets] 取实例、经 CompositionLocal 下发
//      —— 组件只读 `Lx.colors` / `Lx.space` / …，不直接引用某个具体实例。
//      九组合全量展开（无回落依赖），切换变体/色温即整页换肤。
//   2. 不调用 `MaterialTheme(colorScheme = ...)` —— 未显式覆盖的槽位会保留 Material
//      基线紫，套上去等于埋一颗破坏中性风的雷。本设计系统的组件全部显式取 Lx token；
//      Material3 只当底层实现用（涟漪指示、文本排版引擎）。
//   3. 涟漪指示与 focus ring、阴影都从当前 [LxColorSet] 取，随主题一起切换。
package com.moyunteng.lxcode.design.token

import androidx.compose.foundation.LocalIndication
import androidx.compose.material3.ripple
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.ReadOnlyComposable
import androidx.compose.runtime.staticCompositionLocalOf

/** 颜色 token 下发点（默认 A + 暗色 = [LxTheme] 的默认档）。 */
val LocalLxColors = staticCompositionLocalOf<LxColorSet> { LxColorSets.ADark }

/** 阴影 token 下发点（每组合的卡片/浮层阴影色）。 */
val LocalLxShadow = staticCompositionLocalOf { defaultLxShadow(LxColorSets.ADark) }

/** 每个组合的阴影 token 默认值（主阴影 alpha 逐值换算，对齐原型 `--shadow-card`/`--shadow-float`）。 */
fun defaultLxShadow(c: LxColorSet): LxShadow = LxShadow(
    Card = c.Fg.copy(alpha = 0.06f),
    Float = c.Fg.copy(alpha = 0.07f),
)

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
 * 组件写 `Lx.colors.Fg` 而不是 `LxColorSets.ADark.Fg` —— 走 CompositionLocal 才换得了主题。
 */
object Lx {
    /** 颜色 token */
    val colors: LxColorSet
        @Composable @ReadOnlyComposable get() = LocalLxColors.current

    /** 阴影 token（卡片/浮层） */
    val shadow: LxShadow
        @Composable @ReadOnlyComposable get() = LocalLxShadow.current

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
 * 用法：`LxTheme(variant = LxVariant.A, temp = LxTemp.Dark) { … }`。
 * 它只做三件事：按 (variant, temp) 下发 Lx 颜色 token、下发同组合的阴影 token、
 * 把按压涟漪换成主题色。它**不**提供 Material3 的默认配色（理由见文件头注释）。
 *
 * @param variant 变体（A 纯净精修 / B 品牌紫 / C 陶土助理）
 * @param temp 色温（亮 / 暗 / 暖）
 * @param colors 颜色实例（默认按 variant/temp 解析；显式传入用于预览/测试单组合）
 * @param shadow 阴影 token（默认按 colors 推导）
 */
@Composable
fun LxTheme(
    variant: LxVariant = LxVariant.A,
    temp: LxTemp = LxTemp.Dark,
    colors: LxColorSet = LxColorSets.of(variant, temp),
    shadow: LxShadow = defaultLxShadow(colors),
    content: @Composable () -> Unit,
) {
    CompositionLocalProvider(
        LocalLxColors provides colors,
        LocalLxShadow provides shadow,
        LocalLxSpace provides LxSpace,
        LocalLxRadius provides LxRadius,
        LocalLxType provides LxType,
        LocalLxMotion provides LxMotion,
        LocalIndication provides ripple(color = colors.Ripple),
        content = content,
    )
}
