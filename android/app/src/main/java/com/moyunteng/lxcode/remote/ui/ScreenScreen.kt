// ScreenScreen —— 投屏页（第一阶段：只看）。
//
// 流程：进入即 refreshScreen（显示器枚举 + 当前状态）→ 选显示器/清晰度 →
// 「开始投屏」→ 后端通知壳起 ffmpeg → 二进制视频帧经 LxWsClient.frameListener
// 直投 ScreenDecoder → SurfaceView 渲染。断线重连后**必须重新要关键帧**：
// LxWsClient 重连会重建 WS，而 ffmpeg 的 GOP 是 2s（壳侧 -g 60）——重连后
// 最多 2s 内下一个关键帧自动恢复画面，解码器按「还没配置过」处理首个关键帧
//（configured 标志在流中断 ≥3s 时复位——重连后丢掉残余非关键帧等 IDR）。
//
// 清晰度档（internal/protocol/screen.go）：low / medium / high（壳侧映射
// constqp qp 26/22/18）。Stage 1 分辨率固定 1080p 档。
//
// 视觉（agent-console-v3）：顶栏 = ScreenHeader + 连接状态点（LxStatusDot，
// 对齐 ConnPill 的脉冲语义）；视频区（设置页）= TermBg 黑底圆角卡（Lx.radius.CardShape）
// + 按视频真实比例 letterbox（fit-center，SPS 解析的宽高驱动）；观看态 = 无边框
// 铺满整页（无 padding/圆角，黑底贴合屏幕，0.3.30 拍板）；
// 控制区 = LxSectionHeader 分段 + LxListItem 风格选择行 + LxButton（Large 主行动）；
// 断线/重连横幅复用 BackendDownBar（与连接页同一套三态）。
package com.moyunteng.lxcode.remote.ui

import android.os.SystemClock
import android.view.SurfaceView
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.EnterTransition
import androidx.compose.animation.ExitTransition
import androidx.compose.animation.core.MutableTransitionState
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.scaleIn
import androidx.compose.animation.scaleOut
import androidx.compose.animation.slideInVertically
import androidx.compose.animation.slideOutVertically
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import androidx.compose.foundation.gestures.calculateCentroid
import androidx.compose.foundation.gestures.calculatePan
import androidx.compose.foundation.gestures.calculateZoom
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsPressedAsState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Cast
import androidx.compose.material.icons.filled.DesktopWindows
import androidx.compose.material.icons.filled.FullscreenExit
import androidx.compose.material.icons.filled.KeyboardArrowLeft
import androidx.compose.material.icons.filled.Stop
import androidx.compose.material.icons.filled.TouchApp
import androidx.compose.material.icons.outlined.ScreenRotation
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.SideEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.isSpecified
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.TransformOrigin
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.withTransform
import androidx.compose.ui.graphics.vector.PathParser
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.input.pointer.PointerEvent
import androidx.compose.ui.input.pointer.positionChange
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalDensity
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
import androidx.compose.ui.viewinterop.AndroidView
import com.moyunteng.lxcode.design.component.LxButton
import com.moyunteng.lxcode.design.component.LxButtonSize
import com.moyunteng.lxcode.design.component.LxButtonVariant
import com.moyunteng.lxcode.design.component.LxIconButton
import com.moyunteng.lxcode.design.component.LxPill
import com.moyunteng.lxcode.design.component.LxPillVariant
import com.moyunteng.lxcode.design.component.LxSectionHeader
import com.moyunteng.lxcode.design.component.LxSpinner
import com.moyunteng.lxcode.design.component.LxStatus
import com.moyunteng.lxcode.design.component.LxStatusDot
import com.moyunteng.lxcode.design.motion.lxAnimateColor
import com.moyunteng.lxcode.design.motion.lxAnimateFloat
import com.moyunteng.lxcode.design.token.Lx
import com.moyunteng.lxcode.design.token.LxMotion
import kotlinx.coroutines.delay
import kotlin.math.roundToInt

/** 显示器信息（screen.monitors 的投影；RealBackend 装配）。 */
data class ScreenMonitorInfo(
    val id: Int,
    val name: String,
    val primary: Boolean,
    val width: Int,
    val height: Int,
)

/** 投屏状态（screen.state / screen.changed 的投影）。 */
data class ScreenStateInfo(
    val active: Boolean,
    val monitor: Int,
    val quality: String,
    val encoder: String,
    val error: String?,
)

/** 清晰度档（键 → 显示名）。 */
private val QUALITY_LABELS = listOf("low" to "流畅", "medium" to "标准", "high" to "高清")

@Composable
fun ScreenScreen(state: com.moyunteng.lxcode.remote.ui.AppState) {
    val real = state.real
    val connected = real.phase == com.moyunteng.lxcode.remote.net.RealPhase.Connected

    // 全屏态提升到页面级：未投屏的设置页「视频卡 ↔ 整页」切换（0.3.30 拍板，
    // 观看态本身就是整页，fullscreen 只影响设置页入口的双击全屏）。
    var fullscreen by rememberSaveable { mutableStateOf(false) }
    DisposableEffect(Unit) {
        onDispose { fullscreen = false }
    }

    // 观看态（投屏进行中）的「全屏 ↔ 缩小」用 AppState.screenFullscreen（页面级，
    // AppRoot 返回键与 safeDrawing 豁免都按它判定）；未投屏时的设置页双击全屏
    // 仍用本地的 fullscreen rememberSaveable（其复位靠上方 DisposableEffect 的
    // onDispose）。screenFullscreen 的复位收口在 AppRoot 的 LaunchedEffect(route)
    // （路由离开投屏页即复位）——这里不再放 effect：路由同步切换时本子树直接
    // 卸载、effect 根本不会跑（根因 B：伪复位掩盖了收口缺失）。观看中途
    // active 翻 false 的路径由 AppRoot 的断开自动退回处理（route 离开 → 连带复位）。
    val watching = real.screenState?.active == true

    if (watching) {
        if (state.screenFullscreen) {
            // ===== 全屏观看态：视频铺满整页 + 观看顶栏 + 悬浮球（原观看态形态）=====
            // 方向锁定：进入观看态即按用户在悬浮球菜单选的方向重放 requestedOrientation。
            // configChanges 后旋转/锁方向不再触发 Activity 重建（AndroidManifest 配置，
            // RustDesk 同款方案），这条 SideEffect 只是兜底：进程恢复/其他极端路径下
            // 重建后重组回来，仍靠 RealBackend.screenOrientationLock 找回方向锁。
            val activity = androidx.compose.ui.platform.LocalContext.current as? android.app.Activity
            SideEffect {
                real.screenOrientationLock?.let { lock ->
                    activity?.requestedOrientation = lock
                }
            }
            // 离开观看态（停止投屏/断线回设置页）恢复用户默认方向——requestedOrientation
            // 是 Activity 级的，不恢复会让「横屏」泄漏到整个 app 的所有页面。
            // configChanges 后旋转不再触发 dispose，这里只在真实离开观看态时执行，
            // 判据（screenOrientationLock != null）不动。
            DisposableEffect(Unit) {
                onDispose {
                    if (real.screenOrientationLock != null) {
                        real.screenOrientationLock = null
                        activity?.requestedOrientation =
                            android.content.pm.ActivityInfo.SCREEN_ORIENTATION_FULL_USER
                    }
                }
            }
            WatchSurface(real, state, fullscreen = true, onToggleFullscreen = { state.screenFullscreen = false })
            return
        }
        // ===== 缩小观看态（投屏继续）：设置页形态，视频卡继续播同一解码流 =====
        MinimizedCastScreen(real, state, onExpand = { state.screenFullscreen = true })
        return
    }
    if (fullscreen) {
        // 整页视频：VideoSurface 自带黑底 + 顶部悬浮提示条 + 双击/双指手势
        VideoSurface(real, fullscreen = true, onToggleFullscreen = { fullscreen = !fullscreen }, state = state)
        return
    }

    Column(
        Modifier
            .fillMaxSize()
            .background(Lx.colors.Bg),
    ) {
        ScreenHeader(
            title = "投屏",
            onBack = { state.route = com.moyunteng.lxcode.remote.ui.Route.Connections },
            // 尾部插槽 = 连接状态点（对齐 ConnPill 的点语义：success/run/idle）
            trailing = { HeaderConnStatus(real) },
        )

        Column(
            Modifier
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(Lx.space.s12),
            verticalArrangement = Arrangement.spacedBy(Lx.space.s10),
        ) {
            // 断线/重连横幅（与连接页同一套三态：danger 错误条 / amber 重连条）
            if (!connected) {
                com.moyunteng.lxcode.remote.ui.BackendDownBar(
                    down = real.phase == com.moyunteng.lxcode.remote.net.RealPhase.Failed,
                    reconnecting = real.reconnecting,
                    onRetry = { real.retry(state.realAddr, state.realToken) },
                    message = real.phaseMessage,
                )
                Hint("未连接后端——先在连接页连上电脑再投屏")
                return@Column
            }

            // 视频画面（SurfaceView；黑底圆角卡 + 按视频真实比例 letterbox；
            // 双指缩放 + 双击全屏——SurfaceView bounds 由 coverVideoSize 算出）
            VideoSurface(
                real,
                fullscreen = false,
                onToggleFullscreen = { fullscreen = !fullscreen },
            )

            // 实时状态行：投屏中 pill + 显示器/画质/编码器明细
            StatusLine(real)

            // 诊断面板（黑屏排查数据：收帧/解码状态/最近错误；默认折叠，
            // 点开显示——让用户能直接告诉我们卡在哪一步）
            DiagPanel(real)

            // 显示器选择（壳在线才可选；单屏不罗列选择行，只提示当前显示器）
            if (!real.screenShellOnline) {
                Hint("桌面端 Lxcode 壳不在线（电脑上的应用没开或后端连不上壳）——无法投屏")
            } else if (real.screenMonitors.size > 1) {
                LxSectionHeader("显示器")
                real.screenMonitors.forEach { m ->
                    SelectableRow(
                        title = m.name,
                        subtitle = "${m.width}×${m.height}" + if (m.primary) " · 主显示器" else "",
                        selected = (real.screenState?.active == false && state.screenPickMonitor == m.id) ||
                            (real.screenState?.active == true && real.screenState?.monitor == m.id),
                        onClick = { state.screenPickMonitor = m.id },
                    )
                }
            } else if (real.screenMonitors.size == 1) {
                val m = real.screenMonitors[0]
                Text(
                    "显示器：${m.name}（${m.width}×${m.height}）",
                    style = Lx.type.BodySmall,
                    color = Lx.colors.FgFaint,
                )
            }

            // 清晰度（分段 pill：流畅/标准/高清 → qp 26/22/18）
            LxSectionHeader("清晰度")
            Row(horizontalArrangement = Arrangement.spacedBy(Lx.space.s4)) {
                QUALITY_LABELS.forEach { (key, label) ->
                    val selected = state.screenPickQuality == key
                    Box(
                        Modifier
                            .clip(Lx.radius.FieldShape)
                            .background(if (selected) Lx.colors.Surface else Lx.colors.BgSide)
                            .border(
                                1.dp,
                                if (selected) Lx.colors.BorderStrong else Lx.colors.Border,
                                Lx.radius.FieldShape,
                            )
                            .clickable { state.screenPickQuality = key }
                            .padding(horizontal = Lx.space.s6, vertical = Lx.space.s4),
                    ) {
                        Text(
                            label,
                            style = Lx.type.Button,
                            color = if (selected) Lx.colors.Fg else Lx.colors.FgFaint,
                        )
                    }
                }
            }

            // 开始/停止（Large = 44dp 触摸目标下限；busy 时按钮自带 loading 态禁点）
            val busy = real.screenBusy
            if (real.screenState?.active == true) {
                LxButton(
                    "停止投屏",
                    {
                        real.stopScreen { err -> if (err != null) state.screenToast = err }
                    },
                    variant = LxButtonVariant.Secondary,
                    size = LxButtonSize.Large,
                    enabled = !busy,
                )
            } else if (real.screenShellOnline) {
                LxButton(
                    if (real.screenStartPending) "正在启动…" else "开始投屏",
                    {
                        real.startScreen(state.screenPickMonitor, state.screenPickQuality) { err ->
                            state.screenToast = err // null = 成功
                            // 成功即记（0.3.30：连接页入口下次直接按这份开始）
                            if (err == null) state.rememberScreenCast(state.screenPickMonitor, state.screenPickQuality)
                        }
                    },
                    variant = LxButtonVariant.Primary,
                    size = LxButtonSize.Large,
                    enabled = !busy && !real.screenStartPending,
                )
            }

            Hint("投屏默认只观看。悬浮球菜单里可开「控制模式」用手机操作电脑——电脑端需在远程访问设置里打开「允许控制」（默认关），被控时电脑托盘会显示「正在被控制」。")

            // 一次性提示（toast 语义：几秒后自动清）
            state.screenToast?.let { msg ->
                if (msg.isNotEmpty()) {
                    Text(msg, style = Lx.type.BodySmall, color = Lx.colors.FgMuted)
                }
                LaunchedEffect(msg) {
                    delay(4000)
                    state.screenToast = null
                }
            }
        }
    }
}

