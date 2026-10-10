// lxcode 动效层 · 入场 / 交错 / 退场 / 按压微交互
//
// 这一层把桌面端的 keyframes 固化成 Compose 修饰符，每条规格的 KDoc 都写明它对应的
// 桌面端 keyframes 名 + 时长 + 曲线，调用点不需要记数值。
//
// 门控（[LocalLxMotionEnabled]，见 LxMotionGate.kt）在这里是**唯一**的开关：
//  * 入场：关闭时直接落终态（不播、不延迟）；
//  * 退场（[LxCollapseAway]）：关闭时立刻完成（不等动画）。
// 打字机（[rememberStreamReveal]）**不**受门控影响 —— 桌面端注释明说 reduced-motion 不关它。
package com.moyunteng.lxcode.design.motion

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.EnterTransition
import androidx.compose.animation.ExitTransition
import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.AnimationSpec
import androidx.compose.animation.core.Easing
import androidx.compose.animation.core.SpringSpec
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.snap
import androidx.compose.animation.core.tween
import androidx.compose.animation.expandVertically
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.shrinkVertically
import androidx.compose.foundation.interaction.InteractionSource
import androidx.compose.foundation.interaction.collectIsPressedAsState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.State
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.unit.IntSize
import androidx.compose.ui.unit.dp
import com.moyunteng.lxcode.design.token.LxMotion
import kotlinx.coroutines.delay
import kotlin.math.min

/**
 * 一条入场动画规格 —— 对应桌面端一个 keyframes。
 *
 * @param keyframes 桌面端 keyframes 名（报告与 KDoc 的追溯锚点，不参与计算）
 * @param durationMillis 时长（ms）
 * @param easing 曲线（默认全仓唯一标准曲线 `cubic-bezier(.22,1,.36,1)`）
 * @param fromTranslateYDp 入场起始的纵向位移（正值 = 从下方 4dp/8dp 浮上来；负值 = 从上方落下）
 * @param fromTranslateXDp 入场起始的横向位移（正值 = 从左侧滑入）
 * @param fromScale 入场起始缩放（1 = 不缩放）
 * @param alphaFrom 入场起始透明度（桌面端入场基线恒为 0）
 * @param spring 非空时用 spring 替代 tween（回弹档：用户气泡 / 裁决徽标）
 */
data class LxEnterSpec(
    val keyframes: String,
    val durationMillis: Int,
    val easing: Easing = LxMotion.Easing,
    val fromTranslateYDp: Float = 0f,
    val fromTranslateXDp: Float = 0f,
    val fromScale: Float = 1f,
    val alphaFrom: Float = 0f,
    val spring: SpringSpec<Float>? = null,
) {
    /** 本规格对应的动画规格（spring 优先）。 */
    fun animationSpec(): AnimationSpec<Float> =
        spring ?: tween(durationMillis = durationMillis, easing = easing)

    companion object {
        /** `block-in` —— 消息块入场：opacity 0→1 + translateY(4px)→0，320ms 标准曲线 */
        val BlockIn = LxEnterSpec("block-in", 320, fromTranslateYDp = 4f)

        /** `ap-card-in` —— 确认卡入场：同款但 translateY(8px)、380ms */
        val ApCardIn = LxEnterSpec("ap-card-in", 380, fromTranslateYDp = 8f)

        /** `pop-in` —— 浮层：opacity 0→1 + translateY(-4px) + scale(.98)→无变换，160ms */
        val PopIn = LxEnterSpec(
            keyframes = "pop-in",
            durationMillis = 160,
            fromTranslateYDp = -4f,
            fromScale = 0.98f,
        )

        /** `mp-slide-in` —— 左侧滑入：translateX(-10px) + 淡入，200ms */
        val MpSlideIn = LxEnterSpec("mp-slide-in", 200, fromTranslateXDp = -10f)

        /** `settings-in` —— 设置面板 / 大模态：scale(.97) + 淡入，220ms */
        val SettingsIn = LxEnterSpec("settings-in", 220, fromScale = 0.97f)

        /** `settings-mask-in` —— 遮罩：纯淡入 160ms（无位移无缩放） */
        val SettingsMaskIn = LxEnterSpec("settings-mask-in", 160)

        /**
         * 页面切换 —— 底部导航切页时内容淡入 + 轻微上浮，200ms 标准曲线。
         *
         * 桌面端没有对应 keyframes（桌面端是侧栏切路由，无页面切换动画）；
         * 6dp 是规格未给数值时的取档：落在 `block-in` 的 4px 与 `ap-card-in` 的 8px 之间。
         */
        val PageIn = LxEnterSpec("page-in（规格未给数值，取 6dp）", 200, fromTranslateYDp = 6f)

        /** `staggerIn` 的单条目 —— y 8dp + 淡入，300ms 标准曲线（延迟由 [LxStagger] 给） */
        val StaggerItem = LxEnterSpec("staggerIn item", 300, fromTranslateYDp = 8f)

        /**
         * 断线错误条滑入 —— y 8dp + 淡入，220ms 标准曲线。
         *
         * 桌面端没有单独命名的 keyframes（错误条是 `.ag-err-bar` 的行内出现）；
         * 数值取自规格 D 的「错误条滑入（y+8dp 淡入 220ms）」。
         */
        val ErrorBarIn = LxEnterSpec("error-bar-in（规格未给 keyframes 名）", 220, fromTranslateYDp = 8f)

        /** todo 条目入场 —— translateY(-7px) + 淡入，360ms 标准曲线（延迟 = i*50ms） */
        val TodoItemIn = LxEnterSpec("todo-item-in", 360, fromTranslateYDp = -7f)

        /** 用户气泡 / 裁决徽标回弹 —— 桌面端 `back.out(1.8)`：低阻尼 spring，无 tween 时长 */
        val BubbleIn = LxEnterSpec(
            keyframes = "back.out(1.8)（spring）",
            durationMillis = 0,
            fromTranslateYDp = 6f,
            spring = SpringSpec(
                dampingRatio = LxMotion.SpringDampingRatio,
                stiffness = LxMotion.SpringStiffness,
            ),
        )
    }
}

