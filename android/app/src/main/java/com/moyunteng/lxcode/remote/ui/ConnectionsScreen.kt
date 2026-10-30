// 连接页（ConnectionsScreen）+ 连接表单（ConnectionFormScreen）。
//
// 结构照桌面端连接管理弹窗（ConnectionManager.tsx / RemoteForm.tsx）做成一整页：
//   ① 本机卡（绿点 + 「本机」+ mono「127.0.0.1:7789」+ active 时「当前」黑底小标签 +
//      非 active 时右侧「连接」按钮）
//   ② 每条远程连接（点 + 名字 + mono「{地址} · {掩码Token}」+ active 时「当前」+
//      「断开」/「连接」+「编辑」+ 删除（两步确认变「确认」））
//   ③ 底部「添加远程后端」
//   ④ 提示行
// 行视觉（对齐 .conn-item）：1px --border-soft 边、圆角 11、active 时边框 --fg-muted、
// 点 7px（绿 = --success）。Token 掩码 = 保头尾各 4 位、中间 ••••。
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
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Cast
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Edit
import androidx.compose.material.icons.filled.ErrorOutline
import androidx.compose.material.icons.filled.QrCode2
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material.icons.filled.SystemUpdate
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import android.widget.Toast
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.moyunteng.lxcode.design.component.LxButton
import com.moyunteng.lxcode.design.component.LxButtonSize
import com.moyunteng.lxcode.design.component.LxButtonVariant
import com.moyunteng.lxcode.design.component.LxConfirmDialog
import com.moyunteng.lxcode.design.component.LxListItem
import com.moyunteng.lxcode.design.component.LxSpinner
import com.moyunteng.lxcode.design.component.LxTextField
import com.moyunteng.lxcode.design.motion.LxEnterSpec
import com.moyunteng.lxcode.design.motion.LxStagger
import com.moyunteng.lxcode.design.motion.lxEnter
import com.moyunteng.lxcode.design.motion.lxPressScale
import com.moyunteng.lxcode.design.motion.lxStaggerEnter
import com.moyunteng.lxcode.design.token.Lx
import com.moyunteng.lxcode.design.token.LxColors
import com.moyunteng.lxcode.remote.BuildConfig
import com.moyunteng.lxcode.remote.model.BackendConn

import com.moyunteng.lxcode.remote.model.LOCAL_ADDR
import com.moyunteng.lxcode.remote.model.addrValid
import com.moyunteng.lxcode.remote.model.maskToken
import com.moyunteng.lxcode.remote.model.nameFromAddr
import kotlinx.coroutines.launch
import java.util.UUID

