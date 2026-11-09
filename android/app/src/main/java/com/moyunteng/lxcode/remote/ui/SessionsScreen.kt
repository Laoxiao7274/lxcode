// 会话列表页（SessionsScreen）—— 照桌面端侧栏（Sidebar.tsx / SessionRow.tsx）的结构。
//
// 顺序：① 导航项（仅「新对话」——Agents/拓展/Git 管理/自动化/远程访问是桌面端
//       管理本机的面板，纯远控壳的手机端不出现）
//      ② 搜索框（占位符「搜索对话与项目」，右侧「Ctrl K」角标）
//      ③ 「项目」分组标题（项目属于远程后端，只读展示，无新增入口）
//      ④ 项目行（文件夹图标 + 项目名 + 会话数 + 「守则」按钮）
//      ⑤ 未分组行
//      ⑥ 「对话」分组标题 + 会话行（标题 + 时间 + 运行中状态点）
//      ⑦ 底部「设置」行
package com.moyunteng.lxcode.remote.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.Image
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Archive
import androidx.compose.material.icons.filled.ChatBubbleOutline
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Edit
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.QrCode2
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material3.AlertDialog
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
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import android.widget.Toast
import androidx.core.net.toUri
import androidx.compose.ui.res.painterResource
import com.moyunteng.lxcode.remote.R
import com.moyunteng.lxcode.design.component.LxButton
import com.moyunteng.lxcode.design.component.LxButtonSize
import com.moyunteng.lxcode.design.component.LxButtonVariant
import com.moyunteng.lxcode.design.component.LxIconButton
import com.moyunteng.lxcode.design.component.LxListItem
import com.moyunteng.lxcode.design.component.LxSpinner
import com.moyunteng.lxcode.design.motion.LxEnterSpec
import com.moyunteng.lxcode.design.motion.LxStagger
import com.moyunteng.lxcode.design.motion.lxEnter
import com.moyunteng.lxcode.design.motion.lxPressScale
import com.moyunteng.lxcode.design.motion.lxStaggerEnter
import com.moyunteng.lxcode.design.component.LxSectionHeader
import com.moyunteng.lxcode.design.component.LxStatus
import com.moyunteng.lxcode.design.component.LxStatusDot
import com.moyunteng.lxcode.design.component.LxTextField
import com.moyunteng.lxcode.design.token.Lx
import com.moyunteng.lxcode.remote.model.SessionMeta

// RowsCard / RowDivider / SectionHeader 抽到 ListKit.kt 页面共用（会话页与连接页同规格）。

/** 搜索框（原型 `.search`：--soft 底、无边、圆角 12、放大镜 + placeholder --fg3；
 *  触摸端无「Ctrl K」角标——键盘提示在手机上没有意义，查询非空时保留「清除」）。
 *  2026-12 规格修正：圆角 9→12、字号 12.5→14、放大镜 15→17dp。 */
@Composable
private fun SearchField(
    value: String,
    onValueChange: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
    androidx.compose.foundation.text.BasicTextField(
        value = value,
        onValueChange = onValueChange,
        singleLine = true,
        textStyle = androidx.compose.ui.text.TextStyle(
            fontFamily = Lx.type.Sans,
            fontSize = Lx.type.Size14,
            color = Lx.colors.Fg,
        ),
        cursorBrush = androidx.compose.ui.graphics.SolidColor(Lx.colors.Fg),
        modifier = modifier,
        decorationBox = { inner ->
            Row(
                Modifier
                    .fillMaxWidth()
                    .clip(RowsCardShape)
                    .background(Lx.colors.BorderSoft)
                    .padding(horizontal = Lx.space.s12, vertical = 10.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(9.dp),
            ) {
                SmallIcon(Icons.Filled.Search, Lx.colors.FgFaint, size = 17)
                Box(Modifier.weight(1f)) {
                    if (value.isEmpty()) {
                        Text(
                            text = "搜索对话与项目",
                            style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size14),
                            color = Lx.colors.FgFaint,
                        )
                    }
                    inner()
                }
                if (value.isNotEmpty()) {
                    Text(
                        text = "清除",
                        style = Lx.type.Button.copy(fontSize = Lx.type.Size10_5),
                        color = Lx.colors.FgFaint,
                        modifier = Modifier.clickable(
                            interactionSource = remember { MutableInteractionSource() },
                            indication = null,
                        ) { onValueChange("") },
                    )
                }
            }
        },
    )
}

/** 分组标题（ListKit 的 SectionHeader；计数弱化跟在尾部动作之后）。 */
@Composable
private fun SectionHeaderWithCount(
    title: String,
    count: Int,
    topPadding: androidx.compose.ui.unit.Dp = 18.dp,
    trailing: (@Composable () -> Unit)? = null,
) {
    SectionHeader(title = title, topPadding = topPadding) {
        if (trailing != null) trailing()
        Text(
            text = "$count",
            style = Lx.type.SectionTitle.copy(fontWeight = FontWeight.Medium),
            color = Lx.colors.FgFaint,
        )
    }
}

