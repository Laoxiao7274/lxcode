// 设计系统展示页 —— :design 里每个组件的每个变体铺开成一节，用来肉眼验收设计语言。
//
// 这一页刻意只做「铺样例」：没有业务状态、没有网络、没有导航。
// 它同时是组件库的活文档（变体清单在页面上就是分节标题）与验收证据（云机截图）。
package com.moyunteng.lxcode.remote

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material.icons.filled.Star
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
import androidx.compose.ui.unit.dp
import com.moyunteng.lxcode.design.component.LxBadge
import com.moyunteng.lxcode.design.component.LxButton
import com.moyunteng.lxcode.design.component.LxButtonSize
import com.moyunteng.lxcode.design.component.LxButtonVariant
import com.moyunteng.lxcode.design.component.LxCard
import com.moyunteng.lxcode.design.component.LxConfirmDialog
import com.moyunteng.lxcode.design.component.LxDivider
import com.moyunteng.lxcode.design.component.LxEmptyState
import com.moyunteng.lxcode.design.component.LxIconButton
import com.moyunteng.lxcode.design.component.LxIconButtonVariant
import com.moyunteng.lxcode.design.component.LxListItem
import com.moyunteng.lxcode.design.component.LxPill
import com.moyunteng.lxcode.design.component.LxPillVariant
import com.moyunteng.lxcode.design.component.LxSectionHeader
import com.moyunteng.lxcode.design.component.LxSpinner
import com.moyunteng.lxcode.design.component.LxStatus
import com.moyunteng.lxcode.design.component.LxStatusDot
import com.moyunteng.lxcode.design.component.LxSurface
import com.moyunteng.lxcode.design.component.LxTextField
import com.moyunteng.lxcode.design.token.Lx
import com.moyunteng.lxcode.design.token.LxColors
import com.moyunteng.lxcode.design.token.LxRadius
import com.moyunteng.lxcode.design.token.LxType

/** 色板样例：token 名 + 桌面端 CSS 变量名 + 色值。 */
private data class Swatch(val name: String, val css: String, val color: Color)

/**
 * 设计系统展示页。
 *
 * 分节铺开 :design 的每个组件与变体，用来在真机/云机上肉眼验收设计语言。
 */