/** 连接页：头部 → 扫码配对 → 检查更新 → （失败错误条）→ 连接卡片列表 → 添加远程后端 → 提示行。 */
@Composable
fun ConnectionsScreen(state: AppState) {
    Column(
        Modifier
            .fillMaxSize()
            .background(LxColors.Bg),
    ) {
        ScreenHeader(
            title = "连接",
            onBack = { state.route = Route.Sessions },
        )

        Column(
            Modifier
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(Lx.space.s12),
            verticalArrangement = Arrangement.spacedBy(Lx.space.s10),
        ) {
            // 扫码配对入口（纯远控壳：配对不再是底部导航 tab，改从连接页进入）
            LxListItem(
                title = "扫码配对",
                leading = { SmallIcon(Icons.Filled.QrCode2, Lx.colors.FgMuted, size = 15) },
                onClick = { state.route = Route.Pairing },
            )

            // 投屏（第一阶段：只看——手机实时看电脑画面；screen.* 协议）。
            // 0.3.30 拍板：入口点击 = 直接开始（远程工具语义——点了就是看画面）。
            // 成功投屏过 = 按记忆的显示器/清晰度直接发 screen.start，路由仍切投屏页，
            // active=true 上报后自然进观看态；首次使用落设置页选参数（选过才出现快捷）。
            // 失败回设置页看错误（screenToast），不吞。
            // 挂起语义：已在投屏（active=true）时点击 = 只进投屏页（缩小态恢复观看，
            // 可再放大），**不再发 screen.start**——重复 start 会让后端按命令电平
            // 重起 ffmpeg（画面闪断一拍，纯浪费）。
            LxListItem(
                title = "投屏（查看电脑画面）",
                leading = { SmallIcon(Icons.Filled.Cast, Lx.colors.FgMuted, size = 15) },
                onClick = {
                    val real = state.real
                    real.refreshScreen()
                    if (real.screenState?.active != true &&
                        state.hasCastBefore() && !real.screenBusy
                    ) {
                        real.startScreen(state.screenPickMonitor, state.screenPickQuality) { err ->
                            if (err != null) state.screenToast = err
                            else state.rememberScreenCast(state.screenPickMonitor, state.screenPickQuality)
                        }
                    }
                    state.route = Route.Screen
                },
            )

            // 检查更新（手动入口）：启动时也会自动检查（失败/已最新都静默）；
            // 这里三态齐全——有新版弹层给详情 + 下载按钮，已最新/失败给 toast。
            UpdateCheckItem(state)

            // 后端不可达错误条（全局态：连接失败时浮在卡片列表上方；重试重连当前凭证）
            val real = state.real
            if (real.phase == com.moyunteng.lxcode.remote.net.RealPhase.Failed) {
                UnreachableBar(
                    onRetry = { real.retry(state.realAddr, state.realToken) },
                    message = real.phaseMessage ?: "后端不可达：无法连接 ws://${state.realAddr}/rpc",
                )
            }

            state.conns.forEachIndexed { i, conn ->
                ConnCard(
                    modifier = Modifier.lxStaggerEnter(count = state.conns.size + 1, index = i),
                    conn = conn,
                    // 「当前」标签：正在连接的条目，或最后选中（重启后未连也标记，作为
                    // 重连目标）；「连接/断开」按钮看 connected——见 ConnCard 的 KDoc
                    active = (state.realOn && state.isCurrentConn(conn.id)) ||
                        (!state.realOn && conn.id == state.activeConnId),
                    connected = state.realOn && state.isCurrentConn(conn.id),
                    // 「连接」动作 = 真实连接：把条目的 addr/token 设为真实模式凭证并连
                    // （等价于调试区开关 + 手填地址，入口从调试区收口到连接列表）
                    onConnect = { state.connectTo(conn) },
                    onDisconnect = { state.disconnectReal() },
                    onEdit = {
                        state.editing = ConnDraft(conn.id, conn.addr, conn.name, conn.token, isNew = false)
                        state.route = Route.ConnForm(fromScan = false)
                    },
                    onDelete = { state.removeConn(conn.id) },
                )
            }

            // ③ 底部「+ 添加远程后端」（弹层入场 pop-in：translateY(-4dp) + scale .98，160ms）
            val addInteraction = remember { MutableInteractionSource() }
            Row(
                Modifier
                    .lxPressScale(addInteraction)
                    .lxEnter(
                        spec = LxEnterSpec.PopIn,
                        delayMillis = LxStagger.delayMillis(state.conns.size + 1, state.conns.size),
                    )
                    .fillMaxWidth()
                    .clip(Lx.radius.FieldShape)
                    .background(LxColors.Surface)
                    .border(1.dp, Lx.colors.BorderStrong, Lx.radius.FieldShape)
                    .clickable(
                        interactionSource = addInteraction,
                        indication = null,
                    ) {
                        state.editing = ConnDraft(
                            id = UUID.randomUUID().toString(),
                            addr = "",
                            name = "",
                            token = "",
                            isNew = true,
                        )
                        state.route = Route.ConnForm(fromScan = false)
                    }
                    .padding(vertical = Lx.space.s10),
                horizontalArrangement = Arrangement.Center,
                verticalAlignment = Alignment.CenterVertically,
            ) {
                SmallIcon(Icons.Filled.Add, Lx.colors.FgMuted)
                Box(Modifier.width(Lx.space.s6))
                Text(
                    text = "添加远程后端",
                    style = Lx.type.Button,
                    color = Lx.colors.FgMuted,
                )
            }

            // ④ 提示行
            Text(
                text = "要连接一台桌面端？在它的「连接 → 远程访问」页展示二维码，" +
                    "点上方「扫码配对」扫它，凭证自动填入。",
                style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size10_5),
                color = Lx.colors.FgFaint,
            )
        }
    }
}

