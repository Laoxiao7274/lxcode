// Composer 的两个真下拉菜单（模型 / 审批档）——app 内私有组件，:design 公开 API 不动。
//
// 逐项对齐桌面端 ModelPicker.tsx / PermPicker.tsx + composer.css（agent-console-v3）：
//  * 容器（.mp-panel / .perm-menu 公共配方）：白底 --surface、1px --border-strong 边、
//    圆角 12、菜单级阴影、padding 5、**向上弹**（bottom = 触发器顶 + 8、左对齐触发药丸）、
//    入场 pop-in（translateY(-4) + scale(0.98) + 淡入，160ms 标准曲线）、
//    退场「收回触发器」（opacity→0、y+8、scale→0.96、origin 左下，200ms power2.in 型）；
//  * 模型菜单（宽 200）：「模型」标题 → 按提供商分组 → 条目（模型名 12.5 + 描述 10.5 灰 +
//    选中勾）→ 底部分隔线 + 「推理强度 · X」跳转行 → 点开二级右面板（从左滑入 200ms，
//    紧贴左面板右缘 +8）；模型不支持 effort 时跳转行整个隐藏；
//  * 权限菜单（宽 252）：三档 = 14dp 图标 + 两行文本 + 选中勾；「完全访问」两步确认
//    （confirming 态红底红字换文案，再点才生效，收起即复位，切回安全档免确认）；
//  * 关闭：点面板外部 / 返回键 / 再点触发器（focusable popup 的 onDismissRequest 三者全覆盖）。
//
// 360dp 窄屏适配（唯一偏差，见报告）：二级面板理想 x = 锚点 + 208dp、权限菜单 252dp，
// 在 2x 密度 720px（=360dp）屏上都可能超右缘——位置统一钳到「屏右缘 - 8dp」。
package com.moyunteng.lxcode.remote.ui

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.EnterTransition
import androidx.compose.animation.ExitTransition
import androidx.compose.animation.core.MutableTransitionState
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.scaleIn
import androidx.compose.animation.scaleOut
import androidx.compose.animation.slideInHorizontally
import androidx.compose.animation.slideInVertically
import androidx.compose.animation.slideOutVertically
import androidx.compose.foundation.Canvas
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
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ErrorOutline
import androidx.compose.material.icons.filled.KeyboardArrowDown
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.TransformOrigin
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.withTransform
import androidx.compose.ui.graphics.vector.PathParser
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.IntRect
import androidx.compose.ui.unit.IntSize
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import androidx.compose.ui.unit.sp
import androidx.compose.ui.window.Popup
import androidx.compose.ui.window.PopupPositionProvider
import androidx.compose.ui.window.PopupProperties
import com.moyunteng.lxcode.design.motion.lxAnimateColor
import com.moyunteng.lxcode.design.token.Lx
import com.moyunteng.lxcode.design.token.LxColors
import com.moyunteng.lxcode.design.token.LxMotion
import com.moyunteng.lxcode.remote.mock.MockData
import com.moyunteng.lxcode.remote.mock.MockModelGroup

// ===== 形状 / 色值（桌面端散落字面量，app 内私有）=====

/** 菜单面板圆角 —— composer.css `.mp-panel/.perm-menu { border-radius: 12px }`。 */
private val MenuPanelShape = RoundedCornerShape(12.dp)

/** 菜单条目圆角 —— `.mp-item/.perm-item { border-radius: 7px }`。 */
private val MenuItemShape = RoundedCornerShape(7.dp)

/** 「完全访问」confirming 态底色 —— `.perm-item.confirming { background: rgba(220,38,38,0.08) }`。 */
private val ConfirmingBg = Color(0x14DC2626)

/** 审批档橙色 —— composer.css `.perm-chip { color: #c2410c }`。 */
private val ApprovalOrange = Color(0xFFC2410C)

/** 面板与触发器的间距 —— `.mp-left { bottom: calc(100% + 8px) }`。 */
private val MenuGap = 8.dp

// ===== 位置：向上弹 + 左对齐锚点（超屏钳到屏右缘 - 8dp）=====

/**
 * 菜单位置提供器 —— 桌面端 `.mp-left { bottom: calc(100% + 8px); left: 0 }` 的 Compose 版：
 * x = 锚点左缘 + [shiftXDp]（二级右面板传 208 = 左面板 200 + 8），超屏钳到屏右缘 - 8dp；
 * y = 锚点顶 - 内容高 - 8dp（向上弹），上方放不下回落到锚点下方。
 */