/**
 * 缩小观看态（投屏进行中，fullscreen=false）：设置页形态 + 投屏继续跑。
 *
 * 与未投屏设置页（ScreenScreen 尾段 Column）同构：ScreenHeader + 16:9 视频卡 +
 * 显示器/清晰度选择 + 「停止投屏」。差异三处：
 *  * 视频卡带「投屏中」pill，点击/双击卡片 = 放大回全屏观看（[onExpand]）；
 *  * 底部新增 [AgentControlPanel] 占位（后续在小屏下面做 agent 控制电脑）；
 *  * 返回键语义（AppRoot BackHandler）：缩小态返回 = 离开投屏页回连接页，
 *    投屏继续（挂起语义）；从连接页/其它入口再进投屏页回到本形态。
 *
 * insets：本形态不在 AppRoot 的 watching 豁免里（watching 要求 fullscreen），
 * 走正常 safeDrawing，无需页面内自补系统条。
 */
@Composable
private fun MinimizedCastScreen(
    real: com.moyunteng.lxcode.remote.net.RealBackend,
    state: com.moyunteng.lxcode.remote.ui.AppState,
    onExpand: () -> Unit,
) {
    val connected = real.phase == com.moyunteng.lxcode.remote.net.RealPhase.Connected
    Column(
        Modifier
            .fillMaxSize()
            .background(Lx.colors.Bg),
    ) {
        ScreenHeader(
            title = "投屏",
            onBack = { state.route = com.moyunteng.lxcode.remote.ui.Route.Connections },
            trailing = { HeaderConnStatus(real) },
        )

        Column(
            Modifier
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(Lx.space.s12),
            verticalArrangement = Arrangement.spacedBy(Lx.space.s10),
        ) {
            if (!connected) {
                com.moyunteng.lxcode.remote.ui.BackendDownBar(
                    down = real.phase == com.moyunteng.lxcode.remote.net.RealPhase.Failed,
                    reconnecting = real.reconnecting,
                    onRetry = { real.retry(state.realAddr, state.realToken) },
                    message = real.phaseMessage,
                )
            }

            // 视频卡：同一解码流（real.screenDecoder 不因形态切换重建——SurfaceView
            // 销毁重建走 setOutputSurface 热重绑），点击 = 放大回全屏观看。
            Box {
                VideoSurface(
                    real,
                    fullscreen = false,
                    onToggleFullscreen = onExpand,
                )
                // 「投屏中」pill：叠在视频卡顶部左侧，双击卡片放大回全屏观看
                LxPill(
                    "投屏中 · 双击放大",
                    variant = LxPillVariant.Success,
                    modifier = Modifier
                        .align(Alignment.TopStart)
                        .padding(Lx.space.s6),
                )
            }

            // 实时状态行：投屏 pill + 显示器/画质/编码器明细
            StatusLine(real)

            // 诊断面板（黑屏排查数据；默认折叠，点开显示）
            DiagPanel(real)

            // 显示器选择（壳在线才可选；单屏不罗列选择行，只提示当前显示器）
            if (!real.screenShellOnline) {
                Hint("桌面端 Lxcode 壳不在线（电脑上的应用没开或后端连不上壳）——无法投屏")
            } else if (real.screenMonitors.size > 1) {
                LxSectionHeader("显示器")
                real.screenMonitors.forEach { m ->
                    SelectableRow(
                        title = m.name,
                        subtitle = "${m.width}×${m.height}" + if (m.primary) " · 主显示器" else "",
                        // 投屏中：选中态跟随实际投屏的显示器（切换走悬浮球 setScreenMonitor）
                        selected = real.screenState?.monitor == m.id,
                        onClick = {
                            real.setScreenMonitor(m.id) { err ->
                                if (err != null) state.screenToast = err
                                else state.rememberScreenCast(m.id, real.screenState?.quality ?: "medium")
                            }
                        },
                    )
                }
            } else if (real.screenMonitors.size == 1) {
                val m = real.screenMonitors[0]
                Text(
                    "显示器：${m.name}（${m.width}×${m.height}）",
                    style = Lx.type.BodySmall,
                    color = Lx.colors.FgFaint,
                )
            }

            // 清晰度（分段 pill：流畅/标准/高清 → qp 26/22/18；投屏中切换走
            // setScreenQuality——后端按命令电平重起 ffmpeg，与悬浮球同一语义）
            LxSectionHeader("清晰度")
            Row(horizontalArrangement = Arrangement.spacedBy(Lx.space.s4)) {
                QUALITY_LABELS.forEach { (key, label) ->
                    val selected = real.screenState?.quality == key
                    Box(
                        Modifier
                            .clip(Lx.radius.FieldShape)
                            .background(if (selected) Lx.colors.Surface else Lx.colors.BgSide)
                            .border(
                                1.dp,
                                if (selected) Lx.colors.BorderStrong else Lx.colors.Border,
                                Lx.radius.FieldShape,
                            )
                            .clickable {
                                real.setScreenQuality(key) { err ->
                                    if (err != null) state.screenToast = err
                                    else state.rememberScreenCast(real.screenState?.monitor ?: 0, key)
                                }
                            }
                            .padding(horizontal = Lx.space.s6, vertical = Lx.space.s4),
                    ) {
                        Text(
                            label,
                            style = Lx.type.Button,
                            color = if (selected) Lx.colors.Fg else Lx.colors.FgFaint,
                        )
                    }
                }
            }

            // 停止投屏（Large 主行动；busy 时按钮自带 loading 态禁点）
            val busy = real.screenBusy
            LxButton(
                "停止投屏",
                {
                    real.stopScreen { err -> if (err != null) state.screenToast = err }
                },
                variant = LxButtonVariant.Secondary,
                size = LxButtonSize.Large,
                enabled = !busy,
            )

            Hint("投屏默认只观看。悬浮球菜单里可开「控制模式」用手机操作电脑——电脑端需在远程访问设置里打开「允许控制」（默认关），被控时电脑托盘会显示「正在被控制」。")

            // Agent 控制区（占位）：后续在小屏下面做 agent 控制电脑
            AgentControlPanel()

            // 一次性提示（toast 语义：几秒后自动清）
            state.screenToast?.let { msg ->
                if (msg.isNotEmpty()) {
                    Text(msg, style = Lx.type.BodySmall, color = Lx.colors.FgMuted)
                }
                LaunchedEffect(msg) {
                    delay(4000)
                    state.screenToast = null
                }
            }
        }
    }
}

/**
 * Agent 控制区（占位组件，缩小观看态视频卡下方）。
 *
 * 后续形态：在小屏下面做 agent 控制电脑（发指令、看 agent 执行）。本期只落
 * 结构占位——独立组件 + 明确的「开发中」卡片，后续往里填功能不动外层布局。
 */
@Composable
private fun AgentControlPanel() {
    Column(
        Modifier
            .fillMaxWidth()
            .clip(Lx.radius.CardShape)
            .background(Lx.colors.BgSide)
            .border(1.dp, Lx.colors.Border, Lx.radius.CardShape)
            .padding(Lx.space.s10),
        verticalArrangement = Arrangement.spacedBy(Lx.space.s4),
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(Lx.space.s4),
        ) {
            Icon(
                imageVector = Icons.Filled.DesktopWindows,
                contentDescription = null,
                tint = Lx.colors.FgFaint,
                modifier = Modifier.size(16.dp),
            )
            Text("Agent 控制", style = Lx.type.ListTitle, color = Lx.colors.FgMuted)
            LxPill("开发中", variant = LxPillVariant.Muted)
        }
        Text(
            "之后可以在这里用 agent 控制电脑（发指令、执行任务）——敬请期待。",
            style = Lx.type.BodySmall,
            color = Lx.colors.FgFaint,
        )
    }
}

/** 顶栏尾部连接状态点：success（已连接）/ run+spinner 语义（连接中）/ idle（离线）。 */

@Composable
private fun HeaderConnStatus(real: com.moyunteng.lxcode.remote.net.RealBackend) {
    val (status, label) = when (real.phase) {
        com.moyunteng.lxcode.remote.net.RealPhase.Connected -> LxStatus.Success to "已连接"
        com.moyunteng.lxcode.remote.net.RealPhase.Connecting -> LxStatus.Run to "连接中"
        com.moyunteng.lxcode.remote.net.RealPhase.Failed -> LxStatus.Danger to "连接失败"
        com.moyunteng.lxcode.remote.net.RealPhase.Offline -> LxStatus.Idle to "离线"
    }
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(Lx.space.s4),
    ) {
        LxStatusDot(status = status)
        Text(label, style = Lx.type.Pill, color = Lx.colors.FgMuted)
    }
}

/**
 * 观看模式根：视频铺满整页（letterbox 保持宽高比）+ 右上角状态 pill + 悬浮球。
 *
 * 双击全屏手势与悬浮球共存：手势在 [VideoSurface] 的 pointerInput 里，悬浮球
 * 只占自己那一小块点击区，不挡手势区；视频一律无边框铺满整页（0.3.30 拍板：
 * 横屏观看旁边不要有边框——去 padding/圆角，黑底 letterbox 贴合屏幕）。
 */