@Composable
fun ShowcaseScreen(modifier: Modifier = Modifier) {
    var fieldValue by remember { mutableStateOf("") }
    var dialogOpen by remember { mutableStateOf(false) }

    Column(
        modifier = modifier
            .fillMaxSize()
            .background(Lx.colors.Bg)
            .verticalScroll(rememberScrollState())
            .padding(horizontal = Lx.space.s16),
    ) {
        PageHeader()
        ColorSection()
        SpacingSection()
        RadiusSection()
        TypeSection()

        Section("LxButton · 主按钮 Primary") {
            Row(horizontalArrangement = Arrangement.spacedBy(Lx.space.s8)) {
                LxButton(text = "运行", onClick = {})
                LxButton(text = "加载中", onClick = {}, loading = true)
                LxButton(text = "禁用", onClick = {}, enabled = false)
            }
            Row(horizontalArrangement = Arrangement.spacedBy(Lx.space.s8)) {
                LxButton(text = "小", onClick = {}, size = LxButtonSize.Small)
                LxButton(text = "中", onClick = {}, size = LxButtonSize.Medium)
                LxButton(text = "大", onClick = {}, size = LxButtonSize.Large)
            }
        }

        Section("LxButton · 次按钮 Secondary") {
            Row(horizontalArrangement = Arrangement.spacedBy(Lx.space.s8)) {
                LxButton(text = "取消", onClick = {}, variant = LxButtonVariant.Secondary)
                LxButton(
                    text = "禁用",
                    onClick = {},
                    variant = LxButtonVariant.Secondary,
                    enabled = false,
                )
                LxButton(
                    text = "带图标",
                    onClick = {},
                    variant = LxButtonVariant.Secondary,
                    leadingIcon = { Icon(Icons.Default.Search, null, Modifier.size(14.dp)) },
                )
            }
        }

        Section("LxButton · 幽灵按钮 Ghost") {
            Row(horizontalArrangement = Arrangement.spacedBy(Lx.space.s8)) {
                LxButton(text = "跳过", onClick = {}, variant = LxButtonVariant.Ghost)
                LxButton(
                    text = "更多",
                    onClick = {},
                    variant = LxButtonVariant.Ghost,
                    size = LxButtonSize.Small,
                )
            }
        }

        Section("LxButton · 危险按钮 Danger") {
            Row(horizontalArrangement = Arrangement.spacedBy(Lx.space.s8)) {
                LxButton(text = "删除", onClick = {}, variant = LxButtonVariant.Danger)
                LxButton(
                    text = "确认删除",
                    onClick = {},
                    variant = LxButtonVariant.Danger,
                    size = LxButtonSize.Small,
                )
            }
        }

        Section("LxIconButton · 小图标按钮（圆角 7）") {
            Row(horizontalArrangement = Arrangement.spacedBy(Lx.space.s4)) {
                LxIconButton(onClick = {}) { Icon(Icons.Default.Search, null, Modifier.size(16.dp)) }
                LxIconButton(onClick = {}) { Icon(Icons.Default.Refresh, null, Modifier.size(16.dp)) }
                LxIconButton(onClick = {}) { Icon(Icons.Default.MoreVert, null, Modifier.size(16.dp)) }
                LxIconButton(onClick = {}) { Icon(Icons.Default.Star, null, Modifier.size(16.dp)) }
                LxIconButton(onClick = {}, variant = LxIconButtonVariant.Danger) {
                    Icon(Icons.Default.Delete, null, Modifier.size(16.dp))
                }
                LxIconButton(onClick = {}, enabled = false) {
                    Icon(Icons.Default.Settings, null, Modifier.size(16.dp))
                }
            }
        }

        Section("LxTextField · 输入框（圆角 9 / 聚焦变 #a8a8b0）") {
            LxTextField(
                value = fieldValue,
                onValueChange = { fieldValue = it },
                placeholder = "https://127.0.0.1:7789",
                trailing = {
                    if (fieldValue.isNotEmpty()) {
                        LxIconButton(onClick = { fieldValue = "" }) {
                            Icon(Icons.Default.Close, null, Modifier.size(14.dp))
                        }
                    }
                },
            )
            LxTextField(
                value = "",
                onValueChange = {},
                placeholder = "禁用态",
                enabled = false,
            )
        }

        Section("LxCard · 卡片（白底 / --border 发丝边 / 圆角 12）") {
            LxCard {
                Row(
                    modifier = Modifier.fillMaxWidth(),
                    horizontalArrangement = Arrangement.SpaceBetween,
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Column {
                        Text("本地后端", style = Lx.type.ListTitle)
                        Text("127.0.0.1:7789 · 已连接", style = Lx.type.ListSubtitle)
                    }
                    LxPill("成功", LxPillVariant.Success)
                }
            }
            LxSurface {
                Text("LxSurface · 无阴影的基础面（发丝边 + 圆角 12）", style = Lx.type.BodySmall)
            }
        }

        Section("LxPill / LxBadge · 药丸标签") {
            Row(horizontalArrangement = Arrangement.spacedBy(Lx.space.s6)) {
                LxPill("中性")
                LxPill("成功", LxPillVariant.Success)
                LxPill("危险", LxPillVariant.Danger)
                LxPill("运行中", LxPillVariant.Amber)
            }
            Row(
                horizontalArrangement = Arrangement.spacedBy(Lx.space.s6),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text("LxBadge：", style = Lx.type.BodySmall)
                LxBadge("3")
                LxBadge("12", LxPillVariant.Success)
                LxBadge("!", LxPillVariant.Danger)
                LxBadge("7", LxPillVariant.Amber)
            }
        }

        Section("LxListItem · 列表行（主标题 + 副标题 + 尾部插槽）") {
            LxListItem(
                title = "重构 compaction 选区间",
                subtitle = "12 分钟前 · claude-sonnet",
                leading = { LxStatusDot(LxStatus.Success) },
                trailing = { LxPill("进行中", LxPillVariant.Amber) },
                onClick = {},
            )
            LxListItem(
                title = "已选中的会话行",
                subtitle = "点击态 / 选中态都是 --border-soft 填充",
                leading = { LxStatusDot(LxStatus.Idle) },
                selected = true,
                onClick = {},
            )
            LxListItem(
                title = "只有主标题的单行式",
                trailing = {
                    LxIconButton(onClick = {}) { Icon(Icons.Default.MoreVert, null, Modifier.size(16.dp)) }
                },
                onClick = {},
            )
            LxListItem(title = "不可点的纯展示行")
            LxDivider()
            LxListItem(
                title = "LxRow · 前插槽 + 内容 + 后插槽",
                subtitle = "LxRow 是布局骨架，LxListItem 才是成品行",
                leading = { Icon(Icons.Default.Settings, null, Modifier.size(16.dp)) },
                trailing = { LxBadge("5") },
            )
        }

        Section("LxDivider · 发丝分隔线（--border）") {
            LxDivider()
            Row(
                modifier = Modifier.height(24.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(Lx.space.s8),
            ) {
                Text("工具条", style = Lx.type.BodySmall)
                com.moyunteng.lxcode.design.component.LxVerticalDivider()
                Text("18dp 竖线", style = Lx.type.BodySmall)
            }
        }

        Section("LxSectionHeader · 分组小标题（fg-muted / 11sp 大写）") {
            LxSectionHeader("这是 LxSectionHeader 自身的样子", topPadding = 0.dp)
            Text("上一行的分组标题就是本组件渲染的，不是另写的一段文字。", style = Lx.type.BodySmall)
        }

        Section("LxStatusDot · 状态圆点（run 用琥珀）") {
            Row(
                horizontalArrangement = Arrangement.spacedBy(Lx.space.s16),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                StatusSample("idle", LxStatus.Idle)
                StatusSample("run", LxStatus.Run)
                StatusSample("success", LxStatus.Success)
                StatusSample("danger", LxStatus.Danger)
            }
        }

        Section("LxEmptyState · 空态") {
            LxEmptyState(
                text = "还没有会话",
                description = "在下方输入一句话开始，或者从连接页扫一个后端。",
                icon = { Icon(Icons.Default.Add, null, Modifier.size(20.dp)) },
            )
        }

        Section("LxSpinner · 加载态（细线旋转 / 120ms 一步）") {
            Row(
                horizontalArrangement = Arrangement.spacedBy(Lx.space.s16),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                LxSpinner()
                LxSpinner(size = 20.dp)
                LxButton(text = "按钮内加载", onClick = {}, loading = true, size = LxButtonSize.Small)
            }
        }

        Section("LxConfirmDialog · 确认门（批准 = 黑主按钮 / 拒绝 = ghost）") {
            Row(horizontalArrangement = Arrangement.spacedBy(Lx.space.s8)) {
                LxButton(text = "打开确认门", onClick = { dialogOpen = true })
            }
            Text(
                "对齐桌面端 ApprovalCard：标题问句 + 正文 + 「拒绝 / 批准」，点遮罩可关。",
                style = Lx.type.BodySmall,
            )
        }

        LxDivider(modifier = Modifier.padding(vertical = Lx.space.s20))
        Text(
            "lxcode design system · :design 模块（token + 组件）· 本页由 ShowcaseScreen 渲染",
            style = Lx.type.BodySmall,
            modifier = Modifier.padding(bottom = Lx.space.s24),
        )
    }

    if (dialogOpen) {
        LxConfirmDialog(
            title = "执行此命令？",
            text = "高危 bash 需要你显式批准才会执行。",
            body = {
                Column(
                    modifier = Modifier
                        .fillMaxWidth()
                        .clip(LxRadius.CardShape)
                        .background(LxColors.TermBg)
                        .padding(horizontal = 14.dp, vertical = Lx.space.s10),
                ) {
                    Text("~/lxcode/android", style = LxType.Mono12.copy(color = LxColors.TermDim))
                    Text("$ gradle assembleDebug", style = LxType.Mono12)
                }
            },
            onApprove = { dialogOpen = false },
            onDeny = { dialogOpen = false },
            onDismissRequest = { dialogOpen = false },
        )
    }
}

/** 页面标题区。 */
@Composable
private fun ColumnScope.PageHeader() {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(top = Lx.space.s20),
        horizontalArrangement = Arrangement.SpaceBetween,
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column {
            Text("lxcode design system", style = LxType.Body.copy(fontSize = LxType.Size14))
            Text(":design 模块 · token + 核心组件", style = LxType.BodySmall)
        }
        LxBadge("v0.1")
    }
}