@Composable
private fun rememberAboveAnchorProvider(shiftXDp: Dp): PopupPositionProvider {
    val density = LocalDensity.current
    return remember(shiftXDp, density) {
        val shiftX = with(density) { shiftXDp.roundToPx() }
        val gap = with(density) { MenuGap.roundToPx() }
        val margin = with(density) { 8.dp.roundToPx() }
        object : PopupPositionProvider {
            override fun calculatePosition(
                anchorBounds: IntRect,
                windowSize: IntSize,
                layoutDirection: LayoutDirection,
                popupContentSize: IntSize,
            ): IntOffset {
                val minX = margin
                val maxX = (windowSize.width - popupContentSize.width - margin).coerceAtLeast(minX)
                val x = (anchorBounds.left + shiftX).coerceIn(minX, maxX)
                val above = anchorBounds.top - popupContentSize.height - gap
                val y = (if (above < margin) anchorBounds.bottom + gap else above)
                    .coerceIn(0, (windowSize.height - popupContentSize.height).coerceAtLeast(0))
                return IntOffset(x, y)
            }
        }
    }
}

// ===== 菜单容器 =====

/**
 * 锚定下拉菜单容器 —— 挂在触发药丸的同一个 Box 里（Popup 锚定父布局）。
 *
 *  * 入场 pop-in：translateY(-4dp) + scale(0.98) + 淡入，160ms 标准曲线（[popInEnter]）；
 *  * 退场收回触发器：淡出 + y+8dp + scale→0.96（origin 左下），200ms power2.in 型（[collapseExit]）；
 *    退场动画跑完才卸载 Popup（`mounted`），退场中重开会打断退场补一次入场（对齐桌面端 usePopover）；
 *  * 关闭三路：[onDismiss] 由 focusable popup 的「点外部 / 返回键」触发；「再点触发器」由
 *    调用方的 onToggle 处理（外部点击被 popup 吃掉时也会走到 onDismiss，等效关闭）。
 *
 * @param expanded 是否展开（逻辑态；false 后退场动画仍在跑，跑完才移除浮层）
 * @param focusable 是否抢焦点（二级面板传 false：不抢主面板焦点，点左面板条目不会误关二级面板）
 * @param shiftXDp 相对锚点左缘的横向偏移（模型二级右面板 = 208dp）
 * @param enter 入场过渡（默认 pop-in；effort 二级面板传 mp-slide-in）
 */
@Composable
internal fun AnchorMenu(
    expanded: Boolean,
    onDismiss: () -> Unit,
    focusable: Boolean = true,
    shiftXDp: Dp = 0.dp,
    enter: EnterTransition? = null,
    content: @Composable () -> Unit,
) {
    // mounted：Popup 挂载态。展开即挂载；收起后等退场动画跑完（isIdle）才卸载。
    var mounted by remember { mutableStateOf(false) }
    if (expanded) mounted = true
    if (!mounted) return
    val provider = rememberAboveAnchorProvider(shiftXDp)
    val vis = remember { MutableTransitionState(false) }
    vis.targetState = expanded
    LaunchedEffect(vis.isIdle) {
        if (vis.isIdle && !vis.targetState) mounted = false
    }
    Popup(
        popupPositionProvider = provider,
        onDismissRequest = onDismiss,
        properties = PopupProperties(focusable = focusable),
    ) {
        AnimatedVisibility(
            visibleState = vis,
            enter = enter ?: popInEnter(),
            exit = collapseExit(),
        ) {
            content()
        }
    }
}

/** 入场 pop-in —— composer.css `@keyframes pop-in`：translateY(-4px) scale(0.98) 淡入，160ms。 */
@Composable
private fun popInEnter(): EnterTransition {
    val fromY = with(LocalDensity.current) { (-4).dp.roundToPx() }
    val spec = tween<Float>(LxMotion.DurationPopIn, easing = LxMotion.Easing)
    return fadeIn(spec) +
        slideInVertically(tween(LxMotion.DurationPopIn, easing = LxMotion.Easing)) { fromY } +
        scaleIn(spec, initialScale = 0.98f)
}

/**
 * 退场收回触发器 —— 桌面端菜单收起走 gsap：opacity→0、y+8、scale→0.96、transformOrigin 左下、
 * 200ms power2.in 型（LxMotion.EasingExit）。
 */
