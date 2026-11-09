// lxcode 设计 token · 阴影
//
// 桌面端阴影三档（box-shadow 字面量），Compose 的 Modifier.shadow 只吃
// elevation + 颜色，没有「双段阴影」的表达能力——这里取每档的「主阴影」半径做 elevation，
// 主阴影的 alpha 做阴影色。视觉意图（浮层比卡片重、模态比浮层重）逐档保留。
// 用法：`Modifier.lxShadow(LxElevation.Card, LxRadius.CardShape)`。
package com.moyunteng.lxcode.design.token

import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.RectangleShape
import androidx.compose.ui.graphics.Shape
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp

/**
 * 阴影档位。elevation 取原型 `--shadow-card` / `--shadow-float` 的主阴影模糊半径量级；
 * 阴影颜色从主题 [LxColorSet] 取（[LxTheme] 下发），不同 variant/temp 各有自己的
 * 阴影浓度（暗色更重、暖色偏棕）——不再是全局固定色。
 */
enum class LxElevation(
    /** Compose 的 elevation（dp）—— 取原型主阴影的模糊半径量级 */
    val elevation: Dp,
) {
    /** 无阴影 —— 平铺元素（列表行、ghost 按钮） */
    None(0.dp),

    /** 卡片 —— CSS `--shadow-card`（如 A+Light `0 1px 2px …, 0 6px 20px …`） */
    Card(6.dp),

    /** 菜单/浮层 —— CSS `--shadow-float`（底边浮层阴影，如 `0 -8px 28px …`） */
    Menu(8.dp),

    /** 模态 —— 沿用旧档位（原型未单列模态阴影，取浮层的加重档） */
    Modal(24.dp),
}

/**
 * 每个主题组合的阴影 token（卡片 / 浮层）—— [LxTheme] 按当前组合下发。
 * 字段对齐原型 CSS 变量：[Card] = `--shadow-card`、[Float] = `--shadow-float`。
 */
data class LxShadow(
    /** 卡片阴影色 —— `--shadow-card` 主阴影的 alpha（如 A+Light rgba(16,16,20,.06)） */
    val Card: Color,

    /** 浮层阴影色 —— `--shadow-float` 主阴影的 alpha（如 A+Light rgba(16,16,20,.07)） */
    val Float: Color,
) {
    /** 模态阴影 = 浮层的加重档（原型未单列）。 */
    val Modal: Color get() = Float
}

/**
 * 按 token 档位给元素加阴影。
 *
 * @param level 阴影档位（[LxElevation]）
 * @param shape 与元素自身圆角一致的 Shape（不一致会出现方角阴影）
 */
fun Modifier.lxShadow(level: LxElevation, shape: Shape = RectangleShape, color: Color = fallbackShadowColor(level)): Modifier =
    if (level == LxElevation.None) this
    else shadow(
        elevation = level.elevation,
        shape = shape,
        clip = false,
        ambientColor = color,
        spotColor = color,
    )

/** 各档位在无主题上下文时的回落色（旧全局阴影 alpha；[LxTheme] 内使用主题色）。 */
private fun fallbackShadowColor(level: LxElevation): Color = when (level) {
    LxElevation.None -> Color.Transparent
    LxElevation.Card -> Color(0x0F000000)
    LxElevation.Menu -> Color(0x12000000)
    LxElevation.Modal -> Color(0x12000000)
}