@Composable
private fun WatchSurface(
    real: com.moyunteng.lxcode.remote.net.RealBackend,
    state: com.moyunteng.lxcode.remote.ui.AppState,
    fullscreen: Boolean,
    onToggleFullscreen: () -> Unit,
) {
    // 观看态豁免了 AppRoot 的 safeDrawing（画面铺到系统条之下），本页自补系统条
    // 间距：顶栏加 statusBarsPadding（控件不顶进状态栏）、悬浮球坐标含
    // navigationBars/mime insets 补偿（吸边不贴进手势条）。退出观看态（顶栏 ←/
    // 悬浮球「停止投屏」/返回键/断开自动退回）后 AppRoot 恢复 safeDrawing，
    // 这里的补偿只作用于观看态。
    Box(Modifier.fillMaxSize().background(Lx.colors.TermBg)) {
        // 视频主体：整页根约束（有限高度），fillMaxSize 不会塌条；
        // 比例适配在 VideoSurface 内部按视频真实宽高比 **cover**（最小铺满，超出
        // 部分裁掉——零边框），缩放/平移手势基准同为 cover 语义。
        // fullscreen 恒为 true：本组件只在全屏观看态挂载（缩小态走
        // MinimizedCastScreen），双击/左箭头 = onToggleFullscreen（回缩小态）。
        Box(
            Modifier.fillMaxSize(),
            contentAlignment = Alignment.Center,
        ) {
            VideoSurface(real, fullscreen = fullscreen, onToggleFullscreen = onToggleFullscreen, state = state)
        }

        // 观看态顶栏（0.3.31 拍板：投屏会话页自带顶栏，不再「包一层」设置页）：
        // 叠在视频上方的一条 40dp 半透明黑渐变——左「←」缩小回设置页形态
        //（投屏继续，不停止）、中间「正在投屏 · 显示器N · 画质」、右侧连接状态点。
        // 悬浮球保留为快捷控制（停止/清晰度/显示器/方向/缩小）。
        Row(
            verticalAlignment = Alignment.CenterVertically,
            modifier = Modifier
                .align(Alignment.TopCenter)
                .fillMaxWidth()
                // 观看态豁免 safeDrawing 后自补状态栏高度：渐变背景从屏幕最顶画起
                //（盖住状态栏区域），内容（←/标题/状态点）排在状态栏之下不顶进系统条
                .statusBarsPadding()
                .height(40.dp)
                .background(
                    androidx.compose.ui.graphics.Brush.verticalGradient(
                        0f to Color.Black.copy(alpha = 0.62f),
                        1f to Color.Transparent,
                    ),
                )
                .padding(horizontal = Lx.space.s4),
        ) {
            LxIconButton(onClick = onToggleFullscreen) {
                Icon(
                    imageVector = Icons.Filled.KeyboardArrowLeft,
                    contentDescription = "缩小（保持投屏）",
                    tint = Color.White.copy(alpha = 0.85f),
                    modifier = Modifier.size(20.dp),
                )
            }
            Text(
                text = "正在投屏 · 显示器 ${(real.screenState?.monitor ?: 0) + 1} · ${qualityLabel(real.screenState?.quality ?: "medium")}",
                style = Lx.type.Pill,
                color = Color.White.copy(alpha = 0.85f),
                maxLines = 1,
                modifier = Modifier
                    .weight(1f)
                    .padding(start = Lx.space.s2),
            )
            val dotStatus = when (real.phase) {
                com.moyunteng.lxcode.remote.net.RealPhase.Connected -> LxStatus.Success
                com.moyunteng.lxcode.remote.net.RealPhase.Connecting -> LxStatus.Run
                com.moyunteng.lxcode.remote.net.RealPhase.Failed -> LxStatus.Danger
                com.moyunteng.lxcode.remote.net.RealPhase.Offline -> LxStatus.Idle
            }
            LxStatusDot(status = dotStatus)
        }

        // 悬浮球（菜单开着时状态行隐藏，避免与菜单项重叠）
        ScreenFloatingBall(real, state, onToggleFullscreen)

        // 虚拟键盘条（控制模式时显示）：常用键一行 + 文本输入转发。
        // 键序：Ctrl Alt Win Tab Esc ← ↑ ↓ → Enter Backspace + 「键入」输入框。
        if (state.screenControlMode) {
            Box(Modifier.align(Alignment.BottomCenter)) {
                ControlKeyboardBar(real, state)
            }
        }
    }
}

/**
 * 虚拟键盘条（投屏控制模式）：屏幕底部半透明条，常用修饰键（锁定式：点一下
 * 按下、再点抬起——对应组合键按住场景）+ 单发键 + 文本输入框（IME 文本经
 * char 事件逐条转发，中文/表情走 Unicode 注入，不依赖电脑端键盘布局）。
 */
@Composable
private fun ControlKeyboardBar(
    real: com.moyunteng.lxcode.remote.net.RealBackend,
    state: com.moyunteng.lxcode.remote.ui.AppState,
) {
    // 锁定的修饰键（点击切换按下/抬起；组合键 = 修饰键按下状态 + 单发键）
    val locked = remember { mutableStateOf(setOf<String>()) }
    var textDraft by remember { mutableStateOf("") }
    val focus = remember { androidx.compose.ui.focus.FocusRequester() }

    fun sendKey(key: String) {
        real.sendScreenInput(com.moyunteng.lxcode.remote.net.ScreenInputParams(
            type = "key",
            key = key,
            modifiers = locked.value.toList(),
            monitor = 0,
        ))
    }

    fun toggleLock(mod: String) {
        val next = locked.value.toMutableSet()
        if (!next.remove(mod)) next.add(mod)
        locked.value = next
        // 锁定切换时同步电脑端按下/抬起状态（key 事件 = down+up，锁定键单独用 down/up）
        real.sendScreenInput(com.moyunteng.lxcode.remote.net.ScreenInputParams(
            type = if (mod in next) "down" else "up",
            key = mod,
            monitor = 0,
        ))
    }

    Column(
        Modifier
            .fillMaxWidth()
            .navigationBarsPadding()
            .background(Color.Black.copy(alpha = 0.72f)),
    ) {
        // 单发键行
        Row(
            Modifier
                .fillMaxWidth()
                .padding(horizontal = Lx.space.s4, vertical = Lx.space.s2),
            horizontalArrangement = Arrangement.spacedBy(Lx.space.s2),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            listOf("ctrl", "alt", "shift", "win").forEach { mod ->
                KeyBarChip(
                    label = mod.uppercase(),
                    active = mod in locked.value,
                    onClick = { toggleLock(mod) },
                )
            }
            listOf(
                "tab" to "⇥", "esc" to "esc", "up" to "↑", "down" to "↓",
                "left" to "←", "right" to "→", "enter" to "⏎", "backspace" to "⌫",
            ).forEach { (key, label) ->
                KeyBarChip(label = label, active = false) { sendKey(key) }
            }
        }
        // 文本输入行：输入框 + 发送（IME 组好词后整段经 char 事件转发）
        Row(
            Modifier
                .fillMaxWidth()
                .padding(horizontal = Lx.space.s4, vertical = Lx.space.s2),
            horizontalArrangement = Arrangement.spacedBy(Lx.space.s2),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            androidx.compose.material3.OutlinedTextField(
                value = textDraft,
                onValueChange = { textDraft = it },
                modifier = Modifier
                    .weight(1f)
                    .height(52.dp)
                    .focusRequester(focus),                placeholder = { Text("键入文本发到电脑…", style = Lx.type.BodySmall, color = Lx.colors.FgFaint) },
                textStyle = Lx.type.BodySmall.copy(color = Color.White),
                singleLine = true,
            )
            LxButton(
                "发送",
                {
                    if (textDraft.isNotEmpty()) {
                        real.sendScreenInput(com.moyunteng.lxcode.remote.net.ScreenInputParams(
                            type = "char", text = textDraft, monitor = 0,
                        ))
                        textDraft = ""
                        focus.requestFocus()
                    }
                },
                variant = LxButtonVariant.Secondary,
                enabled = textDraft.isNotEmpty(),
            )
        }
    }
}

/** 键盘条上的单颗键（锁定式修饰键 active 高亮）。 */
@Composable
private fun KeyBarChip(label: String, active: Boolean, onClick: () -> Unit) {
    Box(
        Modifier
            .clip(Lx.radius.FieldShape)
            .background(if (active) Lx.colors.Surface else Color.White.copy(alpha = 0.10f))
            .border(1.dp, if (active) Lx.colors.BorderStrong else Color.White.copy(alpha = 0.16f), Lx.radius.FieldShape)
            .clickable { onClick() }
            .padding(horizontal = Lx.space.s4, vertical = Lx.space.s2),
    ) {
        Text(label, style = Lx.type.Button, color = Color.White.copy(alpha = 0.9f))
    }
}

/**
 * 投屏悬浮球：可拖动小圆球（半透明），点按展开底部操作菜单。
 *
 * - 拖动：pointerInput 手动累积位移（detectDragGestures 不行——它会吞掉 tap，
 *   而本球要求「拖 = 移动 / 点 = 开菜单」同一根手指区分）；按下即消费，不透传
 *   给视频手势（球不参与缩放/双击）。
 * - 吸边：拖动结束（或旋转/容器尺寸变化）后水平吸到最近的左/右边缘，垂直钳在
 *   视口内。位置是**锚点语义**（左上角原点），旋转后只吸边就能回到可见区，
 *   不需要精确记住旧坐标。
 * - rememberSaveable 存归一化位置（x∈[0,1] 从左边缘、y∈[0,1] 从上边缘）：
 *   旋转后按新视口重投影 + 吸边，避免绝对像素位置在竖屏换横屏后出屏。
 */