@Composable
private fun Section(title: String, content: @Composable ColumnScope.() -> Unit) {
    LxSectionHeader(title)
    Column(
        modifier = Modifier.fillMaxWidth(),
        verticalArrangement = Arrangement.spacedBy(Lx.space.s8),
        content = content,
    )
}

@Composable
private fun StatusSample(label: String, status: LxStatus) {
    Row(
        horizontalArrangement = Arrangement.spacedBy(Lx.space.s6),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        LxStatusDot(status)
        Text(label, style = LxType.BodySmall)
    }
}

@Composable
private fun ColorSection() {
    LxSectionHeader("Token · 颜色（:root 十四色 + 高频硬编码色）")
    Column(verticalArrangement = Arrangement.spacedBy(Lx.space.s6)) {
        SWATCHES.chunked(2).forEach { pair ->
            Row(horizontalArrangement = Arrangement.spacedBy(Lx.space.s6)) {
                pair.forEach { swatch ->
                    SwatchCell(swatch, modifier = Modifier.weight(1f))
                }
                if (pair.size == 1) Box(Modifier.weight(1f))
            }
        }
    }
}

@Composable
private fun SwatchCell(swatch: Swatch, modifier: Modifier = Modifier) {
    Row(
        modifier = modifier,
        horizontalArrangement = Arrangement.spacedBy(Lx.space.s8),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(
            modifier = Modifier
                .size(22.dp)
                .clip(LxRadius.IconButtonShape)
                .background(swatch.color)
                .border(1.dp, LxColors.Border, LxRadius.IconButtonShape),
        )
        Column {
            Text(swatch.name, style = LxType.BodySmall.copy(color = LxColors.Fg))
            Text(swatch.css, style = LxType.Badge.copy(color = LxColors.FgFaint))
        }
    }
}

