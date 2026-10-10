// lxcode 设计 token · 字体（字号 / 字重 / 行高 / 字族）
//
// 桌面端字号是散落 px 字面量（9.5/10/10.5/11/11.5/12/12.5/13/14，正文基准 14，11/11.5 最常用）。
// 这里固化成 `SizeN` 刻度 + 语义 TextStyle：调用点写 `Lx.type.label` 而不是拼 fontSize/lineHeight。
// 行高倍数 1.5/1.6/1.71 是桌面端的三档，换算成 sp 后逐条写进 TextStyle。
// 字族：桌面端用 Inter / JetBrains Mono（自托管字体）；安卓端不内置字体资产
//（多 300KB 换不回等价收益），回落到系统 sans / monospace。
package com.moyunteng.lxcode.design.token

import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp

object LxType {

    // ===== 字号刻度（sp，逐值对齐桌面端 px）=====

    /** 9.5sp —— 桌面端 9.5px：最小号（徽标、密集元信息） */
    val Size9_5 = 9.5.sp

    /** 10sp —— 桌面端 10px：分组小标题、轮次徽标 */
    val Size10 = 10.sp

    /** 10.5sp —— 桌面端 10.5px：辅助说明 */
    val Size10_5 = 10.5.sp

    /** 11sp —— 桌面端 11px（最常用之一）：分组标题、行内元信息 */
    val Size11 = 11.sp

    /** 11.5sp —— 桌面端 11.5px（最常用之一）：列表副标题、次要按钮 */
    val Size11_5 = 11.5.sp

    /** 12sp —— 桌面端 12px：等宽正文、小按钮 */
    val Size12 = 12.sp

    /** 12.5sp —— 桌面端 12.5px：按钮默认字号（.fd-btn-p / .fd-btn-g） */
    val Size12_5 = 12.5.sp

    /** 13sp —— 桌面端 13px：输入框、列表行主标题 */
    val Size13 = 13.sp

    /** 14sp —— 桌面端 14px：正文基准（body font-size: 14px） */
    val Size14 = 14.sp

    // ===== 字重（桌面端 400/500/600/700，600 最常用）=====

    /** 400 —— 正文 */
    val W400 = FontWeight.Normal

    /** 500 —— 按钮、列表行主标题 */
    val W500 = FontWeight.Medium

    /** 600 —— 最常用：小标题、药丸、分组标题 */
    val W600 = FontWeight.SemiBold

    /** 700 —— 强调（主徽标） */
    val W700 = FontWeight.Bold

    // ===== 行高倍数（桌面端 1.5 / 1.6 / 1.71）=====

    /** 1.5 —— 紧凑行高（控件内文字） */
    const val LineHeight15 = 1.5f

    /** 1.6 —— 正文行高（body line-height: 1.6） */
    const val LineHeight16 = 1.6f

    /** 1.71 —— 宽松行高（长段落阅读） */
    const val LineHeight171 = 1.71f

    // ===== 字族 =====

    /** 无衬线族 —— 桌面端 `--font-sans`（Inter / Segoe UI / 雅黑）；安卓回落系统 sans */
    val Sans: FontFamily = FontFamily.SansSerif

    /** 等宽族 —— 桌面端 `--font-mono`（JetBrains Mono / Cascadia）；安卓回落系统 monospace */
    val Mono: FontFamily = FontFamily.Monospace

    // ===== 组合样式（token 化的成品，组件直接消费）=====

    /** 正文 —— 14sp / 行高 1.6 / 400 / `--fg` */
    val Body = TextStyle(
        fontFamily = Sans,
        fontSize = Size14,
        lineHeight = (Size14.value * LineHeight16).sp,
        fontWeight = W400,
        color = LxColors.Fg,
    )

    /** 次要正文 —— 14sp / 1.6 / 400 / `--fg-muted` */
    val BodyMuted = TextStyle(
        fontFamily = Sans,
        fontSize = Size14,
        lineHeight = (Size14.value * LineHeight16).sp,
        fontWeight = W400,
        color = LxColors.FgMuted,
    )

    /** 小号正文 —— 12.5sp / 1.5 / 400 / `--fg-muted` */
    val BodySmall = TextStyle(
        fontFamily = Sans,
        fontSize = Size12_5,
        lineHeight = (Size12_5.value * LineHeight15).sp,
        fontWeight = W400,
        color = LxColors.FgMuted,
    )

    /** 输入框文字 —— 13sp / 1.5 / 400 / `--fg`（.fd-input 的 font-size: 13px） */
    val Field = TextStyle(
        fontFamily = Sans,
        fontSize = Size13,
        lineHeight = (Size13.value * LineHeight15).sp,
        fontWeight = W400,
        color = LxColors.Fg,
    )

    /** 列表行主标题 —— 13sp / 1.5 / 500 / `--fg`（.mset-connect-row 的 13px） */    val ListTitle = TextStyle(
        fontFamily = Sans,
        fontSize = Size13,
        lineHeight = (Size13.value * LineHeight15).sp,
        fontWeight = W500,
        color = LxColors.Fg,
    )

    /** 列表行副标题 —— 11.5sp / 1.5 / 400 / `--fg-faint`（.mset-connect-row-tagline） */
    val ListSubtitle = TextStyle(
        fontFamily = Sans,
        fontSize = Size11_5,
        lineHeight = (Size11_5.value * LineHeight15).sp,
        fontWeight = W400,
        color = LxColors.FgFaint,
    )

    /** 按钮文字 —— 12.5sp / 1.5 / 500（.fd-btn-p / .fd-btn-g 的 font-size: 12.5px） */
    val Button = TextStyle(
        fontFamily = Sans,
        fontSize = Size12_5,
        lineHeight = (Size12_5.value * LineHeight15).sp,
        fontWeight = W500,
    )

    /** 分组小标题 —— 11sp / 1.5 / 600 / `--fg-muted`，大写 + 0.07em 字距
     *（桌面端 .mset-connect-group-title 是 10px / --fg-faint，移动端上浮一档到 11 / --fg-muted） */
    val SectionTitle = TextStyle(
        fontFamily = Sans,
        fontSize = Size11,
        lineHeight = (Size11.value * LineHeight15).sp,
        fontWeight = W600,
        color = LxColors.FgMuted,
        letterSpacing = Size11 * 0.07f,
    )

    /** 药丸标签 —— 9.5sp / 1 / 600（.ag-pill 的 font-size: 9.5px / font-weight: 600） */
    val Pill = TextStyle(
        fontFamily = Sans,
        fontSize = Size9_5,
        lineHeight = Size9_5,
        fontWeight = W600,
    )

    /** 徽标计数 —— 10sp / 1 / 600 / 等宽（.turn-badge 的 font-size: 10px / font-mono） */
    val Badge = TextStyle(
        fontFamily = Mono,
        fontSize = Size10,
        lineHeight = Size10,
        fontWeight = W600,
    )

    /** 弹层标题 —— 13.5sp / 1.25 / 500（ApprovalCard `.title` 的 13.5px） */
    val DialogTitle = TextStyle(
        fontFamily = Sans,
        fontSize = 13.5.sp,
        lineHeight = 17.sp,
        fontWeight = W500,
        color = LxColors.Fg,
    )

    /** 等宽正文 —— 12sp / 1.6 / 400 / `--term-fg`（终端块 .cmd 的 12px） */
    val Mono12 = TextStyle(
        fontFamily = Mono,
        fontSize = Size12,
        lineHeight = (Size12.value * LineHeight16).sp,
        fontWeight = W400,
        color = LxColors.TermFg,
    )
}