/**
 * 交错入场的延迟表 —— 对齐桌面端 `staggerIn`：
 * 每项延迟 = `min(40ms, 360ms / 条目数)`，于是条目多时总时长封顶 360ms。
 */
object LxStagger {
    /**
     * 第 [index] 项（0 起）的入场延迟（ms）。
     *
     * @param count 本列表的条目总数
     * @param index 本项的序号（0 起）
     */
    fun delayMillis(count: Int, index: Int): Int {
        if (count <= 0 || index <= 0) return 0
        val step = min(LxMotion.StaggerStepMaxMillis, LxMotion.StaggerTotalMaxMillis / count)
        return step * index
    }
}

/**
 * 入场修饰符 —— 把 [LxEnterSpec] 落到 `graphicsLayer` 的 alpha / 位移 / 缩放上。
 *
 * 门控关闭时：直接落终态（不播动画、不延迟），画面第一帧就是最终位置。
 * 动画只在首次出现时播一次；[key] 变化时重播（用于页面切换），[visible] 置 false 时回到起始态。
 *
 * @param spec 入场规格（默认 `block-in`）
 * @param delayMillis 延迟（ms；交错入场用 [LxStagger.delayMillis]）
 * @param key 变化即重播（页面切换传路由）
 * @param visible 是否处于「已出现」态
 */
@Composable
fun Modifier.lxEnter(
    spec: LxEnterSpec = LxEnterSpec.BlockIn,
    delayMillis: Int = 0,
    key: Any? = null,
    visible: Boolean = true,
): Modifier {
    val motion = LocalLxMotionEnabled.current
    val progress = remember(key) { Animatable(if (motion && visible) 0f else 1f) }
    LaunchedEffect(key, visible, motion) {
        when {
            !motion || !visible -> progress.snapTo(if (visible) 1f else 0f)

            else -> {
                progress.snapTo(0f)
                if (delayMillis > 0) delay(delayMillis.toLong())
                progress.animateTo(1f, spec.animationSpec())
            }
        }
    }
    val p = progress.value
    return this.graphicsLayer {
        alpha = spec.alphaFrom + (1f - spec.alphaFrom) * p
        translationY = spec.fromTranslateYDp.dp.toPx() * (1f - p)
        translationX = spec.fromTranslateXDp.dp.toPx() * (1f - p)
        val s = spec.fromScale + (1f - spec.fromScale) * p
        scaleX = s
        scaleY = s
    }
}

/**
 * 交错入场修饰符 —— [lxEnter] 加 [LxStagger] 的延迟（列表逐项入场）。
 *
 * @param count 本列表条目总数
 * @param index 本项序号（0 起）
 * @param spec 单条目规格（默认 `staggerIn` 的 300ms / y+8dp）
 * @param key 变化即重播
 */
@Composable
fun Modifier.lxStaggerEnter(
    count: Int,
    index: Int,
    spec: LxEnterSpec = LxEnterSpec.StaggerItem,
    key: Any? = null,
): Modifier = lxEnter(
    spec = spec,
    delayMillis = LxStagger.delayMillis(count, index),
    key = key,
)

/**
 * 退场容器（`collapseAway`）—— 高度 / 内距 / 外距归零 + 淡出，220ms，`power2.in` 型。
 *
 * 与「条件渲染」的区别：条件渲染是瞬灭（用户看不见过程），这里动画跑完才真正移除。
 * 门控关闭时：立刻完成（无动画直接移除）。
 *
 * @param visible 是否保留
 * @param modifier 外部 Modifier
 * @param enter 入场过渡；默认 null = `collapseAway` 自带的「淡入 + 高度展开」，
 *   传 [EnterTransition.None] 则入场交给内容自己（如错误条用 `lxEnter(ErrorBarIn)` 走 y+8dp 滑入）
 * @param content 内容
 */
