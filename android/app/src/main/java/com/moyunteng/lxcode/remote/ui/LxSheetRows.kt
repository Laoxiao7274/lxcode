// LxSheetRows —— bottom sheet 的行构件（模型 / effort / 审批 / 上下文明细共用）。
//
// 逐项对齐原型 `.sheet .sgroup/.srow/.radio/.ctr-row/.ctr-foot`：
//  * 组标题 `.sgroup`：10.5sp/700、字距 .08em、大写、色 --fg3、padding 12/22/5；
//  * 行 `.srow`：左右 10dp 内边距 10/12、圆角 --r-md、选中态 accent-soft 底 +
//    24% accent 边；标题 13sp/600 + 描述 11.5sp/1.45；选中侧勾（非 radio 款）；
//  * 单选档位行：左侧 18dp 圆标 radio（选中 accent 边 + accent 勾）；
//  * 上下文明细条 `.ctr-row`：类别名 104dp + 6dp 条形 + 右侧数值（tabular-nums）；
//    `.ctr-foot` 脚注：上分隔线 + 10.5sp 灰说明。
package com.moyunteng.lxcode.remote.ui

import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import androidx.compose.ui.unit.sp
import com.moyunteng.lxcode.design.token.Lx
import com.moyunteng.lxcode.design.token.LxIcons
import com.moyunteng.lxcode.design.token.LxMotion

/** 行 Shape —— 原型 `.srow { border-radius: var(--r-md) }`（= 10px）。 */
private val SheetRowShape = RoundedCornerShape(10.dp)

/** sheet 组标题（`.sgroup`）。[label] 原型走 text-transform: uppercase——中文无大写，原样。 */
@Composable
internal fun SheetGroupTitle(label: String) {
    Text(
        text = label,
        style = TextStyle(
            fontFamily = Lx.type.Sans,
            fontSize = 10.5.sp,
            fontWeight = FontWeight.Bold,
            letterSpacing = 0.08.em,
        ),
        color = Lx.colors.FgFaint,
        modifier = Modifier.padding(start = 22.dp, end = 22.dp, top = 12.dp, bottom = 5.dp),
    )
}

/**
 * sheet 选择行（`.srow`）：可选左侧 radio 圆标 / 文案（标题+描述）/ 选中侧勾。
 *
 * @param selected 选中态（accent-soft 底 + accent 边 + 侧勾）
 * @param radio true = 左侧 18dp 圆标（effort/审批档的单选款）；false = 无圆标（模型款）
 * @param enabled false = 行内灰掉（如 effort 对当前模型不可用）
 */
@Composable
internal fun SheetSelectRow(
    title: String,
    desc: String,
    selected: Boolean,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    radio: Boolean = false,
    enabled: Boolean = true,
) {
    val interaction = remember { MutableInteractionSource() }
    val alpha = if (enabled) 1f else 0.45f
    Row(
        modifier
            .padding(horizontal = 10.dp)
            .fillMaxWidth()
            .alpha(alpha)
            .clip(SheetRowShape)
            .background(if (selected) Lx.colors.AccentSoft else Color.Transparent)
            .border(
                width = 1.dp,
                color = if (selected) Lx.colors.Accent.copy(alpha = 0.24f) else Color.Transparent,
                shape = SheetRowShape,
            )
            .clickable(
                interactionSource = interaction,
                indication = null,
                enabled = enabled,
                onClick = onClick,
            )
            .padding(horizontal = 12.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        if (radio) {
            // 单选档位圆标（`.radio`：18dp 圆、1.6px 边；选中 accent 边 + accent 勾）
            Box(
                Modifier
                    .size(18.dp)
                    .clip(CircleShape)
                    .border(
                        width = 1.6.dp,
                        color = if (selected) Lx.colors.Accent else Lx.colors.BorderStrong,
                        shape = CircleShape,
                    ),
                contentAlignment = Alignment.Center,
            ) {
                if (selected) {
                    Icon(
                        imageVector = LxIcons.Check,
                        contentDescription = null,
                        tint = Lx.colors.Accent,
                        modifier = Modifier.size(11.dp),
                    )
                }
            }
        }
        Column(Modifier.weight(1f)) {
            Text(
                text = title,
                style = TextStyle(
                    fontFamily = Lx.type.Sans,
                    fontSize = 13.sp,
                    fontWeight = FontWeight.SemiBold,
                ),
                color = if (selected) Lx.colors.Accent else Lx.colors.Fg,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                text = desc,
                style = TextStyle(fontFamily = Lx.type.Sans, fontSize = 11.5.sp),
                color = Lx.colors.FgMuted,
                lineHeight = 11.5.sp * 1.45f,
            )
        }
        if (selected && !radio) {
            Icon(
                imageVector = LxIcons.Check,
                contentDescription = "已选中",
                tint = Lx.colors.Accent,
                modifier = Modifier.size(14.dp),
            )
        }
    }
}

/** 上下文明细条（`.ctr-row`）：类别名 + 条形（占窗口比例）+ 数值。 */
@Composable
internal fun ContextUsageRow(
    label: String,
    tokens: String,
    fraction: Float,
    color: Color,
) {
    val bar by animateFloatAsState(
        targetValue = fraction.coerceIn(0f, 1f),
        animationSpec = tween(LxMotion.DurationLong, easing = LxMotion.Easing),
        label = "ctx-bar",
    )
    Row(
        Modifier
            .fillMaxWidth()
            .padding(horizontal = 22.dp, vertical = 7.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        Text(
            text = label,
            style = TextStyle(fontFamily = Lx.type.Sans, fontSize = 11.5.sp, fontWeight = FontWeight.SemiBold),
            color = Lx.colors.FgMuted,
            modifier = Modifier.width(104.dp),
        )
        Box(
            Modifier
                .weight(1f)
                .height(6.dp)
                .clip(RoundedCornerShape(3.dp))
                .background(Lx.colors.BorderSoft),
        ) {
            Box(
                Modifier
                    .fillMaxWidth(bar)
                    .height(6.dp)
                    .clip(RoundedCornerShape(3.dp))
                    .background(color),
            )
        }
        Text(
            text = tokens,
            style = TextStyle(
                fontFamily = Lx.type.Sans,
                fontSize = 11.5.sp,
                fontWeight = FontWeight.Bold,
                fontFeatureSettings = "tnum",
            ),
            color = Lx.colors.Fg,
            modifier = Modifier.width(62.dp),
            textAlign = androidx.compose.ui.text.style.TextAlign.Right,
        )
    }
}

/** 上下文明细脚注（`.ctr-foot`：上分隔线 + 说明行）。 */
@Composable
internal fun ContextUsageFoot(text: String) {
    Column(
        Modifier
            .padding(horizontal = 10.dp)
            .fillMaxWidth()
            .padding(top = 6.dp),
    ) {
        Box(
            Modifier
                .fillMaxWidth()
                .height(1.dp)
                .background(Lx.colors.Border),
        )
        Text(
            text = text,
            style = TextStyle(fontFamily = Lx.type.Sans, fontSize = 10.5.sp, lineHeight = 10.5.sp * 1.6f),
            color = Lx.colors.FgFaint,
            modifier = Modifier.padding(horizontal = 12.dp, vertical = 10.dp),
        )
    }
}
