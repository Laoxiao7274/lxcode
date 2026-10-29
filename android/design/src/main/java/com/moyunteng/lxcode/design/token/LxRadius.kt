// lxcode 设计 token · 圆角
//
// 桌面端 border-radius 是散落字面量（高频档：7 小按钮 / 9 表单控件 / 12 卡片与弹层 / 999 药丸）。
// 这里把实际出现的 4/5/6/7/8/9/10/12/14 + 药丸固化成 token，并给高频档加语义别名，
// 让调用点写 `Lx.radius.card` 而不是记「卡片是 12」。
package com.moyunteng.lxcode.design.token

import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.ui.unit.dp

object LxRadius {
    /** 4dp —— 桌面端 4px：最小圆角（小徽标、tag） */
    val r4 = 4.dp

    /** 5dp —— 桌面端 5px：药丸（方角款，如 .ag-pill） */
    val r5 = 5.dp

    /** 6dp —— 桌面端 6px：迷你按钮、图标按钮的紧凑档 */
    val r6 = 6.dp

    /** 7dp —— 桌面端 7px：小图标按钮（高频档） */
    val r7 = 7.dp

    /** 8dp —— 桌面端 8px：列表行、小按钮次档 */
    val r8 = 8.dp

    /** 9dp —— 桌面端 9px：表单控件、主/次按钮（高频档） */
    val r9 = 9.dp

    /** 10dp —— 桌面端 10px：终端块、大按钮 */
    val r10 = 10.dp

    /** 12dp —— 桌面端 12px：卡片、弹层（高频档） */
    val r12 = 12.dp

    /** 14dp —— 桌面端 14px：大容器、抽屉 */
    val r14 = 14.dp

    /** 药丸圆角 —— 桌面端 999px（完全圆角，高度的一半以上） */
    val pill = 999.dp

    // ===== 语义别名（调用点用语义，值仍指回上面的刻度）=====

    /** 小图标按钮 —— = r7 */
    val iconButton = r7

    /** 表单控件（输入框/下拉） —— = r9 */
    val field = r9

    /** 卡片与弹层 —— = r12 */
    val card = r12

    // ===== Shape 便捷值（避免每个调用点手写 RoundedCornerShape）=====

    /** 小图标按钮 Shape（7dp） */
    val IconButtonShape = RoundedCornerShape(r7)

    /** 列表行 Shape（8dp） */
    val RowShape = RoundedCornerShape(r8)

    /** 表单控件 Shape（9dp） */
    val FieldShape = RoundedCornerShape(r9)

    /** 卡片 Shape（12dp） */
    val CardShape = RoundedCornerShape(r12)

    /** 药丸 Shape（999dp） */
    val PillShape = RoundedCornerShape(pill)
}
