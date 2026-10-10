// lxcode 组件 · LxRow / LxListItem（列表行）
package com.moyunteng.lxcode.design.component

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsHoveredAsState
import androidx.compose.foundation.interaction.collectIsPressedAsState
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.moyunteng.lxcode.design.motion.lxAnimateColor
import com.moyunteng.lxcode.design.motion.lxPressScale
import com.moyunteng.lxcode.design.token.Lx

/**
 * lxcode 列表行骨架 —— 「前插槽 + 主内容 + 后插槽」的横向布局。
 *
 * 变体：本组件只是布局，没有语义变体；[leading] / [trailing] 都是可选插槽，
 * [content] 拿到剩余空间（`weight(1f)`）。真正的列表行请用 [LxListItem]。
 *
 * 桌面端对应：`.mset-connect-row { display: flex; align-items: center; gap: 10px }`。
 *
 * @param modifier 外部 Modifier
 * @param leading 前插槽（图标/状态点）
 * @param trailing 后插槽（药丸/箭头/开关）
 * @param gap 插槽间距（默认 10dp = 桌面端 10px）
 * @param content 主内容（占满剩余宽度）
 */
@Composable
fun LxRow(
    modifier: Modifier = Modifier,
    leading: (@Composable () -> Unit)? = null,
    trailing: (@Composable () -> Unit)? = null,
    gap: Dp = Lx.space.s10,
    content: @Composable RowScope.() -> Unit,
) {
    Row(
        modifier = modifier.fillMaxWidth(),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(gap),
    ) {
        if (leading != null) leading()
        Row(
            modifier = Modifier.weight(1f),
            verticalAlignment = Alignment.CenterVertically,
            content = content,
        )
        if (trailing != null) trailing()
    }
}

/**
 * lxcode 列表行 —— 会话列表 / 连接列表用的一行（主标题 + 副标题 + 尾部插槽）。
 *
 * 变体：
 *  * 有 [subtitle]：两行式（主标题 13sp/500 + 副标题 11.5sp/`--fg-faint`）；
 *  * 无 [subtitle]：单行式（只有主标题）；
 *  * 传 [onClick]：可点行，按压/hover 时底色变 `--border-soft`；
 *  * [selected]：选中态，底色同样用 `--border-soft`（选中与悬停视觉一致，靠前置状态点区分归属）。
 *
 * 桌面端对应：`.mset-connect-row { padding: 7px 8px; font-size: 13px; border-radius: 8px }` +
 * `.mset-connect-row:hover { background: var(--border-soft) }` +
 * `.mset-connect-row-tagline { color: var(--fg-faint); font-size: 11.5px }`。
 *
 * @param title 主标题
 * @param modifier 外部 Modifier
 * @param subtitle 可选副标题
 * @param leading 可选前插槽
 * @param trailing 可选尾部插槽
 * @param selected 是否选中
 * @param onClick 可选点击回调；为 null 时不可点
 */
@Composable
fun LxListItem(
    title: String,
    modifier: Modifier = Modifier,
    subtitle: String? = null,
    leading: (@Composable () -> Unit)? = null,
    trailing: (@Composable () -> Unit)? = null,
    selected: Boolean = false,
    onClick: (() -> Unit)? = null,
) {
    val interaction = remember { MutableInteractionSource() }
    val hovered by interaction.collectIsHoveredAsState()
    val pressed by interaction.collectIsPressedAsState()

    val filled = selected || hovered || pressed
    // hover / 按压填充过渡 120ms（门控关闭时瞬变）—— 桌面端 `.mset-connect-row { transition: background .12s }`
    val fill by lxAnimateColor(target = if (filled) Lx.colors.BorderSoft else Color.Transparent)

    LxRow(
        modifier = modifier
            .lxPressScale(interaction, enabled = onClick != null)
            .clip(Lx.radius.RowShape) // 桌面端 border-radius: 8px
            .background(fill)
            .heightIn(min = 36.dp) // 触摸目标下限（桌面端行高 30px，手指目标上浮）
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
            .padding(horizontal = Lx.space.s8, vertical = Lx.space.s8),
        leading = leading,
        trailing = trailing,
    ) {
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = title,
                style = Lx.type.ListTitle,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            if (subtitle != null) {
                Text(
                    text = subtitle,
                    style = Lx.type.ListSubtitle,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
    }
}
