// 内联确认卡（ConfirmCard）/ 提问卡（AskCard）—— P2：确认门从「弹出对话框」改为
// 助手域消息流内的内联卡片（对齐原型 `.confirmcard` / `.askcard`）。
//
//  * ConfirmCard（`.confirmcard`）：warn 染色边 + warn 7% 混底、标题行（⚠ 需要你确认）
//    + 正文 + 命令块 + 两按钮（先不 / 允许，primary = accent 底）；
//  * AskCard（`.askcard`）：accent-soft 底、标题行（? 提问）+ 问题正文 + 选项按钮列表
//    （`.askopt`：白底 1px 边、选中 accent 边 + 右侧勾）+ 底部「自己打字回答」入口——
//    回答随下一条消息发出（answer 路径走 tool.confirm 的 answer，不占输入队列）。
//
// 入场：cardin（y+8dp 淡入 260ms——LxEnterSpec.ErrorBarIn 同款档）。
package com.moyunteng.lxcode.remote.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.WarningAmber
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
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.moyunteng.lxcode.design.component.LxButton
import com.moyunteng.lxcode.design.component.LxButtonSize
import com.moyunteng.lxcode.design.component.LxButtonVariant
import com.moyunteng.lxcode.design.motion.LxEnterSpec
import com.moyunteng.lxcode.design.motion.lxEnter
import com.moyunteng.lxcode.design.token.Lx
import com.moyunteng.lxcode.design.token.LxIcons
import com.moyunteng.lxcode.remote.net.PendingConfirm


// 原型 `.confirmcard/.askcard` 的节奏（2026-12 修「挤」的对齐值）：
// 卡 radius 12、内距 13、选项间 7、按钮行上 10（此前 10/11/5/5 全面紧于原型）。
private val CardShape = RoundedCornerShape(12.dp)
private val OptShape = RoundedCornerShape(7.dp)

/**
 * 内联工具确认卡（`.confirmcard`）。
 *
 * @param confirm 挂起确认（name/arguments/prompt）
 * @param terminal 命令块插槽（TerminalBlock；助手域/主域共用）
 * @param onAllow 允许
 * @param onDeny 拒绝
 */