@Composable
private fun SpacingSection() {
    LxSectionHeader("Token · 间距刻度（2/4/6/8/10/12/16/20/24）")
    Row(
        modifier = Modifier.height(40.dp),
        horizontalArrangement = Arrangement.spacedBy(Lx.space.s8),
        verticalAlignment = Alignment.Bottom,
    ) {
        SpacingBar("2", Lx.space.s2)
        SpacingBar("4", Lx.space.s4)
        SpacingBar("6", Lx.space.s6)
        SpacingBar("8", Lx.space.s8)
        SpacingBar("10", Lx.space.s10)
        SpacingBar("12", Lx.space.s12)
        SpacingBar("16", Lx.space.s16)
        SpacingBar("20", Lx.space.s20)
        SpacingBar("24", Lx.space.s24)
    }
}

@Composable
private fun SpacingBar(label: String, value: androidx.compose.ui.unit.Dp) {
    Column(horizontalAlignment = Alignment.CenterHorizontally) {
        Box(
            modifier = Modifier
                .width(10.dp)
                .height(value)
                .background(LxColors.Fg, LxRadius.r4.let { androidx.compose.foundation.shape.RoundedCornerShape(it) }),
        )
        Text(label, style = LxType.Badge.copy(color = LxColors.FgFaint))
    }
}