/**
 * 后端断线/重连横幅（通用，连接页与会话页共用；由连接页 UnreachableBar 提取）：
 *
 *  * [down] = 断线态（DangerSoft 底 + 错误图标 + 人话原因 + 「立即重连」）；
 *  * [reconnecting] = 自动重连进行中（琥珀底 + spinner + 「正在重连…」）——断线
 *    时后台一直在自动重连，把它表达出来，用户不用手动去连接页重连；
 *  * [pairingInvalid] = 配对已失效（401：token 错/被吊销）——重连必再 401，
 *    「重新配对」按钮跳扫码页重新配对（不显示「立即重连」——那是无用动作）。
 *
 * 三条互斥显示；都为 false 时本组件不渲染（调用方包 if）。
 */
@Composable
fun BackendDownBar(
    down: Boolean,
    reconnecting: Boolean,
    onRetry: () -> Unit,
    message: String? = null,
    pairingInvalid: Boolean = false,
    onRepair: () -> Unit = {},
) {
    when {
        pairingInvalid -> Row(
            Modifier
                .fillMaxWidth()
                .lxEnter(spec = LxEnterSpec.ErrorBarIn)
                .clip(Lx.radius.FieldShape)
                .background(Lx.colors.DangerSoft)
                .padding(horizontal = Lx.space.s10, vertical = Lx.space.s8),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(Lx.space.s8),
        ) {
            Icon(
                imageVector = Icons.Filled.ErrorOutline,
                contentDescription = null,
                tint = Lx.colors.Danger,
                modifier = Modifier.size(16.dp),
            )
            Text(
                text = message ?: "配对已失效：请在电脑端重新生成配对码",
                style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size12),
                color = Lx.colors.Danger,
                modifier = Modifier.weight(1f),
            )
            LxButton(
                text = "重新配对",
                onClick = onRepair,
                variant = LxButtonVariant.Secondary,
                size = LxButtonSize.Small,
            )
        }
        down -> Row(
            Modifier
                .fillMaxWidth()
                // 断线态滑入：y 8dp + 淡入，220ms 标准曲线
                .lxEnter(spec = LxEnterSpec.ErrorBarIn)
                .clip(Lx.radius.FieldShape)
                .background(Lx.colors.DangerSoft)
                .padding(horizontal = Lx.space.s10, vertical = Lx.space.s8),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(Lx.space.s8),
        ) {
            Icon(
                imageVector = Icons.Filled.ErrorOutline,
                contentDescription = null,
                tint = Lx.colors.Danger,
                modifier = Modifier.size(16.dp),
            )
            Text(
                text = message ?: "后端不可达：无法连接 ws://127.0.0.1:7789/rpc",
                style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size12),
                color = Lx.colors.Danger,
                modifier = Modifier.weight(1f),
            )
            LxButton(
                text = "立即重连",
                onClick = onRetry,
                variant = LxButtonVariant.Secondary,
                size = LxButtonSize.Small,
                leadingIcon = {
                    Icon(
                        imageVector = Icons.Filled.Refresh,
                        contentDescription = null,
                        tint = Lx.colors.FgMuted,
                        modifier = Modifier.size(12.dp),
                    )
                },
            )
        }
        reconnecting -> Row(
            Modifier
                .fillMaxWidth()
                .lxEnter(spec = LxEnterSpec.ErrorBarIn)
                .clip(Lx.radius.FieldShape)
                .background(Lx.colors.AmberSoft)
                .padding(horizontal = Lx.space.s10, vertical = Lx.space.s8),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(Lx.space.s8),
        ) {
            LxSpinner(size = 12.dp)
            Text(
                text = "连接已断开，正在重连…",
                style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size12),
                color = LxColors.AmberInk,
                modifier = Modifier.weight(1f),
            )
        }
    }
}

/** 「后端不可达」错误条（--danger 文案 + 重试按钮）。 */
@Composable
private fun UnreachableBar(onRetry: () -> Unit, message: String? = null) {
    BackendDownBar(down = true, reconnecting = false, onRetry = onRetry, message = message)
}