/** 会话列表页（首页）。 */
@Composable
fun SessionsScreen(state: AppState) {
    // 安装权限引导（Compose 弹窗模式对齐 LxConfirmDialog 用法）+ 启动更新确认弹窗。
    // 两个弹窗挂在页面层而非 AppRoot：横幅与「安装」按钮也在这里，弹窗消费的状态
    // （showInstallPermissionGuide / pendingUpdatePrompt）归本页编排。
    InstallPermissionGuideDialog(state)
    UpdateFoundDialog(state)
    val context = LocalContext.current
    // 断线/重连横幅的数据源（real.phase / real.reconnecting，见下方横幅块）
    val real = state.real
    // 会话行操作：行尾 ⋯ 菜单 → 重命名 / 归档（协议有 session.rename /
    // session.archive，见 PROTOCOL-NOTES.md §3）；删除 / 置顶协议没有——记缺口不造。
    var menuFor by remember { mutableStateOf<SessionMeta?>(null) }
    var renaming by remember { mutableStateOf<SessionMeta?>(null) }
    // 返回键第一层：行菜单 / 重命名弹窗 / 更新弹窗闭环态开着 → 返回先关弹层（AppRoot 的 BackHandler 链）
    LaunchedEffect(menuFor, renaming, state.updateDialogActive) {
        state.backInterceptor =
            if (menuFor != null || renaming != null || state.updateDialogActive) {
                {
                    menuFor = null
                    renaming = null
                    // 更新闭环弹窗：返回 = 收起弹窗（下载保留，横幅兜底）
                    if (state.updateDialogActive) {
                        state.updateDialogActive = false
                        state.pendingUpdatePrompt = null
                    }
                    true
                }
            } else {
                null
            }
    }
    Column(
        Modifier
            .fillMaxSize()
            .background(Lx.colors.Bg),
    ) {
        // 顶部：品牌 + 连接药丸（品牌条形态：LxCode 15.5sp/700 + 尾部齿轮→设置，
        // 与助手页/连接页同一形态；底部导航选中态不再摆「设置」行）
        Row(
            Modifier
                .fillMaxWidth()
                .background(Lx.colors.Surface)
                .padding(horizontal = Lx.space.s10, vertical = Lx.space.s6),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(Lx.space.s8),
        ) {
            Box(
                Modifier
                    .size(20.dp)
                    .clip(RoundedCornerShape(Lx.radius.r6)),
                contentAlignment = Alignment.Center,
            ) {
                // 正式 LOGO（scripts/gen-app-icons.cjs 从 shell/build/icon.png 派生），不再手绘占位「L」
                Image(
                    painter = painterResource(R.drawable.ic_logo),
                    contentDescription = null,
                    modifier = Modifier.fillMaxSize(),
                    contentScale = ContentScale.Crop,
                )
            }
            Text(
                text = "LxCode",
                style = Lx.type.ListTitle.copy(
                    fontSize = 15.5.sp,
                    fontWeight = FontWeight.Bold,
                ),
                color = Lx.colors.Fg,
            )
            Box(
                Modifier
                    .size(width = 1.dp, height = 14.dp)
                    .background(Lx.colors.Border),
            )
            ConnPill(
                online = state.pillOnline(),
                name = state.pillName(),
                onClick = { state.route = Route.Connections },
            )
            Box(Modifier.weight(1f))
            // 齿轮 → 设置页（2026-12 拍板：替代列表底部「设置」行——入口上浮到顶栏）
            LxIconButton(onClick = { state.route = Route.Settings }) {
                Icon(
                    imageVector = Icons.Filled.Settings,
                    contentDescription = "设置",
                    tint = Lx.colors.FgMuted,
                    modifier = Modifier.size(18.dp),
                )
            }
        }

        // 未连接任何后端：连接引导态——居中引导（logo + 文案 +
        // 扫码/手动添加入口 → 连接页）。连接成功（realOn=true）才渲染会话列表。
        if (!state.realOn) {
            ConnectGuide(
                onScan = { state.route = Route.Pairing },
                onManual = { state.route = Route.Connections },
                modifier = Modifier.weight(1f),
            )
            return@Column
        }

        // 断线/重连横幅（会话列表页就地表达，与会话页同一只横幅）：断线时后台
        // 一直在自动重连，不表达它用户会以为必须手动去连接页重连。
        if (real.phase == com.moyunteng.lxcode.remote.net.RealPhase.Failed || real.reconnecting) {
            Box(Modifier.padding(horizontal = Lx.space.s8)) {
                BackendDownBar(
                    down = real.phase == com.moyunteng.lxcode.remote.net.RealPhase.Failed,
                    reconnecting = real.reconnecting,
                    onRetry = { real.retry(state.realAddr, state.realToken) },
                    message = real.phaseMessage ?: "后端已断开：请检查电脑端 lxcode 是否在运行",
                    pairingInvalid = real.pairingInvalid,
                    onRepair = { state.route = Route.Pairing },
                )
            }
        }

        // 搜索框固定顶栏下（2026-12 规格修正：移出 LazyColumn——滚动列表时搜索框
        // 不随内容滚走，与原型固定筛选区一致）
        Box(
            Modifier
                .lxEnter(spec = LxEnterSpec.StaggerItem)
                .padding(horizontal = Lx.space.s12, vertical = Lx.space.s6),
        ) {
            SearchField(
                value = state.query,
                onValueChange = { state.query = it },
            )
        }

        LazyColumn(
            Modifier
                .fillMaxSize(),
        ) {
            // ⓪ 更新横幅（启动自动检查发现新版本时出现；非阻断，可关）：
            // 新版本号 + 大小 + 「下载并安装」（应用内 DownloadManager → sha256 校验 →
            // 系统安装器）/「×」忽略；下载中显示进度，失败显示人话原因 + 重试。
            state.updateAvailable?.let { release ->
                item(key = "update-banner") {
                    UpdateBanner(
                        release = release,
                        download = state.downloadState,
                        installHint = state.installHint,
                        onDismiss = {
                            state.updateAvailable = null
                            state.downloadState = null
                            state.installHint = null
                            state.updater.cancel()
                        },
                        onDownload = {
                            state.installHint = null
                            state.updater.enqueue(release)
                        },
                        onRetry = {
                            state.downloadState = null
                            state.installHint = null
                            state.updater.enqueue(release)
                        },
                        onInstall = {
                            (state.downloadState as? com.moyunteng.lxcode.remote.update.DownloadState.Ready)
                                ?.let {
                                    // 安装编排的唯一路径（AppRoot 不再自动 installVerified——
                                    // 曾经两条路径都会拉安装器，重复弹且冷启动不打招呼）。
                                    // 权限预检先行：Android 8+ 未授权「安装未知应用」时
                                    // 不发 ACTION_VIEW（系统安装器只会弹晦涩的拦截框），
                                    // 改弹我们自己的引导对话框去系统设置授权页。
                                    if (!state.updater.canRequestInstall()) {
                                        state.showInstallPermissionGuide = true
                                        return@let
                                    }
                                    // installVerified 内部先校验后安装；校验失败时 onState
                                    // 已置 Failed 人话态；拉起失败给安装器指引。
                                    if (!state.updater.installVerified(it, release)) {
                                        if (state.downloadState !is com.moyunteng.lxcode.remote.update.DownloadState.Failed) {
                                            state.installHint = state.updater.installerLaunchHint(it.file.name)
                                        }
                                    } else {
                                        state.installHint = null
                                    }
                                }
                        },
                        onCancel = {
                            state.downloadState = null
                            state.installHint = null
                            state.updater.cancel()
                        },
                        modifier = Modifier.padding(horizontal = Lx.space.s4, vertical = Lx.space.s6),
                    )
                }
            }

            // （导航项已并入分组标题尾插槽：2026-12 规格修正——「新对话」迁到
            // 「对话」SectionHeader 的尾插槽，替代独立 NavRow 行；助手入口 = 底部导航。）

            // ③ 「项目」分组标题（带计数；只读：项目属于远程后端，手机端不提供新增入口）
            item {
                SectionHeaderWithCount(
                    title = "项目",
                    count = state.real.projects.size + 1, // + 未分组
                )
            }

            // ④ 项目行 + 未分组（rows-card 组卡：行间 1px 分隔线）
            // 项目来自 project.list（真数据）；会话按 workspace（= 项目 id）归组，
            // 各组计数 = 该项目 id 下的会话数（对齐桌面端 Sidebar 的 count 口径）；
            // 无归属（workspace 空）进「未分组」。
            item {
                RowsCard {
                    state.real.projects.forEachIndexed { i, project ->
                        if (i > 0) RowDivider()
                        ProjectRow(
                            modifier = Modifier.lxStaggerEnter(count = state.real.projects.size + 1, index = i),
                            name = project.name,
                            count = state.realProjectSessionCount(project.id),
                            runningCount = state.realProjectRunningCount(project.id),
                            active = state.scope == project.id,
                            onClick = { state.scope = project.id },
                        )
                    }
                    if (state.real.projects.isNotEmpty()) RowDivider()
                    ProjectRow(
                        modifier = Modifier.lxEnter(
                            spec = LxEnterSpec.StaggerItem,
                            delayMillis = LxStagger.delayMillis(state.real.projects.size + 1, state.real.projects.size),
                        ),
                        name = "未分组",
                        count = state.realLooseSessionCount(),
                        runningCount = state.realLooseRunningCount(),
                        active = state.scope.isEmpty(),
                        // 弱化形态（原型未分组行：plus 头像 + fg2 标题 + opacity .6）
                        muted = true,
                        onClick = { state.scope = "" },
                    )
                }
            }

            // ⑤ 「对话」分组标题（带计数；尾插槽 = 「新对话」——2026-12 迁回此处）
            item {
                val newChatInteraction = remember { MutableInteractionSource() }
                SectionHeaderWithCount(
                    "对话",
                    state.realSessionsInScope().size,
                    trailing = {
                        Row(
                            Modifier
                                .lxPressScale(newChatInteraction)
                                .clip(Lx.radius.PillShape)
                                .clickable(
                                    interactionSource = newChatInteraction,
                                    indication = null,
                                    // 项目组内新建：scope 是当前范围的项目 id（未分组 = ""）——
                                    // session.new 的 workspace 参数，会话归属该项目（对齐桌面端
                                    // Sidebar 的 filter 语义：在哪个范围里点的新对话就归哪个项目）
                                ) {
                                    state.real.newSession(
                                        workspace = state.scope,
                                        onReady = { id -> state.route = Route.Thread(id) },
                                    )
                                }
                                .padding(horizontal = Lx.space.s8, vertical = Lx.space.s4),
                            verticalAlignment = Alignment.CenterVertically,
                            horizontalArrangement = Arrangement.spacedBy(Lx.space.s4),
                        ) {
                            SmallIcon(Icons.Filled.Add, Lx.colors.Accent, size = 13)
                            Text(
                                text = "新对话",
                                style = Lx.type.Button.copy(fontSize = Lx.type.Size11_5, fontWeight = FontWeight.SemiBold),
                                color = Lx.colors.Accent,
                            )
                        }
                    },
                )
            }

            // ⑥ 会话行（rows-card 组卡；搜索过滤）：列表来自 session.list（真数据）
            val sessions = state.realSessionsInScope().filter {
                state.query.trim().isEmpty() ||
                    it.title.contains(state.query.trim(), ignoreCase = true)
            }
            if (sessions.isEmpty()) {
                item {
                    // 空态（原型 `.empty` 形态：图标框 + 标题 + 副行）
                    SmallEmpty(
                        text = "还没有对话",
                        icon = Icons.Filled.ChatBubbleOutline,
                        title = if (state.query.trim().isEmpty()) "还没有对话" else "没有匹配的对话",
                        description = if (state.query.trim().isEmpty()) {
                            "点上方「新对话」开始"
                        } else {
                            "试试换一个关键词"
                        },
                    )
                }
            } else {
                item {
                    RowsCard {
                        sessions.forEachIndexed { i, s ->
                            if (i > 0) RowDivider()
                            Box {
                                SessionRowItem(
                                    s,
                                    Modifier.lxStaggerEnter(count = sessions.size, index = i),
                                    // 状态点 = 快照位 **或** 实时 busy 投影（并集）：
                                    // 投影是 Compose 可观察的 state map，电脑端在这条会话里
                                    // 开跑时手机上立刻亮，不必点进去（chat.history）才补上。
                                    running = s.running || state.real.isSessionRunning(s.id),
                                    onClick = { state.route = Route.Thread(s.id) },
                                    onMenu = { menuFor = s },
                                )
                                // 行尾 ⋯ 菜单（对齐桌面端 SessionRow.tsx 的 hover 菜单；触摸端改为点按弹出）
                                AnchorMenu(expanded = menuFor?.id == s.id, onDismiss = { menuFor = null }) {
                                    SessionOpsMenu(
                                        onRename = {
                                            menuFor = null
                                            renaming = s
                                        },
                                        onArchive = {
                                            menuFor = null
                                            state.real.archiveSession(s.id)
                                        },
                                    )
                                }
                            }
                        }
                    }
                }
            }

            // （底部「设置」行已摘除——入口上浮到顶栏齿轮，2026-12 拍板）
            item { Box(Modifier.height(18.dp)) }
        }
    }

    // 重命名弹窗（session.rename；真实模式生效并刷新列表，未连接时如实提示）
    renaming?.let { target ->
        var draft by remember(target.id) { mutableStateOf(target.title) }
        AlertDialog(
            onDismissRequest = { renaming = null },
            containerColor = Lx.colors.Surface,
            title = {
                Text(
                    text = "重命名会话",
                    style = Lx.type.ListTitle.copy(fontWeight = FontWeight.SemiBold),
                    color = Lx.colors.Fg,
                )
            },
            text = {
                LxTextField(
                    value = draft,
                    onValueChange = { draft = it },
                    placeholder = "会话标题",
                )
            },
            confirmButton = {
                LxButton(
                    text = "保存",
                    onClick = {
                        val title = draft.trim()
                        if (title.isNotEmpty()) {
                            state.real.renameSession(target.id, title)
                        }
                        renaming = null
                    },
                    variant = LxButtonVariant.Primary,
                    size = LxButtonSize.Medium,
                )
            },
            dismissButton = {
                LxButton(
                    text = "取消",
                    onClick = { renaming = null },
                    variant = LxButtonVariant.Secondary,
                    size = LxButtonSize.Medium,
                )
            },
        )
    }
}