@Composable
private fun ScreenFloatingBall(
    real: com.moyunteng.lxcode.remote.net.RealBackend,
    state: com.moyunteng.lxcode.remote.ui.AppState,
    onToggleFullscreen: () -> Unit,
) {
    val ballSize = 48.dp
    var menuOpen by rememberSaveable { mutableStateOf(false) }
    // 归一化位置（0..1，左上原点）；rememberSaveable：进程重建/页面重入后位置尽量保留
    var normX by rememberSaveable { mutableStateOf(0.92f) }
    var normY by rememberSaveable { mutableStateOf(0.30f) }
    // 拖动中的绝对像素位置（拖动时不想每帧做归一化换算，松手才归一 + 吸边）
    var dragPx by remember { mutableStateOf<Offset?>(null) }

    val view = androidx.compose.ui.platform.LocalView.current
    val density = androidx.compose.ui.platform.LocalDensity.current
    val ballPx = with(density) { ballSize.toPx() }
    // 观看态豁免了 AppRoot 的 safeDrawing：view 的坐标系铺到系统条之下，吸边/钳制
    // 边界要自补 insets——顶部让开状态栏（含刘海 displayCutout）、底部让开导航栏
    //（手势条），球不顶进系统条、不被手势条遮住。取的是窗口真实 insets
    //（view.rootWindowInsets），横竖屏/手势导航/三键导航的设备差异天然覆盖。
    // **API 分层**：rootWindowInsets.getInsets(int) 是 API 29 的 API（minSdk 26，
    // 直接调用会在 API 26/27/28 真机 NoSuchMethodError）——按 SDK_INT >= Q 守卫；
    // 低版本回落 getSystemWindowInsetTop/Bottom（API 20 起可用、29 起 deprecated，
    // 合成 insets 本就含状态栏+刘海 top / 导航栏 bottom，语义等价近似）。
    fun safePadding(): Pair<Float, Float> {
        val insets = view.rootWindowInsets ?: return 0f to 0f
        if (android.os.Build.VERSION.SDK_INT >= android.os.Build.VERSION_CODES.Q) {
            val bars = insets.getInsets(
                android.view.WindowInsets.Type.statusBars() or
                    android.view.WindowInsets.Type.displayCutout() or
                    android.view.WindowInsets.Type.navigationBars(),
            )
            return with(density) { bars.top.toDp().toPx() to bars.bottom.toDp().toPx() }
        }
        // 低版本（26/27/28）：系统单值 insets（top = 状态栏+刘海，bottom = 导航栏）
        return with(density) {
            insets.systemWindowInsetTop.toDp().toPx() to
                insets.systemWindowInsetBottom.toDp().toPx()
        }
    }

    // 容器尺寸（像素）：悬浮球的坐标系 = 本 Box 的根尺寸（观看态下含系统条区域，
    // 垂直钳制边界经 safePadding 收窄）。
    fun containerSize(): Pair<Float, Float> =
        view.width.toFloat() to view.height.toFloat()

    // 吸边 + 归一化：把绝对像素位置钳进视口并水平吸边，写回 normX/normY。
    fun snapToEdge(px: Offset) {
        val (w, h) = containerSize()
        if (w <= 0f || h <= 0f) return
        val (padTop, padBottom) = safePadding()
        val minY = padTop
        val maxY = (h - padBottom - ballPx).coerceAtLeast(minY)
        val clampedX = px.x.coerceIn(0f, (w - ballPx).coerceAtLeast(0f))
        val clampedY = px.y.coerceIn(minY, maxY)
        // 水平吸边：中心点在左半 → 吸左，否则吸右
        val snappedX = if (clampedX + ballPx / 2f < w / 2f) 0f else (w - ballPx).coerceAtLeast(0f)
        normX = snappedX / w
        normY = clampedY / h
    }

    // 旋转 / 容器尺寸变化：按归一化位置重投影 + 重新吸边（旧像素位置可能出屏，
    // 归一化 + 吸边保证球永远在可见区）。
    val config = androidx.compose.ui.platform.LocalConfiguration.current
    LaunchedEffect(config.orientation, config.screenWidthDp, config.screenHeightDp) {
        val (w, h) = containerSize()
        if (w > 0f && h > 0f) {
            snapToEdge(Offset(normX * w, normY * h))
        }
    }

    // 悬浮球本体位置：拖动中用像素，静止用归一化换算（垂直钳制边界同 safePadding）
    val (w0, h0) = containerSize()
    val (padTop0, padBottom0) = safePadding()
    val restingX = (normX * w0).coerceIn(0f, (w0 - ballPx).coerceAtLeast(0f))
    val restingY = (normY * h0).coerceIn(padTop0, (h0 - padBottom0 - ballPx).coerceAtLeast(padTop0))
    val px = dragPx ?: Offset(restingX, restingY)

    // 菜单开着：球在左/右半屏 → 菜单贴内侧弹（左半 → 菜单左缘对球左缘；
    // 右半 → 菜单右缘对球右缘 = shiftX = 负面板宽），垂直一律向上弹
    //（上方放不下由 provider 回落到球下方，与 AnchorMenu 同一套钳制）。
    val menuWidth = 240.dp
    val ballOnLeft = px.x + ballPx / 2f < w0 / 2f
    val menuShiftX = if (ballOnLeft) 0.dp else -menuWidth
    // 遮罩淡入淡出（160ms 标准曲线），点遮罩关菜单
    val scrimAlpha by lxAnimateFloat(if (menuOpen) 0.22f else 0f, durationMillis = LxMotion.DurationPopIn)

    Box(
        Modifier
            .fillMaxSize()
            .then(if (menuOpen || scrimAlpha > 0.01f) {
                // 菜单开着：全屏半透明遮罩（0.22，淡入淡出），点遮罩 = 关菜单
                Modifier
                    .graphicsLayer { alpha = if (scrimAlpha > 0.01f) 1f else 0f }
                    .background(Lx.colors.TermBg.copy(alpha = scrimAlpha))
                    .let { m ->
                        if (menuOpen) m.pointerInput(Unit) {
                            awaitEachGesture {
                                awaitFirstDown().consume()
                                menuOpen = false
                            }
                        } else m
                    }
            } else Modifier),
    ) {
        // 菜单 = AnchorMenu 模式（PickerMenus.kt 同款白卡 + pop-in/退场动效）：
        // Popup 锚定悬浮球本体（本 Box 是球位置的父布局——球用 offset 摆位，
        // 这里再垫一层同 offset 的零尺寸 Box 作锚点），按球在左/右半屏贴内侧弹、
        // 向上弹、clamp 视口；「停止投屏」是危险项（红字红图标 + DangerSoft 按压）。
        BallAnchoredMenu(
            expanded = menuOpen,
            onDismiss = { menuOpen = false },
            anchorOffsetX = px.x.roundToInt(),
            anchorOffsetY = px.y.roundToInt(),
            ballSizePx = ballPx,
            shiftXDp = menuShiftX,
        ) {
            // 旋转屏幕：requestedOrientation 显式切换（菜单文案带当前方向语义——
            // 现在竖屏就写「横屏观看」）。切回竖屏用 FULL_USER（恢复用户默认跟随
            // 传感器），横屏用 SENSOR_LANDSCAPE（跟随传感器的两个横屏方向）。
            val inLandscape =
                androidx.compose.ui.platform.LocalConfiguration.current.orientation ==
                    android.content.res.Configuration.ORIENTATION_LANDSCAPE
            MenuColumn {
                // 控制模式开关（观看/控制切换；2026-12 第二阶段拍板）：控制模式下
                // 触摸手势映射为电脑键鼠（VideoSurface 的控制手势分支）+ 底部虚拟
                // 键盘条出现。后端「允许控制」关时手势的 screen.input 会被拒——
                // 首个错误 toast 提示，模式本身不阻止切。
                MenuRow(
                    icon = { MenuIcon(Icons.Filled.TouchApp) },
                    title = if (state.screenControlMode) "关闭控制（只观看）" else "控制模式（操作电脑）",
                ) {
                    menuOpen = false
                    state.screenControlMode = !state.screenControlMode
                }
                MenuRow(
                    icon = { MenuIcon(Icons.Filled.Stop) },
                    title = "停止投屏",
                    danger = true,
                ) {
                    menuOpen = false
                    // 显式停止动作：stopScreen + 回连接页 + toast（[endScreenSession]）。
                    // 缩小/放大不走这里——那是纯 UI 态切换，投屏继续。
                    state.endScreenSession()
                }
                MenuDivider()
                MenuTitle("清晰度")
                Row(
                    Modifier.padding(horizontal = 10.dp),
                    horizontalArrangement = Arrangement.spacedBy(4.dp),
                ) {
                    QUALITY_LABELS.forEach { (key, label) ->
                        val selected = real.screenState?.quality == key
                        QualityChip(
                            label = label,
                            selected = selected,
                            onClick = {
                                real.setScreenQuality(key) { err ->
                                    if (err != null) state.screenToast = err
                                    else state.rememberScreenCast(real.screenState?.monitor ?: 0, key)
                                }
                            },
                        )
                    }
                }
                // 显示器切换（多屏才出；点选 = start 换 monitor —— 后端按命令电平
                // 重起 ffmpeg，画面自动切到目标屏）
                if (real.screenMonitors.size > 1) {
                    MenuDivider()
                    MenuTitle("显示器")
                    real.screenMonitors.forEach { m ->
                        MonitorRow(
                            title = m.name,
                            subtitle = "${m.width}×${m.height}" + if (m.primary) " · 主显示器" else "",
                            selected = real.screenState?.monitor == m.id,
                            onClick = {
                                real.setScreenMonitor(m.id) { err ->
                                    if (err != null) state.screenToast = err
                                    else state.rememberScreenCast(m.id, real.screenState?.quality ?: "medium")
                                }
                            },
                        )
                    }
                }
                MenuDivider()
                MenuRow(
                    icon = { MenuIcon(Icons.Filled.FullscreenExit) },
                    title = "缩小（保持投屏）",
                ) {
                    menuOpen = false
                    // 缩小回设置页形态，投屏继续跑（不停止）
                    onToggleFullscreen()
                }
                MenuRow(
                    icon = { MenuIcon(Icons.Outlined.ScreenRotation) },
                    title = if (inLandscape) "竖屏观看" else "横屏观看",
                ) {
                    menuOpen = false
                    val act = view.context as? android.app.Activity
                    val orient =
                        if (inLandscape) android.content.pm.ActivityInfo.SCREEN_ORIENTATION_FULL_USER
                        else android.content.pm.ActivityInfo.SCREEN_ORIENTATION_SENSOR_LANDSCAPE
                    act?.requestedOrientation = orient
                    // 记到 RealBackend：旋转本身会触发 Activity 重建，重建后由
                    // ScreenScreen 的方向 SideEffect 按这份记录重放锁定。
                    real.screenOrientationLock =
                        if (inLandscape) null else android.content.pm.ActivityInfo.SCREEN_ORIENTATION_SENSOR_LANDSCAPE
                }
            }
        }

        // 球本体：半透明深色圆 + Cast 图标；拖动 / 点按展开菜单。
        // 菜单开着时呼应菜单：背景提亮（alpha 0.55→0.75）+ 图标转 90°。
        val ballBgAlpha by lxAnimateFloat(if (menuOpen) 0.75f else 0.55f, durationMillis = LxMotion.DurationPopIn)
        val ballIconRotation by lxAnimateFloat(if (menuOpen) 90f else 0f, durationMillis = LxMotion.DurationPopIn)
        Box(
            Modifier
                .size(ballSize)
                .offset { IntOffset(px.x.roundToInt(), px.y.roundToInt()) }
                .clip(CircleShape)
                .background(Lx.colors.TermDim.copy(alpha = ballBgAlpha))
                .border(1.dp, Lx.colors.TermBg.copy(alpha = 0.6f), CircleShape)
                .pointerInput(Unit) {
                    awaitEachGesture {
                        val down = awaitFirstDown()
                        down.consume()
                        val startPos = px
                        var moved = false
                        var acc = Offset.Zero
                        while (true) {
                            val event = awaitPointerEvent()
                            val change = event.changes.firstOrNull() ?: break
                            if (!change.pressed) break
                            val delta = change.positionChange()
                            if (delta != Offset.Zero) {
                                acc += delta
                                if (acc.getDistance() > viewConfiguration.touchSlop) moved = true
                                if (moved) {
                                    val cur = (dragPx ?: startPos) + delta
                                    val (cw, ch) = containerSize()
                                    val (padTopD, padBottomD) = safePadding()
                                    val minY = padTopD
                                    val maxY = (ch - padBottomD - ballPx).coerceAtLeast(minY)
                                    dragPx = Offset(
                                        cur.x.coerceIn(0f, (cw - ballPx).coerceAtLeast(0f)),
                                        cur.y.coerceIn(minY, maxY),
                                    )
                                    change.consume()
                                }
                            }
                        }
                        if (moved) {
                            dragPx?.let { snapToEdge(it) }
                            dragPx = null
                        } else {
                            // 点按（未过 slop）= 开关菜单
                            menuOpen = !menuOpen
                        }
                    }
                },
            contentAlignment = Alignment.Center,
        ) {
            Icon(
                imageVector = Icons.Filled.Cast,
                contentDescription = "投屏控制",
                tint = androidx.compose.ui.graphics.Color.White,
                modifier = Modifier
                    .size(22.dp)
                    .graphicsLayer { rotationZ = ballIconRotation },
            )
        }
    }
}

