// lxcode 设计 token · 图标雪碧表
//
// 原型（temp/android-redesign/index.html）的 SVG 图标逐个转 Compose ImageVector：
// viewBox 24 / stroke 2 / round 线帽线拐 / currentColor —— 与原型同一套笔形。
// 用法：`Icon(imageVector = LxIcons.Send, …, tint = …)`；着色统一走 tint（Compose 的
// Icon() 用 tint 实现 currentColor 语义），图标本身不带颜色。
//
// 形态取舍：手写 PathData 而不引 material-icons——material-icons-extended 是 ~10MB
// 级的整包依赖，且原型笔形与 Material 图形并不一一对应，逐个映射反而不可维护。
// 新增图标从原型 SVG 摘 path 填进 [addPath] 即可。
package com.moyunteng.lxcode.design.token

import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.graphics.vector.addPathNodes
import androidx.compose.ui.unit.dp

/** 图标雪碧表（全部 24×24 / stroke 2 / round / currentColor）。 */
object LxIcons {

    /** 建一个 24×24 图标；[d] 为 SVG path 数据（多个子路径用 " M" 分隔，同原型）。 */
    private fun icon(
        name: String,
        vararg d: String,
        filled: Boolean = false,
    ): ImageVector {
        val builder = ImageVector.Builder(
            name = name,
            defaultWidth = 24.dp,
            defaultHeight = 24.dp,
            viewportWidth = 24f,
            viewportHeight = 24f,
        )
        // stroke 图标的画笔色是占位（构建期写死会被 tint 覆盖；Icon() 语义即 currentColor）
        val brush = SolidColor(Color.Black)
        d.forEach { pathData ->
            val nodes = addPathNodes(pathData)
            if (filled) {
                builder.addPath(nodes, fill = brush)
            } else {
                builder.addPath(
                    nodes,
                    stroke = brush,
                    strokeLineWidth = 2f,
                    strokeLineCap = StrokeCap.Round,
                    strokeLineJoin = StrokeJoin.Round,
                )
            }
        }
        return builder.build()
    }

    /** 消息气泡 —— 会话 tab */
    val Chat: ImageVector by lazy {
        icon(
            "LxChat",
            "M21 11.5a8.38 8.38 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.38 8.38 0 0 1-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.38 8.38 0 0 1 3.8-.9h.5a8.48 8.48 0 0 1 8 8v.5z",
        )
    }

    /** 搜索（放大镜） */
    val Search: ImageVector by lazy {
        icon("LxSearch", "M11 19a8 8 0 1 0 0-16 8 8 0 0 0 0 16z", "M21 21l-4.35-4.35")
    }

    /** 设置（齿轮） */
    val Settings: ImageVector by lazy {
        icon(
            "LxSettings",
            "M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6z",
            "M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z",
        )
    }

    /** 连接（服务器堆叠） —— 连接 tab */
    val Server: ImageVector by lazy {
        icon(
            "LxServer",
            "M2 3h20v6H2z",
            "M2 15h20v6H2z",
            "M6 6h.01",
            "M6 18h.01",
        )
    }

    /** 助手（灯泡） —— 助手 tab */
    val Bulb: ImageVector by lazy {
        icon(
            "LxBulb",
            "M9 18h6",
            "M10 22h4",
            "M12 2a7 7 0 0 0-4 12.7c.6.5 1 1.4 1 2.3h6c0-.9.4-1.8 1-2.3A7 7 0 0 0 12 2z",
        )
    }

    /** 发送（纸飞机，斜向上） */
    val Send: ImageVector by lazy {
        icon("LxSend", "M22 2L11 13", "M22 2l-7 20-4-9-9-4 20-7z")
    }

    /** 停止（方块） */
    val Stop: ImageVector by lazy { icon("LxStop", "M7 7h10v10H7z") }

    /** 加号 */
    val Plus: ImageVector by lazy { icon("LxPlus", "M12 5v14", "M5 12h14") }

    /** 返回（左箭头） */
    val Back: ImageVector by lazy { icon("LxBack", "M19 12H5", "M12 19l-7-7 7-7") }

