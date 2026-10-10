// lxcode 设计 token · 动效
//
// 桌面端动效只有一个标准曲线：`cubic-bezier(0.22, 1, 0.36, 1)`（全仓唯一，不允许出现第二条），
// 时长档见下方常量（120ms 标准 / 140 / 160 / 200 / 220 / 240 / 320 / 380 / 420 / 700 / 800 /
// 1000 / 1600 / 2250 / 2600，每条都标注对应桌面端哪个 keyframes）。
// 安卓端不发明第二套节奏：需要缓动的过渡一律用 [Easing]，时长从三档里挑。
package com.moyunteng.lxcode.design.token

import androidx.compose.animation.core.CubicBezierEasing
import androidx.compose.animation.core.Easing
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.TweenSpec
import androidx.compose.animation.core.tween

object LxMotion {
    /** 120ms —— 桌面端标准时长（border-color / box-shadow 过渡的默认值） */
    const val DurationStandard = 120

    /** 160ms —— 桌面端中档时长（composer 的 transform 过渡） */
    const val DurationMedium = 160

    /** 220ms —— 桌面端长档时长（面板展开/收起） */
    const val DurationLong = 220

    /**
     * 标准缓动 —— CSS `cubic-bezier(0.22, 1, 0.36, 1)`。
     * 全仓唯一标准曲线：任何过渡/动画都用它，不要引入 ease-in-out 之类的第二套手感。
     */
    val Easing: Easing = CubicBezierEasing(0.22f, 1f, 0.36f, 1f)

    /** 线性缓动 —— 只用于连续旋转这类「不该有加减速」的循环动画 */
    val LinearEasing: Easing = FastOutSlowInEasing

    /** 标准过渡（120ms + 标准曲线） */
    fun <T> standard(): TweenSpec<T> = tween(durationMillis = DurationStandard, easing = Easing)

    /** 中档过渡（160ms + 标准曲线） */
    fun <T> medium(): TweenSpec<T> = tween(durationMillis = DurationMedium, easing = Easing)

    /** 长档过渡（220ms + 标准曲线） */
    fun <T> long(): TweenSpec<T> = tween(durationMillis = DurationLong, easing = Easing)

    /** 按压反馈缩放 —— 桌面端 `:active { transform: scale(0.98) }` */
    const val PressedScale = 0.98f

    /** 禁用态透明度 —— 桌面端 `:disabled { opacity: 0.35 }`（.fd-btn-p） */
    const val DisabledAlpha = 0.35f

    /** 次要禁用态透明度 —— 桌面端 `.fd-trigger:disabled { opacity: 0.6 }` */
    const val DisabledAlphaSoft = 0.6f

    // ===== 完整动效档（2026-10 补齐，对齐桌面端全部 keyframes 的时长与曲线）=====
    //
    // 每一条都在 KDoc 里写明它对应桌面端的哪个 keyframes / 哪条规则；
    // 没有对应 keyframes 的字面量（如页面切换）单独标注来源，不假装有出处。

    /** 140ms —— 桌面端会话菜单（`.session-menu` 淡入） */
    const val DurationMenu = 140

    /** 160ms —— 桌面端浮层与遮罩（`pop-in` / `settings-mask-in`） */
    const val DurationPopIn = 160

    /** 200ms —— 桌面端左侧滑入（`mp-slide-in`）与页面切换 */
    const val DurationPageIn = 200

    /** 220ms —— 桌面端退场（`collapseAway`）与设置面板 / 大模态（`settings-in`） */
    const val DurationExit = 220

    /** 240ms —— 桌面端 toast 进出 */
    const val DurationToast = 240

    /** 320ms —— 桌面端消息块入场（`block-in`） */
    const val DurationBlockIn = 320

    /** 380ms —— 桌面端确认卡入场（`ap-card-in`） */
    const val DurationApCardIn = 380

    /** 420ms —— 桌面端 markdown 段落 / 思考句（`md-block-in`） */
    const val DurationMarkdown = 420

    /** 800ms —— 桌面端连接 spinner 一圈（匀速） */
    const val DurationSpinnerLarge = 800

    /** 700ms —— 桌面端小 spinner 一圈（匀速） */
    const val DurationSpinnerSmall = 700

    /** 300ms —— 桌面端交错入场单条目的时长（`staggerIn` 的 `duration: .3`） */
    const val DurationStaggerItem = 300

    /** 40ms —— 桌面端交错入场的**步长上限**（`Math.min(40, 360 / n)` 里的 40） */
    const val StaggerStepMaxMillis = 40

    /** 360ms —— 桌面端交错入场的**总时长上限**（`Math.min(40, 360 / n)` 里的 360） */
    const val StaggerTotalMaxMillis = 360

    /** 1600ms —— 桌面端呼吸（`job-pulse`，`opacity 1 → .35 → 1`，`ease-in-out infinite`） */
    const val DurationPulse = 1600

    /** 1000ms —— 桌面端光标闪烁（`caret-blink`，`1s steps(1) infinite`：硬切，不是渐变） */
    const val DurationCaret = 1000

    /** 2250ms —— 桌面端思考微光（`think-shimmer`，`background-size: 300%`） */
    const val DurationShimmer = 2250

    /** 2600ms —— 桌面端工具行扫光（`tool-sweep`，300px 宽渐变从左扫到右） */
    const val DurationToolSweep = 2600

    /**
     * 退场缓动 —— 桌面端退场一律用 `power2.in` 型（GSAP `power2.in` ≈ CSS `cubic-bezier(.4,0,1,1)`）。
     *
     * 与入场曲线刻意不同：入场要「一冲一收」，退场要「慢慢起步、快速收尾」，
     * 两者用同一条曲线会让退场显得拖沓（桌面端也是分开的）。
     */
    val EasingExit: Easing = CubicBezierEasing(0.4f, 0f, 1f, 1f)

    /** 真线性 —— 循环动画（spinner 旋转、微光扫过）用，不带加减速 */
    val EasingLinear: Easing = LinearEasing

    /** 回弹阻尼比 —— 桌面端 `back.out(1.8)` / `back.out(2)` 的低阻尼 spring 等价 */
    const val SpringDampingRatio = 0.55f

    /** 回弹刚度 —— 与 [SpringDampingRatio] 配对，过冲幅度与 `back.out(1.8)` 相当 */
    const val SpringStiffness = 500f
}
