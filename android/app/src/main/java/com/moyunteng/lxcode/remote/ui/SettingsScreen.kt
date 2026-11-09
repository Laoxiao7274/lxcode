// 设置页（SettingsScreen，P2 新建）—— 对齐原型页 13 `data-page="settings"`：
//   外观：色温三态分段（亮/暗/暖，接 AppState.setTheme）+ 动效开关（接 LxMotionGate 的
//         门控；安卓端动效门控读系统动画缩放，本开关是 app 级覆盖——落 ConnStore）
//   通知：运行完成通知 / 子会话进度（开关位照原型落位；推送通道未接，开关是 UI 态——
//         缺口如实在行描述里标注，不假装生效）
//   模型：默认模型行（读 real.allModels 的 default 投影）+「连接与投屏」行（跳连接页）
//   关于：版本行（BuildConfig.VERSION_NAME + 协议 v2 + WS 7789）+「检查更新」入口
//         （复用 SessionsScreen 的更新弹窗编排：pendingUpdatePrompt → UpdateFoundDialog）
// 变体（A/B/C）选择：原型在工具栏切变体、设置页只切色温——安卓端把变体也放进本页
// （两维正交，9 组合；工具栏没有变体位）。
package com.moyunteng.lxcode.remote.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Bolt
import androidx.compose.material.icons.filled.Dns
import androidx.compose.material.icons.filled.KeyboardArrowRight
import androidx.compose.material.icons.filled.NotificationsNone
import androidx.compose.material.icons.filled.Mail
import androidx.compose.material.icons.filled.Memory
import androidx.compose.material.icons.filled.HelpOutline
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import androidx.compose.ui.unit.sp
import com.moyunteng.lxcode.design.component.LxIconButton
import com.moyunteng.lxcode.design.token.Lx
import com.moyunteng.lxcode.design.token.LxIcons
import com.moyunteng.lxcode.design.token.LxTemp
import com.moyunteng.lxcode.design.token.LxVariant
import com.moyunteng.lxcode.remote.BuildConfig
import com.moyunteng.lxcode.remote.net.RealPhase