// ===== 悬浮球菜单组件（对齐 PickerMenus.kt 的弹层语言）=====

/** 菜单面板圆角 —— composer.css `.mp-panel { border-radius: 12px }`。 */
private val BallMenuPanelShape = RoundedCornerShape(12.dp)

/** 菜单条目圆角 —— `.mp-item { border-radius: 7px }`。 */
private val BallMenuItemShape = RoundedCornerShape(7.dp)

/** 面板与悬浮球的间距。 */
private val BallMenuGap = 8.dp

/** 菜单宽 —— 240dp（规格拍板）。 */
private val BallMenuWidth = 240.dp

/**
 * 球锚定菜单容器 —— PickerMenus.kt `AnchorMenu` 的悬浮球版：
 *  * 位置：锚点 = 悬浮球本体（垫一层与球同 offset 的零尺寸 Box 作 Popup 锚点）；
 *    x 按球在左/右半屏贴内侧弹（[shiftXDp]，右半屏传负面板宽），钳进视口 ±8dp；
 *    y 一律向上弹（球顶 - 内容高 - 8dp），上方放不下回落到球下方（Provider 内钳制）；
 *  * 动效：入场 pop-in（translateY(-4) + scale(0.98) + 淡入，160ms 标准曲线）、
 *    退场（淡出 + y+8 + scale→0.96，200ms power2.in 型），退场跑完才卸载 Popup；
 *  * 关闭：focusable popup 的「点外部 / 返回键」→ [onDismiss]；遮罩/再点球由调用方处理。
 */
