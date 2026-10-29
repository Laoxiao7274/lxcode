// lxcode 组件 · LxTextField（输入框）
package com.moyunteng.lxcode.design.component

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsFocusedAsState
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.moyunteng.lxcode.design.token.Lx

/**
 * lxcode 输入框 —— 单行文本输入（连接地址、Key、搜索）。
 *
 * 变体：
 *  * [singleLine] = false → 多行（等同桌面端 `.fd-textarea`，等宽字体）；
 *  * [trailing] → 尾部插槽（清空按钮、可见性切换）；
 *  * [enabled] = false → 降透明度且不可聚焦。
 *
 * 状态：默认边框 `--border-strong`，聚焦边框 `#a8a8b0`（桌面端 `:focus { border-color: #a8a8b0 }`）。
 *
 * 桌面端对应：`.fd-input { font-size: 13px; color: var(--fg); background: var(--surface);
 * border: 1px solid var(--border-strong); border-radius: 9px; padding: 8px 10px;
 * transition: border-color 120ms }`。
 *
 * @param value 当前文本
 * @param onValueChange 文本变化回调
 * @param modifier 外部 Modifier
 * @param placeholder 占位文案（`--fg-faint`）
 * @param enabled 是否可用
 * @param singleLine 是否单行（默认 true）
 * @param trailing 可选尾部插槽
 */
@Composable
fun LxTextField(
    value: String,
    onValueChange: (String) -> Unit,
    modifier: Modifier = Modifier,
    placeholder: String = "",
    enabled: Boolean = true,
    singleLine: Boolean = true,
    trailing: (@Composable () -> Unit)? = null,
) {
    val interaction = remember { MutableInteractionSource() }
    val focused by interaction.collectIsFocusedAsState()

    // 聚焦才提亮边框：桌面端唯一的输入框状态反馈
    val borderColor = when {
        focused -> Lx.colors.FocusRing // #a8a8b0
        else -> Lx.colors.BorderStrong // #e5e5e5
    }
    val minHeight: Dp = if (singleLine) 38.dp else 110.dp // 桌面端 textarea min-height: 110px

    Row(
        modifier = modifier
            .fillMaxWidth()
            .heightIn(min = minHeight)
            .background(Lx.colors.Surface, Lx.radius.FieldShape)
            .border(1.dp, borderColor, Lx.radius.FieldShape)
            .padding(horizontal = Lx.space.s10, vertical = Lx.space.s8), // 桌面端 8px 10px
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(modifier = Modifier.weight(1f)) {
            if (value.isEmpty() && placeholder.isNotEmpty()) {
                Text(
                    text = placeholder,
                    style = Lx.type.Field,
                    color = Lx.colors.FgFaint,
                    maxLines = 1,
                )
            }
            BasicTextField(
                value = value,
                onValueChange = onValueChange,
                enabled = enabled,
                singleLine = singleLine,
                textStyle = Lx.type.Field,
                cursorBrush = SolidColor(Lx.colors.Fg),
                interactionSource = interaction,
                keyboardOptions = KeyboardOptions(imeAction = if (singleLine) ImeAction.Done else ImeAction.Default),
                modifier = Modifier.fillMaxWidth(),
            )
        }
        if (trailing != null) trailing()
    }
}
