// LxBottomSheet —— 底部弹层（移动端惯例的 picker 容器，替代锚定 Popup 菜单）。
//
// 逐项对齐原型 temp/android-redesign/index.html 的 `.sheet-wrap/.sheet`（SHEETS 容器）：
//  * 两段式打开动画：原型注释明说「display:none→block 与 transform/opacity 同帧
//    变化会吞掉 transition 起点（首次打开瞬现）」——先挂 .on 提交布局、再挂 .in。
//    Compose 的等价坑是挂载帧与动画起点同帧（AnimatedVisibility 从中间值起跳）。
//    这里用 Animatable 显式驱动：挂载帧先以离屏态绘制（progress=0 即 translateY
//    106%），LaunchedEffect 下一帧才 animateTo(1)，与原型的 reflow 两段式逐帧等价；
//  * 遮罩：rgba(10,10,12,.44) 随 progress 淡入（原型 transition opacity .22s）；点遮罩关闭；
//  * 面板：左右下 10dp、圆角 14（--r-xl）、Surface 底 + 1px Border + 上浮投影
//    （0 -10px 44px 量级）、max-height 72% 屏高、grip 把手（38×4 圆角 2）；
//  * 下拉关闭（跟手）：竖直拖拽位移实时跟手，松手 ≥64dp 即关闭、否则回弹；
//  * 关闭四路：点遮罩 / 关闭钮 / 返回键（调用方接 backInterceptor）/ 下拉越阈。
// 退场：面板回落 + 遮罩淡出，200ms power2.in 型（LxMotion.EasingExit）。
package com.moyunteng.lxcode.remote.ui

import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectVerticalDragGestures
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.moyunteng.lxcode.design.motion.LocalLxMotionEnabled
import com.moyunteng.lxcode.design.token.Lx
import com.moyunteng.lxcode.design.token.LxMotion

/** 面板 Shape —— 原型 `.sheet { border-radius: var(--r-xl) }`（= 14px 档）。 */
internal val SheetShape = RoundedCornerShape(14.dp)

/** 遮罩色 —— 原型 `.sheet-mask { background: rgba(10,10,12,.44) }`。 */
internal val SheetMaskColor = Color(0f, 0f, 0f, 0.44f)

/** 下拉关闭的位移阈值（原型 `dy > 64`）。 */
internal val DragCloseThreshold = 64.dp

/**
 * 底部弹层容器。
 *
 * @param visible 是否打开（false 走退场动画，跑完卸载内容）
 * @param onDismiss 关闭请求（点遮罩 / 下拉越阈 / 关闭钮）；调用方把它接回自己的状态位
 * @param title 标题（`.shead .st`；null = 不渲染头部行——自带头部的调用方传 null）
 * @param subtitle 标题左侧说明（`.shead .sub`）
 * @param showClose 头部右侧关闭钮（原型 `.sclose`）
 * @param content 面板内容（`.sbody`；调用方自行放滚动列）
 */
