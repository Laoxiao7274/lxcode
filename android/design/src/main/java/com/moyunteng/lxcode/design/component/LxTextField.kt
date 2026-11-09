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
 * 状态：默认边框 `--line2`，聚焦 `--fg3`（原型 `.field input:focus { border-color: var(--fg3) }`）。
 *
 * 移动端规格（原型 `.field input`）：soft 底 + 1px --line2 边 + 圆角 12（--r-md）+
 * padding 10/12 + 14sp 文字——「soft 底浅边」是移动端输入控件的统一语言，
 * 与列表行（--soft hover）、分段容器（--soft）同一套。
 *
 * 桌面端原型源：`.field input { border: 1px solid var(--line2); background: var(--soft);
 * border-radius: var(--r-md); padding: 10px 12px; font-size: var(--t-md) }`。
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

    // 聚焦才提亮边框：原型 `.field input:focus { border-color: var(--fg3) }`（主题的
    // focus 边框色，随变体/色温切换）
    val borderColor = when {
        focused -> Lx.colors.FgFaint
        else -> Lx.colors.BorderStrong
    }
    val minHeight: Dp = if (singleLine) 40.dp else 110.dp

    Row(
        modifier = modifier
            .fillMaxWidth()
            .heightIn(min = minHeight)
            // 原型 `.field input`：`background: var(--soft); border-radius: var(--r-md)`（12px）
            .background(Lx.colors.BorderSoft, Lx.radius.CardShape)
            .border(1.dp, borderColor, Lx.radius.CardShape)
            .padding(horizontal = Lx.space.s12, vertical = 10.dp), // 原型：10px 12px
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(modifier = Modifier.weight(1f)) {
            if (value.isEmpty() && placeholder.isNotEmpty()) {
                Text(
                    text = placeholder,
                    // 原型 `.field input`：font-size var(--t-md) = 14px
                    style = Lx.type.Body.copy(fontSize = Lx.type.Size14),
                    color = Lx.colors.FgFaint,
                    maxLines = 1,
                )
            }
            BasicTextField(
                value = value,
                onValueChange = onValueChange,
                enabled = enabled,
                singleLine = singleLine,
                // 原型 `.field input`：font-size 14px / --fg
                textStyle = Lx.type.Body.copy(fontSize = Lx.type.Size14, color = Lx.colors.Fg),
                cursorBrush = SolidColor(Lx.colors.Fg),
                interactionSource = interaction,
                keyboardOptions = KeyboardOptions(imeAction = if (singleLine) ImeAction.Done else ImeAction.Default),
                modifier = Modifier.fillMaxWidth(),
            )
        }
        if (trailing != null) trailing()
    }
}