internal data class NavEntry(val label: String, val icon: ImageVector, val onTap: (() -> Unit)?)

/** 导航项一行（图标 + 文字；对齐 .nav-item）。 */
@Composable
internal fun NavRow(entry: NavEntry, modifier: Modifier = Modifier) {
    LxListItem(
        title = entry.label,
        modifier = modifier,
        leading = { SmallIcon(entry.icon, Lx.colors.FgMuted, size = 15) },
        onClick = entry.onTap,
    )
}

/** 项目行（36dp 字母 pavatar + 名字 + 副标题会话数 + 「守则」药丸 chip；对齐 .proj-row）。
 *  [runningCount] > 0 时标题旁亮运行中 spinner + 计数（对齐桌面端侧栏项目行的
 *  spinner——项目内任一会话在生成就亮，多端共享进度）。
 *  2026-12 规格修正：首列从 14dp 文件夹图标升级为 36dp 字母头像（取项目名首字符、
 *  accent-soft 底）；副标题 = 「N 条会话」（project.list 协议不带分支字段——
 *  副标题只用会话数，不编造分支信息）；「守则」从裸文字升级为 chip 药丸。 */
@Composable
private fun ProjectRow(
    name: String,
    count: Int,
    runningCount: Int,
    active: Boolean,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    // 弱化形态（原型未分组行：头像 opacity .6 + 内放 plus 图标、标题 --fg2——
    // 「未分组」不是真项目，视觉退半档；2026-12 扫尾条目 31）
    muted: Boolean = false,
) {
    val interaction = remember { MutableInteractionSource() }
    Row(
        modifier
            .fillMaxWidth()
            .clip(Lx.radius.RowShape)
            .background(if (active) Lx.colors.BorderSoft else androidx.compose.ui.graphics.Color.Transparent)

            .clickable(interactionSource = interaction, indication = null, onClick = onClick)
            // 原型 `.row`：padding 11px 12px
            .padding(horizontal = Lx.space.s12, vertical = 11.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(Lx.space.s10),
    ) {
        // 36dp 字母 pavatar（.pavatar：radius 11 + 1px --line 边框 + accent-soft 底 + 首字符）
        Box(
            Modifier
                .size(36.dp)
                .alpha(if (muted) 0.6f else 1f)
                .clip(ProjectAvatarShape)
                .background(Lx.colors.AccentSoft)
                .border(1.dp, Lx.colors.Border, ProjectAvatarShape),
            contentAlignment = Alignment.Center,
        ) {
            if (muted) {
                // 弱化形态：头像位放 plus 图标（原型未分组行的 `<svg #i-plus>`）
                Icon(
                    imageVector = Icons.Filled.Add,
                    contentDescription = null,
                    tint = Lx.colors.FgMuted,
                    modifier = Modifier.size(16.dp),
                )
            } else {
                Text(
                    text = name.firstOrNull()?.toString() ?: "·",
                    style = Lx.type.ListTitle.copy(fontWeight = FontWeight.Bold, fontSize = Lx.type.Size14),
                    color = Lx.colors.Accent,
                )
            }
        }
        Column(Modifier.weight(1f)) {
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(Lx.space.s6),
            ) {
                Text(
                    text = name,
                    // 原型 `.row .t1`：14px / 600；弱化形态标题 --fg2（--fg-muted）
                    style = Lx.type.ListTitle.copy(
                        fontSize = Lx.type.Size14,
                        fontWeight = FontWeight.SemiBold,
                    ),
                    color = if (muted) Lx.colors.FgMuted else Lx.colors.Fg,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                if (runningCount > 0) {
                    LxSpinner(size = 10.dp)
                }
            }
            Text(
                text = "$count 条会话",
                // 原型 `.row .t2`：12.5px / --fg2
                style = Lx.type.ListSubtitle.copy(fontSize = Lx.type.Size12_5),
                color = Lx.colors.FgMuted,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(Lx.space.s8),
        ) {
            if (runningCount > 0) {
                Text(
                    text = "$runningCount 运行中",
                    style = Lx.type.Badge.copy(fontSize = Lx.type.Size10_5),
                    color = Lx.colors.Amber,
                )
            }
            // 「守则」chip 药丸（原型 .chip：边框药丸 + 10.5sp）
            Text(
                text = "守则",
                style = Lx.type.Button.copy(fontSize = Lx.type.Size10_5, fontWeight = FontWeight.SemiBold),
                color = Lx.colors.FgMuted,
                modifier = Modifier
                    .clip(Lx.radius.PillShape)
                    .border(1.dp, Lx.colors.BorderStrong, Lx.radius.PillShape)
                    .padding(horizontal = Lx.space.s8, vertical = 2.dp),
            )
            Text(
                text = "$count",
                style = Lx.type.Badge,
                color = Lx.colors.FgFaint,
            )
        }
    }
}

/** 会话行（标题 + 副标题 N 条消息 + 行尾时间/状态点/⋯；对齐 .session-item）。
 *  2026-12 规格修正：加副标题 .t2（messages 计数派生「N 条消息」）；状态点从
 *  行首移到行尾时间旁（运行中才亮，空闲不占位）。 */
@Composable
private fun SessionRowItem(
    session: SessionMeta,
    modifier: Modifier = Modifier,
    running: Boolean = session.running,
    onClick: () -> Unit,
    onMenu: () -> Unit,
) {
    val interaction = remember { MutableInteractionSource() }
    Row(
        modifier
            .fillMaxWidth()
            .clip(Lx.radius.RowShape)
            .clickable(interactionSource = interaction, indication = null, onClick = onClick)
            // 原型 `.row`：padding 11px 12px（2026-12 规格修正——原 8/10 偏挤）
            .padding(horizontal = Lx.space.s12, vertical = 11.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(Lx.space.s10),
    ) {
        Column(Modifier.weight(1f)) {
            Text(
                text = session.title,
                // 原型 `.row .t1`：14px / 600
                style = Lx.type.ListTitle.copy(
                    fontSize = Lx.type.Size14,
                    fontWeight = FontWeight.SemiBold,
                ),
                color = Lx.colors.Fg,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                text = "${session.messages} 条消息",
                // 原型 `.row .t2`：12.5px / --fg2
                style = Lx.type.ListSubtitle.copy(fontSize = Lx.type.Size12_5),
                color = Lx.colors.FgMuted,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(Lx.space.s6),
        ) {
            // 状态点移到行尾（时间旁）：运行中 = 琥珀呼吸点，空闲不渲染——
            // 行首常驻灰点改为按需出现在时间旁边（红色只给失败态，这里没有失败语义）
            if (running) {
                LxStatusDot(status = LxStatus.Run, size = 7.dp)
            }
            Text(
                text = session.updatedAt,
                style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size10_5),
                color = Lx.colors.FgFaint,
            )
            // 行尾操作入口（桌面端 SessionRow 的 hover 菜单 → 触摸端点按弹出）
            Box(
                Modifier
                    .clip(RoundedCornerShape(Lx.radius.r6))
                    .clickable(
                        interactionSource = remember { MutableInteractionSource() },
                        indication = null,
                        onClick = onMenu,
                    )
                    .padding(2.dp),
            ) {
                SmallIcon(Icons.Filled.MoreVert, Lx.colors.FgFaint, size = 12)
            }
        }
    }
}

/** 行操作菜单 Shape —— 对齐 PickerMenus 的 .mp-panel（圆角 12、1px 边、菜单级阴影）。 */
private val SessionOpsShape = RoundedCornerShape(12.dp)

/** 项目头像 Shape（原型 `.pavatar`：`border-radius: 11px`）。 */
private val ProjectAvatarShape = RoundedCornerShape(11.dp)

/** 会话行操作菜单（重命名 / 归档；删除与置顶协议没有——缺口记 PROTOCOL-NOTES.md，不造）。 */
@Composable
private fun SessionOpsMenu(onRename: () -> Unit, onArchive: () -> Unit) {
    Column(
        Modifier
            .width(150.dp)
            .shadow(
                elevation = 8.dp,
                shape = SessionOpsShape,
                clip = false,
                ambientColor = Lx.shadow.Float.copy(alpha = 0.08f),
                spotColor = Lx.shadow.Float.copy(alpha = 0.12f),
            )
            .clip(SessionOpsShape)
            .background(Lx.colors.Surface)
            .border(1.dp, Lx.colors.BorderStrong, SessionOpsShape)
            .padding(5.dp),
        verticalArrangement = Arrangement.spacedBy(1.dp),
    ) {
        SessionOpsRow("重命名", Icons.Filled.Edit, onRename)
        SessionOpsRow("归档", Icons.Filled.Archive, onArchive)
    }
}

/** 菜单条目（图标 + 文字；对齐 .mp-item：padding 8/10、圆角 8、12sp）。 */
@Composable
private fun SessionOpsRow(label: String, icon: ImageVector, onClick: () -> Unit) {
    Row(
        Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(8.dp))
            .clickable(
                interactionSource = remember { MutableInteractionSource() },
                indication = null,
                onClick = onClick,
            )
            .padding(horizontal = 10.dp, vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(Lx.space.s8),
    ) {
        SmallIcon(icon, Lx.colors.FgMuted, size = 13)
        Text(
            text = label,
            style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size12_5),
            color = Lx.colors.Fg,
        )
    }
}

/**
 * 更新横幅 —— 应用内下载替换（2026-12 用户拍板，替代旧的「去下载」跳浏览器）。
 *
 * 四态：空闲（「下载并安装」）/ Running（进度条 + 已下载/总量）/ Ready（「安装」，
 * installVerified 单入口：sha256 校验通过后拉起安装器——这行是手动兜底）/
 * Failed（人话原因 + 重试）。[installHint] 非空 = 拉起安装器失败——指引重新下载
 * （APK 落 app 私有目录，文件管理器看不到，旧的「去目录手动装」不再可行）。
 */
@Composable
private fun UpdateBanner(
    release: com.moyunteng.lxcode.remote.update.ApkRelease,
    download: com.moyunteng.lxcode.remote.update.DownloadState?,
    installHint: String?,
    onDismiss: () -> Unit,
    onDownload: () -> Unit,
    onRetry: () -> Unit,
    onInstall: () -> Unit,
    onCancel: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Row(
        modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(11.dp))
            .background(Lx.colors.Surface)
            .border(1.dp, Lx.colors.Border, RoundedCornerShape(11.dp))
            .padding(horizontal = Lx.space.s10, vertical = Lx.space.s8),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(Lx.space.s8),
    ) {
        Column(Modifier.weight(1f)) {
            Text(
                text = "发现新版本 v${release.version}",
                style = Lx.type.ListTitle,
                color = Lx.colors.Fg,
            )
            val sizeText = com.moyunteng.lxcode.remote.update.formatBytes(release.size)
            val sub = when {
                // 安装器拉起失败的手动指引优先展示（Ready 态但自动打开被系统拦了）
                installHint != null -> installHint
                else -> when (val d = download) {
                    is com.moyunteng.lxcode.remote.update.DownloadState.Running -> {
                        val pct = if (d.total > 0) " · ${(d.downloaded * 100 / d.total)}%" else ""
                        "$sizeText · 下载中 $pct"
                    }
                    is com.moyunteng.lxcode.remote.update.DownloadState.Ready -> "$sizeText · 校验通过，可以安装"
                    is com.moyunteng.lxcode.remote.update.DownloadState.Failed -> d.message
                    is com.moyunteng.lxcode.remote.update.DownloadState.Idle ->
                        "$sizeText · ${release.notes.firstOrNull() ?: "新的 APK 已发布"}"
                    null -> "$sizeText · ${release.notes.firstOrNull() ?: "新的 APK 已发布"}"
                }
            }
            Text(
                text = sub,
                style = Lx.type.ListSubtitle,
                color = if (download is com.moyunteng.lxcode.remote.update.DownloadState.Failed || installHint != null) {
                    Lx.colors.Danger
                } else {
                    Lx.colors.FgFaint
                },
                maxLines = 3,
                overflow = TextOverflow.Ellipsis,
            )
            // 进度条（Running 态；total 未知时不画——不编进度）
            if (download is com.moyunteng.lxcode.remote.update.DownloadState.Running && download.total > 0) {
                Box(
                    Modifier
                        .padding(top = Lx.space.s6)
                        .fillMaxWidth()
                        .height(3.dp)
                        .clip(RoundedCornerShape(2.dp))
                        .background(Lx.colors.BorderSoft),
                ) {
                    Box(
                        Modifier
                            .fillMaxWidth(download.downloaded.toFloat() / download.total)
                            .fillMaxHeight()
                            .background(Lx.colors.Success),
                    )
                }
            }
        }
        // 动作位：空闲=下载并安装 / Running=取消 / Ready=安装 / Failed(可重试)=重试
        val actionText = when (download) {
            is com.moyunteng.lxcode.remote.update.DownloadState.Running -> "取消"
            is com.moyunteng.lxcode.remote.update.DownloadState.Ready -> "安装"
            is com.moyunteng.lxcode.remote.update.DownloadState.Failed -> if (download.retryable) "重试" else ""
            is com.moyunteng.lxcode.remote.update.DownloadState.Idle -> "下载并安装"
            null -> "下载并安装"
        }
        if (actionText.isNotEmpty()) {
            Text(
                text = actionText,
                style = Lx.type.ListTitle.copy(fontSize = Lx.type.Size12_5),
                color = Lx.colors.Fg,
                modifier = Modifier
                    .clip(Lx.radius.RowShape)
                    .clickable(
                        interactionSource = remember { MutableInteractionSource() },
                        indication = null,
                    ) {
                        when (download) {
                            is com.moyunteng.lxcode.remote.update.DownloadState.Running -> onCancel()
                            is com.moyunteng.lxcode.remote.update.DownloadState.Ready -> onInstall()
                            null -> onDownload()
                            is com.moyunteng.lxcode.remote.update.DownloadState.Idle -> onDownload()
                            is com.moyunteng.lxcode.remote.update.DownloadState.Failed ->
                                if (download.retryable) onRetry()
                        }
                    }
                    .padding(horizontal = Lx.space.s8, vertical = Lx.space.s6),
            )
        }
        Box(
            Modifier
                .clip(Lx.radius.RowShape)
                .clickable(
                    interactionSource = remember { MutableInteractionSource() },
                    indication = null,
                    onClick = onDismiss,
                )
                .padding(Lx.space.s4),
        ) {
            SmallIcon(Icons.Filled.Close, Lx.colors.FgFaint, size = 14)
        }
    }
}

/** 横幅「取消」：调 AppRoot 传入的取消路径（经 onDismiss 携带 cancel；这里只停下载）。 */

/**
 * 「安装未知应用」权限引导对话框：Android 8+ 未授权时点「安装」弹出——
 * 不让系统安装器弹晦涩的「lxcode 没有权限安装此应用」拦截框，改由我们给出
 * 人话说明 + 直达系统设置里 lxcode 自己的授权页（ApkDownloader.openInstall
 * PermissionSettings）。MIUI「纯净模式」无法程序检测，文案里带一句提示。
 * 批准 = 跳设置页；拒绝/点遮罩 = 留在 Ready 态（授权回来再点一次「安装」）。
 */
@Composable
private fun InstallPermissionGuideDialog(state: AppState) {
    if (!state.showInstallPermissionGuide) return
    val context = LocalContext.current
    com.moyunteng.lxcode.design.component.LxConfirmDialog(
        title = "需要「安装未知应用」权限",
        text = com.moyunteng.lxcode.remote.update.ApkDownloader.installPermissionGuideMessage(),
        approveText = "去设置开启",
        denyText = "暂不",
        onApprove = {
            state.showInstallPermissionGuide = false
            // 直达 lxcode 的授权页；机型不支持该入口时退化为打开应用详情设置
            if (!state.updater.openInstallPermissionSettings()) {
                try {
                    context.startActivity(
                        android.content.Intent(
                            android.provider.Settings.ACTION_APPLICATION_DETAILS_SETTINGS,
                            "package:${context.packageName}".toUri(),
                        ).addFlags(android.content.Intent.FLAG_ACTIVITY_NEW_TASK),
                    )
                } catch (e: Exception) {
                    Toast.makeText(context, "无法打开系统设置，请手动前往", Toast.LENGTH_SHORT).show()
                }
            }
        },
        onDeny = { state.showInstallPermissionGuide = false },
        onDismissRequest = { state.showInstallPermissionGuide = false },
    )
}

/**
 * 启动自动检查发现新版本的弹窗（与连接页「检查更新」同一个弹窗形态，
 * 2026-12 用户拍板统一）：含版本/大小/notes，批准 = 应用内下载。
 *
 * **弹窗内闭环**（2026-12 用户拍板，替代「批准即关 → 横幅接管」）：确认下载后
 * 弹窗不关，直接切换为进度/就绪/失败态（数据源 = 全局 downloadState 投影，
 * 与横幅共用同一份，两个入口表现一致）：
 *  * Running → 进度条 + 百分比 + 已下载/总量（total 未知转圈，不编进度）；
 *  * Ready → 「安装」主按钮，走与横幅同一条 installVerified/权限预检编排；
 *  * Failed → 人话原因，retryable 时给「重试」。
 * 关闭弹窗（点遮罩/返回/以后再说）不取消下载——置 updateDialogActive=false，
 * 横幅（updateAvailable 已置上）继续兜底。安装完成（Idle）即收口关弹窗。
 */
@Composable
private fun UpdateFoundDialog(state: AppState) {
    val release = state.pendingUpdatePrompt ?: return
    if (!state.updateDialogActive) {
        // 发现态：确认弹窗原样
        com.moyunteng.lxcode.design.component.LxConfirmDialog(
            title = "发现新版本 v${release.version}",
            text = buildString {
                append("当前 v${com.moyunteng.lxcode.remote.BuildConfig.VERSION_NAME} · ")
                append(com.moyunteng.lxcode.remote.update.formatBytes(release.size))
                if (release.notes.isNotEmpty()) {
                    append("\n")
                    append(release.notes.joinToString("\n"))
                }
            },
            approveText = "下载并安装",
            denyText = "以后再说",
            onApprove = {
                state.updateAvailable = release
                state.installHint = null
                // 弹窗不关：切进度态（横幅同步出现，作为兜底入口）
                state.updateDialogActive = true
                state.updater.enqueue(release)
            },
            onDeny = { state.pendingUpdatePrompt = null },
            onDismissRequest = { state.pendingUpdatePrompt = null },
        )
        return
    }
    // 闭环态：进度 / 就绪 / 失败（Running 起不可点遮罩关——必须显式选，防误触
    // 把进行中的下载「关没」；用户仍可从横幅取消）
    val download = state.downloadState
    UpdateDialogProgress(
        release = release,
        download = download,
        installHint = state.installHint,
        onInstall = {
            (download as? com.moyunteng.lxcode.remote.update.DownloadState.Ready)?.let {
                // 与 SessionsScreen 横幅「安装」同一条编排：权限预检 → installVerified
                if (!state.updater.canRequestInstall()) {
                    state.showInstallPermissionGuide = true
                    return@let
                }
                if (!state.updater.installVerified(it, release)) {
                    if (state.downloadState !is com.moyunteng.lxcode.remote.update.DownloadState.Failed) {
                        state.installHint = state.updater.installerLaunchHint(it.file.name)
                    }
                } else {
                    state.installHint = null
                }
            }
        },
        onRetry = {
            state.downloadState = null
            state.installHint = null
            state.updater.enqueue(release)
        },
        onCancel = {
            state.downloadState = null
            state.installHint = null
            state.updateDialogActive = false
            state.pendingUpdatePrompt = null
            state.updater.cancel()
        },
        onClose = {
            // 只收起弹窗：下载/就绪任务保留，横幅兜底
            state.updateDialogActive = false
            state.pendingUpdatePrompt = null
        },
    )
    // 安装完成（onInstalled → Idle）：收口——弹窗与横幅一起关
    androidx.compose.runtime.LaunchedEffect(download) {
        if (download is com.moyunteng.lxcode.remote.update.DownloadState.Idle) {
            state.downloadState = null
            state.updateAvailable = null
            state.installHint = null
            state.updateDialogActive = false
            state.pendingUpdatePrompt = null
        }
    }
}

/**
 * 更新弹窗的闭环态卡（进度/就绪/失败）：不复用 LxConfirmDialog——它只有批准/
 * 拒绝两按钮，闭环需要随态变化的主动作 + 常驻关闭位，故用同款视觉手绘一卡。
 */
@Composable
private fun UpdateDialogProgress(
    release: com.moyunteng.lxcode.remote.update.ApkRelease,
    download: com.moyunteng.lxcode.remote.update.DownloadState?,
    installHint: String?,
    onInstall: () -> Unit,
    onRetry: () -> Unit,
    onCancel: () -> Unit,
    onClose: () -> Unit,
) {
    androidx.compose.ui.window.Dialog(
        onDismissRequest = onClose,
        properties = androidx.compose.ui.window.DialogProperties(usePlatformDefaultWidth = false),
    ) {
        // 平台默认调光关掉，自绘 0.22 遮罩（对齐 LxConfirmDialog 的遮罩语义）
        val dialogView = androidx.compose.ui.platform.LocalView.current
        androidx.compose.runtime.LaunchedEffect(dialogView) {
            (dialogView.parent as? androidx.compose.ui.window.DialogWindowProvider)
                ?.window?.setDimAmount(0f)
        }
        Box(
            Modifier
                .fillMaxSize()
                .background(androidx.compose.ui.graphics.Color.Black.copy(alpha = 0.22f))
                .clickable(
                    interactionSource = remember { MutableInteractionSource() },
                    indication = null,
                    onClick = onClose,
                ),
            contentAlignment = Alignment.Center,
        ) {
            Column(
                Modifier
                    .padding(horizontal = Lx.space.s24)
                    .widthIn(max = 420.dp)
                    .fillMaxWidth()
                    .shadow(2.dp, RoundedCornerShape(Lx.radius.r10 + 2.dp))
                    .clip(RoundedCornerShape(Lx.radius.r10 + 2.dp))
                    .background(Lx.colors.Surface)
                    .border(1.dp, Lx.colors.Border, RoundedCornerShape(Lx.radius.r10 + 2.dp))
                    .clickable(
                        interactionSource = remember { MutableInteractionSource() },
                        indication = null,
                        onClick = { /* 吃掉点击，不透给遮罩 */ },
                    )
                    .padding(Lx.space.s12),
                verticalArrangement = Arrangement.spacedBy(Lx.space.s10),
            ) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(
                        text = "正在更新 · v${release.version}",
                        style = Lx.type.ListTitle.copy(fontWeight = FontWeight.SemiBold),
                        color = Lx.colors.Fg,
                        modifier = Modifier.weight(1f),
                    )
                    Box(
                        Modifier
                            .clip(Lx.radius.RowShape)
                            .clickable(
                                interactionSource = remember { MutableInteractionSource() },
                                indication = null,
                                onClick = onClose,
                            )
                            .padding(Lx.space.s4),
                    ) {
                        SmallIcon(Icons.Filled.Close, Lx.colors.FgFaint, size = 14)
                    }
                }
                val sizeText = com.moyunteng.lxcode.remote.update.formatBytes(release.size)
                val failed = download as? com.moyunteng.lxcode.remote.update.DownloadState.Failed
                Text(
                    text = when {
                        installHint != null -> installHint
                        failed != null -> failed.message
                        download is com.moyunteng.lxcode.remote.update.DownloadState.Ready ->
                            "校验通过，可以安装"
                        download is com.moyunteng.lxcode.remote.update.DownloadState.Running ->
                            download.total.let { total ->
                                if (total > 0) {
                                    "已下载 ${com.moyunteng.lxcode.remote.update.formatBytes(download.downloaded)} / $sizeText · ${download.downloaded * 100 / total}%"
                                } else {
                                    "已下载 ${com.moyunteng.lxcode.remote.update.formatBytes(download.downloaded)} · 总大小未知"
                                }
                            }
                        else -> sizeText
                    },
                    style = Lx.type.ListSubtitle,
                    color = if (failed != null || installHint != null) Lx.colors.Danger else Lx.colors.FgFaint,
                    maxLines = 3,
                    overflow = TextOverflow.Ellipsis,
                )
                // 进度条（Running 且 total 已知才画——不编进度；total 未知转圈）
                when (val d = download) {
                    is com.moyunteng.lxcode.remote.update.DownloadState.Running -> {
                        if (d.total > 0) {
                            Box(
                                Modifier
                                    .fillMaxWidth()
                                    .height(4.dp)
                                    .clip(RoundedCornerShape(2.dp))
                                    .background(Lx.colors.BorderSoft),
                            ) {
                                Box(
                                    Modifier
                                        .fillMaxWidth(d.downloaded.toFloat() / d.total)
                                        .fillMaxHeight()
                                        .background(Lx.colors.Success),
                                )
                            }
                        } else {
                            LxSpinner()
                        }
                    }
                    else -> Unit
                }
                // 动作行：Running=取消 / Ready=安装 / Failed(可重试)=重试
                Row(
                    Modifier.fillMaxWidth(),
                    horizontalArrangement = Arrangement.spacedBy(Lx.space.s6, Alignment.End),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    when (val d = download) {
                        is com.moyunteng.lxcode.remote.update.DownloadState.Running -> {
                            LxButton(text = "取消并关闭", onClick = onCancel, variant = LxButtonVariant.Ghost, size = LxButtonSize.Small)
                        }
                        is com.moyunteng.lxcode.remote.update.DownloadState.Ready -> {
                            LxButton(text = "安装", onClick = onInstall, variant = LxButtonVariant.Primary, size = LxButtonSize.Small)
                        }
                        is com.moyunteng.lxcode.remote.update.DownloadState.Failed -> {
                            if (d.retryable) {
                                LxButton(text = "重试", onClick = onRetry, variant = LxButtonVariant.Primary, size = LxButtonSize.Small)
                            } else {
                                LxButton(text = "关闭", onClick = onClose, variant = LxButtonVariant.Ghost, size = LxButtonSize.Small)
                            }
                        }
                        else -> Unit
                    }
                }
            }
        }
    }
}

