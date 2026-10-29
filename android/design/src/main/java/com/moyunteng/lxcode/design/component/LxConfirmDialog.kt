// lxcode 组件 · LxConfirmDialog（确认门对话框）
package com.moyunteng.lxcode.design.component

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import androidx.compose.ui.window.DialogWindowProvider
import com.moyunteng.lxcode.design.motion.LxEnterSpec
import com.moyunteng.lxcode.design.motion.lxEnter
import com.moyunteng.lxcode.design.token.Lx
import com.moyunteng.lxcode.design.token.LxElevation
import com.moyunteng.lxcode.design.token.lxShadow

/**
 * lxcode 确认门对话框 —— 高危工具执行前的裁决弹层。
 *
 * 语义对齐桌面端 ApprovalCard：标题问句 + 正文（被确认的内容）+ 「拒绝 / 批准」两个按钮。
 *  * 批准 = 黑主按钮（[LxButtonVariant.Primary]），放右边（桌面端动作区右对齐，主按钮在最右）；
 *  * 拒绝 = 幽灵按钮（[LxButtonVariant.Ghost]），放批准左边。
 *
 * 变体：`onDismissRequest` 为 null 时不可点遮罩关闭（真正的确认门：必须显式选择）。
 * [body] 插槽用于放具体被确认的内容（如终端块预览），不传则只显示 [text]。
 *
 * 桌面端对应：ApprovalCard.module.css `.card { padding: 12px; border-radius: 12px;
 * background: #fff; border: 1px solid #ececec; box-shadow: 0 1px 2px rgba(0,0,0,.03) }` +
 * `.actionBtns { gap: 6px; justify-content: flex-end }`。
 *
 * 动效（2026-10 补齐）：遮罩纯淡入 160ms（`settings-mask-in`）+ 卡片 `ap-card-in`
 * （translateY(8px) + 淡入，380ms 标准曲线）；批准/拒绝按钮的按压缩放走 120ms 标准曲线。
 * 遮罩值对齐桌面端 `.ag-doc-mask { background: rgba(0,0,0,.22) }`：平台默认调光（0.6）在
 * 首帧被置零，遮罩改由本组件自绘，这样「遮罩淡入」才有可控的动画对象。
 *
 * @param title 标题（桌面端是问句，如「执行此命令？」）
 * @param onApprove 批准回调
 * @param onDeny 拒绝回调
 * @param modifier 外部 Modifier
 * @param text 可选正文说明
 * @param body 可选内容插槽（如终端命令块），放在标题与按钮之间
 * @param approveText 批准按钮文案
 * @param denyText 拒绝按钮文案
 * @param onDismissRequest 点遮罩/返回键的回调；为 null 时不可取消
 */
@Composable
fun LxConfirmDialog(
    title: String,
    onApprove: () -> Unit,
    onDeny: () -> Unit,
    modifier: Modifier = Modifier,
    text: String? = null,
    body: (@Composable () -> Unit)? = null,
    approveText: String = "批准",
    denyText: String = "拒绝",
    onDismissRequest: (() -> Unit)? = null,
) {
    Dialog(
        onDismissRequest = { onDismissRequest?.invoke() },
        properties = DialogProperties(
            dismissOnBackPress = onDismissRequest != null,
            dismissOnClickOutside = onDismissRequest != null,
            usePlatformDefaultWidth = false,
        ),
    ) {
        // 平台调光（Android 默认 0.6）关掉：遮罩由本组件自绘，值对齐桌面端 `.ag-doc-mask`
        // 的 `rgba(0,0,0,.22)`，这样「遮罩纯淡入 160ms」才有可控的动画对象。
        val view = LocalView.current
        LaunchedEffect(view) {
            (view.parent as? DialogWindowProvider)?.window?.setDimAmount(0f)
        }

        Box(
            modifier = Modifier
                .fillMaxSize()
                .lxEnter(LxEnterSpec.SettingsMaskIn) // settings-mask-in：纯淡入 160ms ease
                .background(Color.Black.copy(alpha = 0.22f)),
            contentAlignment = Alignment.Center,
        ) {
            Column(
                modifier = Modifier
                    .lxEnter(LxEnterSpec.ApCardIn) // ap-card-in：translateY(8px) + 淡入，380ms 标准曲线
                    .padding(horizontal = Lx.space.s24)
                    .widthIn(max = 420.dp)
                    .fillMaxWidth()
                    .lxShadow(LxElevation.Modal, Lx.radius.CardShape)
                    .clip(Lx.radius.CardShape)
                    .background(Lx.colors.Surface)
                    .border(1.dp, Lx.colors.Border, Lx.radius.CardShape)
                    .padding(Lx.space.s12), // 桌面端 ApprovalCard .card { padding: 12px }
                verticalArrangement = Arrangement.spacedBy(Lx.space.s12),
            ) {
            Text(
                text = title,
                style = Lx.type.DialogTitle,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )

            if (text != null) {
                Text(
                    text = text,
                    style = Lx.type.BodyMuted,
                )
            }

            if (body != null) body()

            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.spacedBy(Lx.space.s6, Alignment.End), // 桌面端 .actionBtns { gap: 6px }
                verticalAlignment = Alignment.CenterVertically,
            ) {
                LxButton(
                    text = denyText,
                    onClick = onDeny,
                    variant = LxButtonVariant.Ghost,
                    size = LxButtonSize.Small,
                )
                LxButton(
                    text = approveText,
                    onClick = onApprove,
                    variant = LxButtonVariant.Primary,
                    size = LxButtonSize.Small,
                )
            }
            }
        }
    }
}
