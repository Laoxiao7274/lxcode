// 应用级 bottom sheet 的宿主（AppSheetHost）—— AppRoot 最外层唯一挂载点的渲染体。
//
// 内容按 AppSheet 分发，全部是纯参数化的 LxSheetBodies（ModelSheetBody/EffortSheetBody/
// ApprovalSheetBody/ContextSheetBody）+ 各域的 onPick 收口（写后关 sheet）。标题/副标题
// 逐项继承原 Composer/AssistantComposer 两处 when 的文案（Sheet 语义注不丢）。
//
// 职责边界：本宿主只渲染与收口，不持业务状态——effort 视图与「自动执行」两步确认态
// 在 AppState（appSheetEffort 提升处），数据源直读 state.real（真实后端对象替换时
// 重组自动跟随，与页面取数同一口径）。
package com.moyunteng.lxcode.remote.ui

import androidx.compose.runtime.Composable
import com.moyunteng.lxcode.remote.model.EFFORT_IDS

/** AppSheet 的标题行（原 Composer/AssistantComposer 两处 when 的合并，文案不变）。 */
private fun sheetTitle(kind: AppSheet): String = when (kind) {
    AppSheet.Model, AppSheet.AssistantModel, AppSheet.SettingsModel -> "选择模型"
    AppSheet.Effort, AppSheet.AssistantEffort -> "思考力度"
    AppSheet.Approval, AppSheet.AssistantApproval -> "工具审批档"
    AppSheet.Context, AppSheet.AssistantContext -> "上下文占用"
}

/** AppSheet 的副标题（语义注——原两处 when 的合并 + 设置页默认模型的新条目）。 */
private fun sheetSubtitle(kind: AppSheet): String? = when (kind) {
    AppSheet.Model -> "chat.model · 仅本会话"
    AppSheet.Effort -> "chat.send effort · 仅声明 reasoning 的模型生效"
    AppSheet.Approval -> "chat.approval · 会话级实时状态，当轮生效"
    AppSheet.AssistantModel -> "assistant.model · 助手域语义"
    AppSheet.AssistantEffort -> "assistant.send effort"
    AppSheet.AssistantApproval -> "assistant.approval · 助手域语义"
    AppSheet.SettingsModel -> "role.set · 全局 default 角色（新会话的默认模型）"
    else -> null
}

/** 切模型后 effort 本地视图的回落（原 Composer/AssistantComposer 同款逻辑）：
 *  当前档在新模型上不存在 → 回落「中」，否则首档；模型不支持 effort 不动。 */
private fun effortFallback(effort: String, supported: List<String>): String =
    if (supported.isNotEmpty() && effort !in supported) {
        if ("medium" in supported) "medium" else supported.first()
    } else {
        effort
    }

