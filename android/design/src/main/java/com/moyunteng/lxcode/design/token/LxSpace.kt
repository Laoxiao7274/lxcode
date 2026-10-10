// lxcode 设计 token · 间距
//
// 桌面端 `--space` 不是 CSS 变量，是散落在各处的 px 字面量（de-facto 刻度）。
// 这里把实际用到的 9 档固化成 token，常量名直接带数值 —— 调用点看到 `Lx.space.s12`
// 就知道它对应桌面端的 `12px`，追溯不需要查表。
// 单位是 dp（密度无关像素），安卓端 1dp 的视觉尺寸对齐桌面端 1px（同为 ~1/160 英寸基准）。
package com.moyunteng.lxcode.design.token

import androidx.compose.ui.unit.dp

object LxSpace {
    /** 2dp —— 桌面端 2px：图标与文字之间的微调间隙 */
    val s2 = 2.dp

    /** 4dp —— 桌面端 4px：最小刻度（药丸内边距、紧密堆叠） */
    val s4 = 4.dp

    /** 6dp —— 桌面端 6px：按钮组间距、药丸水平内边距 */
    val s6 = 6.dp

    /** 8dp —— 桌面端 8px：列表行内边距、常规堆叠 */
    val s8 = 8.dp

    /** 10dp —— 桌面端 10px：输入框内边距、行内元素间距 */
    val s10 = 10.dp

    /** 12dp —— 桌面端 12px：卡片内边距、区块内间距 */
    val s12 = 12.dp

    /** 16dp —— 桌面端 16px：区块之间的间距 */
    val s16 = 16.dp

    /** 20dp —— 桌面端 20px：大区块间距 */
    val s20 = 20.dp

    /** 24dp —— 桌面端 24px：页面级边距 */
    val s24 = 24.dp

    /** 触摸目标下限 —— 安卓无障碍推荐 48dp；本设计系统的交互控件不小于 44dp */
    val touchTarget = 44.dp
}
