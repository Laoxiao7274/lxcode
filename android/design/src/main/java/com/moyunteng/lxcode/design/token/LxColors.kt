// lxcode 设计 token · 颜色
//
// 唯一事实源 = 桌面端 `frontend/src/styles/base.css` 的 `:root`（agent-console-v3 设计语言）。
// 安卓端不发明第二套视觉语言：白底近黑字、发丝边框、黑主按钮，绿色只作成功语义不做装饰。
// 每个常量的 KDoc 都标注它对应桌面端的哪个 CSS 变量或字面量——任何一个值都能双向追溯。
// 改色先改桌面端，再同步到这里；不要在这里「调一下更好看」。
package com.moyunteng.lxcode.design.token

import androidx.compose.ui.graphics.Color

object LxColors {

    // ===== :root 十四色（base.css 里是裸名，无前缀）=====

    /** 页面底色 —— CSS `--bg: #ffffff` */
    val Bg = Color(0xFFFFFFFF)

    /** 侧栏/次级面板底色 —— CSS `--bg-side: #f9f9f9` */
    val BgSide = Color(0xFFF9F9F9)

    /** 卡片、浮层、输入框底色 —— CSS `--surface: #ffffff` */
    val Surface = Color(0xFFFFFFFF)

    /** 主文字、黑主按钮底 —— CSS `--fg: #0d0d0d` */
    val Fg = Color(0xFF0D0D0D)

    /** 次要文字（副标题、ghost 按钮文字） —— CSS `--fg-muted: #6e6e80` */
    val FgMuted = Color(0xFF6E6E80)

    /** 三级文字（占位符、分组标题、空态文案） —— CSS `--fg-faint: #8e8ea0` */
    val FgFaint = Color(0xFF8E8EA0)

    /** 发丝边框（卡片、分隔线） —— CSS `--border: #ececec` */
    val Border = Color(0xFFECECEC)

    /** 控件边框（输入框、描边次按钮） —— CSS `--border-strong: #e5e5e5` */
    val BorderStrong = Color(0xFFE5E5E5)

    /** 极浅填充（列表行 hover、次级药丸底） —— CSS `--border-soft: #f4f4f5` */
    val BorderSoft = Color(0xFFF4F4F5)

    /** 成功语义（只作语义，不做装饰） —— CSS `--success: #10a37f` */
    val Success = Color(0xFF10A37F)

    /** 危险语义（删除/拒绝/失败） —— CSS `--danger: #e02e2a` */
    val Danger = Color(0xFFE02E2A)

    /** 终端/代码块底 —— CSS `--term-bg: #1a1a1a` */
    val TermBg = Color(0xFF1A1A1A)

    /** 终端正文 —— CSS `--term-fg: #d4d4d4` */
    val TermFg = Color(0xFFD4D4D4)

    /** 终端次要文字（cwd、提示符说明） —— CSS `--term-dim: #8e8e96` */
    val TermDim = Color(0xFF8E8E96)

    // ===== 未 token 化但高频的硬编码色（桌面端散落字面量，这里收进色板）=====

    /** 用户气泡底 —— thread.css `.msg-user { background: #f1f1f3 }` */
    val BubbleUser = Color(0xFFF1F1F3)

    /** 代码/终端卡底（`--code-bg` 的回落值） —— thread.css `background: var(--code-bg, #f4f5f7)` */
    val CodeBg = Color(0xFFF4F5F7)

    /** focus 边框（输入框、下拉触发器） —— agents.css / catalog.css `border-color: #a8a8b0` */
    val FocusRing = Color(0xFFA8A8B0)

    /** ghost 按钮 hover 边框 —— agents.css `border-color: #c9c9cf` */
    val BorderGhostHover = Color(0xFFC9C9CF)

    /** chip hover 边框 —— agents.css / catalog.css `border-color: #c2c2c8` */
    val BorderChipHover = Color(0xFFC2C2C8)

    /** 卡片 hover 边框 —— catalog.css `border-color: #d4d4d8` */
    val BorderCardHover = Color(0xFFD4D4D8)

    /** 运行中/警示琥珀 —— panels.css `--turn-run: #d97706` */
    val Amber = Color(0xFFD97706)

    /** 琥珀药丸文字（比 #d97706 更深一档，保证白底上可读） —— agents.css `.ag-pill.warn { color: #9a6400 }` */
    val AmberInk = Color(0xFF9A6400)

    // ===== 派生色（低饱和底 = 语义色 × 固定透明度，桌面端同款做法，不是新色值）=====

    /** 成功药丸底 —— agents.css `.ag-pill.risk-low { background: rgba(16,163,127,.1) }` */
    val SuccessSoft = Success.copy(alpha = 0.10f)

    /** 危险药丸底 —— agents.css `.ag-pill.risk-high { background: rgba(224,46,42,.08) }` */
    val DangerSoft = Danger.copy(alpha = 0.08f)

    /** 琥珀药丸底 —— 对齐 `.ag-pill.warn` 的 rgba(230,162,60,.14) 做法，改用琥珀 token 派生 */
    val AmberSoft = Amber.copy(alpha = 0.12f)

    /** 成功文字（比 --success 深一档，白底上可读） —— agents.css `.ag-pill.risk-low { color: #0f7a5c }` */
    val SuccessInk = Color(0xFF0F7A5C)

    /** 涟漪/按压指示 —— 取 --fg 的低透明度，桌面端 hover 填充是 rgba(26,26,26,6%~10%) */
    val Ripple = Color(0x14000000)
}