@Composable
private fun collapseExit(): ExitTransition {
    val dropY = with(LocalDensity.current) { 8.dp.roundToPx() }
    val floatSpec = tween<Float>(200, easing = LxMotion.EasingExit)
    val offsetSpec = tween<IntOffset>(200, easing = LxMotion.EasingExit)
    return fadeOut(floatSpec) +
        slideOutVertically(offsetSpec) { dropY } +
        scaleOut(floatSpec, targetScale = 0.96f, transformOrigin = TransformOrigin(0f, 1f))
}

/** 二级右面板入场 —— composer.css `@keyframes mp-slide-in`：translateX(-10px) + 淡入，200ms。 */
@Composable
internal fun mpSlideInEnter(): EnterTransition {
    val fromX = with(LocalDensity.current) { (-10).dp.roundToPx() }
    val spec = tween<Float>(LxMotion.DurationPageIn, easing = LxMotion.Easing)
    return fadeIn(spec) + slideInHorizontally(tween(LxMotion.DurationPageIn, easing = LxMotion.Easing)) { fromX }
}

// ===== 面板 / 条目通用件 =====

/** 白卡面板 —— `.mp-panel/.perm-menu`：宽 [width]、1px 边 + 圆角 12 + 菜单级阴影 + padding 5。 */
@Composable
private fun Modifier.menuPanel(width: Dp): Modifier =
    this
        .width(width)
        .shadow(
            elevation = 8.dp,
            shape = MenuPanelShape,
            clip = false,
            ambientColor = Color(0x14000000),
            spotColor = Color(0x1F000000),
        )
        .clip(MenuPanelShape)
        .background(Lx.colors.Surface)
        .border(1.dp, Lx.colors.BorderStrong, MenuPanelShape)
        .padding(5.dp)

/** 小节标题 —— `.mp-title/.mp-group-title`：10sp/600/#8e8ea0/大写/字距 0.07em/padding 6/10/4。 */
@Composable
private fun MenuTitle(text: String) {
    Text(
        text = text,
        style = TextStyle(
            fontFamily = Lx.type.Sans,
            fontSize = 10.sp,
            fontWeight = FontWeight.SemiBold,
            color = Lx.colors.FgFaint,
            letterSpacing = 0.07.em,
        ),
        modifier = Modifier.padding(start = 10.dp, end = 10.dp, top = 6.dp, bottom = 4.dp),
    )
}

/** 选中勾 —— 桌面端 IconCheck：13dp、stroke 2.2、色 #0d0d0d（`.mp-check { color: var(--fg) }`）。 */
@Composable
private fun MenuCheck(modifier: Modifier = Modifier) {
    StrokePathIcon(
        d = "M20 6 9 17l-5-5",
        size = 13.dp,
        color = Lx.colors.Fg,
        strokeWidth = 2.2f,
        modifier = modifier,
    )
}

/** 右向 chevron —— 桌面端 IconChevronRight：12dp、stroke 2、色 #8e8ea0。 */
@Composable
private fun MenuChevronRight() {
    StrokePathIcon(
        d = "m9 6 6 6-6 6",
        size = 12.dp,
        color = Lx.colors.FgFaint,
        strokeWidth = 2f,
    )
}

/**
 * 24 视窗描边路径图标 —— 桌面端内联 SVG 的 Compose 版（PathParser 不引新依赖）。
 * [strokeWidth] 是 24 视窗里的值（随缩放同步），与桌面端 SVG 的 strokeWidth 同源。
 */
@Composable
private fun StrokePathIcon(
    d: String,
    size: Dp,
    color: Color,
    modifier: Modifier = Modifier,
    strokeWidth: Float = 1.8f,
) {
    val path = remember(d) { PathParser().parsePathString(d).toPath() }
    Canvas(modifier.size(size)) {
        val s = this.size.width / 24f
        withTransform({ scale(s, s, pivot = Offset.Zero) }) {
            drawPath(
                path,
                color,
                style = Stroke(width = strokeWidth, cap = StrokeCap.Round, join = StrokeJoin.Round),
            )
        }
    }
}

// ===== 模型菜单（左面板，宽 200）=====

/**
 * 模型菜单内容 —— ModelPicker.tsx 的 `.mp-left`：
 * 「模型」标题 → 按提供商分组（组标题同款小标题样式、组内条目缩进 18px）→
 * 条目（模型名 12.5sp + 描述 10.5sp 灰 + 选中勾）→
 * 支持 effort 时：分隔线 + 「推理强度 · X」跳转行（右侧 chevron-right）。
 */