@Composable
private fun BallAnchoredMenu(
    expanded: Boolean,
    onDismiss: () -> Unit,
    anchorOffsetX: Int,
    anchorOffsetY: Int,
    ballSizePx: Float,
    shiftXDp: Dp,
    content: @Composable () -> Unit,
) {
    // mounted：Popup 挂载态。展开即挂载；收起后等退场动画跑完（isIdle）才卸载。
    var mounted by remember { mutableStateOf(false) }
    if (expanded) mounted = true
    if (!mounted) return
    val density = LocalDensity.current
    val provider = remember(anchorOffsetX, anchorOffsetY, ballSizePx, shiftXDp, density) {
        val shiftX = with(density) { shiftXDp.roundToPx() }
        val gap = with(density) { BallMenuGap.roundToPx() }
        val margin = with(density) { 8.dp.roundToPx() }
        object : PopupPositionProvider {
            override fun calculatePosition(
                anchorBounds: IntRect,
                windowSize: IntSize,
                layoutDirection: LayoutDirection,
                popupContentSize: IntSize,
            ): IntOffset {
                // 锚点 = 球的左上角（锚定 Box 与球同 offset、同大小）
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
    // 锚定 Box：与球同 offset、同大小，让 Popup 的 anchorBounds 对准球本体
    Box(
        Modifier
            .offset { IntOffset(anchorOffsetX, anchorOffsetY) }
            .size(with(density) { ballSizePx.toDp() }),
    ) {
        Popup(
            popupPositionProvider = provider,
            onDismissRequest = onDismiss,
            properties = PopupProperties(focusable = true),
        ) {
            val vis = remember { MutableTransitionState(false) }
            vis.targetState = expanded
            androidx.compose.runtime.LaunchedEffect(vis.isIdle) {
                if (vis.isIdle && !vis.targetState) mounted = false
            }
            AnimatedVisibility(
                visibleState = vis,
                enter = ballMenuPopIn(),
                exit = ballMenuCollapse(),
            ) {
                content()
            }
        }
    }
}

/** 入场 pop-in —— composer.css `@keyframes pop-in`：translateY(-4px) scale(0.98) 淡入，160ms。 */
@Composable
private fun ballMenuPopIn(): androidx.compose.animation.EnterTransition {
    val fromY = with(LocalDensity.current) { (-4).dp.roundToPx() }
    val spec = tween<Float>(LxMotion.DurationPopIn, easing = LxMotion.Easing)
    return fadeIn(spec) +
        slideInVertically(tween(LxMotion.DurationPopIn, easing = LxMotion.Easing)) { fromY } +
        scaleIn(spec, initialScale = 0.98f)
}

/** 退场收回 —— 淡出 + y+8 + scale→0.96（origin 左下），200ms power2.in 型。 */
@Composable
private fun ballMenuCollapse(): androidx.compose.animation.ExitTransition {
    val dropY = with(LocalDensity.current) { 8.dp.roundToPx() }
    val floatSpec = tween<Float>(200, easing = LxMotion.EasingExit)
    val offsetSpec = tween<IntOffset>(200, easing = LxMotion.EasingExit)
    return fadeOut(floatSpec) +
        slideOutVertically(offsetSpec) { dropY } +
        scaleOut(floatSpec, targetScale = 0.96f, transformOrigin = TransformOrigin(0f, 1f))
}

/** 白卡面板 —— `.mp-panel`：宽 240、1px BorderStrong 边 + 圆角 12 + 菜单级阴影 + padding 8。 */
@Composable
private fun MenuColumn(content: @Composable () -> Unit) {
    Column(
        Modifier
            .width(BallMenuWidth)
            .shadow(
                elevation = 8.dp,
                shape = BallMenuPanelShape,
                clip = false,
                ambientColor = Lx.shadow.Float.copy(alpha = 0.08f),
                spotColor = Lx.shadow.Float.copy(alpha = 0.12f),
            )
            .clip(BallMenuPanelShape)
            .background(Lx.colors.Surface)
            .border(1.dp, Lx.colors.BorderStrong, BallMenuPanelShape)
            .padding(8.dp),
        verticalArrangement = Arrangement.spacedBy(2.dp),
    ) {
        content()
    }
}

/** 小节标题 —— `.mp-title`：10sp/600/FgFaint/大写/padding 10/10/4/2。 */
@Composable
private fun MenuTitle(text: String) {
    Text(
        text = text.uppercase(),
        style = TextStyle(
            fontFamily = Lx.type.Sans,
            fontSize = 10.sp,
            fontWeight = FontWeight.SemiBold,
            color = Lx.colors.FgFaint,
            letterSpacing = 0.07.em,
        ),
        modifier = Modifier.padding(start = 10.dp, end = 10.dp, top = 4.dp, bottom = 2.dp),
    )
}

/** 1dp 分组分隔线 —— BorderSoft。 */
@Composable
private fun MenuDivider() {
    Box(
        Modifier
            .padding(vertical = 4.dp)
            .height(1.dp)
            .fillMaxWidth()
            .background(Lx.colors.BorderSoft),
    )
}

/** 16dp 语义图标（动作行左槽）。 */
@Composable
private fun MenuIcon(icon: androidx.compose.ui.graphics.vector.ImageVector, tint: Color = Lx.colors.FgMuted) {
    Icon(
        imageVector = icon,
        contentDescription = null,
        tint = tint,
        modifier = Modifier.size(16.dp),
    )
}

/**
 * 动作行 —— 统一「图标 + 13sp/500 文字」：高 44dp 固定、按压 BorderSoft 底
 * （危险项 Danger 色文字/图标 + 按压 DangerSoft 底）。
 */
@Composable
private fun MenuRow(
    icon: @Composable () -> Unit,
    title: String,
    danger: Boolean = false,
    onClick: () -> Unit,
) {
    val interaction = remember { MutableInteractionSource() }
    val pressed by interaction.collectIsPressedAsState()
    val bg by lxAnimateColor(
        target = when {
            pressed && danger -> Lx.colors.DangerSoft
            pressed -> Lx.colors.BorderSoft
            else -> Color.Transparent
        },
    )
    val ink = if (danger) Lx.colors.Danger else Lx.colors.Fg
    Row(
        Modifier
            .fillMaxWidth()
            .height(44.dp)
            .clip(BallMenuItemShape)
            .background(bg)
            .clickable(interactionSource = interaction, indication = null, onClick = onClick)
            .padding(horizontal = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        icon()
        Text(
            title,
            style = TextStyle(
                fontFamily = Lx.type.Sans,
                fontSize = 13.sp,
                fontWeight = FontWeight.Medium,
                color = ink,
            ),
            modifier = Modifier.weight(1f),
        )
    }
}

/** 清晰度 chip —— 三档统一 Surface 底（选中 Fg 字 + BorderStrong 边），按压 BorderSoft。 */
@Composable
private fun QualityChip(
    label: String,
    selected: Boolean,
    onClick: () -> Unit,
) {
    val interaction = remember { MutableInteractionSource() }
    val pressed by interaction.collectIsPressedAsState()
    val bg = if (pressed) Lx.colors.BorderSoft else Lx.colors.Surface
    Row(
        Modifier
            .clip(BallMenuItemShape)
            .background(bg)
            .border(1.dp, if (selected) Lx.colors.BorderStrong else Lx.colors.Border, BallMenuItemShape)
            .clickable(interactionSource = interaction, indication = null, onClick = onClick)
            .padding(horizontal = 10.dp, vertical = 5.dp),
    ) {
        Text(
            label,
            style = TextStyle(
                fontFamily = Lx.type.Sans,
                fontSize = 12.sp,
                fontWeight = FontWeight.Medium,
                color = if (selected) Lx.colors.Fg else Lx.colors.FgFaint,
            ),
        )
    }
}

/** 显示器行 —— `.mp-item` 风格：12.5sp 标题 + FgFaint 副标题 + 选中勾。 */
@Composable
private fun MonitorRow(
    title: String,
    subtitle: String,
    selected: Boolean,
    onClick: () -> Unit,
) {
    val interaction = remember { MutableInteractionSource() }
    val pressed by interaction.collectIsPressedAsState()
    val bg by lxAnimateColor(target = if (pressed) Lx.colors.BorderSoft else Color.Transparent)
    Row(
        Modifier
            .fillMaxWidth()
            .clip(BallMenuItemShape)
            .background(bg)
            .clickable(interactionSource = interaction, indication = null, onClick = onClick)
            .padding(start = 10.dp, end = 10.dp, top = 5.dp, bottom = 5.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(1.dp)) {
            Text(
                title,
                style = TextStyle(
                    fontFamily = Lx.type.Sans,
                    fontSize = 12.5.sp,
                    fontWeight = if (selected) FontWeight.Medium else FontWeight.Normal,
                    color = Lx.colors.Fg,
                ),
            )
            Text(
                subtitle,
                style = TextStyle(fontFamily = Lx.type.Sans, fontSize = 10.5.sp, color = Lx.colors.FgFaint),
            )
        }
        if (selected) BallMenuCheck()
    }
}

/** 选中勾 —— PickerMenus 的 MenuCheck：13dp、stroke 2.2、Fg 色。 */
@Composable
private fun BallMenuCheck() {
    BallStrokePathIcon(
        d = "M20 6 9 17l-5-5",
        size = 13.dp,
        color = Lx.colors.Fg,
        strokeWidth = 2.2f,
    )
}

/** 24 视窗描边路径图标 —— PickerMenus StrokePathIcon 同款（PathParser 不引新依赖）。 */
@Composable
private fun BallStrokePathIcon(
    d: String,
    size: Dp,
    color: Color,
    strokeWidth: Float = 1.8f,
) {
    val path = remember(d) {
        androidx.compose.ui.graphics.vector.PathParser().parsePathString(d).toPath()
    }
    androidx.compose.foundation.Canvas(Modifier.size(size)) {
        val s = this.size.width / 24f
        withTransform({ scale(s, s, pivot = Offset.Zero) }) {
            drawPath(
                path,
                color,
                style = Stroke(
                    width = strokeWidth,
                    cap = StrokeCap.Round,
                    join = StrokeJoin.Round,
                ),
            )
        }
    }
}

/** 悬浮球菜单的分组小标题。 */

/** 实时状态行：投屏 pill + 明细（显示器 · 画质 · 编码器）。 */
@Composable
private fun StatusLine(real: com.moyunteng.lxcode.remote.net.RealBackend) {
    val st = real.screenState
    when {
        st?.active == true -> Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(Lx.space.s6),
        ) {
            LxPill("正在投屏", variant = LxPillVariant.Success)
            Text(
                "显示器 ${st.monitor + 1} · ${qualityLabel(st.quality)}" +
                    (st.encoder.takeIf { it.isNotEmpty() }?.let { " · $it" } ?: ""),
                style = Lx.type.BodySmall,
                color = Lx.colors.FgMuted,
            )
        }
        st?.error != null -> Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(Lx.space.s6),
        ) {
            LxPill("上次失败", variant = LxPillVariant.Danger)
            Text(
                st.error,
                style = Lx.type.BodySmall,
                color = Lx.colors.FgMuted,
                modifier = Modifier.weight(1f),
            )
        }
        real.screenBusy -> Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(Lx.space.s6),
        ) {
            LxSpinner(size = 12.dp)
            Text("正在切换投屏…", style = Lx.type.BodySmall, color = Lx.colors.FgMuted)
        }
        else -> LxPill("未投屏", variant = LxPillVariant.Muted)
    }
}

@Composable
private fun VideoSurface(
    real: com.moyunteng.lxcode.remote.net.RealBackend,
    fullscreen: Boolean = false,
    onToggleFullscreen: () -> Unit = {},
    /** AppState（控制模式开关与 toast 写入口）——控制手势分支用；观看/卡内
     *  手势分支不碰它。 */
    state: com.moyunteng.lxcode.remote.ui.AppState? = null,
) {
    // 尺寸语义（见下方容器注释）：观看态 cover 铺满 + clipToBounds；设置页卡内态
    // fit letterbox。手势算术（ScreenGestureMath）共用，边界按态注入：全屏观看 =
    // fitBaseScale 下限 + coverOverflowAt 真实溢出钳制（可缩回 fit 露黑边）；
    // 卡内态不进 transform 分支（只留 tap/双击，卡内不需要缩放/平移）。
    // 解码器生命周期与 SurfaceView 绑定：surface 建好即建解码器、销毁即停；
    // 断线 ≥3s 丢弃解码器（重连后等关键帧重建）。
    // 旋转：Surface 销毁重建（Activity 重建 → AndroidView 重 factory）时**不重建
    // 解码器**——新 surface 到达后优先 mediaCodec.setOutputSurface（API 23+）热重绑，
    // 异常才走整解码器重建（KeyframeGate 保证等下一个 IDR ≤2s 恢复）。
    var surface by remember { mutableStateOf<android.view.Surface?>(null) }
    var decoder by remember { mutableStateOf<ScreenDecoder?>(null) }
    var wasConnected by remember { mutableStateOf(true) }
    // 全屏切换由页面级状态驱动（ScreenScreen 提升所有权后经参数传入——
    // VideoSurface 不再自持状态，双击只回调、不重排外部布局）
    // 手势缩放（pinch）：1×（cover 基准，已铺满）起、上限 5×；全屏观看态下限放宽
    // 到 fitBaseScale（可缩回完整画面露黑边，见下方手势块注释）。配合平移查看被
    // 裁的部分。
    // SurfaceView 是连续表面，缩放通过 graphicsLayer 对整块视图做变换（解码帧
    // 自身按容器比例拉伸）。
    // 旋转后视口尺寸变化：缩放/平移一律复位基准 + 居中（无跳变感的可接受行为）
    var scale by remember { mutableStateOf(1f) }
    var offsetX by remember { mutableStateOf(0f) }
    var offsetY by remember { mutableStateOf(0f) }

    fun resetGestures() {
        scale = coverBaseScale(); offsetX = 0f; offsetY = 0f
    }

    // 断线检测：连接丢失 ≥3s → 停解码器（重连后等关键帧重建）
    LaunchedEffect(Unit) {
        while (true) {
            delay(1000)
            val nowConnected = real.phase == com.moyunteng.lxcode.remote.net.RealPhase.Connected
            if (wasConnected && !nowConnected) {
                delay(3000)
                if (real.phase != com.moyunteng.lxcode.remote.net.RealPhase.Connected) {
                    decoder?.stop()
                    decoder = null
                    real.screenDecoder = null
                }
            }
            wasConnected = real.phase == com.moyunteng.lxcode.remote.net.RealPhase.Connected
        }
    }

    // 旋转/重组：视口尺寸变化时把缩放/平移复位（cover 基准 + 居中，铺满重新生效）
    val config = androidx.compose.ui.platform.LocalConfiguration.current
    LaunchedEffect(config.orientation, config.screenWidthDp, config.screenHeightDp) {
        resetGestures()
    }

    // 离开页面时兜底复位手势
    DisposableEffect(Unit) {
        onDispose { resetGestures() }
    }

    // 视频容器约束：黑底铺满（观看态整页、设置页态 fillMaxWidth 高度撑开）。
    // 比例适配统一在内部 letterbox，不再用固定 16:9 容器硬套电脑画面；
    // 视口 = 容器实测 bounds（onSizeChanged 本地回写），按 DecoderStats 的
    // streamWidth/Height（SPS 解析）算尺寸（算法在 ScreenGestureMath，纯函数
    // JVM 单测覆盖）：
    //  - 观看态（fullscreen）：**cover**（coverVideoSize，最小铺满——画面必盖满
    //    视口，多出部分被 clipToBounds 裁掉，「整个页面全是投屏画面、四周零边框」，
    //    2026-12 拍板）；
    //  - 设置页卡内态：fit（letterbox，卡内完整画面）。
    // 不用 BoxWithConstraints：configChanges 旋转只重组不重建，重组瞬间约束可能
    // 还是旋转前值（0.3.31 真机巨大黑边根因）。
    // 观看态无卡片：无圆角/内边距（0.3.30 拍板）；clipToBounds 是 cover 的配套——
    // SurfaceView bounds 超出视口，不裁会把溢出画面画到容器外。
    var containerSize by remember { mutableStateOf(android.util.Size(0, 0)) }
    // cover 布局的平移钳制边界：SurfaceView bounds = cover 尺寸（中心对齐视口），
    // 允许的平移半程 = 每轴溢出量的一半（拖动极限 = 画面边缘贴住视口边缘）。
    // 视频未知时溢出为 0（画面即视口，不可拖）。容器尺寸/视频比例变化即重算；
    // fitTick 每 500ms 递增并作为 remember key（stats() 不是 Compose 状态，解码
    // 线程更新——SPS 配置完成即重排，同 DiagPanel 的轮询刷新法）。
    var fitTick by remember { mutableIntStateOf(0) }
    LaunchedEffect(Unit) {
        while (true) {
            delay(500)
            fitTick++
        }
    }
    // 手势边界参数（根因 A+C）：全屏观看态允许双指缩回完整画面——缩放下限 =
    // fitBaseScale（fit/cover 比，通常 <1；缩到底 = 画面完整、四周黑边的 fit 等效），
    // 平移钳制 = coverOverflowAt(scale)（当前 scale 的真实溢出半程：scale ≥1 时与
    // 旧 cover 语义一致；scale <1 的黑边态按 fit 边界算——画面小于视口的轴钳 0
    // 居中，仍有溢出的轴可拖动看被裁内容）。设置页卡内态无缩放/平移（卡内手势
    // 只留 tap/双击，见 pointerInput），不需要边界。
    // 视口/视频尺寸都是普通 remember 快照：pointerInput(Unit) 闭包**不直接捕获
    // 它们**（根因 C：旧 panLimit 是 remember 快照且被闭包捕获，containerSize
    // 初值 0 时算出 (0,0) 恒死钳居中——全屏放大后拖不动），一律经
    // rememberUpdatedState 取 current；也不把 containerSize 塞 pointerInput key
    //（key 变化会取消并重启协程，掐断进行中的手势）。fitTick 每 500ms 递增并作为
    // remember key（stats() 不是 Compose 状态，解码线程更新——SPS 配置完成即重算，
    // 同 DiagPanel 的轮询刷新法）。
    val viewportPx = remember(containerSize) {
        containerSize.width.toFloat() to containerSize.height.toFloat()
    }
    val currentViewportPx by rememberUpdatedState(viewportPx)
    val videoDims = remember(fitTick) {
        val st = real.screenDecoder?.stats()
        (st?.width ?: 0) to (st?.height ?: 0)
    }
    val currentVideoDims by rememberUpdatedState(videoDims)
    // SurfaceView bounds（cover/fit 尺寸）提前到手势层：控制模式的坐标换算
    // （touchToNormalized）与 AndroidView 的布局用同一份（fitTick 驱动重算）。
    // state == null（理论不可达：两处调用都传）→ 控制模式退化为观看。
    val controlMode = state?.screenControlMode == true && fullscreen
    val surfaceSize = remember(fitTick, containerSize) {
        if (fullscreen) {
            coverVideoSize(
                viewportW = containerSize.width.toFloat(),
                viewportH = containerSize.height.toFloat(),
                videoW = real.screenDecoder?.stats()?.width ?: 0,
                videoH = real.screenDecoder?.stats()?.height ?: 0,
            )
        } else {
            fitVideoSize(
                viewportW = containerSize.width.toFloat(),
                viewportH = containerSize.height.toFloat(),
                videoW = real.screenDecoder?.stats()?.width ?: 0,
                videoH = real.screenDecoder?.stats()?.height ?: 0,
            )
        }
    }
    val currentSurfaceSize by rememberUpdatedState(surfaceSize)
    // 控制事件上报（sendScreenInput）：控制模式下首个错误 toast 提示（后续错误
    // 静默——「控制未开启」的提示弹一次就够，move 高频失败不能刷屏）。
    var controlErrorShown by remember { mutableStateOf(false) }
    val sendInput: (com.moyunteng.lxcode.remote.net.ScreenInputParams) -> Unit = { p ->
        real.sendScreenInput(p) { err ->
            if (!controlErrorShown && state != null) {
                controlErrorShown = true
                state.screenToast = err
            }
        }
    }
    Box(
        Modifier
            .then(
                if (fullscreen) {
                    // 观看/全屏态：无边框铺满（无 padding/圆角/卡片，0.3.30 拍板）
                    // + clipToBounds：cover 布局的 SurfaceView 比视口大，裁掉溢出
                    Modifier
                        .fillMaxSize()
                        .clipToBounds()
                } else {
                    // 设置页卡内态：黑底圆角卡（原设计语言）+ 内部统一 letterbox
                    Modifier
                        .fillMaxWidth()
                        .aspectRatio(16f / 9f)
                        .clip(Lx.radius.CardShape)
                },
            )
            .background(Lx.colors.TermBg)
            .onSizeChanged { containerSize = android.util.Size(it.width, it.height) }
        // 手势单入口：awaitEachGesture 顺序判定——多指或有移动 = transform（缩放/平移，
        // 仅全屏观看态；设置页卡内态只留 tap/双击——根因 D：卡内 16:9 卡片不需要
        // 双指缩放，旧行为能缩到 5× 而 offset 钳 0、且无复位入口，画面永久变形占卡）；
        // 单指快速按下抬起×2（<300ms）= 双击全屏。原双 pointerInput 并存时 transform
        // 检测器在双击首按期间以 zoom≈1 微调画面。
        .pointerInput(Unit) {
                if (controlMode && fullscreen) {
                    // ===== 控制模式手势（2026-12 第二阶段拍板）=====
                    // 映射：单指 tap = 左键单击；双指 tap（无位移）= 右键单击；
                    // 单指按下移动 = 按住左键拖动（down→move×N→up）；双指移动 =
                    // 滚轮（双指整体位移 → 滚轮刻度，dy 每 ~40px 一刻度）；
                    // 双指捏合 = 本地缩放（观看需要，不发给电脑）。
                    // 坐标换算：touchToNormalized（ScreenGestureMath，纯函数）——
                    // 触点视口 px 反变换 graphicsLayer（scale/translation）再除
                    // cover 尺寸归一化。
                    awaitEachGesture {
                        val first = awaitFirstDown()
                        first.consume()
                        var sentDown = false
                        var sawSecond = false
                        var isScroll = false
                        var isPinch = false
                        var lastScrollY = first.position.y
                        var lastScrollX = first.position.x
                        // 拖动阈值前先记起点（没过 slop 且快速抬起 = tap）
                        val startX = first.position.x
                        val startY = first.position.y
                        var moved = false
                        while (true) {
                            val event = awaitPointerEvent()
                            val pressed = event.changes.count { it.pressed }
                            if (pressed == 0) break
                            val pos = event.changes.firstOrNull()?.position ?: break
                            if (pressed >= 2) {
                                sawSecond = true
                                // 双指：区分滚轮（同向整体位移）与捏合（间距变化）
                                val zoom = event.calculateZoom()
                                if (kotlin.math.abs(zoom - 1f) > ZOOM_NOISE_EPSILON) {
                                    isPinch = true
                                }
                                if (!isPinch) {
                                    // 滚轮：双指位移累积（以任一指的位移近似整体位移）
                                    val dy = pos.y - lastScrollY
                                    val dx = pos.x - lastScrollX
                                    lastScrollY = pos.y
                                    lastScrollX = pos.x
                                    if (kotlin.math.abs(dy) > 8f || kotlin.math.abs(dx) > 8f) {
                                        isScroll = true
                                        val (nx, ny) = touchToNormalized(
                                            pos.x, pos.y,
                                            currentViewportPx.first, currentViewportPx.second,
                                            scale, offsetX, offsetY,
                                            currentSurfaceSize.width, currentSurfaceSize.height,
                                        ) ?: continue
                                        sendInput(com.moyunteng.lxcode.remote.net.ScreenInputParams(
                                            type = "scroll", x = nx.toDouble(), y = ny.toDouble(),
                                            dx = 0.0, dy = (-dy / 40.0), // 双指上滑 = 内容上滚 = 滚轮上（dy 正）；触点 y 减小（上滑）→ 内容上滚
                                            monitor = 0,
                                        ))
                                    }
                                }
                                event.changes.forEach { it.consume() }
                                continue
                            }
                            // 单指：过 slop = 按住拖动（首次先发 down）
                            if (moved || (pos - Offset(startX, startY)).getDistance() > viewConfiguration.touchSlop) {
                                moved = true
                                val (nx, ny) = touchToNormalized(
                                    pos.x, pos.y,
                                    currentViewportPx.first, currentViewportPx.second,
                                    scale, offsetX, offsetY,
                                    currentSurfaceSize.width, currentSurfaceSize.height,
                                ) ?: continue
                                if (!sentDown) {
                                    sentDown = true
                                    sendInput(com.moyunteng.lxcode.remote.net.ScreenInputParams(
                                        type = "down", x = nx.toDouble(), y = ny.toDouble(), button = "left", monitor = 0,
                                    ))
                                }
                                sendInput(com.moyunteng.lxcode.remote.net.ScreenInputParams(
                                    type = "move", x = nx.toDouble(), y = ny.toDouble(), monitor = 0,
                                ))
                                event.changes.forEach { it.consume() }
                            }
                        }
                        // 抬起：按拖动类型收尾
                        if (sentDown) {
                            // 按住拖动 → up（up 只发按键 up；坐标无效字段被壳忽略）
                            sendInput(com.moyunteng.lxcode.remote.net.ScreenInputParams(
                                type = "up", button = "left", monitor = 0,
                            ))
                        } else if (sawSecond) {
                            // 双指快速 tap（无位移/未捏合）= 右键单击（点在首指位置）
                            touchToNormalized(
                                startX, startY,
                                currentViewportPx.first, currentViewportPx.second,
                                scale, offsetX, offsetY,
                                currentSurfaceSize.width, currentSurfaceSize.height,
                            )?.let { (nx, ny) ->
                                sendInput(com.moyunteng.lxcode.remote.net.ScreenInputParams(
                                    type = "click", x = nx.toDouble(), y = ny.toDouble(), button = "right", monitor = 0,
                                ))
                            }
                        } else if (!isScroll && !isPinch) {
                            // 单指快速 tap = 左键单击
                            touchToNormalized(
                                startX, startY,
                                currentViewportPx.first, currentViewportPx.second,
                                scale, offsetX, offsetY,
                                currentSurfaceSize.width, currentSurfaceSize.height,
                            )?.let { (nx, ny) ->
                                sendInput(com.moyunteng.lxcode.remote.net.ScreenInputParams(
                                    type = "click", x = nx.toDouble(), y = ny.toDouble(), button = "left", monitor = 0,
                                ))
                            }
                        }
                    }
                } else {
                    // ===== 观看/卡内模式手势（原逻辑不动）=====
                    awaitEachGesture {
                    val down = awaitFirstDown()
                    down.consume()
                    var pan = Offset.Zero
                    var zoomMul = 1f
                    var isTransform = false
                    var pastSlop = false
                    var upSeen = false
                    // 阶段一：分类本轮手势——出现多指或位移过 touchSlop = transform，
                    // 否则是 tap（快速抬起 + 300ms 内再来一次 = 双击）。
                    // 卡内态（fullscreen=false）多指/移动也归类为 tap 的失败分支：
                    // 不进 transform（不缩放不平移不消费），落空即结束本轮。
                    val classifyDeadline = SystemClock.uptimeMillis() + 300
                    while (true) {
                        val remaining = classifyDeadline - SystemClock.uptimeMillis()
                        val event = withTimeoutOrNull(remaining.coerceAtLeast(1L)) { awaitPointerEvent() }
                        if (event == null) break
                        val pressed = event.changes.count { it.pressed }
                        val anyPos = event.changes.firstOrNull() ?: break
                        if (event.changes.size > 1 || pressed > 1) { isTransform = true }
                        else if (!pastSlop &&
                            anyPos.positionChange().getDistance() > viewConfiguration.touchSlop
                        ) {                            pastSlop = true; isTransform = true
                        }
                        if (!anyPos.pressed) upSeen = true
                        if (isTransform) break
                        if (upSeen) break
                    }
                    if (!isTransform || !fullscreen) {
                        // tap：等第二次按下（300ms）再等抬起
                        var doubleTap = false
                        val second = withTimeoutOrNull(300L) { awaitFirstDown(requireUnconsumed = false) }
                        if (second != null) {
                            second.consume()
                            val secondUp = withTimeoutOrNull(300L) {
                                while (true) {
                                    val ev = awaitPointerEvent()
                                    if (ev.changes.all { !it.pressed }) break
                                }
                                true
                            }
                            doubleTap = secondUp == true
                        }
                        if (doubleTap) {
                            onToggleFullscreen()
                            // 显式目标值：切换全屏一律回 1× + 居中（不残留隐式缩放/偏移）
                            resetGestures()
                        }
                    } else {
                        // 阶段二：transform（仅全屏观看态；卡内已被上面的 !fullscreen
                        // 分支收窄掉）——多指围绕质心缩放，放大（或缩离基准）后平移。
                        // scale/offset 算术在 ScreenGestureMath（纯函数，JVM 单测覆盖）：
                        // zoom 是相对增量、|zoom-1|<ε 的噪声帧不消费（0.3.22 真机
                        // 「缩成一点」根因就是每帧消费 zoom 噪声并塌向质心）；
                        // 缩放下限 = fitBaseScale（根因 A：cover 布局 SurfaceView
                        // bounds 本身已是铺满的放大态，允许缩到 fit/cover 比 —— 画面
                        // 完整、四周黑边（fit 等效）；不许再低——那只是画面变小）。
                        val minScale = if (fullscreen) {
                            val (vw, vh) = currentViewportPx
                            val (dw, dh) = currentVideoDims
                            fitBaseScale(vw, vh, dw, dh)
                        } else {
                            coverBaseScale()
                        }
                        while (true) {
                            val event = awaitPointerEvent()
                            val pressed = event.changes.count { it.pressed }
                            if (pressed == 0) break
                            val zoom = event.calculateZoom()
                            val pan = event.calculatePan()
                            val c = event.calculateCentroid()
                            if (pressed >= 2 && c.isSpecified) {
                                val (newScale, newOffset) = applyPinchZoom(
                                    scale, zoom, Offset(offsetX, offsetY), c,
                                    minScale = minScale,
                                )
                                scale = newScale
                                offsetX = newOffset.x
                                offsetY = newOffset.y
                            }
                            // 平移在偏离基准 scale 时消费（钳制边界 = 当前 scale 的
                            // 真实溢出半程——根因 A：scale ≥1 时与旧 cover 语义一致
                            //（拖回被裁部分）；scale <1 的黑边态按 fit 边界算：画面
                            // 小于视口的轴钳 0 居中，仍有溢出的轴可拖动看被裁内容）。
                            // 视口/视频尺寸读 rememberUpdatedState 的 current（根因 C：
                            // 闭包捕获 remember 快照会钉死初值——containerSize 初值 0
                            // 时 panLimit 恒 (0,0)，全屏放大后拖不动）。
                            if (scale > minScale + 1e-4f) {
                                val (vw, vh) = currentViewportPx
                                val (dw, dh) = currentVideoDims
                                val (limX, limY) = coverOverflowAt(scale, vw, vh, dw, dh)
                                val panned = clampPanToCover(
                                    applyPinchPan(Offset(offsetX, offsetY), pan),
                                    overflowX = limX,
                                    overflowY = limY,
                                )
                                offsetX = panned.x
                                offsetY = panned.y
                                event.changes.forEach { it.consume() }
                            }
                        }
                        if (scale <= minScale + 0.001f) {
                            scale = minScale; offsetX = 0f; offsetY = 0f
                        }
                    }
                    }
                }
            },
        contentAlignment = Alignment.Center,
    ) {
        // （SurfaceView bounds 已提前到手势层——见上方 surfaceSize 注释）
        // SurfaceView 的 bounds = 视频按真实比例的尺寸（居中）。视口 = 容器实测
        // bounds（onSizeChanged 本地回写，px）：不用 BoxWithConstraints 的
        // maxWidth/maxHeight——configChanges 旋转只重组不重建，重组瞬间
        // LocalConfiguration 可能仍是旋转前值（2340×1080 → 1080×2340 旧值），
        // 按竖视口算 16:9 视频会把画面算错 = 0.3.31 真机「四周巨大黑边」；实测
        // bounds 由布局阶段给出，旋转后（包括 insets 收窄）必然正确且无时序歧义。
        // 尺寸语义按态分：观看态（fullscreen）= cover（最小铺满，超出被
        // clipToBounds 裁掉）；设置页卡内态 = fit（letterbox 完整画面）。
        // 视频未知（未配置）时 = 视口本身（fill，帧到位后 stats 变化自动重排）。
        // fitTick/边界参数（currentViewportPx/currentVideoDims）见容器声明处。
        val density = LocalDensity.current
        AndroidView(
            factory = { ctx ->
                SurfaceView(ctx).apply {
                    holder.addCallback(object : android.view.SurfaceHolder.Callback {
                        override fun surfaceCreated(holder: android.view.SurfaceHolder) {
                            surface = holder.surface
                        }

                        override fun surfaceChanged(holder: android.view.SurfaceHolder, format: Int, width: Int, height: Int) {}

                        override fun surfaceDestroyed(holder: android.view.SurfaceHolder) {
                            surface = null
                        }
                    })
                }
            },
            modifier = Modifier
                .size(with(density) { surfaceSize.width.toDp() }, with(density) { surfaceSize.height.toDp() })
                .graphicsLayer {
                    scaleX = scale
                    scaleY = scale
                    translationX = offsetX
                    translationY = offsetY
                },
        )
        // 占位态：未投屏且没有解码器 → 44dp 图标框 + 引导文案（对齐 LxEmptyState）
        if (real.screenState?.active != true && decoder == null) {
            Column(
                horizontalAlignment = Alignment.CenterHorizontally,
                verticalArrangement = Arrangement.spacedBy(Lx.space.s6),
            ) {
                Box(
                    Modifier
                        .size(44.dp)
                        .border(1.dp, Lx.colors.BorderStrong, Lx.radius.CardShape),
                    contentAlignment = Alignment.Center,
                ) {
                    Icon(
                        imageVector = Icons.Filled.Cast,
                        contentDescription = null,
                        tint = Lx.colors.TermDim,
                        modifier = Modifier.size(20.dp),
                    )
                }
                Text("选择显示器开始查看", style = Lx.type.Pill, color = Lx.colors.TermDim)
            }
        }
        // 全屏态双击提示（顶部悬浮角标，整页视频之上）：文案对齐实际行为——
        // 观看态全屏下双击 = 缩小回设置页形态（投屏继续），不是「退出全屏」。
        // 缩小态不显示本角标（双击是进全屏，卡片上有「投屏中 · 双击放大」提示）。
        if (fullscreen) {
            Text(
                "双击缩小 · 双指缩放",
                style = Lx.type.Pill,
                color = Lx.colors.TermDim,
                modifier = Modifier
                    .align(Alignment.TopEnd)
                    .padding(Lx.space.s6),
            )
        }
    }

    // surface 就绪 → 建解码器并挂上 real（LxWsClient 的二进制帧直投它）；
    // surface 没了 → 停。旋转时 Surface 销毁重建：decoder 尚在 → 热重绑
    // setOutputSurface（API 23+，无关键帧空窗）；decoder 已被断线清理 → 走新建。
    LaunchedEffect(surface) {
        val s = surface
        if (s != null && s.isValid) {
            val existing = decoder
            if (existing != null) {
                if (existing.rebindSurface(s)) return@LaunchedEffect // 热重绑成功
                // 重绑失败（codec 已死）：清理后走全新建
                existing.stop()
                decoder = null
                real.screenDecoder = null
            }
            val d = ScreenDecoder(onError = { msg ->
                real.screenState = real.screenState?.copy(error = msg, active = false)
            })
            d.start(s)
            decoder = d
            real.screenDecoder = d
        } else {
            decoder?.stop()
            decoder = null
            real.screenDecoder = null
        }
    }

    DisposableEffect(Unit) {
        onDispose {
            decoder?.stop()
            real.screenDecoder = null
        }
    }
}

/**
 * 诊断面板弹层触发器（观看态）——把 [DiagPanel] 挪出悬浮球菜单后的家：
 * 内容作为 [trigger] 的 Popup 弹出（向下弹、钳视口），点外部关闭。
 * [trigger] 通常是右上角状态 pill；无解码器数据时 DiagPanel 不渲染、触发器原样显示。
 */
@Composable
private fun DiagPopup(real: com.moyunteng.lxcode.remote.net.RealBackend, trigger: @Composable () -> Unit) {
    var open by remember { mutableStateOf(false) }
    val density = LocalDensity.current
    val marginPx = with(density) { 8.dp.roundToPx() }
    Box {
        Box(
            Modifier.clickable(
                interactionSource = remember { MutableInteractionSource() },
                indication = null,
            ) { open = !open },
        ) { trigger() }
        if (open) {
            Popup(
                popupPositionProvider = remember(marginPx) {
                    object : PopupPositionProvider {
                        override fun calculatePosition(
                            anchorBounds: IntRect,
                            windowSize: IntSize,
                            layoutDirection: LayoutDirection,
                            popupContentSize: IntSize,
                        ): IntOffset {
                            // 向下弹、水平居中锚点，钳进视口 ±8dp
                            val margin = marginPx
                            val x = (anchorBounds.left + (anchorBounds.width - popupContentSize.width) / 2)
                                .coerceIn(margin, (windowSize.width - popupContentSize.width - margin).coerceAtLeast(margin))
                            val below = anchorBounds.bottom + margin
                            val y = if (below + popupContentSize.height <= windowSize.height - margin) below
                            else (anchorBounds.top - popupContentSize.height - margin).coerceAtLeast(margin)
                            return IntOffset(x, y)
                        }
                    }
                },
                onDismissRequest = { open = false },
                properties = PopupProperties(focusable = true),
            ) {
                Column(
                    Modifier
                        .width(320.dp)
                        .shadow(
                            elevation = 8.dp,
                            shape = BallMenuPanelShape,
                            clip = false,
                            ambientColor = Lx.shadow.Float.copy(alpha = 0.08f),
                            spotColor = Lx.shadow.Float.copy(alpha = 0.12f),
                        )
                        .clip(BallMenuPanelShape)
                        .background(Lx.colors.Surface)
                        .border(1.dp, Lx.colors.BorderStrong, BallMenuPanelShape)
                        .padding(8.dp),
                ) {
                    DiagPanel(real)
                }
            }
        }
    }
}

/** 诊断面板（黑屏排查的数据来源）：默认折叠一行，点开显示全部计数。
 *  帧到达（received>0）而黑屏 → 问题在解码/渲染；received 恒 0 → 问题在
 *  网络/后端转发（用户回报面板即可定位断点侧）。 */
@Composable
private fun DiagPanel(real: com.moyunteng.lxcode.remote.net.RealBackend) {
    var expanded by remember { mutableStateOf(false) }
    // 每 500ms 拉一次快照（计数在 WS/解码线程更新，Compose 状态需要刷新驱动）
    var tick by remember { mutableIntStateOf(0) }
    LaunchedEffect(Unit) {
        while (true) {
            delay(500)
            tick++
        }
    }
    val d = remember(tick) { real.screenDecoder?.stats() }
    if (d == null) return
    Row(
        Modifier
            .fillMaxWidth()
            .clip(Lx.radius.FieldShape)
            .background(Lx.colors.BgSide)
            .clickable { expanded = !expanded }
            .padding(horizontal = Lx.space.s6, vertical = Lx.space.s4),
        horizontalArrangement = Arrangement.spacedBy(Lx.space.s6),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        val stale = d.lastFrameAtMs > 0 &&
            android.os.SystemClock.elapsedRealtime() - d.lastFrameAtMs > 5000
        val statusText = when {
            d.framesReceived == 0 -> "未收到帧"
            !d.configured -> "等关键帧配置"
            d.framesFed == 0 -> "未喂数"
            stale -> "帧停滞"
            else -> "解码中"
        }
        val statusVariant = when {
            d.framesReceived == 0 -> LxPillVariant.Muted
            !d.configured || d.framesFed == 0 || stale -> LxPillVariant.Amber
            else -> LxPillVariant.Success
        }
        LxPill(statusText, variant = statusVariant)
        Text(
            "收 ${d.framesReceived}（关键 ${d.keyframesReceived}）· 解码 ${d.framesFed}" +
                (if (d.width > 0) " · ${d.width}×${d.height}" else "") +
                (if (d.profileIdc >= 0) " · SPS profile=${d.profileIdc} level=${(d.levelIdc / 10)}.${d.levelIdc % 10}" else "") +
                (d.lastError?.let { " · $it" } ?: ""),
            style = Lx.type.Pill,
            color = Lx.colors.FgMuted,
            modifier = Modifier.weight(1f),
            maxLines = if (expanded) 4 else 1,
        )
        Text(if (expanded) "收起" else "详情", style = Lx.type.Pill, color = Lx.colors.FgFaint)
    }
    if (expanded) {
        Text(
            "丢弃：等关键帧 ${d.framesDroppedGate} · 队列满 ${d.framesDroppedQueue} · 压入失败 ${d.framesDroppedFeed}\n" +
                "帧到达 = 0：检查桌面端是否在推流/隧道是否断；\n" +
                "帧到达 > 0 且解码 > 0 仍黑屏：点开报错信息给开发者。",
            style = Lx.type.BodySmall,
            color = Lx.colors.FgFaint,
            modifier = Modifier.padding(horizontal = Lx.space.s6, vertical = Lx.space.s4),
        )
    }
}

fun qualityLabel(q: String): String = QUALITY_LABELS.firstOrNull { it.first == q }?.second ?: q

@Composable
private fun Hint(text: String) {
    Text(
        text,
        style = Lx.type.BodySmall,
        color = Lx.colors.FgFaint,
        modifier = Modifier.fillMaxWidth(),
    )
}

/** 显示器选择行（LxListItem 行风格：标题 + 右侧次要信息；选中 = 强边框 + Fg）。 */
@Composable
private fun SelectableRow(
    title: String,
    subtitle: String,
    selected: Boolean,
    onClick: () -> Unit,
) {
    Row(
        Modifier
            .fillMaxWidth()
            .clip(Lx.radius.FieldShape)
            .background(if (selected) Lx.colors.Surface else Lx.colors.BgSide)
            .border(1.dp, if (selected) Lx.colors.BorderStrong else Lx.colors.Border, Lx.radius.FieldShape)
            .clickable(onClick = onClick)
            .padding(horizontal = Lx.space.s6, vertical = Lx.space.s6),
        horizontalArrangement = Arrangement.spacedBy(Lx.space.s6),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(
            imageVector = Icons.Filled.DesktopWindows,
            contentDescription = null,
            tint = if (selected) Lx.colors.Fg else Lx.colors.FgFaint,
            modifier = Modifier.size(16.dp),
        )
        Column(Modifier.weight(1f)) {
            Text(title, style = Lx.type.ListTitle, color = if (selected) Lx.colors.Fg else Lx.colors.FgMuted)
            Text(subtitle, style = Lx.type.Pill, color = Lx.colors.FgFaint)
        }
        if (selected) LxPill("已选", variant = LxPillVariant.Muted)
    }
}
