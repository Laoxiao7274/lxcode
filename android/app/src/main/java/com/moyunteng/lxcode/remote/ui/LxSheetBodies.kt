// Composer 的 bottom sheet 内容（模型 / effort / 审批档 / 上下文明细）——
// 替代锚定 Popup 菜单的容器换壳：菜单内容逻辑与 PickerMenus.kt 同源（分组/选中态/
// 两步确认纪律），只把「面板」从锚定 Popup 换成 LxBottomSheet 的行。
//
// 逐项对齐原型 SHEETS 定义（temp/android-redesign/index.html 行 1803-1869）：
//  * model：标题「选择模型」+ 副标题语义注 + 按提供商分组条目（名+desc+选中勾）；
//  * effort：radio 单选四档（minimal/low/medium/high，仅声明 reasoning 的模型生效）；
//  * approval：radio 单选三档（auto/confirm/strict；「自动执行」两步确认纪律保留——
//    首次点击进 armed 态，文案换「再次点击确认」，切回安全档免确认）；
//  * ctx：五类明细条（system/tools/tool_results/messages/reasoning，占窗口比例条形）
//    + 脚注（真实实现读 agent.ContextUsage；估算值带 ~ 记号）。
//
// 2026-12：sheet 的 kind 枚举提升为 AppSheet（AppRoot.kt），body 全部参数化、
// 挂载点唯一在 AppRoot 的 AppSheetHost——本文件只剩纯内容组件。
package com.moyunteng.lxcode.remote.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.moyunteng.lxcode.design.token.Lx
import com.moyunteng.lxcode.remote.model.ContextUsage
import com.moyunteng.lxcode.remote.model.ModelGroup
import com.moyunteng.lxcode.remote.model.effortLabel

/** 五类上下文拆分的展示配置（对齐原型 CTX_CLASSES：label + 条形色）。 */

/** 五类上下文的取值与条形比例（占窗口比例）。 */
private fun ctxRows(u: ContextUsage): List<Triple<String, Int, Float>> = listOf(
    Triple("系统提示词", u.system, u.system.toFloat() / u.window),
    Triple("工具声明", u.tools, u.tools.toFloat() / u.window),
    Triple("工具结果", u.toolResults, u.toolResults.toFloat() / u.window),
    Triple("对话历史", u.messages, u.messages.toFloat() / u.window),
    Triple("思考过程", u.reasoning, u.reasoning.toFloat() / u.window),
)

/** 五类条形的色（对齐原型：accent / ok / warn / fg2 / fg3）。 */
@Composable
private fun ctxColor(index: Int): androidx.compose.ui.graphics.Color = when (index) {
    0 -> Lx.colors.Accent
    1 -> Lx.colors.Success
    2 -> Lx.colors.Amber
    3 -> Lx.colors.FgMuted
    else -> Lx.colors.FgFaint
}

/** 模型选择 sheet 内容：按提供商分组的模型条目（选中勾）。 */
@Composable
internal fun ModelSheetBody(
    groups: List<ModelGroup>,
    currentModelId: String,
    onPick: (String) -> Unit,
) {
    LazyColumn(Modifier.fillMaxWidth()) {
        if (groups.isEmpty()) {
            item {
                Text(
                    text = "没有启用的模型——到桌面端 设置 → 模型 里开启",
                    style = TextStyle(fontFamily = Lx.type.Sans, fontSize = 11.5.sp),
                    color = Lx.colors.FgFaint,
                    modifier = Modifier.padding(horizontal = 22.dp, vertical = 12.dp),
                )
            }
        }
        groups.forEach { group ->
            item(key = "g:${group.name}") { SheetGroupTitle(group.name.uppercase()) }
            itemsIndexed(group.models, key = { _, m -> m.id }) { _, m ->
                SheetSelectRow(
                    title = m.name,
                    desc = m.desc.ifBlank { m.modelId },
                    selected = m.id == currentModelId,
                    onClick = { onPick(m.id) },
                )
            }
        }
    }
}

