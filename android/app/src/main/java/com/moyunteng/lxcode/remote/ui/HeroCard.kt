// HeroCard —— 助手页头部（对齐原型 `.hero`）：渐变底 + 问候行 + 副行。
//
// `.hero`：padding 18/20/14、底 = --grad-hero（160~165deg 线性渐变，用
// Lx.colors.GradHero 的两档色）、下 1px 边。
// `.hi`：logo 38dp 圆 + 问候语（17sp/800、字距 -.02em）；`.sub`：11.5sp --fg2。
// 本文件顺带落 `.asstcard`（助手会话卡：accent 左标条 + 标题/副行 + 时间）。
package com.moyunteng.lxcode.remote.ui

import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsPressedAsState
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
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import androidx.compose.ui.unit.sp
import com.moyunteng.lxcode.design.component.LxStatus
import com.moyunteng.lxcode.design.component.LxStatusDot
import com.moyunteng.lxcode.design.motion.lxAnimateColor
import com.moyunteng.lxcode.design.token.Lx
import com.moyunteng.lxcode.design.token.LxMotion
import com.moyunteng.lxcode.remote.R

/**
 * 助手页头部 hero 卡。
 *
 * 原型 `.hero`：padding 18/20/14、渐变底、**只画底边**（`border-bottom: 1px solid
 * var(--line)`，不再整圈描边）；渐变止点按变体（A=100%、B=58%、C=62%，原型
 * `--grad-hero` 逐值）。
 *
 * @param greeting 问候语（如「下午好」；原型带 👋 波浪图标——表情随平台字体，不画图标）
 * @param sub 副行（日期/挂起事项摘要）
 * @param gradientStop 渐变第二色止点（0~1；按 LxVariant 给 A=1f/B=0.58f/C=0.62f）
 */
@Composable
fun HeroCard(
    greeting: String,
    sub: String,
    modifier: Modifier = Modifier,
    gradientStop: Float = 1f,
) {
    val colors = Lx.colors.GradHero
    Column(
        modifier
            .fillMaxWidth()
            .background(
                Brush.linearGradient(
                    colorStops = arrayOf(
                        0f to colors[0],
                        gradientStop.coerceIn(0.01f, 1f) to colors[1],
                        1f to colors[1],
                    ),
                ),
            )
            .padding(horizontal = 20.dp, vertical = 18.dp),
        verticalArrangement = Arrangement.spacedBy(4.dp),
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            Box(
                Modifier
                    .size(38.dp)
                    .clip(CircleShape),
                contentAlignment = Alignment.Center,
            ) {
                Image(
                    painter = painterResource(R.drawable.ic_logo),
                    contentDescription = null,
                    contentScale = ContentScale.Crop,
                    modifier = Modifier.size(38.dp),
                )
            }
            Text(
                text = greeting,
                // 原型 `.hero .hi`：font-size var(--t-xl) = 19px / 800 / -.02em
                style = TextStyle(
                    fontFamily = Lx.type.Sans,
                    fontSize = 19.sp,
                    fontWeight = FontWeight.ExtraBold,
                    letterSpacing = (-0.02).em,
                ),
                color = Lx.colors.Fg,
            )
        }
        Text(
            text = sub,
            // 原型 `.hero .sub`：font-size var(--t-sm) = 12.5px / --fg2
            style = TextStyle(fontFamily = Lx.type.Sans, fontSize = 12.5.sp),
            color = Lx.colors.FgMuted,
        )
    }
    // 原型 `.hero { border-bottom: 1px solid var(--line) }`——只画底边
    Box(
        Modifier
            .fillMaxWidth()
            .height(1.dp)
            .background(Lx.colors.Border),
    )
}

/** 助手会话卡（`.asstcard`）：accent 左标条 + 标题/副行 + 时间；整卡可点进对话。 */
@Composable
fun AsstCard(
    title: String,
    sub: String,
    time: String,
    running: Boolean,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val interaction = remember { MutableInteractionSource() }
    val pressed by interaction.collectIsPressedAsState()
    val pressScale by animateFloatAsState(
        targetValue = if (pressed) 0.985f else 1f,
        animationSpec = tween(LxMotion.DurationStandard, easing = LxMotion.Easing),
        label = "asstcard-press",
    )
    Row(
        modifier
            .padding(horizontal = 12.dp, vertical = 5.dp)
            .graphicsLayer {
                scaleX = pressScale
                scaleY = pressScale
            }
            .fillMaxWidth()
            .shadow(
                elevation = 3.dp,
                shape = AsstCardShape,
                clip = false,
                ambientColor = Lx.shadow.Card,
                spotColor = Lx.shadow.Card,
            )
            .clip(AsstCardShape)
            .background(Lx.colors.Surface)
            .border(1.dp, Lx.colors.Border, AsstCardShape)
            .clickable(interactionSource = interaction, indication = null, onClick = onClick)
            // 原型 `.asstcard`：padding 13px 14px（2026-12 规格修正——原无内距，
            // 内容贴边；左标条 3dp 吃掉首列，主列再 14dp 起步）
            .padding(start = 14.dp, end = 14.dp, top = 13.dp, bottom = 13.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        // accent 左标条（`.asstcard::before`：宽 3、上下 12、圆角 3、opacity .85）
        Box(
            Modifier
                .padding(start = 0.dp)
                .height(36.dp)
                .width(3.dp)
                .clip(RoundedCornerShape(3.dp))
                .background(if (running) Lx.colors.Amber else Lx.colors.Accent.copy(alpha = 0.85f)),
        )
        Column(Modifier.weight(1f)) {
            Text(
                text = title,
                style = TextStyle(fontFamily = Lx.type.Sans, fontSize = 13.sp, fontWeight = FontWeight.Bold),
                color = Lx.colors.Fg,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                text = sub,
                style = TextStyle(fontFamily = Lx.type.Sans, fontSize = 11.5.sp),
                color = Lx.colors.FgMuted,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.padding(top = 3.dp),
            )
        }
        if (running) {
            LxStatusDot(status = LxStatus.Run, size = 7.dp)
        }
        Text(
            text = time,
            style = TextStyle(fontFamily = Lx.type.Sans, fontSize = 10.5.sp),
            color = Lx.colors.FgFaint,
        )
    }
}

/** 卡 Shape —— 原型 `.asstcard { border-radius: var(--r-lg) }` = 16px。 */
private val AsstCardShape = RoundedCornerShape(16.dp)
