// 页面共用列表小件（ListKit）—— 会话页 / 连接页 / 设置页对齐原型的组卡形态。
//
// 原型规格（原型页 12/13 的 `.rows-card` / `.row` 家族）：
//   * RowsCard：Surface 底 + 1px --line 边 + 圆角 16 + 双层柔影（y1 小 + y20 大）
//     + 外边距 10dp；行间分隔线**通宽**（原型 `.row + .row` 无缩进）。
//   * SectionHeader（`.sect`）：11sp / 700 / 字距 0.1em / --fg3、上留白 18dp；
//     尾插槽给「新对话」等行内动作。
//   * InfoLine：页面底部提示行 —— 12.5sp / --fg2，可携带 AnnotatedString 强调段
//    （关键词加粗 / 变色），不再是 10.5sp 的浅灰小字。
//
// 原先 RowsCard/RowDivider/SectionHeader 私有在 SessionsScreen、SettingRows 私有在
// SettingsScreen（同款样式各写一份）——抽到这里一处共用，规格修正只改一份。
package com.moyunteng.lxcode.remote.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.ReadOnlyComposable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import androidx.compose.ui.unit.sp
import com.moyunteng.lxcode.design.token.Lx

/** 组卡圆角 16dp（原型 `.rows-card` 2026-12 规格修正：12→16）。 */
internal val RowsCardShape
    @Composable @ReadOnlyComposable get() = androidx.compose.foundation.shape.RoundedCornerShape(16.dp)

/** 组卡双层柔影：ey1 = 近距小阴影（y 1 / blur 3），ey20 = 远距大柔影（y 20 / blur 40）。 */
internal object LxCardShadow {
    /** 近距小阴影的 elevation（y1 档） */
    val ey1 = 3.dp

    /** 远距大柔影的 elevation（y20 档） */
    val ey20 = 20.dp
}

/**
 * 行组卡（原型 `.rows-card`，全页面统一规格）：
 * 外边距 10dp + Surface 底 + 1px --line 边 + 圆角 16 + 双层柔影。
 * 双层阴影用两次 Modifier.shadow 叠加（Compose 的 shadow 不吃双段 box-shadow；
 * 按从大到小的顺序叠：远距柔影在下、近距锐影在上）。
 */
@Composable
internal fun RowsCard(
    modifier: Modifier = Modifier,
    content: @Composable ColumnScope.() -> Unit,
) {
    Column(
        modifier
            .padding(horizontal = 10.dp)
            .fillMaxWidth()
            .shadow(
                elevation = LxCardShadow.ey20,
                shape = RowsCardShape,
                clip = false,
                ambientColor = Lx.shadow.Card,
                spotColor = Lx.shadow.Card,
            )
            .shadow(
                elevation = LxCardShadow.ey1,
                shape = RowsCardShape,
                clip = false,
                ambientColor = Lx.shadow.Card,
                spotColor = Lx.shadow.Card,
            )
            .clip(RowsCardShape)
            .background(Lx.colors.Surface)
            .border(1.dp, Lx.colors.Border, RowsCardShape),
        content = content,
    )
}

/**
 * 行间分隔线（原型 `.rows-card .row + .row`）：**通宽**（2026-12 规格修正——
 * 不再按头像缩进；行内 padding 由 [RowDividerInset] 的调用方行自己承担）。
 */
@Composable
internal fun RowDivider() {
    Box(
        Modifier
            .fillMaxWidth()
            .height(1.dp)
            .background(Lx.colors.Border),
    )
}

/**
 * 分组标题（原型 `.sect`）：11sp / 700 / 字距 0.1em / --fg3，上留白 18dp、下 8dp。
 * [trailing] 尾插槽 —— 「新对话」「扫码配对」这类行内动作（替代原 NavRow 独立行）。
 */
@Composable
internal fun SectionHeader(
    title: String,
    modifier: Modifier = Modifier,
    topPadding: Dp = 18.dp,
    trailing: (@Composable () -> Unit)? = null,
) {
    Row(
        modifier
            .fillMaxWidth()
            .padding(start = 20.dp, end = 12.dp, top = topPadding, bottom = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = title.uppercase(),
            // 原型 `.sect`：font-size var(--t-xs)=11px / 700 / 字距 .1em
            //（2026-12 扫尾条目 33：10.5→11 对齐 --t-xs 刻度）
            style = TextStyle(
                fontFamily = Lx.type.Sans,
                fontSize = 11.sp,
                fontWeight = FontWeight.Bold,
                letterSpacing = 0.1.em,
            ),
            color = Lx.colors.FgFaint,
            modifier = Modifier.weight(1f),
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        if (trailing != null) trailing()
    }
}

/**
 * 页面底部提示行（原型 `.infoline`）：12.5sp / --fg2 / 行高 1.5；
 * [text] 直接吃 AnnotatedString —— 强调段（关键词 600 字重 / 换色）由调用方拼装。
 */
@Composable
internal fun InfoLine(
    text: AnnotatedString,
    modifier: Modifier = Modifier,
) {
    Text(
        text = text,
        style = Lx.type.BodySmall.copy(
            fontSize = 12.5.sp,
            fontWeight = FontWeight.Normal,
        ),
        color = Lx.colors.FgMuted,
        // 原型 `.infoline { padding: 2px 20px 10px }`（2026-12 扫尾条目 30：
        // 上 2 / 左右 20 / 下 10——旧值 4/12 侧距不足，底部不留白）
        modifier = modifier.padding(top = 2.dp, start = 20.dp, end = 20.dp, bottom = 10.dp),
    )
}

/** InfoLine 的纯文本便捷入口。 */
@Composable
internal fun InfoLine(
    text: String,
    modifier: Modifier = Modifier,
) = InfoLine(AnnotatedString(text), modifier)