/** 设置页（Route.Settings）。 */
@Composable
fun SettingsScreen(state: AppState) {
    val context = LocalContext.current
    val real = state.real

    // 动效开关：app 级覆盖门控（AppState.motionOverride；LxMotionGate.override 传它）。
    // null = 跟随系统（默认），true/false = 显式覆盖；写口 AppState.setMotionOverride 落 ConnStore。
    val motionOn = state.motionOverride ?: true

    Column(
        Modifier
            .fillMaxSize()
            .background(Lx.colors.Bg)
            .verticalScroll(rememberScrollState()),
    ) {
        // 顶栏：返回 + 标题
        Row(
            Modifier
                .fillMaxWidth()
                .background(Lx.colors.Bg)
                .padding(horizontal = 6.dp, vertical = 6.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(6.dp),
        ) {
            LxIconButton(onClick = { state.route = Route.Sessions }) {
                Icon(
                    imageVector = LxIcons.Back,
                    contentDescription = "返回",
                    tint = Lx.colors.FgMuted,
                    modifier = Modifier.size(20.dp),
                )
            }
            Text(
                text = "设置",
                style = TextStyle(fontFamily = Lx.type.Sans, fontSize = 17.sp, fontWeight = FontWeight.Bold),
                color = Lx.colors.Fg,
                modifier = Modifier.weight(1f),
            )
        }

        // ===== 外观 =====
        SettingSection("外观")
        // 色温三态分段（`.seg`）：亮 / 暗 / 暖 —— 接 AppState.setTheme（落 ConnStore）
        SegmentedControl(
            options = listOf(LxTemp.Light, LxTemp.Dark, LxTemp.Warm).map { it.name },
            selected = state.themeTemp.name,
            onSelect = { name ->
                LxTemp.entries.firstOrNull { it.name == name }?.let {
                    state.setTheme(state.themeVariant, it)
                }
            },
            labelOf = { when (it) {
                LxTemp.Light.name -> "亮色"
                LxTemp.Warm.name -> "暖色"
                else -> "暗色"
            } },
            iconOf = { when (it) {
                LxTemp.Light.name -> LxIcons.Sun
                LxTemp.Warm.name -> LxIcons.SunWarm
                else -> LxIcons.Moon
            } },
            modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp),
        )
        // 变体分段（A 纯净 / B 品牌紫 / C 陶土）——两维的另一维（工具栏无此位）
        SegmentedControl(
            options = listOf(LxVariant.A, LxVariant.B, LxVariant.C).map { it.name },
            selected = state.themeVariant.name,
            onSelect = { name ->
                LxVariant.entries.firstOrNull { it.name == name }?.let {
                    state.setTheme(it, state.themeTemp)
                }
            },
            labelOf = { when (it) {
                LxVariant.B.name -> "品牌紫"
                LxVariant.C.name -> "陶土"
                else -> "纯净"
            } },
            modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp),
        )
        SettingRows {
            SettingRow(
                icon = Icons.Filled.Bolt,
                title = "动效",
                sub = if (state.motionOverride == null) "跟随系统（页面切换 / 呼吸点 / 打字机）" else "已覆盖系统设置（页面切换 / 呼吸点 / 打字机）",
                trailing = { SettingSwitch(checked = motionOn, onChecked = {
                    state.writeMotionOverride(it)
                }) },
            )
        }

        // ===== 通知 =====
        SettingSection("通知")
        var notifRun by remember { mutableStateOf(true) }
        var notifChild by remember { mutableStateOf(false) }
        SettingRows {
            SettingRow(
                icon = Icons.Filled.NotificationsNone,
                title = "运行完成通知",
                sub = "一轮跑完 / 高危工具等待确认时推送（推送通道未接，开关暂存本机）",
                trailing = { SettingSwitch(checked = notifRun, onChecked = { notifRun = it }) },
            )
            SettingRow(
                icon = Icons.Filled.Mail,
                title = "子会话进度",
                sub = "派发卡有新结论时提醒（推送通道未接，开关暂存本机）",
                trailing = { SettingSwitch(checked = notifChild, onChecked = { notifChild = it }) },
            )
        }

        // ===== 模型 =====
        SettingSection("模型")
        SettingRows {
            val defaultModel = real.defaultModelId.ifBlank { "未设置" }
            SettingRow(
                icon = Icons.Filled.Memory,
                title = "默认模型",
                sub = defaultModel,
                trailing = {
                    Icon(
                        imageVector = Icons.Filled.KeyboardArrowRight,
                        contentDescription = null,
                        tint = Lx.colors.FgFaint,
                        modifier = Modifier.size(14.dp),
                    )
                },
                onClick = { /* 模型默认值管理在桌面端 settings；手机端只读展示 */ },
            )
            SettingRow(
                icon = Icons.Filled.Dns,
                title = "连接与投屏",
                sub = if (real.phase == RealPhase.Connected) "已连接" else "未连接",
                trailing = {
                    Icon(
                        imageVector = Icons.Filled.KeyboardArrowRight,
                        contentDescription = null,
                        tint = Lx.colors.FgFaint,
                        modifier = Modifier.size(14.dp),
                    )
                },
                onClick = { state.route = Route.Connections },
            )
        }

        // ===== 关于 =====
        SettingSection("关于")
        SettingRows {
            SettingRow(
                icon = Icons.Filled.HelpOutline,
                title = "版本",
                sub = "lxcode ${BuildConfig.VERSION_NAME} · 协议 v2 · WS 7789",
                trailing = {
                    Text(
                        text = "检查更新",
                        style = TextStyle(
                            fontFamily = Lx.type.Sans,
                            fontSize = 10.5.sp,
                            fontWeight = FontWeight.SemiBold,
                        ),
                        color = Lx.colors.FgMuted,
                        modifier = Modifier
                            .clip(RoundedCornerShape(999.dp))
                            .border(1.dp, Lx.colors.BorderStrong, RoundedCornerShape(999.dp))
                            .clickable(
                                interactionSource = remember { MutableInteractionSource() },
                                indication = null,
                            ) {
                                // 与连接页「检查更新」同一条编排：手动检查 → 有新版弹确认
                                // （UpdateFoundDialog 消费 pendingUpdatePrompt）
                                state.pendingUpdatePrompt =
                                    com.moyunteng.lxcode.remote.update.UpdateChecker.check(
                                        BuildConfig.VERSION_CODE.toLong(),
                                    ).let { r ->
                                        (r as? com.moyunteng.lxcode.remote.update.UpdateCheckResult.Available)?.release
                                    }
                            }
                            .padding(horizontal = 9.dp, vertical = 3.dp),
                    )
                },
            )
        }
        Box(Modifier.height(18.dp))
    }
}