/** 应用级 sheet 宿主（AppRoot 唯一挂载点调用；appSheet == null 时不组合内容）。 */
@Composable
fun AppSheetHost(state: AppState) {
    val kind = state.appSheet ?: return
    val real = state.real
    val asst = real.asst
    val contextKind = kind
    LxBottomSheet(
        visible = true,
        onDismiss = { state.closeAppSheet() },
        title = sheetTitle(contextKind),
        subtitle = sheetSubtitle(contextKind),
    ) {
        when (contextKind) {
            // ===== 主域（chat.* 语义）=====
            AppSheet.Model -> ModelSheetBody(
                groups = real.modelGroups,
                currentModelId = real.activeModelId,
                onPick = { id ->
                    // 切模型：**会话级**（chat.model：后端落库 + 广播，只改这一条会话
                    // ——不是 role.set 的全局 default 角色）；当前档位在新模型上
                    // 不存在 → 本地回落（优先「中」，否则首档）
                    val m = real.allModels.find { it.id == id }
                    val supported = m?.efforts?.filter { it in EFFORT_IDS } ?: emptyList()
                    state.composerEffort = effortFallback(state.composerEffort, supported)
                    real.changeSessionModel(id) { ok, msg ->
                        // 失败要给反馈：选中态由 chat.modelChanged 广播对齐（后端没改
                        // 成功就不会广播），本地乐观选中会永远停在旧模型上——toast 说明
                        if (!ok) state.showToast("切换模型失败${if (msg.isNotBlank()) "：$msg" else ""}")
                    }
                    state.closeAppSheet()
                },
            )
            AppSheet.Effort -> {
                val currentModel = real.allModels.find { it.id == real.activeModelId }
                val effortOptions = currentModel?.efforts?.filter { it in EFFORT_IDS } ?: emptyList()
                val effortId = if (state.composerEffort in effortOptions) {
                    state.composerEffort
                } else {
                    effortOptions.lastOrNull() ?: ""
                }
                EffortSheetBody(
                    effortOptions = effortOptions,
                    currentEffort = effortId,
                    onPick = { id ->
                        state.composerEffort = id
                        state.closeAppSheet()
                    },
                )
            }
            AppSheet.Approval -> ApprovalSheetBody(
                current = real.approval ?: "confirm",
                confirming = state.appSheetPermConfirming,
                onPick = { id ->
                    if (id == "auto" && real.approval != "auto") {
                        // 危险方向才拦：首次点击进 armed 态，再点才生效；
                        // 从自动执行切回安全档不需要确认（收权总是安全的）
                        if (!state.appSheetPermConfirming) {
                            state.appSheetPermConfirming = true
                            return@ApprovalSheetBody
                        }
                    }
                    state.appSheetPermConfirming = false
                    // 走协议（chat.approval：后端落库 + 广播，approvalChanged 回来对齐药丸）
                    real.changeApproval(id)
                    state.closeAppSheet()
                },
            )
            AppSheet.Context -> ContextSheetBody(real.contextUsage)

            // ===== 助手域（assistant.* 语义）=====
            AppSheet.AssistantModel -> ModelSheetBody(
                groups = real.modelGroups,
                currentModelId = asst.pickedModelId.ifBlank { real.defaultModelId },
                onPick = { id ->
                    val m = real.allModels.find { it.id == id }
                    val supported = m?.efforts?.filter { it in EFFORT_IDS } ?: emptyList()
                    state.assistantEffort = effortFallback(state.assistantEffort, supported)
                    real.changeAssistantModel(id) { ok, msg ->
                        // 选中态由 assistant.modelChanged 广播对齐；失败 toast
                        if (!ok) state.showToast("切换模型失败${if (msg.isNotBlank()) "：$msg" else ""}")
                    }
                    state.closeAppSheet()
                },
            )
            AppSheet.AssistantEffort -> {
                val asstModelId = asst.pickedModelId.ifBlank { real.defaultModelId }
                val currentModel = real.allModels.find { it.id == asstModelId }
                val effortOptions = currentModel?.efforts?.filter { it in EFFORT_IDS } ?: emptyList()
                val effortId = if (state.assistantEffort in effortOptions) {
                    state.assistantEffort
                } else {
                    effortOptions.lastOrNull() ?: ""
                }
                EffortSheetBody(
                    effortOptions = effortOptions,
                    currentEffort = effortId,
                    onPick = { id ->
                        state.assistantEffort = id
                        state.closeAppSheet()
                    },
                )
            }
            AppSheet.AssistantApproval -> ApprovalSheetBody(
                current = asst.approval ?: "confirm",
                confirming = state.appSheetPermConfirming,
                onPick = { id ->
                    if (id == "auto" && asst.approval != "auto") {
                        // 危险方向才拦（两步确认，与主域同一纪律）
                        if (!state.appSheetPermConfirming) {
                            state.appSheetPermConfirming = true
                            return@ApprovalSheetBody
                        }
                    }
                    state.appSheetPermConfirming = false
                    real.changeAssistantApproval(id)
                    state.closeAppSheet()
                },
            )
            AppSheet.AssistantContext -> ContextSheetBody(asst.contextUsage)

            // ===== 设置页「默认模型」（role.set · 全局 default 角色）=====
            AppSheet.SettingsModel -> ModelSheetBody(
                groups = real.modelGroups,
                currentModelId = real.defaultModelId,
                onPick = { id ->
                    // role.set：改全局 default 角色（= 新会话的默认模型）；与 chat.model
                    // 的会话级切换分工明确（后端 settings-models.ts 同语义）。成功后
                    // model.changed/快照刷新回读对齐；失败 toast 不假装成功。
                    real.setDefaultModel(id) { ok, msg ->
                        if (!ok) state.showToast("设置默认模型失败${if (msg.isNotBlank()) "：$msg" else ""}")
                    }
                    state.closeAppSheet()
                },
            )
        }
    }
}