/** 一条连接卡（本机与远程共用；本机不显示编辑/删除）。
 *  [active] = 「当前」标签（最后选中/正在连接的条目）；[connected] = 真实连接
 *  正跑在这条上（决定「断开」/「连接」按钮——重启后按 realOn 持久化自动恢复连接，
 *  恢复失败/条目被删时当前条目仍显示「连接」按钮可一键重连，而不是卡在
 *  没有意义的「断开」上）。 */
@Composable
private fun ConnCard(
    conn: BackendConn,
    active: Boolean,
    connected: Boolean,
    modifier: Modifier = Modifier,
    onConnect: () -> Unit,
    onDisconnect: () -> Unit,
    onEdit: () -> Unit,
    onDelete: () -> Unit,
) {
    Column(
        modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(Lx.radius.r10 + 1.dp))
            .background(LxColors.Surface)
            .border(
                width = 1.dp,
                color = if (active) Lx.colors.FgMuted else Lx.colors.BorderSoft,
                shape = RoundedCornerShape(Lx.radius.r10 + 1.dp),
            )
            .padding(horizontal = Lx.space.s12, vertical = Lx.space.s10),
        verticalArrangement = Arrangement.spacedBy(Lx.space.s8),
    ) {
        Row(
            Modifier.fillMaxWidth(),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(Lx.space.s10),
        ) {
            Box(
                Modifier
                    .size(7.dp)
                    .background(if (active) Lx.colors.Success else Lx.colors.BorderStrong, CircleShape),
            )
            Column(Modifier.weight(1f)) {
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(Lx.space.s6),
                ) {
                    Text(
                        text = conn.name,
                        style = Lx.type.ListTitle,
                        color = Lx.colors.Fg,
                    )
                    if (active) CurrentTag()
                }
                Text(
                    text = if (conn.local) {
                        LOCAL_ADDR
                    } else {
                        "${conn.addr} · ${maskToken(conn.token)}"
                    },
                    style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size11_5),
                    color = Lx.colors.FgFaint,
                )
            }
        }

        Row(
            Modifier.fillMaxWidth(),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(Lx.space.s6, Alignment.End),
        ) {
            if (connected) {
                if (!conn.local) {
                    LxButton("断开", onDisconnect, variant = LxButtonVariant.Ghost, size = LxButtonSize.Small)
                }
            } else {
                LxButton("连接", onConnect, variant = LxButtonVariant.Ghost, size = LxButtonSize.Small)
            }
            if (!conn.local) {
                LxButton(
                    text = "编辑",
                    onClick = onEdit,
                    variant = LxButtonVariant.Secondary,
                    size = LxButtonSize.Small,
                    leadingIcon = { SmallIcon(Icons.Filled.Edit, Lx.colors.FgMuted, size = 12) },
                )
                DeleteBtn(onDelete)
            }
        }
    }
}

/** 「当前」黑底小标签（对齐 .conn-tag）。 */
@Composable
private fun CurrentTag() {
    Text(
        text = "当前",
        style = Lx.type.Pill,
        color = LxColors.Bg,
        modifier = Modifier
            // 裁决/当前徽标回弹入场：桌面端 back.out(2)（低阻尼 spring）
            .lxEnter(spec = LxEnterSpec.BubbleIn)
            .background(Lx.colors.Fg, RoundedCornerShape(Lx.radius.r4))
            .padding(horizontal = 5.dp, vertical = 1.dp),
    )
}

/** 删除按钮（两步确认：删除 → 确认；对齐 .ag-mini-btn 的 useConfirmClick）。 */
@Composable
private fun DeleteBtn(onConfirm: () -> Unit) {
    var confirming by remember { mutableStateOf(false) }
    val interaction = remember { MutableInteractionSource() }
    Row(
        Modifier
            .lxPressScale(interaction)
            .clip(Lx.radius.RowShape)
            .clickable(
                interactionSource = interaction,
                indication = null,
            ) {
                if (confirming) {
                    onConfirm()
                } else {
                    confirming = true
                }
            }
            .padding(horizontal = Lx.space.s6, vertical = Lx.space.s4),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(Lx.space.s4),
    ) {
        SmallIcon(Icons.Filled.Delete, Lx.colors.Danger, size = 12)
        Text(
            text = if (confirming) "确认" else "删除",
            style = Lx.type.Button,
            color = Lx.colors.Danger,
        )
    }
}

