// lxcode 组件 · LxEmptyState（空态）
package com.moyunteng.lxcode.design.component

import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.moyunteng.lxcode.design.token.Lx

/**
 * lxcode 空态 —— 列表/面板没有内容时的居中提示。
 *
 * 变体：`icon` 与 `action` 都是可选插槽，由此组成三档 ——
 *  * 纯文案（都不传）；
 *  * 图标 + 文案（传 [icon]）；
 *  * 图标 + 文案 + 按钮（再传 [action]，如「新建会话」）。
 *
 * 桌面端对应：`.git-empty`（居中，30px 内边距）+
 * `.git-empty-icon { width: 44px; height: 44px; border: 1px solid var(--border); border-radius: 12px }` +
 * `.git-empty p { color: var(--fg-muted); font-size: 12px; max-width: 340px }`。
 *
 * @param text 空态主文案（`--fg-faint`）
 * @param modifier 外部 Modifier
 * @param icon 可选图标位（建议 20~22dp 的线描图标）
 * @param description 可选第二行说明（同为 `--fg-faint`，小一档）
 * @param action 可选动作位（一般放一个 [LxButton]）
 */
@Composable
fun LxEmptyState(
    text: String,
    modifier: Modifier = Modifier,
    icon: (@Composable () -> Unit)? = null,
    description: String? = null,
    action: (@Composable () -> Unit)? = null,
) {
    Column(
        modifier = modifier
            .fillMaxWidth()
            .padding(horizontal = Lx.space.s24, vertical = Lx.space.s24),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
    ) {
        if (icon != null) {
            Box(
                modifier = Modifier
                    .padding(bottom = 13.dp) // 桌面端 `.git-empty-icon { margin-bottom: 13px }`
                    .size(44.dp) // 桌面端 44×44
                    .border(1.dp, Lx.colors.Border, Lx.radius.CardShape),
                contentAlignment = Alignment.Center,
            ) {
                icon()
            }
        }

        Text(
            text = text,
            style = Lx.type.BodyMuted.copy(color = Lx.colors.FgFaint), // 空态主文案 = --fg-faint
            textAlign = TextAlign.Center,
            modifier = Modifier.widthIn(max = 340.dp), // 桌面端 `max-width: 340px`
        )

        if (description != null) {
            Text(
                text = description,
                style = Lx.type.BodySmall,
                textAlign = TextAlign.Center,
                modifier = Modifier
                    .padding(top = Lx.space.s6)
                    .widthIn(max = 340.dp),
            )
        }

        if (action != null) {
            Box(modifier = Modifier.padding(top = Lx.space.s16)) {
                action()
            }
        }
    }
}
