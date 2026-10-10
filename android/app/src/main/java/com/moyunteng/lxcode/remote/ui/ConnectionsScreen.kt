// 连接页（ConnectionsScreen）+ 连接表单（ConnectionFormScreen）。
//
// 结构照桌面端连接管理弹窗（ConnectionManager.tsx / RemoteForm.tsx）做成一整页：
//   ① 本机卡（绿点 + 「本机」+ mono「127.0.0.1:7789」+ active 时「当前」黑底小标签 +
//      非 active 时右侧「连接」按钮）
//   ② 每条远程连接（点 + 名字 + mono「{地址} · {掩码Token}」+ active 时「当前」+
//      「断开」/「连接」+「编辑」+ 删除（两步确认变「确认」））
//   ③ 底部「+ 添加远程后端」
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
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Edit
import androidx.compose.material.icons.filled.ErrorOutline
import androidx.compose.material.icons.filled.QrCode2
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.moyunteng.lxcode.design.component.LxButton
import com.moyunteng.lxcode.design.component.LxButtonSize
import com.moyunteng.lxcode.design.component.LxButtonVariant
import com.moyunteng.lxcode.design.component.LxListItem
import com.moyunteng.lxcode.design.component.LxTextField
import com.moyunteng.lxcode.design.motion.LxCollapseAway
import com.moyunteng.lxcode.design.motion.LxEnterSpec
import com.moyunteng.lxcode.design.motion.LxStagger
import com.moyunteng.lxcode.design.motion.lxEnter
import com.moyunteng.lxcode.design.motion.lxPressScale
import com.moyunteng.lxcode.design.motion.lxStaggerEnter
import com.moyunteng.lxcode.design.token.Lx
import com.moyunteng.lxcode.design.token.LxColors
import com.moyunteng.lxcode.remote.mock.BackendConn
import com.moyunteng.lxcode.remote.mock.MockData
import com.moyunteng.lxcode.remote.mock.addrValid
import com.moyunteng.lxcode.remote.mock.maskToken
import com.moyunteng.lxcode.remote.mock.nameFromAddr
import java.util.UUID