/** 连接表单页：地址 + 名称 + Token 三字段 + 校验 + 取消/保存（不可保存时禁用）。 */
@Composable
fun ConnectionFormScreen(state: AppState, fromScan: Boolean) {
    val context = LocalContext.current
    val draft = state.editing
    if (draft == null) {
        // 没有草稿（理论上进不来）：回连接页，不留空白屏
        state.route = Route.Connections
        return
    }
    var addr by remember(draft) {
        // 新建表单预填上次输入（连接草稿持久化：没保存就重启也不丢——0.2.6 修复）；
        // 编辑既有条目仍从条目本身开始，不掺草稿。
        val d = if (draft.isNew) state.loadFormDraft() else null
        mutableStateOf(draft.addr.ifEmpty { d?.addr ?: "" })
    }
    var name by remember(draft) {
        val d = if (draft.isNew) state.loadFormDraft() else null
        mutableStateOf(draft.name.ifEmpty { d?.name ?: "" })
    }
    var token by remember(draft) {
        val d = if (draft.isNew) state.loadFormDraft() else null
        mutableStateOf(draft.token.ifEmpty { d?.token ?: "" })
    }

    val addrTrim = addr.trim()
    val addrOk = addrValid(addrTrim)
    val savable = addrTrim.isNotEmpty() && addrOk && token.trim().isNotEmpty() &&
        (name.trim().isNotEmpty() || nameFromAddr(addrTrim) != addrTrim)

    val cancel = {
        state.editing = null
        state.route = Route.Connections
    }
    val save = {
        val saved = ConnDraft(
            id = draft.id,
            addr = addrTrim,
            name = name.trim().ifEmpty { nameFromAddr(addrTrim) },
            token = token.trim(),
            isNew = draft.isNew,
        )
        if (draft.isNew) state.addConn(saved) else state.updateConn(saved)
        state.editing = null
        state.route = Route.Connections
        // 扫码收口最后一环：载荷已保存为连接条目，提示回连接页点「连接」一键接入
        if (fromScan) {
            Toast.makeText(context, "已保存「${saved.name}」，点「连接」接入真实后端", Toast.LENGTH_LONG).show()
        }
    }

    Column(
        Modifier
            .fillMaxSize()
            .background(LxColors.Bg),
    ) {
        ScreenHeader(
            title = if (draft.isNew) "添加远程后端" else "编辑远程后端",
            onBack = cancel,
        )
        Column(
            Modifier
                // 表单（添加/编辑远程后端）弹层入场：pop-in（translateY(-4dp) + scale .98，160ms）
                .lxEnter(spec = LxEnterSpec.PopIn, key = draft.id)
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(Lx.space.s12),
            verticalArrangement = Arrangement.spacedBy(Lx.space.s16),
        ) {
            if (fromScan) {
                Row(
                    Modifier
                        .fillMaxWidth()
                        .clip(Lx.radius.FieldShape)
                        .background(Lx.colors.SuccessSoft)
                        .padding(horizontal = Lx.space.s10, vertical = Lx.space.s8),
                ) {
                    Text(
                        text = "已从扫码结果填入地址与 Token，确认后保存。",
                        style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size12),
                        color = LxColors.SuccessInk,
                    )
                }
            }

            FormField(
                label = "地址",
                value = addr,
                onValueChange = {
                    addr = it
                    state.saveFormDraft(addr, token, name)
                },
                placeholder = "host:port（如 10.0.0.8:7789）或 https://api.example.com",
                mono = true,
                error = if (!addrOk) "地址格式应为 host:port（如 10.0.0.8:7789）或 https://host。" else null,
            )
            FormField(
                label = "名称",
                value = name,
                onValueChange = {
                    name = it
                    state.saveFormDraft(addr, token, name)
                },
                placeholder = "显示名（留空自动取 ${if (addrTrim.isEmpty()) "地址主机名" else nameFromAddr(addrTrim)}）",
                mono = false,
                error = null,
            )
            FormField(
                label = "Token",
                value = token,
                onValueChange = {
                    token = it
                    state.saveFormDraft(addr, token, name)
                },
                placeholder = "连接凭证（服务端生成）",
                mono = true,
                error = null,
                hint = "对方机器「连接 → 远程访问」面板里展示的 Token；或问管理员要。",
            )

            Row(
                Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.spacedBy(Lx.space.s6, Alignment.End),
            ) {
                LxButton("取消", cancel, variant = LxButtonVariant.Ghost, size = LxButtonSize.Medium)
                LxButton(
                    text = "保存",
                    onClick = save,
                    variant = LxButtonVariant.Primary,
                    size = LxButtonSize.Medium,
                    enabled = savable,
                )
            }
        }
    }
}

