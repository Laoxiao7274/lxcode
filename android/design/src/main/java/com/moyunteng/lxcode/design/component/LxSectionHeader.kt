// lxcode 组件 · LxSectionHeader（分组小标题）
package com.moyunteng.lxcode.design.component

import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import com.moyunteng.lxcode.design.token.Lx

/**
 * lxcode 分组小标题 —— 展示页/设置页用来分段的一行小字。
 *
 * 变体：只有一档。样式 = 11sp / 600 / `--fg-muted` / 大写 / 0.07em 字距，
 * 上下留白对齐桌面端 `.mset-connect-group-title { padding: 6px 8px 4px }`。
 *
 * 桌面端对应：`.mset-connect-group-title` / `.mp-group-title`
 *（桌面端是 10px / `--fg-faint`；移动端上浮一档到 11sp / `--fg-muted`，
 * 因为 10px 的浅灰在手机观看距离下几乎读不出来）。
 *
 * @param title 小标题文本（调用方给中文即可，uppercase 只影响拉丁字母）
 * @param modifier 外部 Modifier
 * @param topPadding 上留白（默认 6dp = 桌面端 6px）
 * @param bottomPadding 下留白（默认 4dp = 桌面端 4px）
 */
@Composable
fun LxSectionHeader(
    title: String,
    modifier: Modifier = Modifier,
    topPadding: Dp = Lx.space.s6,
    bottomPadding: Dp = Lx.space.s4,
) {
    Text(
        text = title.uppercase(),
        modifier = modifier.padding(
            start = Lx.space.s8,
            end = Lx.space.s8,
            top = topPadding,
            bottom = bottomPadding,
        ),
        style = Lx.type.SectionTitle,
        maxLines = 1,
        overflow = TextOverflow.Ellipsis,
    )
}