@Composable
fun ConfirmCard(
    confirm: PendingConfirm,
    onAllow: () -> Unit,
    onDeny: () -> Unit,
    modifier: Modifier = Modifier,
    terminal: (@Composable () -> Unit)? = null,
) {
    Column(
        modifier
            .fillMaxWidth()
            .lxEnter(spec = LxEnterSpec.ErrorBarIn)
            .clip(CardShape)
            .background(Lx.colors.Amber.copy(alpha = 0.07f).compositeOver(Lx.colors.Surface))
            .border(1.dp, Lx.colors.Amber.copy(alpha = 0.40f), CardShape)
            .padding(13.dp),
        verticalArrangement = Arrangement.spacedBy(5.dp),
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(7.dp),
        ) {
            Icon(
                imageVector = Icons.Filled.WarningAmber,
                contentDescription = null,
                tint = Lx.colors.Amber,
                modifier = Modifier.size(13.dp),
            )
            Text(
                text = "需要你确认",
                // 原型 `.confirmcard .ct`：继承卡的 font-size var(--t-sm)=12.5px / 700
                style = TextStyle(fontFamily = Lx.type.Sans, fontSize = 12.5.sp, fontWeight = FontWeight.Bold),
                color = Lx.colors.Amber,
            )
            Text(
                text = confirm.name,
                style = TextStyle(fontFamily = Lx.type.Mono, fontSize = 10.5.sp),
                color = Lx.colors.FgFaint,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        Text(
            text = confirm.prompt.ifBlank { "${confirm.name} 工具申请执行，请确认。" },
            style = TextStyle(fontFamily = Lx.type.Sans, fontSize = 11.5.sp, lineHeight = 11.5.sp * 1.5f),
            color = Lx.colors.FgMuted,
        )
        terminal?.invoke()
        Row(
            Modifier.padding(top = 10.dp),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            Box(Modifier.weight(1f)) {
                LxButton(
                    text = "先不",
                    onClick = onDeny,
                    variant = LxButtonVariant.Secondary,
                    size = LxButtonSize.Medium,
                    // 原型 `.cbtn`：radius var(--r-sm)=8 / padding 8px 0 / 12.5px 700
                    //（2026-12 扫尾条目 29；覆写 Medium 档的 9/7·14）
                    shapeOverride = RoundedCornerShape(8.dp),
                    paddingOverride = PaddingValues(vertical = 8.dp),
                    modifier = Modifier.fillMaxWidth(),
                )
            }
            Box(Modifier.weight(1f)) {
                LxButton(
                    text = "允许执行",
                    onClick = onAllow,
                    variant = LxButtonVariant.Primary,
                    size = LxButtonSize.Medium,
                    shapeOverride = RoundedCornerShape(8.dp),
                    paddingOverride = PaddingValues(vertical = 8.dp),
                    modifier = Modifier.fillMaxWidth(),
                )
            }
        }
    }
}

/**
 * 内联提问卡（`.askcard`，ask_user 形态）：问题 + 预设选项按钮 + 「自己打字回答」。
 *
 * 选项**点选即回答**（tool.confirm 带 answer）——预设选项列表下方不再常驻
 * 「跳过/回答选中项」按钮行（2026-12 用户拍板缩小常驻高度）；「打字回答」展开
 * 一个小输入框（提交同样走 answer，空回答提交 = 跳过 → allow=false）。
 *
 * @param confirm ask 形态的挂起确认（prompt = 问题、options = 预设答案）
 * @param onAnswer 文本回答提交（非空 = 该文本；空 = 跳过）
 */
@Composable
fun AskCard(
    confirm: PendingConfirm,
    onAnswer: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
    var typing by remember(confirm.id) { mutableStateOf(false) }
    var draft by remember(confirm.id) { mutableStateOf("") }

    Column(
        modifier
            .fillMaxWidth()
            .lxEnter(spec = LxEnterSpec.ErrorBarIn)
            .clip(CardShape)
            .background(Lx.colors.AccentSoft)
            .border(1.dp, Lx.colors.Border, CardShape)
            .padding(13.dp),
        verticalArrangement = Arrangement.spacedBy(5.dp),
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(7.dp),
        ) {
            Icon(
                imageVector = LxIcons.Bulb,
                contentDescription = null,
                tint = Lx.colors.Accent,
                modifier = Modifier.size(13.dp),
            )
            Text(
                text = "需要你的回答",
                // 原型 `.confirmcard .ct` 同族：12.5px / 700（ask 卡标题同规格）
                style = TextStyle(fontFamily = Lx.type.Sans, fontSize = 12.5.sp, fontWeight = FontWeight.Bold),
                color = Lx.colors.Fg,
            )
        }
        Text(
            text = confirm.prompt,
            style = TextStyle(fontFamily = Lx.type.Sans, fontSize = 11.5.sp, lineHeight = 11.5.sp * 1.5f),
            color = Lx.colors.FgMuted,
        )
        // 选项 = 点选即答（对齐原型 `.askopt`：点选即发送 answer，不再两步选+确认）
        confirm.options.forEach { opt ->
            Row(
                Modifier
                    .fillMaxWidth()
                    .clip(OptShape)
                    .background(Lx.colors.Surface)
                    .border(
                        1.dp,
                        Lx.colors.BorderStrong,
                        OptShape,
                    )
                    .clickable(
                        interactionSource = remember { MutableInteractionSource() },
                        indication = null,
                    ) { onAnswer(opt) }
                    .padding(horizontal = 13.dp, vertical = 9.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                Text(
                    text = opt,
                    style = TextStyle(fontFamily = Lx.type.Sans, fontSize = 11.5.sp, fontWeight = FontWeight.SemiBold),
                    color = Lx.colors.Fg,
                    modifier = Modifier.weight(1f),
                )
            }
        }
        if (confirm.options.isNotEmpty()) {
            Text(
                text = "都不是？自己打字回答 ↓",
                style = TextStyle(fontFamily = Lx.type.Sans, fontSize = 10.5.sp, fontWeight = FontWeight.SemiBold),
                color = Lx.colors.FgFaint,
                modifier = Modifier
                    .padding(top = 2.dp)
                    .clickable(
                        interactionSource = remember { MutableInteractionSource() },
                        indication = null,
                    ) { typing = !typing },
            )
        }
        if (typing || confirm.options.isEmpty()) {
            // 自由回答输入：提交 = answer（空 = 跳过提问）
            Row(
                Modifier
                    .fillMaxWidth()
                    .clip(OptShape)
                    .background(Lx.colors.Surface)
                    .border(1.dp, Lx.colors.BorderStrong, OptShape)
                    .padding(horizontal = 11.dp, vertical = 4.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                androidx.compose.foundation.text.BasicTextField(
                    value = draft,
                    onValueChange = { draft = it },
                    singleLine = true,
                    textStyle = TextStyle(fontFamily = Lx.type.Sans, fontSize = 12.sp, color = Lx.colors.Fg),
                    cursorBrush = androidx.compose.ui.graphics.SolidColor(Lx.colors.Fg),
                    modifier = Modifier.weight(1f),
                    decorationBox = { inner ->
                        Box {
                            if (draft.isEmpty()) {
                                Text(
                                    text = "用一句话回答…",
                                    style = TextStyle(fontFamily = Lx.type.Sans, fontSize = 12.sp),
                                    color = Lx.colors.FgFaint,
                                )
                            }
                            inner()
                        }
                    },
                )
                Text(
                    text = "发送",
                    style = TextStyle(fontFamily = Lx.type.Sans, fontSize = 11.5.sp, fontWeight = FontWeight.Bold),
                    color = Lx.colors.Accent,
                    modifier = Modifier
                        .clickable(
                            interactionSource = remember { MutableInteractionSource() },
                            indication = null,
                        ) {
                            onAnswer(draft.trim())
                            draft = ""
                        },
                )
            }
        }
        // 无预设选项的 ask 才需要常驻按钮行（跳过 = 空回答提交）；有选项时点选
        // 即答 + 打字回答已覆盖两条回答路径，不再常驻「跳过/回答选中项」。
        if (confirm.options.isEmpty()) {
            Row(
                Modifier.padding(top = 10.dp),
                horizontalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                Box(Modifier.weight(1f)) {
                    LxButton(
                        text = "跳过",
                        onClick = { onAnswer("") },
                        variant = LxButtonVariant.Secondary,
                        size = LxButtonSize.Medium,
                        // 与 ConfirmCard 同款 `.cbtn` 规格（radius 8 / padding 8 0）
                        shapeOverride = RoundedCornerShape(8.dp),
                        paddingOverride = PaddingValues(vertical = 8.dp),
                        modifier = Modifier.fillMaxWidth(),
                    )
                }
            }
        }
    }
}

/** warn 7% 混底的合成（原型 `color-mix(in srgb, var(--warn) 7%, var(--surface))`）。 */
private fun androidx.compose.ui.graphics.Color.compositeOver(base: androidx.compose.ui.graphics.Color): androidx.compose.ui.graphics.Color =
    androidx.compose.ui.graphics.Color(
        red = red * alpha + base.red * (1 - alpha),
        green = green * alpha + base.green * (1 - alpha),
        blue = blue * alpha + base.blue * (1 - alpha),
        alpha = 1f,
    )
