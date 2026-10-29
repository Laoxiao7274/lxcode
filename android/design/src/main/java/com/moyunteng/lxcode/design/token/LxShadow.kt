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
 * 阴影档位。每档的 [elevation] 与 [color] 都直接对应桌面端的一条 box-shadow。
 */
enum class LxElevation(
    /** Compose 的 elevation（dp）—— 取桌面端主阴影的模糊半径量级 */
    val elevation: Dp,
    /** 阴影颜色 —— 桌面端主阴影的 rgba alpha 逐值换算 */
    val color: Color,
) {
    /** 无阴影 —— 平铺元素（列表行、ghost 按钮） */
    None(0.dp, Color.Transparent),

    /** 卡片 —— CSS `0 1px 2px rgba(0,0,0,.03)`（.ag-card / ApprovalCard .card） */
    Card(1.dp, Color(0x08000000)),

    /** 菜单/浮层 —— CSS `0 8px 30px rgba(0,0,0,.12), 0 2px 8px rgba(0,0,0,.06)` */
    Menu(8.dp, Color(0x1F000000)),

    /** 模态 —— CSS `0 24px 80px rgba(0,0,0,.2), 0 6px 20px rgba(0,0,0,.08)` */
    Modal(24.dp, Color(0x33000000)),
}

object LxShadow {
    /** 卡片阴影 —— CSS `0 1px 2px rgba(0,0,0,.03)` */
    val Card = LxElevation.Card

    /** 菜单/浮层阴影 —— CSS `0 8px 30px rgba(0,0,0,.12), 0 2px 8px rgba(0,0,0,.06)` */
    val Menu = LxElevation.Menu

    /** 模态阴影 —— CSS `0 24px 80px rgba(0,0,0,.2), 0 6px 20px rgba(0,0,0,.08)` */
    val Modal = LxElevation.Modal
}

/**
 * 按 token 档位给元素加阴影。
 *
 * @param level 阴影档位（[LxElevation]）
 * @param shape 与元素自身圆角一致的 Shape（不一致会出现方角阴影）
 */
fun Modifier.lxShadow(level: LxElevation, shape: Shape = RectangleShape): Modifier =
    if (level == LxElevation.None) this
    else shadow(
        elevation = level.elevation,
        shape = shape,
        clip = false,
        ambientColor = level.color,
        spotColor = level.color,
    )