    /** 勾（对勾） */
    val Check: ImageVector by lazy { icon("LxCheck", "M20 6L9 17l-5-5") }

    /** 空心圆（未完成 todo / radio 未选） */
    val Circle: ImageVector by lazy { icon("LxCircle", "M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18z") }

    /** 实心圆点（运行中指示 / radio 选中） */
    val Dot: ImageVector by lazy { icon("LxDot", "M12 12m-5 0a5 5 0 1 0 10 0a5 5 0 1 0-10 0", filled = true) }

    /** 日历 */
    val Calendar: ImageVector by lazy {
        icon(
            "LxCalendar",
            "M5 5h14a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7a2 2 0 0 1 2-2z",
            "M16 3v4",
            "M8 3v4",
            "M3 10h18",
        )
    }

    /** 笔（编辑/重命名） */
    val Pen: ImageVector by lazy { icon("LxPen", "M17 3a2.85 2.85 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5L17 3z") }

    /** 太阳（亮色温） */
    val Sun: ImageVector by lazy {
        icon(
            "LxSun",
            "M12 17a5 5 0 1 0 0-10 5 5 0 0 0 0 10z",
            "M12 1v2", "M12 21v2",
            "M4.22 4.22l1.42 1.42", "M18.36 18.36l1.42 1.42",
            "M1 12h2", "M21 12h2",
            "M4.22 19.78l1.42-1.42", "M18.36 5.64l1.42-1.42",
        )
    }

    /** 月亮（暗色温） */
    val Moon: ImageVector by lazy {
        icon("LxMoon", "M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z")
    }

    /** 暖阳（暖色温：半明半影的太阳） */
    val SunWarm: ImageVector by lazy {
        icon(
            "LxSunWarm",
            "M12 7a5 5 0 0 1 0 10z",
            "M12 7a5 5 0 1 0 0 10 5 5 0 0 0 0-10z",
            "M12 1v2", "M12 21v2",
            "M4.22 4.22l1.42 1.42", "M18.36 18.36l1.42 1.42",
            "M1 12h2", "M21 12h2",
            "M4.22 19.78l1.42-1.42", "M18.36 5.64l1.42-1.42",
        )
    }

    /** 显示器（投屏） */
    val Monitor: ImageVector by lazy {
        icon("LxMonitor", "M2 4h20v12H2z", "M8 20h8", "M12 16v4")
    }

    /** 下箭头 chevron（展开） */
    val ChevronDown: ImageVector by lazy { icon("LxChevronDown", "M6 9l6 6 6-6") }

    /** 右箭头 chevron（进入） */
    val ChevronRight: ImageVector by lazy { icon("LxChevronRight", "M9 18l6-6-6-6") }

    /** 上箭头 chevron（收起） */
    val ChevronUp: ImageVector by lazy { icon("LxChevronUp", "M18 15l-6-6-6 6") }

    /** 关闭（×） */
    val Close: ImageVector by lazy { icon("LxClose", "M18 6L6 18", "M6 6l12 12") }

    /** 复制 */
    val Copy: ImageVector by lazy {
        icon(
            "LxCopy",
            "M9 9h11v11H9z",
            "M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1",
        )
    }

    /** 刷新（重试/重新生成） */
    val Refresh: ImageVector by lazy {
        icon("LxRefresh", "M23 4v6h-6", "M20.49 15a9 9 0 1 1-2.12-9.36L23 10")
    }

    /** 撤回（回撤箭头） */
    val Undo: ImageVector by lazy {
        icon("LxUndo", "M3 7v6h6", "M21 17a8 8 0 0 0-14-5.3L3 13")
    }

    /** 二维码（配对扫码） */
    val Qr: ImageVector by lazy {
        icon(
            "LxQr",
            "M4 4h6v6H4z",
            "M14 4h6v6h-6z",
            "M4 14h6v6H4z",
            "M14 14h2", "M18 14h2",
            "M14 18h2", "M18 18h2",
        )
    }
}