/**
 * 连接引导态（未连接任何后端时替代会话列表）——居中：logo + 「连接到你的 lxcode 后端」
 * + 说明行 + 「扫码配对」（主按钮）/「手动添加」（次按钮）。不渲染任何编造的会话数据。
 */
@Composable
private fun ConnectGuide(
    onScan: () -> Unit,
    onManual: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier
            .fillMaxWidth()
            .padding(horizontal = Lx.space.s12),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
    ) {
        Box(
            Modifier
                .size(56.dp)
                .clip(RoundedCornerShape(Lx.radius.r10 + 2.dp)),
            contentAlignment = Alignment.Center,
        ) {
            // 正式 LOGO（scripts/gen-app-icons.cjs 从 shell/build/icon.png 派生），不再手绘占位「L」
            Image(
                painter = painterResource(R.drawable.ic_logo),
                contentDescription = null,
                modifier = Modifier.fillMaxSize(),
                contentScale = ContentScale.Crop,
            )
        }
        Box(Modifier.height(Lx.space.s10))
        Text(
            text = "连接到你的 lxcode 后端",
            style = Lx.type.ListTitle.copy(fontWeight = FontWeight.SemiBold),
            color = Lx.colors.Fg,
        )
        Box(Modifier.height(Lx.space.s4))
        Text(
            text = "会话与项目都在后端上。用手机扫码配对，或手动填写后端地址。",
            style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size12),
            color = Lx.colors.FgMuted,
            textAlign = androidx.compose.ui.text.style.TextAlign.Center,
            modifier = Modifier.padding(horizontal = Lx.space.s8),
        )
        Box(Modifier.height(Lx.space.s12))
        LxButton(
            text = "扫码配对",
            onClick = onScan,
            variant = LxButtonVariant.Primary,
            size = LxButtonSize.Large,
            leadingIcon = { SmallIcon(Icons.Filled.QrCode2, Lx.colors.Bg, size = 16) },
        )
        Box(Modifier.height(Lx.space.s4))
        LxButton(
            text = "手动添加连接",
            onClick = onManual,
            variant = LxButtonVariant.Secondary,
            size = LxButtonSize.Large,
        )
    }
}