/** 表单字段（标签 + 输入框 + 可选错误/提示）。 */
@Composable
private fun FormField(
    label: String,
    value: String,
    onValueChange: (String) -> Unit,
    placeholder: String,
    mono: Boolean,
    error: String?,
    hint: String? = null,
) {
    Column(verticalArrangement = Arrangement.spacedBy(Lx.space.s6)) {
        Text(
            text = label,
            style = Lx.type.Pill.copy(fontSize = Lx.type.Size11, fontWeight = FontWeight.SemiBold),
            color = Lx.colors.FgMuted,
        )
        LxTextField(
            value = value,
            onValueChange = onValueChange,
            placeholder = placeholder,
        )
        if (error != null) {
            Text(
                text = error,
                style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size11_5),
                color = Lx.colors.Danger,
            )
        }
        if (hint != null) {
            Text(
                text = hint,
                style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size10_5),
                color = Lx.colors.FgFaint,
            )
        }
    }
}

/**
 * 「检查更新」行：手动检查的三态 —— 有新版弹层（详情 + 去下载）/ 已最新 toast /
 * 失败 toast（人话原因）。启动时的自动检查在 AppRoot（只有「有新版」出横幅）。
 */
@Composable
private fun UpdateCheckItem(state: AppState) {
    val context = LocalContext.current
    fun toast(msg: String) = Toast.makeText(context, msg, Toast.LENGTH_SHORT).show()
    val scope = rememberCoroutineScope()
    var checking by remember { mutableStateOf(false) }
    var found by remember { mutableStateOf<com.moyunteng.lxcode.remote.update.ApkRelease?>(null) }

    LxListItem(
        title = "检查更新",
        subtitle = "当前 v${BuildConfig.VERSION_NAME}",
        leading = { SmallIcon(Icons.Filled.SystemUpdate, Lx.colors.FgMuted, size = 15) },
        trailing = { if (checking) LxSpinner() },
        onClick = {
            if (checking) return@LxListItem
            checking = true
            scope.launch {
                val result = kotlinx.coroutines.withContext(kotlinx.coroutines.Dispatchers.IO) {
                    com.moyunteng.lxcode.remote.update.UpdateChecker.check(
                        BuildConfig.VERSION_CODE.toLong(),
                    )
                }
                checking = false
                when (result) {
                    is com.moyunteng.lxcode.remote.update.UpdateCheckResult.Available ->
                        found = result.release
                    com.moyunteng.lxcode.remote.update.UpdateCheckResult.UpToDate ->
                        toast("已是最新版本（v${BuildConfig.VERSION_NAME}）")
                    is com.moyunteng.lxcode.remote.update.UpdateCheckResult.Failed ->
                        toast("检查更新失败：${result.message}")
                }
            }
        },
    )

    found?.let { release ->
        LxConfirmDialog(
            title = "发现新版本 v${release.version}",
            text = buildString {
                append("当前 v${BuildConfig.VERSION_NAME} · ")
                append(com.moyunteng.lxcode.remote.update.formatBytes(release.size))
                if (release.notes.isNotEmpty()) {
                    append("\n")
                    append(release.notes.joinToString("\n"))
                }
            },
            approveText = "下载并安装",
            denyText = "以后再说",
            onApprove = {
                found = null
                // 应用内下载替换：入队 DownloadManager → 横幅接管进度/校验/安装
                // （流程收口在会话页横幅 + AppRoot 的 verify/install 编排）
                state.updateAvailable = release
            },
            onDeny = { found = null },
            onDismissRequest = { found = null },
        )
    }
}