/** 分组标题（原型 `.sect`：10.5sp/700/字距 .1em/--fg3、padding 18/20/8）。 */
@Composable
private fun SettingSection(label: String) {
    Text(
        text = label,
        style = TextStyle(
            fontFamily = Lx.type.Sans,
            fontSize = 10.5.sp,
            fontWeight = FontWeight.Bold,
            letterSpacing = 0.1.em,
        ),
        color = Lx.colors.FgFaint,
        modifier = Modifier.padding(start = 20.dp, end = 20.dp, top = 18.dp, bottom = 8.dp),
    )
}

/** 行组卡（`.rows-card`：Surface 底 + 1px 边 + 圆角 12 + 行间 1px 分隔线）。 */
@Composable
private fun SettingRows(content: @Composable () -> Unit) {
    Column(
        Modifier
            .padding(horizontal = 10.dp)
            .fillMaxWidth()
            .clip(Lx.radius.CardShape)
            .background(Lx.colors.Surface)
            .border(1.dp, Lx.colors.Border, Lx.radius.CardShape),
    ) {
        content()
    }
}

/** 设置行（`.row .ic/.tx/.t1/.t2` + trailing 插槽）。 */
@Composable
private fun SettingRow(
    icon: ImageVector,
    title: String,
    sub: String,
    modifier: Modifier = Modifier,
    iconAccent: Boolean = false,
    trailing: @Composable () -> Unit = {},
    onClick: (() -> Unit)? = null,
) {
    val interaction = remember { MutableInteractionSource() }
    Row(
        modifier
            .fillMaxWidth()
            .then(if (onClick != null) Modifier.clickable(interactionSource = interaction, indication = null, onClick = onClick) else Modifier)
            .padding(horizontal = 12.dp, vertical = 11.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        // 36dp 图标框（`.row .ic`：--soft 底 + 1px 边 + 圆角 10；accent 款 accent-soft）
        Box(
            Modifier
                .size(36.dp)
                .clip(RoundedCornerShape(10.dp))
                .background(if (iconAccent) Lx.colors.AccentSoft else Lx.colors.BorderSoft)
                .border(
                    1.dp,
                    if (iconAccent) Color.Transparent else Lx.colors.Border,
                    RoundedCornerShape(10.dp),
                ),
            contentAlignment = Alignment.Center,
        ) {
            Icon(
                imageVector = icon,
                contentDescription = null,
                tint = if (iconAccent) Lx.colors.Accent else Lx.colors.FgMuted,
                modifier = Modifier.size(16.dp),
            )
        }
        Column(Modifier.weight(1f)) {
            Text(
                text = title,
                style = TextStyle(fontFamily = Lx.type.Sans, fontSize = 13.sp, fontWeight = FontWeight.SemiBold),
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
                modifier = Modifier.padding(top = 2.dp),
            )
        }
        trailing()
    }
}

/**
 * 开关（原型 `.switch`：40×24 圆角全、off = --line2 底、on = --ok 底；
 * 18dp 白圆钮 translateX 16、180ms 过渡）。
 */
@Composable
private fun SettingSwitch(checked: Boolean, onChecked: (Boolean) -> Unit) {
    val trackColor by com.moyunteng.lxcode.design.motion.lxAnimateColor(
        target = if (checked) Lx.colors.Success else Lx.colors.BorderStrong,
        durationMillis = 180,
    )
    Box(
        Modifier
            .size(width = 40.dp, height = 24.dp)
            .clip(RoundedCornerShape(999.dp))
            .background(trackColor)
            .clickable(
                interactionSource = remember { MutableInteractionSource() },
                indication = null,
            ) { onChecked(!checked) },
        contentAlignment = Alignment.CenterStart,
    ) {
        androidx.compose.animation.core.animateDpAsState(
            targetValue = if (checked) 19.dp else 3.dp,
            animationSpec = androidx.compose.animation.core.tween(180, easing = com.moyunteng.lxcode.design.token.LxMotion.Easing),
            label = "switch-knob",
        ).value.let { x ->
            Box(
                Modifier
                    .padding(start = x)
                    .size(18.dp)
                    .clip(RoundedCornerShape(999.dp))
                    .background(Color.White),
            )
        }
    }
}
