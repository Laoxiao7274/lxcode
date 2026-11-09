// SegmentedControl —— 三态分段（设置页色温：亮 / 暗 / 暖），对齐原型 `.seg/.segbtn`。
//
// 容器：--soft 底、圆角 12（--r-md）、padding 3、gap 4；按钮：flex-1、高 36（padding 7/0
// 的实测高）、圆角 8、选中 = Surface 底 + 卡片阴影 + --fg 字色，未选中 --fg2；
// 字号 --t-sm = 12.5sp/600；过渡：background/color .15s（150ms 标准曲线）。
package com.moyunteng.lxcode.remote.ui

import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.moyunteng.lxcode.design.motion.lxAnimateColor
import com.moyunteng.lxcode.design.token.Lx
import com.moyunteng.lxcode.design.token.LxMotion

private val SegContainerShape = RoundedCornerShape(12.dp)
private val SegButtonShape = RoundedCornerShape(8.dp)

/**
 * 分段控件。
 *
 * @param options 选项（id 顺序 = 显示顺序）
 * @param selected 当前选中 id
 * @param onSelect 选中回调
 * @param labelOf id → 文案
 * @param iconOf id → 图标（可选；有则画在文字前，13dp）
 */
@Composable
fun SegmentedControl(
    options: List<String>,
    selected: String,
    onSelect: (String) -> Unit,
    modifier: Modifier = Modifier,
    labelOf: (String) -> String = { it },
    iconOf: ((String) -> ImageVector?)? = null,
) {
    Row(
        modifier
            .clip(SegContainerShape)
            .background(Lx.colors.BorderSoft)
            .padding(3.dp),
        horizontalArrangement = Arrangement.spacedBy(4.dp),
    ) {
        options.forEach { id ->
            SegButton(
                label = labelOf(id),
                icon = iconOf?.invoke(id),
                selected = id == selected,
                onClick = { onSelect(id) },
            )
        }
    }
}

@Composable
private fun RowScope.SegButton(
    label: String,
    icon: ImageVector?,
    selected: Boolean,
    onClick: () -> Unit,
) {
    val interaction = remember { MutableInteractionSource() }
    val bg by lxAnimateColor(
        target = if (selected) Lx.colors.Surface else Color.Transparent,
        durationMillis = 150,
    )
    val fg by lxAnimateColor(
        target = if (selected) Lx.colors.Fg else Lx.colors.FgMuted,
        durationMillis = 150,
    )
    Row(
        Modifier
            .weight(1f)
            .height(36.dp) // 原型 `.segbtn`：padding 7px 0 + 12.5sp 行高的实测高 ≈ 36px
            .clip(SegButtonShape)
            .background(bg)
            .then(
                if (selected) {
                    Modifier.shadow(
                        elevation = 3.dp,
                        shape = SegButtonShape,
                        clip = false,
                        ambientColor = Lx.shadow.Card,
                        spotColor = Lx.shadow.Card,
                    )
                } else {
                    Modifier
                },
            )
            .clickable(
                interactionSource = interaction,
                indication = null,
                onClick = onClick,
            ),
        horizontalArrangement = Arrangement.Center,
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (icon != null) {
            Icon(
                imageVector = icon,
                contentDescription = null,
                tint = fg,
                modifier = Modifier.padding(end = 5.dp).height(13.dp),
            )
        }
        Text(
            text = label,
            // 原型 `.segbtn`：font-size var(--t-sm) = 12.5px / 600
            style = TextStyle(fontFamily = Lx.type.Sans, fontSize = 12.5.sp, fontWeight = FontWeight.SemiBold),
            color = fg,
        )
    }
}
