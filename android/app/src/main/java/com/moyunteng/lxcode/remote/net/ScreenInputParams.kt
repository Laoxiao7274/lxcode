// ScreenInputParams —— screen.input 的事件参数（与 internal/protocol/screen.go
// 的 ScreenInputParams 逐字段对齐；null 字段不进 JSON——后端按 type 取用）。
package com.moyunteng.lxcode.remote.net

data class ScreenInputParams(
    val type: String,
    /** 视频画面归一化坐标（0-1；move/click/dblclick/down/up/scroll 用）。 */
    val x: Double? = null,
    val y: Double? = null,
    /** left / right / middle（空 = left）。 */
    val button: String? = null,
    /** 滚轮增量（scroll 用；正向 = 上滚，单位 = 刻度）。 */
    val dx: Double? = null,
    val dy: Double? = null,
    /** 键名（key 用）：enter/esc/tab/backspace/delete/方向/home/end/pgup/pgdn/
    space/f1-f12/ctrl/alt/shift/win 或单字符。 */
    val key: String? = null,
    /** 组合键：ctrl/alt/shift/win（key 用）。 */
    val modifiers: List<String> = emptyList(),
    /** 注入文本（char 用；Unicode 逐字符）。 */
    val text: String? = null,
    /** 目标显示器（缺省 0 = 投屏的显示器）。 */
    val monitor: Int = 0,
)