@Composable
internal fun ModelMenuContent(
    groups: List<MockModelGroup>,
    currentModelId: String,
    effortDim: String?,
    effortPanelOpen: Boolean,
    onPickModel: (String) -> Unit,
    onToggleEffortPanel: () -> Unit,
) {
    Column(Modifier.menuPanel(200.dp)) {
        MenuTitle("模型")
        if (groups.isEmpty()) {
            // 空态 —— ModelPicker.tsx `.mp-empty`（settings-models.css：11.5sp / #8e8ea0）
            Text(
                text = "没有启用的模型——到 设置 → 模型 里开启",
                style = TextStyle(
                    fontFamily = Lx.type.Sans,
                    fontSize = 11.5.sp,
                    lineHeight = 11.5.sp * 1.5f,
                    color = Lx.colors.FgFaint,
                ),
                modifier = Modifier
                    .padding(start = 12.dp, end = 12.dp, top = 10.dp, bottom = 12.dp)
                    .semantics { contentDescription = "模型菜单空态" },
            )
        }
        groups.forEach { group ->
            MenuTitle(group.name.uppercase()) // 桌面端 .mp-title 的 text-transform: uppercase
            group.models.forEach { m ->
                ModelMenuItem(
                    name = m.name,
                    desc = m.desc,
                    selected = m.id == currentModelId,
                    onClick = { onPickModel(m.id) },
                )
            }
        }
        if (effortDim != null) {
            // 分隔线 —— `.mp-sep { height: 1px; background: var(--border-soft); margin: 4px 6px }`
            Box(
                Modifier
                    .padding(horizontal = 6.dp, vertical = 4.dp)
                    .fillMaxWidth()
                    .height(1.dp)
                    .background(Lx.colors.BorderSoft),
            )
            MenuJumpRow(
                label = "推理强度 $effortDim",
                highlighted = effortPanelOpen,
                onClick = onToggleEffortPanel,
            )
        }
        // effortDim == null：模型不支持推理强度，跳转行整个隐藏（对齐 ModelPicker.tsx）
    }
}

/** 模型条目 —— `.mp-item`（组内 `.mp-group .mp-item { padding-left: 18px }` 缩进）。 */
@Composable
private fun ModelMenuItem(name: String, desc: String, selected: Boolean, onClick: () -> Unit) {
    val interaction = remember { MutableInteractionSource() }
    val pressed by interaction.collectIsPressedAsState()
    val bg by lxAnimateColor(target = if (pressed) Lx.colors.BorderSoft else Color.Transparent)
    Row(
        Modifier
            .fillMaxWidth()
            .clip(MenuItemShape)
            .background(bg)
            .clickable(interactionSource = interaction, indication = null, onClick = onClick)
            .padding(start = 18.dp, end = 10.dp, top = 6.dp, bottom = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(1.dp)) {
            Text(
                text = name,
                style = TextStyle(
                    fontFamily = Lx.type.Sans,
                    fontSize = 12.5.sp,
                    fontWeight = if (selected) FontWeight.Medium else FontWeight.Normal,
                    color = Lx.colors.Fg,
                ),
            )
            Text(
                text = desc,
                style = TextStyle(fontFamily = Lx.type.Sans, fontSize = 10.5.sp, color = Lx.colors.FgFaint),
            )
        }
        if (selected) MenuCheck()
    }
}