/** 连接页：本机 + 远程列表 + 添加 + （离线时）后端不可达错误条。 */
@Composable
fun ConnectionsScreen(state: MockAppState) {
    Column(
        Modifier
            .fillMaxSize()
            .background(LxColors.Bg),
    ) {
        ScreenHeader(
            title = "连接",
            onBack = { state.route = Route.Sessions },
            trailing = {
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(Lx.space.s4),
                ) {
                    MockSwitch("模拟在线", state.online, onCheckedChange = { state.online = it })
                    // 动效调试开关（原型用）：关掉 = 模拟系统「减少动态效果」（静态到终态）
                    MockSwitch("动效", state.motionOn, onCheckedChange = { state.motionOn = it })
                    // 调试入口：组件库展示页（开发期用；纯远控壳不进底部导航，从这里进）
                    Text(
                        text = "组件展示",
                        style = Lx.type.ListSubtitle.copy(fontSize = Lx.type.Size11_5),
                        color = Lx.colors.FgMuted,
                        modifier = Modifier
                            .clip(Lx.radius.RowShape)
                            .clickable(
                                interactionSource = remember { MutableInteractionSource() },
                                indication = null,
                            ) { state.route = Route.Showcase }
                            .padding(horizontal = Lx.space.s8, vertical = Lx.space.s6),
                    )
                }
            },
        )

        Column(
            Modifier
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(Lx.space.s12),
            verticalArrangement = Arrangement.spacedBy(Lx.space.s10),
        ) {
            // 断线错误条：入场 y+8dp 滑入（错误条自带），退场 collapseAway 220ms 高度归零 + 淡出
            LxCollapseAway(
                visible = !state.online,
                enter = androidx.compose.animation.EnterTransition.None,
            ) {
                UnreachableBar(onRetry = { state.online = true })
            }

            // 扫码配对入口（纯远控壳：配对不再是底部导航 tab，改从连接页进入）
            LxListItem(
                title = "扫码配对",
                leading = { SmallIcon(Icons.Filled.QrCode2, Lx.colors.FgMuted, size = 15) },
                onClick = { state.route = Route.Pairing },
            )

            // 真实后端（调试区第三个开关）：开 = WS 连真实 lxcode 后端（协议见 PROTOCOL-NOTES.md）
            RealBackendSection(state)

            state.conns.forEachIndexed { i, conn ->
                ConnCard(
                    modifier = Modifier.lxStaggerEnter(count = state.conns.size + 1, index = i),
                    conn = conn,
                    active = conn.id == state.activeConnId,
                    onConnect = { state.activeConnId = conn.id },
                    onDisconnect = { state.activeConnId = "local" },
                    onEdit = {
                        state.editing = ConnDraft(conn.id, conn.addr, conn.name, conn.token, isNew = false)
                        state.route = Route.ConnForm(fromScan = false)
                    },
                    onDelete = { state.removeConn(conn.id) },
                )
            }

            // ③ 底部「+ 添加远程后端」（弹层入场 pop-in：translateY(-4dp) + scale .98，160ms）
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
                    text = "+ 添加远程后端",
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

/** 「后端不可达」错误条（--danger 文案 + 重试按钮）。 */
@Composable
private fun UnreachableBar(onRetry: () -> Unit, message: String? = null) {
    Row(
        Modifier
            .fillMaxWidth()
            // 断线态滑入：y 8dp + 淡入，220ms 标准曲线（退场由 LxCollapseAway 承担）
            .lxEnter(spec = LxEnterSpec.ErrorBarIn)
            .clip(Lx.radius.FieldShape)
            .background(Lx.colors.DangerSoft)
            .padding(horizontal = Lx.space.s10, vertical = Lx.space.s8),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(Lx.space.s8),
    ) {
        SmallIcon(Icons.Filled.ErrorOutline, Lx.colors.Danger)
        Text(
            text = message ?: "后端不可达：无法连接 ws://127.0.0.1:7789/rpc",
            style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size12),
            color = Lx.colors.Danger,
            modifier = Modifier.weight(1f),
        )
        LxButton(
            text = "重试",
            onClick = onRetry,
            variant = LxButtonVariant.Secondary,
            size = LxButtonSize.Small,
            leadingIcon = {
                SmallIcon(Icons.Filled.Refresh, Lx.colors.FgMuted, size = 12)
            },
        )
    }
}

/**
 * 「真实后端」调试区：开关 + 地址输入 + 实时连接状态 + 失败重试条。
 *
 * 开：连 `addr`（默认 127.0.0.1:7789）→ hello 握手 → 会话列表换真实数据；
 * 关：断开并完全回到 mock 路径（mock 数据零改动）。断线复用 [UnreachableBar]，
 * 重试按钮真实重连（重发握手）。
 */
@Composable
private fun RealBackendSection(state: MockAppState) {
    val real = state.real
    Column(
        Modifier
            .fillMaxWidth()
            .clip(Lx.radius.FieldShape)
            .background(LxColors.Surface)
            .border(1.dp, Lx.colors.BorderSoft, Lx.radius.FieldShape)
            .padding(horizontal = Lx.space.s12, vertical = Lx.space.s10),
        verticalArrangement = Arrangement.spacedBy(Lx.space.s8),
    ) {
        Row(
            Modifier.fillMaxWidth(),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(Lx.space.s8),
        ) {
            Column(Modifier.weight(1f)) {
                Text(text = "真实后端", style = Lx.type.ListTitle, color = Lx.colors.Fg)
                Text(
                    text = "开 = 连真实 lxcode 后端（WS JSON-RPC）；关 = 回到演示数据",
                    style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size10_5),
                    color = Lx.colors.FgFaint,
                )
            }
            MockSwitch(
                label = "",
                checked = state.realOn,
                onCheckedChange = { on ->
                    state.realOn = on
                    if (on) real.connect(state.realAddr) else real.disconnect()
                },
            )
        }

        // 状态行：阶段点 + 文案（连接中/已连接/失败原因）
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(Lx.space.s6),
        ) {
            Box(
                Modifier
                    .size(7.dp)
                    .background(
                        when (real.phase) {
                            com.moyunteng.lxcode.remote.net.RealPhase.Connected -> Lx.colors.Success
                            com.moyunteng.lxcode.remote.net.RealPhase.Connecting -> LxColors.Amber
                            com.moyunteng.lxcode.remote.net.RealPhase.Failed -> Lx.colors.Danger
                            com.moyunteng.lxcode.remote.net.RealPhase.Offline -> Lx.colors.BorderStrong
                        },
                        CircleShape,
                    ),
            )
            Text(
                text = when (real.phase) {
                    com.moyunteng.lxcode.remote.net.RealPhase.Connected ->
                        "已连接 ${real.addr}（协议 v2）"
                    com.moyunteng.lxcode.remote.net.RealPhase.Connecting -> "连接中 ${real.addr}…"
                    com.moyunteng.lxcode.remote.net.RealPhase.Failed ->
                        "连接失败：${real.phaseMessage ?: "未知原因"}"
                    com.moyunteng.lxcode.remote.net.RealPhase.Offline -> "未连接（地址如下，开关打开即连）"
                },
                style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size11_5),
                color = Lx.colors.FgFaint,
            )
        }

        if (state.realOn) {
            LxTextField(
                value = state.realAddr,
                onValueChange = { state.realAddr = it },
                placeholder = "127.0.0.1:7789",
            )
        }

        // 断线错误条（真实模式专用；mock 断线错误条在页首，两者互斥不并存）
        if (state.realOn && real.phase == com.moyunteng.lxcode.remote.net.RealPhase.Failed) {
            UnreachableBar(
                onRetry = { real.retry() },
                message = real.phaseMessage ?: "后端不可达：无法连接 ws://${state.realAddr}/rpc",
            )
        }
    }
}

/** 一条连接卡（本机与远程共用；本机不显示编辑/删除）。 */
@Composable
private fun ConnCard(
    conn: BackendConn,
    active: Boolean,
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
                        MockData.LOCAL_ADDR
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
            if (active) {
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
fun ConnectionFormScreen(state: MockAppState, fromScan: Boolean) {
    val draft = state.editing
    if (draft == null) {
        // 没有草稿（理论上进不来）：回连接页，不留空白屏
        state.route = Route.Connections
        return
    }
    var addr by remember(draft) { mutableStateOf(draft.addr) }
    var name by remember(draft) { mutableStateOf(draft.name) }
    var token by remember(draft) { mutableStateOf(draft.token) }

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
                onValueChange = { addr = it },
                placeholder = "host:port（如 10.0.0.8:7789）或 https://api.example.com",
                mono = true,
                error = if (!addrOk) "地址格式应为 host:port（如 10.0.0.8:7789）或 https://host。" else null,
            )
            FormField(
                label = "名称",
                value = name,
                onValueChange = { name = it },
                placeholder = "显示名（留空自动取 ${if (addrTrim.isEmpty()) "地址主机名" else nameFromAddr(addrTrim)}）",
                mono = false,
                error = null,
            )
            FormField(
                label = "Token",
                value = token,
                onValueChange = { token = it },
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
