// lxcode 组件 · LxDivider（发丝分隔线）
package com.moyunteng.lxcode.design.component

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.moyunteng.lxcode.design.token.Lx

/**
 * lxcode 分隔线 —— 1dp 的 `--border` 发丝线。
 *
 * 变体：横向（本函数）与竖向（[LxVerticalDivider]）两种。
 *
 * 桌面端对应：`border-bottom: 1px solid var(--border)`（.set-row / .tabbar / 列表组间）。
 *
 * @param modifier 外部 Modifier（宽度与间距由调用方决定）
 * @param thickness 线宽（默认 1dp = 桌面端 1px）
 */
@Composable
fun LxDivider(
    modifier: Modifier = Modifier,
    thickness: Dp = 1.dp,
) {
    Box(
        modifier = modifier
            .fillMaxWidth()
            .height(thickness)
            .background(Lx.colors.Border),
    )
}

/**
 * 竖向分隔线 —— 工具条内两个图标之间的竖线。
 *
 * 桌面端对应：`.tabbar-divider { width: 1px; height: 18px; background: var(--border) }`。
 *
 * @param modifier 外部 Modifier
 * @param height 线高（默认 18dp = 桌面端 18px）
 */
@Composable
fun LxVerticalDivider(
    modifier: Modifier = Modifier,
    height: Dp = 18.dp,
) {
    Box(
        modifier = modifier
            .width(1.dp)
            .height(height)
            .background(Lx.colors.Border),
    )
}