/** 思考力度 sheet 内容：radio 四档（effortOptions 为空 = 当前模型不支持，整表灰置）。 */
@Composable
internal fun EffortSheetBody(
    effortOptions: List<String>,
    currentEffort: String,
    onPick: (String) -> Unit,
) {
    LazyColumn(Modifier.fillMaxWidth()) {
        val options = effortOptions.ifEmpty { com.moyunteng.lxcode.remote.model.EFFORT_IDS }
        itemsIndexed(options, key = { _, id -> id }) { _, id ->
            val supported = effortOptions.isEmpty() || id in effortOptions
            SheetSelectRow(
                title = id,
                desc = effortDesc(id),
                selected = id == currentEffort && supported,
                enabled = supported,
                radio = true,
                onClick = { if (supported) onPick(id) },
            )
        }
    }
}

/** effort 档位的一句话说明（对齐原型 SHEETS.effort.flat 的 desc）。 */
private fun effortDesc(id: String): String = when (id) {
    "minimal" -> "最短思考 · 极速响应"
    "low" -> "简短思考 · 日常问答"
    "medium" -> "适中深度 · 常规改动"
    "high" -> "深度推理 · 复杂任务与架构决策"
    else -> effortLabel(id)
}

/** 审批档 sheet 内容：radio 三档 + 「自动执行」两步确认（armed 态红底换文案）。 */
@Composable
internal fun ApprovalSheetBody(
    current: String,
    confirming: Boolean,
    onPick: (String) -> Unit,
) {
    LazyColumn(Modifier.fillMaxWidth()) {
        itemsIndexed(APPROVAL_SHEET_OPTIONS, key = { _, o -> o.id }) { _, o ->
            val armed = o.id == "auto" && confirming && current != "auto"
            SheetSelectRow(
                title = if (armed) "再次点击确认开启自动执行" else o.label,
                desc = if (armed) "高危工具将不再请求确认" else o.desc,
                selected = current == o.id,
                radio = true,
                onClick = { onPick(o.id) },
                modifier = if (armed) {
                    Modifier.padding(horizontal = 0.dp)
                } else {
                    Modifier
                },
            )
        }
    }
}

/** 审批三档（文案对齐原型 SHEETS.approval.flat；语义与 PermMenuContent 的 PERM_OPTIONS 一致）。 */
internal data class ApprovalOption(val id: String, val label: String, val desc: String)

internal val APPROVAL_SHEET_OPTIONS = listOf(
    ApprovalOption("auto", "自动执行", "不经确认直接运行工具，含 bash 高危命令模式"),
    ApprovalOption("confirm", "每次确认", "高危工具逐个弹确认卡，低危自动放行"),
    ApprovalOption("strict", "严格模式", "所有工具执行前都要求确认"),
)

/** 上下文占用明细 sheet 内容：头部读数 + 五类条形 + 脚注。 */
@Composable
internal fun ContextSheetBody(usage: ContextUsage?) {
    val known = usage != null && usage.window > 0
    LazyColumn(Modifier.fillMaxWidth()) {
        item {
            Column(Modifier.padding(horizontal = 22.dp, vertical = 6.dp)) {
                Text(
                    text = if (known) {
                        val u = usage!!
                        val pct = (u.used.toFloat() / u.window * 100).toInt()
                        "${if (u.estimated) "~" else ""}$pct% · ${u.used} / ${u.window} tok"
                    } else {
                        "—（本轮还没跑过，没有真实用量）"
                    },
                    style = TextStyle(fontFamily = Lx.type.Sans, fontSize = 12.5.sp, fontWeight = FontWeight.SemiBold),
                    color = Lx.colors.Fg,
                )
            }
        }
        if (known) {
            val u = usage!!
            itemsIndexed(ctxRows(u)) { i, (label, tokens, fraction) ->
                ContextUsageRow(
                    label = label,
                    tokens = "$tokens",
                    fraction = fraction,
                    color = ctxColor(i),
                )
            }
            item {
                ContextUsageFoot(
                    text = "窗口 ${u.window} tok · 已用 ${(u.used.toFloat() / u.window * 100).toInt()}%" +
                        "（${u.used} tok）。条宽 = 占窗口比例；读 agent.ContextUsage 五类拆分" +
                        if (u.estimated) "，估算值带 ~ 记号。" else "。",
                )
            }
        }
    }
}
