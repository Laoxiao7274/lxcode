// lxcode 组件 · LxSurface / LxCard（容器）
package com.moyunteng.lxcode.design.component

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsHoveredAsState
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Shape
import androidx.compose.ui.unit.dp
import com.moyunteng.lxcode.design.token.Lx
import com.moyunteng.lxcode.design.token.LxElevation
import com.moyunteng.lxcode.design.token.lxShadow

/**
 * lxcode 基础容器 —— 白底 + 发丝描边 + 圆角 12 的「一块面」。
 *
 * 变体：本组件没有语义变体，只有三个开关：
 *  * [shadow] —— 是否加卡片阴影（默认不加：桌面端纪律是「发丝描边取代阴影浮卡」）；
 *  * [padding] —— 内边距（默认 12dp = 桌面端卡片 12px）；
 *  * [onClick] —— 传了就变成可点面，hover/按压时边框提到 `#d4d4d8`（桌面端 `.ag-card:hover`）。
 *
 * 桌面端对应：`.ag-card { background: var(--surface); border: 1px solid var(--border);
 * border-radius: 12px }` / `.ag-card:hover { border-color: #d4d4d8 }`。
 * 需要卡片语义时优先用 [LxCard]（它就是带卡片阴影的 LxSurface）。
 *
 * @param modifier 外部 Modifier
 * @param padding 内边距
 * @param shape 圆角（默认 12dp 卡片档）
 * @param shadow 阴影档（默认无）
 * @param onClick 可选点击回调；为 null 时不可点
 * @param content 内容
 */
@Composable
fun LxSurface(
    modifier: Modifier = Modifier,
    padding: PaddingValues = PaddingValues(Lx.space.s12),
    shape: Shape = Lx.radius.CardShape,
    shadow: LxElevation = LxElevation.None,
    onClick: (() -> Unit)? = null,
    content: @Composable ColumnScope.() -> Unit,
) {
    val interaction = remember { MutableInteractionSource() }
    val hovered by interaction.collectIsHoveredAsState()

    val borderColor = when {
        onClick == null -> Lx.colors.Border
        hovered -> Lx.colors.BorderCardHover
        else -> Lx.colors.Border
    }

    Column(
        modifier = modifier
            .lxShadow(shadow, shape)
            .clip(shape)
            .background(Lx.colors.Surface)
            .border(1.dp, borderColor, shape)
            .then(
                if (onClick != null) {
                    Modifier.clickable(
                        interactionSource = interaction,
                        indication = null,
                        onClick = onClick,
                    )
                } else {
                    Modifier
                },
            )
            .padding(padding),
        content = content,
    )
}

/**
 * lxcode 卡片 —— [LxSurface] 加一档卡片阴影（0 1px 2px rgba(0,0,0,.03)）。
 *
 * 变体：同 [LxSurface]（[onClick] 传了即可点）。差别只在默认带卡片阴影，
 * 用于「浮在页面上的独立卡片」（如确认门卡片、Agent 卡）。
 *
 * 桌面端对应：`.ag-card { box-shadow: 0 1px 4px rgba(0,0,0,.03) }`。
 *
 * @param modifier 外部 Modifier
 * @param padding 内边距（默认 14dp 水平 / 16dp 垂直 = 桌面端 `.ag-card { padding: 14px 16px }`）
 * @param onClick 可选点击回调
 * @param content 内容
 */
@Composable
fun LxCard(
    modifier: Modifier = Modifier,
    padding: PaddingValues = PaddingValues(horizontal = 14.dp, vertical = 16.dp),
    onClick: (() -> Unit)? = null,
    content: @Composable ColumnScope.() -> Unit,
) {
    LxSurface(
        modifier = modifier,
        padding = padding,
        shape = Lx.radius.CardShape,
        shadow = LxElevation.Card,
        onClick = onClick,
        content = content,
    )
}