/** 跳转行 —— `.mp-item.mp-jump`：「推理强度 · X」12.5sp --fg-muted + chevron-right；展开态 hl 高亮。 */
@Composable
private fun MenuJumpRow(label: String, highlighted: Boolean, onClick: () -> Unit) {
    val interaction = remember { MutableInteractionSource() }
    val pressed by interaction.collectIsPressedAsState()
    val bg by lxAnimateColor(
        target = if (pressed || highlighted) Lx.colors.BorderSoft else Color.Transparent,
    )
    Row(
        Modifier
            .fillMaxWidth()
            .clip(MenuItemShape)
            .background(bg)
            .clickable(interactionSource = interaction, indication = null, onClick = onClick)
            .padding(start = 10.dp, end = 10.dp, top = 6.dp, bottom = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        Text(
            text = label,
            style = TextStyle(
                fontFamily = Lx.type.Sans,
                fontSize = 12.5.sp,
                color = if (highlighted) Lx.colors.Fg else Lx.colors.FgMuted,
            ),
            modifier = Modifier.weight(1f),
        )
        MenuChevronRight()
    }
}

// ===== 推理强度二级右面板（宽 200，从左滑入）=====

/** effort 二级面板内容 —— ModelPicker.tsx 的 `.mp-right`：标题 + 当前模型支持的档位列表。 */
@Composable
internal fun EffortMenuContent(
    effortIds: List<String>,
    currentEffort: String,
    onPick: (String) -> Unit,
) {
    Column(Modifier.menuPanel(200.dp)) {
        MenuTitle("推理强度")
        effortIds.forEach { id ->
            MenuTextItem(
                label = MockData.effortLabel(id),
                selected = id == currentEffort,
                onClick = { onPick(id) },
            )
        }
    }
}

/** 单行文本条目（effort 档位）—— `.mp-item`：12.5sp + 选中勾。 */
@Composable
private fun MenuTextItem(label: String, selected: Boolean, onClick: () -> Unit) {
    val interaction = remember { MutableInteractionSource() }
    val pressed by interaction.collectIsPressedAsState()
    val bg by lxAnimateColor(target = if (pressed) Lx.colors.BorderSoft else Color.Transparent)
    Row(
        Modifier
            .fillMaxWidth()
            .clip(MenuItemShape)
            .background(bg)
            .clickable(interactionSource = interaction, indication = null, onClick = onClick)
            .padding(start = 10.dp, end = 10.dp, top = 6.dp, bottom = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        Text(
            text = label,
            style = TextStyle(
                fontFamily = Lx.type.Sans,
                fontSize = 12.5.sp,
                fontWeight = if (selected) FontWeight.Medium else FontWeight.Normal,
                color = Lx.colors.Fg,
            ),
            modifier = Modifier.weight(1f),
        )
        if (selected) MenuCheck()
    }
}

// ===== 权限菜单（宽 252，三档 + 两步确认）=====

/** 权限三档 —— 文案照抄 shared/settings.tsx APPROVALS；图标路径照抄 PermPicker.tsx ICONS。 */
private data class PermOption(val id: String, val label: String, val hint: String, val iconPath: String)

private val PERM_OPTIONS: List<PermOption> = listOf(
    PermOption(
        id = "confirm",
        label = "默认",
        hint = "低危自动执行，高危需确认",
        iconPath = "M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z",
    ),
    PermOption(
        id = "auto",
        label = "完全访问",
        hint = "全部自动执行，几乎不打断；仅隔离环境用",
        iconPath = "M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10Z",
    ),
    PermOption(
        id = "strict",
        label = "只读",
        hint = "只读取和搜索，不改文件、不执行命令",
        iconPath = "M2 12s3-7 10-7 10 7 10 7-3 7-10 7-10-7-10-7Z",
    ),
)

/**
 * 权限菜单内容 —— PermPicker.tsx 的 `.perm-menu`：三档 = 14dp 图标 + 两行文本 + 选中勾。
 *
 * [confirming] = 「完全访问」两步确认的 armed 态：该条目红底红字、文案换成
 * 「再次点击确认开启完全访问 / 高危工具将不再请求确认」（`.perm-item.confirming`）。
 */
@Composable
internal fun PermMenuContent(
    current: String,
    confirming: Boolean,
    onPick: (String) -> Unit,
) {
    Column(Modifier.menuPanel(252.dp)) {
        PERM_OPTIONS.forEach { p ->
            val armed = p.id == "auto" && confirming && current != "auto"
            PermMenuItem(
                iconPath = p.iconPath,
                label = if (armed) "再次点击确认开启完全访问" else p.label,
                desc = if (armed) "高危工具将不再请求确认" else p.hint,
                selected = current == p.id,
                armed = armed,
                onClick = { onPick(p.id) },
            )
        }
    }
}

/** 权限条目 —— `.perm-item`：14dp 图标 + label 12.5sp / desc 10.5sp 灰 + 选中勾；padding 8/10。 */
@Composable
private fun PermMenuItem(
    iconPath: String,
    label: String,
    desc: String,
    selected: Boolean,
    armed: Boolean,
    onClick: () -> Unit,
) {
    val interaction = remember { MutableInteractionSource() }
    val pressed by interaction.collectIsPressedAsState()
    val bg by lxAnimateColor(
        target = when {
            armed -> ConfirmingBg // rgba(220,38,38,0.08)
            pressed -> Lx.colors.BorderSoft
            else -> Color.Transparent
        },
    )
    Row(
        Modifier
            .fillMaxWidth()
            .clip(MenuItemShape)
            .background(bg)
            .clickable(interactionSource = interaction, indication = null, onClick = onClick)
            .padding(start = 10.dp, end = 10.dp, top = 8.dp, bottom = 8.dp),
        verticalAlignment = Alignment.Top,
        horizontalArrangement = Arrangement.spacedBy(9.dp),
    ) {
        StrokePathIcon(
            d = iconPath,
            size = 14.dp,
            // .perm-item svg:first-child：默认 --fg-faint、选中 --fg、confirming --danger
            color = when {
                armed -> LxColors.Danger
                selected -> Lx.colors.Fg
                else -> Lx.colors.FgFaint
            },
            modifier = Modifier.padding(top = 2.dp),
        )
        Column(
            Modifier.weight(1f),
            verticalArrangement = Arrangement.spacedBy(1.dp),
        ) {
            Text(
                text = label,
                style = TextStyle(
                    fontFamily = Lx.type.Sans,
                    fontSize = 12.5.sp,
                    lineHeight = 17.sp,
                    fontWeight = if (armed) FontWeight.SemiBold else if (selected) FontWeight.Medium else FontWeight.Normal,
                    color = if (armed) LxColors.Danger else Lx.colors.Fg,
                ),
            )
            Text(
                text = desc,
                style = TextStyle(
                    fontFamily = Lx.type.Sans,
                    fontSize = 10.5.sp,
                    lineHeight = 15.2.sp, // 10.5 × 1.45（.perm-desc 的 line-height:1.45）
                    color = Lx.colors.FgFaint,
                ),
            )
        }
        if (selected) MenuCheck(modifier = Modifier.padding(top = 3.dp))
    }
}

// ===== 触发药丸（视觉沿用 Composer 原款，只把「点击循环切值」换成开菜单）=====

/** 模型药丸 —— 唯一带描边的药丸（.model-chip）：模型名 + 灰字档位「· 中」+ 下拉 chevron。 */
@Composable
internal fun ModelPill(label: String, effortDim: String?, expanded: Boolean, onToggle: () -> Unit) {
    val interaction = remember { MutableInteractionSource() }
    val pressed by interaction.collectIsPressedAsState()
    val bg by lxAnimateColor(target = if (pressed) Lx.colors.BorderSoft else Color.Transparent)
    Row(
        Modifier
            .clip(Lx.radius.PillShape)
            .background(bg)
            .border(1.dp, Lx.colors.BorderStrong, Lx.radius.PillShape)
            .clickable(interactionSource = interaction, indication = null, onClick = onToggle)
            .padding(horizontal = 10.dp, vertical = 3.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(4.dp),
    ) {
        Text(
            text = label,
            style = TextStyle(fontFamily = Lx.type.Sans, fontSize = 11.sp, color = Lx.colors.Fg),
        )
        if (effortDim != null) {
            Text(
                text = effortDim,
                style = TextStyle(fontFamily = Lx.type.Sans, fontSize = 11.sp, color = Lx.colors.FgFaint),
            )
        }
        Icon(
            imageVector = Icons.Filled.KeyboardArrowDown,
            contentDescription = null,
            tint = Lx.colors.FgFaint,
            modifier = Modifier.size(10.dp),
        )
    }
}

/** 审批档药丸 —— ghost 无边框、橙色 #c2410c（.perm-chip：11.5sp/500、圆圈感叹号 + 模式名 + chevron）。 */
@Composable
internal fun ApprovalPill(label: String, expanded: Boolean, onToggle: () -> Unit) {
    val interaction = remember { MutableInteractionSource() }
    val pressed by interaction.collectIsPressedAsState()
    // hover 底 rgba(194,65,12,0.07) —— 触屏无 hover，按压档用同值（120ms 过渡）
    val bg by lxAnimateColor(target = if (pressed) ApprovalOrange.copy(alpha = 0.07f) else Color.Transparent)
    Row(
        Modifier
            .clip(Lx.radius.PillShape)
            .background(bg)
            .clickable(interactionSource = interaction, indication = null, onClick = onToggle)
            .padding(horizontal = 10.dp, vertical = 3.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(4.dp),
    ) {
        Icon(
            imageVector = Icons.Filled.ErrorOutline,
            contentDescription = null,
            tint = ApprovalOrange,
            modifier = Modifier.size(12.dp),
        )
        Text(
            text = label,
            style = TextStyle(
                fontFamily = Lx.type.Sans,
                fontSize = 11.5.sp,
                fontWeight = FontWeight.Medium,
                color = ApprovalOrange,
            ),
        )
        Icon(
            imageVector = Icons.Filled.KeyboardArrowDown,
            contentDescription = null,
            tint = ApprovalOrange,
            modifier = Modifier.size(10.dp),
        )
    }
}