@Composable
fun LxBottomSheet(
    visible: Boolean,
    onDismiss: () -> Unit,
    modifier: Modifier = Modifier,
    title: String? = null,
    subtitle: String? = null,
    showClose: Boolean = true,
    content: @Composable ColumnScope.() -> Unit,
) {
    // mounted：退场动画跑完才卸载（对齐 PickerMenus.AnchorMenu 的 mounted 纪律——
    // 条件渲染是瞬灭，用户看不见退场过程）。
    var mounted by remember { mutableStateOf(visible) }
    if (visible) mounted = true
    if (!mounted) return

    val motion = LocalLxMotionEnabled.current
    val density = LocalDensity.current

    // 两段式：挂载帧 progress=0（面板离屏 + 遮罩全透明），下一帧 animateTo(1)。
    val progress = remember { Animatable(if (visible) 0f else 1f) }
    // 跟手位移（dp 值）：拖拽期间 ≥ 0，松手清 0（回落）或关闭。
    val dragDp = remember { mutableFloatStateOf(0f) }
    var sheetHeightPx by remember { mutableStateOf(0) }

    LaunchedEffect(visible, motion) {
        when {
            // 门控关闭（reduced-motion）：直接落终态/终离场（对齐原型 @media reduced-motion）
            !motion -> {
                progress.snapTo(if (visible) 1f else 0f)
                if (!visible) mounted = false
            }
            visible -> {
                progress.snapTo(0f)
                progress.animateTo(1f, tween(280, easing = LxMotion.Easing))
            }
            else -> {
                progress.animateTo(0f, tween(200, easing = LxMotion.EasingExit))
                mounted = false
            }
        }
    }

    val p = progress.value
    Box(
        modifier
            .fillMaxSize()
            // 点遮罩关闭（原型 .sheet-mask 委托关闭）
            .clickable(
                interactionSource = remember { MutableInteractionSource() },
                indication = null,
                onClick = onDismiss,
            ),
    ) {
        // 遮罩层：alpha = 0.44 × progress（淡入/淡出跟随面板）
        // 色 = 原型 .sheet-mask rgba(10,10,12,.44) 的基色（alpha 由 alpha() 修饰符承担）
        Box(
            Modifier
                .fillMaxSize()
                .alpha(0.44f * p)
                .background(Color(0xFF0A0A0C)),
        )
        Column(
            Modifier
                .align(Alignment.BottomCenter)
                .padding(horizontal = 10.dp, vertical = 10.dp)
                .fillMaxWidth()
                .widthIn(max = 520.dp)
                .onSizeChanged { sheetHeightPx = it.height }
                .graphicsLayer {
                    // 面板位移 = 离屏量 × (1-p) + 跟手位移（px）
                    val offscreen = 1.06f * sheetHeightPx * (1f - p)
                    translationY = offscreen + dragDp.value * density.density
                }
                .shadow(
                    elevation = 24.dp,
                    shape = SheetShape,
                    clip = false,
                    ambientColor = Lx.shadow.Float,
                    spotColor = Lx.shadow.Float,
                )
                .clip(SheetShape)
                .background(Lx.colors.Surface)
                .border(1.dp, Lx.colors.Border, SheetShape)
                // 下拉关闭（跟手）：detectVerticalDragGestures 在无竞争时接管——
                // 内容区有 LazyColumn/verticalScroll 时滚动优先，把手与无滚动内容走拖拽
                .pointerInput(visible, motion) {
                    detectVerticalDragGestures(
                        onDragStart = { dragDp.value = 0f },
                        onVerticalDrag = { change, dragAmount ->
                            change.consume()
                            dragDp.value = (dragDp.value + dragAmount / density.density).coerceAtLeast(0f)
                        },
                        onDragEnd = {
                            val t = dragDp.value
                            dragDp.value = 0f
                            if (t >= DragCloseThreshold.value) onDismiss()
                        },
                        onDragCancel = { dragDp.value = 0f },
                    )
                },
        ) {
            // grip 把手（原型 .grip：38×4 圆角 2、上 9dp）
            Box(
                Modifier
                    .padding(top = 9.dp)
                    .align(Alignment.CenterHorizontally)
                    .widthIn(min = 38.dp)
                    .height(4.dp)
                    .clip(RoundedCornerShape(2.dp))
                    .background(Lx.colors.BorderStrong),
            )
            if (title != null) {
                Row(
                    Modifier
                        .fillMaxWidth()
                        .padding(start = 20.dp, end = 10.dp, top = 7.dp, bottom = 4.dp),
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    Text(
                        text = title,
                        style = TextStyle(
                            fontFamily = Lx.type.Sans,
                            fontSize = 14.sp,
                            fontWeight = FontWeight.Bold,
                        ),
                        color = Lx.colors.Fg,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                        modifier = Modifier.weight(1f),
                    )
                    if (subtitle != null) {
                        Text(
                            text = subtitle,
                            style = TextStyle(
                                fontFamily = Lx.type.Sans,
                                fontSize = 10.5.sp,
                                fontWeight = FontWeight.Medium,
                            ),
                            color = Lx.colors.FgFaint,
                        )
                    }
                    if (showClose) {
                        Box(
                            Modifier
                                .size(28.dp)
                                .clip(RoundedCornerShape(14.dp))
                                .clickable(
                                    interactionSource = remember { MutableInteractionSource() },
                                    indication = null,
                                    onClick = onDismiss,
                                ),
                            contentAlignment = Alignment.Center,
                        ) {
                            Icon(
                                imageVector = Icons.Filled.Close,
                                contentDescription = "关闭",
                                tint = Lx.colors.FgMuted,
                                modifier = Modifier.size(15.dp),
                            )
                        }
                    }
                }
            }
            // sbody：调用方内容（通常 Column(verticalScroll) / LazyColumn）
            Column(
                Modifier
                    .fillMaxWidth()
                    .heightIn(max = sheetMaxHeight())
                    .padding(bottom = 12.dp),
                content = content,
            )
        }
    }
}

/** 面板最大高度 —— 原型 `.sheet { max-height: 72% }`（以屏高 72% 计）。 */
@Composable
private fun sheetMaxHeight(): Dp {
    val config = LocalConfiguration.current
    return (config.screenHeightDp * 0.72f).dp
}