@Composable
private fun RadiusSection() {
    LxSectionHeader("Token · 圆角刻度（4/5/6/7/8/9/10/12/14 + 药丸 999）")
    Row(
        horizontalArrangement = Arrangement.spacedBy(Lx.space.s6),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        RadiusCell("4", LxRadius.r4)
        RadiusCell("7", LxRadius.r7)
        RadiusCell("9", LxRadius.r9)
        RadiusCell("12", LxRadius.r12)
        RadiusCell("14", LxRadius.r14)
        RadiusCell("999", LxRadius.pill)
    }
}

@Composable
private fun RadiusCell(label: String, radius: androidx.compose.ui.unit.Dp) {
    Box(
        modifier = Modifier
            .size(34.dp)
            .clip(androidx.compose.foundation.shape.RoundedCornerShape(radius))
            .background(LxColors.BorderSoft)
            .border(1.dp, LxColors.BorderStrong, androidx.compose.foundation.shape.RoundedCornerShape(radius)),
        contentAlignment = Alignment.Center,
    ) {
        Text(label, style = LxType.Badge.copy(color = LxColors.FgMuted))
    }
}

@Composable
private fun TypeSection() {
    LxSectionHeader("Token · 字号刻度（9.5 → 14，正文基准 14）")
    Column(verticalArrangement = Arrangement.spacedBy(Lx.space.s4)) {
        Text("9.5sp 最小号 徽标/密集元信息", style = LxType.Pill.copy(color = LxColors.FgMuted))
        Text("10.5sp 辅助说明", style = LxType.Badge.copy(color = LxColors.FgMuted))
        Text("11.5sp 列表副标题（最常用）", style = LxType.ListSubtitle)
        Text("12.5sp 按钮默认字号", style = LxType.BodySmall.copy(color = LxColors.Fg))
        Text("13sp 列表行主标题 / 输入框", style = LxType.ListTitle)
        Text("14sp 正文基准 / 400 字重", style = LxType.Body)
        Text("14sp 正文 / 600 字重（强调）", style = LxType.Body.copy(fontWeight = LxType.W600))
        Text("12sp 等宽（终端块）", style = LxType.Mono12)
    }
}

private val SWATCHES = listOf(
    Swatch("bg", "--bg", LxColors.Bg),
    Swatch("bg-side", "--bg-side", LxColors.BgSide),
    Swatch("surface", "--surface", LxColors.Surface),
    Swatch("fg", "--fg", LxColors.Fg),
    Swatch("fg-muted", "--fg-muted", LxColors.FgMuted),
    Swatch("fg-faint", "--fg-faint", LxColors.FgFaint),
    Swatch("border", "--border", LxColors.Border),
    Swatch("border-strong", "--border-strong", LxColors.BorderStrong),
    Swatch("border-soft", "--border-soft", LxColors.BorderSoft),
    Swatch("success", "--success", LxColors.Success),
    Swatch("danger", "--danger", LxColors.Danger),
    Swatch("term-bg", "--term-bg", LxColors.TermBg),
    Swatch("term-fg", "--term-fg", LxColors.TermFg),
    Swatch("term-dim", "--term-dim", LxColors.TermDim),
    Swatch("bubble-user", "#f1f1f3", LxColors.BubbleUser),
    Swatch("code-bg", "#f4f5f7", LxColors.CodeBg),
    Swatch("focus-ring", "#a8a8b0", LxColors.FocusRing),
    Swatch("ghost-hover", "#c9c9cf", LxColors.BorderGhostHover),
    Swatch("chip-hover", "#c2c2c8", LxColors.BorderChipHover),
    Swatch("card-hover", "#d4d4d8", LxColors.BorderCardHover),
    Swatch("amber", "#d97706", LxColors.Amber),
)