@Composable
fun LxCollapseAway(
    visible: Boolean,
    modifier: Modifier = Modifier,
    enter: EnterTransition? = null,
    content: @Composable () -> Unit,
) {
    val motion = LocalLxMotionEnabled.current
    AnimatedVisibility(
        visible = visible,
        modifier = modifier,
        enter = if (!motion) {
            EnterTransition.None
        } else if (enter != null) {
            enter
        } else {
            fadeIn(tween(LxMotion.DurationExit, easing = LxMotion.Easing)) +
                expandVertically(tween(LxMotion.DurationExit, easing = LxMotion.Easing), expandFrom = Alignment.Top)
        },
        exit = if (motion) {
            fadeOut(tween(LxMotion.DurationExit, easing = LxMotion.EasingExit)) +
                shrinkVertically(tween(LxMotion.DurationExit, easing = LxMotion.EasingExit), shrinkTowards = Alignment.Top)
        } else {
            ExitTransition.None
        },
        label = "lx-collapse-away",
    ) {
        content()
    }
}

/**
 * 展开 / 折叠容器 —— 高度 + 淡入淡出，两个方向都是 220ms 标准曲线（思考块展开折叠用）。
 *
 * 与 [LxCollapseAway] 的差别：这里退场也用标准曲线（用户主动折叠，不是「收起消失」）。
 *
 * @param visible 是否展开
 * @param modifier 外部 Modifier
 * @param content 内容
 */
@Composable
fun LxExpandable(
    visible: Boolean,
    modifier: Modifier = Modifier,
    content: @Composable () -> Unit,
) {
    val motion = LocalLxMotionEnabled.current
    val fadeSpec = tween<Float>(LxMotion.DurationExit, easing = LxMotion.Easing)
    val sizeSpec = tween<IntSize>(LxMotion.DurationExit, easing = LxMotion.Easing)
    AnimatedVisibility(
        visible = visible,
        modifier = modifier,
        enter = if (motion) fadeIn(fadeSpec) + expandVertically(sizeSpec, expandFrom = Alignment.Top) else EnterTransition.None,
        exit = if (motion) fadeOut(fadeSpec) + shrinkVertically(sizeSpec, shrinkTowards = Alignment.Top) else ExitTransition.None,
        label = "lx-expandable",
    ) {
        content()
    }
}

/**
 * 按压缩放微交互 —— 桌面端 `:active { transform: scale(.98) }`，120ms 标准曲线。
 *
 * 与旧实现（瞬变）的差别：按压与回弹都走 120ms 过渡，手指抬起时不再「啪」地弹回。
 *
 * @param source 交互源（与 `clickable` 共用同一个 [InteractionSource]）
 * @param enabled 是否启用（禁用态不缩放）
 */
@Composable
fun Modifier.lxPressScale(
    source: InteractionSource,
    enabled: Boolean = true,
): Modifier {
    val pressed by source.collectIsPressedAsState()
    val motion = LocalLxMotionEnabled.current
    val target = if (pressed && enabled && motion) LxMotion.PressedScale else 1f
    val scale by animateFloatAsState(
        targetValue = target,
        animationSpec = if (motion) tween(LxMotion.DurationStandard, easing = LxMotion.Easing) else snap(),
        label = "lx-press-scale",
    )
    return this.graphicsLayer {
        scaleX = scale
        scaleY = scale
    }
}

/**
 * 颜色过渡 —— 门控关闭时瞬变，开启时 [durationMillis] 标准曲线过渡。
 *
 * 用于「hover/pressed 填充」与「状态点颜色切换」这类不该瞬变的换色。
 *
 * @param target 目标色
 * @param durationMillis 时长（默认 120ms）
 * @param easing 曲线（默认标准曲线）
 */
@Composable
fun lxAnimateColor(
    target: Color,
    durationMillis: Int = LxMotion.DurationStandard,
    easing: Easing = LxMotion.Easing,
): State<Color> {
    val motion = LocalLxMotionEnabled.current
    return androidx.compose.animation.animateColorAsState(
        targetValue = target,
        animationSpec = if (motion) tween(durationMillis, easing = easing) else snap(),
        label = "lx-animate-color",
    )
}

/**
 * 数值过渡 —— 门控关闭时瞬变（上下文环读数用，220ms 标准曲线）。
 *
 * @param target 目标值
 * @param durationMillis 时长（默认 220ms）
 * @param easing 曲线（默认标准曲线）
 */
@Composable
fun lxAnimateFloat(
    target: Float,
    durationMillis: Int = LxMotion.DurationExit,
    easing: Easing = LxMotion.Easing,
): State<Float> {
    val motion = LocalLxMotionEnabled.current
    return animateFloatAsState(
        targetValue = target,
        animationSpec = if (motion) tween(durationMillis, easing = easing) else snap(),
        label = "lx-animate-float",
    )
}
