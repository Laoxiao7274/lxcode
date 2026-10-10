// lxcode 组件 · LxPill / LxBadge（药丸标签 / 徽标）
package com.moyunteng.lxcode.design.component

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp
import com.moyunteng.lxcode.design.token.Lx

/**
 * 药丸标签的语义档。
 *
 * 四档与桌面端的 pill 变体一一对应，颜色全部来自 token。
 */
enum class LxPillVariant {
    /** 中性 —— `--fg-faint` 文字 + `--border-soft` 底（桌面端 `.ag-pill.src`） */
    Muted,

    /** 成功 —— `#0f7a5c` 文字 + `--success` 10% 底（桌面端 `.ag-pill.risk-low`） */
    Success,

    /** 危险 —— `--danger` 文字 + `--danger` 8% 底（桌面端 `.ag-pill.risk-high`） */
    Danger,

    /** 警示 —— 琥珀 `#d97706` 文字 + 12% 底（桌面端 `.ag-pill.warn`，色值统一到 `--turn-run`） */
    Amber,
}

/**
 * lxcode 药丸标签 —— 风险档、来源档、运行态这类短标签。
 *
 * 变体：`muted` / `success` / `danger` / `amber`（见 [LxPillVariant]）。
 *
 * 桌面端对应：`.ag-pill { font-size: 9.5px; font-weight: 600; padding: 1.5px 6px; border-radius: 5px }`，
 * 四档配色分别是 `.ag-pill.src` / `.risk-low` / `.risk-high` / `.warn`。
 *
 * @param text 标签文本
 * @param variant 语义档
 * @param modifier 外部 Modifier
 */
@Composable
fun LxPill(
    text: String,
    variant: LxPillVariant = LxPillVariant.Muted,
    modifier: Modifier = Modifier,
) {
    val (ink, container) = pillColors(variant)

    Text(
        text = text,
        modifier = modifier
            .background(color = container, shape = Lx.radius.PillShape)
            .padding(horizontal = 6.dp, vertical = 1.5.dp), // 桌面端 1.5px 6px
        style = Lx.type.Pill,
        color = ink,
    )
}

/**
 * lxcode 徽标 —— 药丸的「计数/短代号」形态（轮次号、未读数）。
 *
 * 变体：与 [LxPill] 共用四档语义（见 [LxPillVariant]），但等宽字体、数字对齐、
 * 左右内边距更紧、最小宽度 16dp —— 数字列在视觉上要能对齐。
 *
 * 桌面端对应：`.turn-badge { min-width: 16px; padding: 0 3px; border-radius: 4px;
 * background: var(--border-soft); color: var(--fg-faint); font-family: var(--font-mono);
 * font-size: 10px; font-variant-numeric: tabular-nums }`。
 *
 * @param text 徽标文本（一般是一个数字或两三个字符）
 * @param variant 语义档
 * @param modifier 外部 Modifier
 */
@Composable
fun LxBadge(
    text: String,
    variant: LxPillVariant = LxPillVariant.Muted,
    modifier: Modifier = Modifier,
) {
    val (ink, container) = pillColors(variant)

    Row(
        modifier = modifier
            .background(color = container, shape = Lx.radius.PillShape)
            .padding(horizontal = 6.dp)
            .heightIn(16.dp), // 桌面端 `.turn-badge { min-width: 16px }`
        horizontalArrangement = Arrangement.Center,
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(text = text, style = Lx.type.Badge, color = ink)
    }
}

/** 四档语义 → (文字色, 底色)。底色由语义色派生（低透明度），不是新色值。 */
@Composable
private fun pillColors(variant: LxPillVariant): Pair<Color, Color> = when (variant) {
    LxPillVariant.Muted -> Lx.colors.FgFaint to Lx.colors.BorderSoft
    LxPillVariant.Success -> Lx.colors.SuccessInk to Lx.colors.SuccessSoft
    LxPillVariant.Danger -> Lx.colors.Danger to Lx.colors.DangerSoft
    LxPillVariant.Amber -> Lx.colors.Amber to Lx.colors.AmberSoft
}
