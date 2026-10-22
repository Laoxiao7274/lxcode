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
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Archive
import androidx.compose.material.icons.filled.ChatBubbleOutline
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Edit
import androidx.compose.material.icons.filled.Folder
import androidx.compose.material.icons.filled.FolderOpen
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.QrCode2
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import android.widget.Toast
import androidx.core.net.toUri
import com.moyunteng.lxcode.design.component.LxButton
import com.moyunteng.lxcode.design.component.LxButtonSize
import com.moyunteng.lxcode.design.component.LxButtonVariant
import com.moyunteng.lxcode.design.component.LxListItem
import com.moyunteng.lxcode.design.component.LxSpinner
import com.moyunteng.lxcode.design.motion.LxEnterSpec
import com.moyunteng.lxcode.design.motion.LxStagger
import com.moyunteng.lxcode.design.motion.lxEnter
import com.moyunteng.lxcode.design.motion.lxStaggerEnter
import com.moyunteng.lxcode.design.component.LxSectionHeader
import com.moyunteng.lxcode.design.component.LxStatus
import com.moyunteng.lxcode.design.component.LxStatusDot
import com.moyunteng.lxcode.design.component.LxTextField
import com.moyunteng.lxcode.design.token.Lx
import com.moyunteng.lxcode.design.token.LxColors
import com.moyunteng.lxcode.remote.model.SessionMeta

/** 会话列表页（首页）。 */
@Composable
fun SessionsScreen(state: AppState) {
    // 安装权限引导（Compose 弹窗模式对齐 LxConfirmDialog 用法）+ 启动更新确认弹窗。
    // 两个弹窗挂在页面层而非 AppRoot：横幅与「安装」按钮也在这里，弹窗消费的状态
    // （showInstallPermissionGuide / pendingUpdatePrompt）归本页编排。
    InstallPermissionGuideDialog(state)
    UpdateFoundDialog(state)
    val context = LocalContext.current
    // 会话行操作：行尾 ⋯ 菜单 → 重命名 / 归档（协议有 session.rename /
    // session.archive，见 PROTOCOL-NOTES.md §3）；删除 / 置顶协议没有——记缺口不造。
    var menuFor by remember { mutableStateOf<SessionMeta?>(null) }
    var renaming by remember { mutableStateOf<SessionMeta?>(null) }
    // 返回键第一层：行菜单 / 重命名弹窗开着 → 返回先关弹层（AppRoot 的 BackHandler 链）
    LaunchedEffect(menuFor, renaming) {
        state.backInterceptor =
            if (menuFor != null || renaming != null) {
                {
                    menuFor = null
                    renaming = null
                    true
                }
            } else {
                null
            }
    }
    Column(
        Modifier
            .fillMaxSize()
            .background(LxColors.Bg),
    ) {
        // 顶部：品牌 + 连接药丸
        Row(
            Modifier
                .fillMaxWidth()
                .background(LxColors.Surface)
                .padding(horizontal = Lx.space.s10, vertical = Lx.space.s6),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(Lx.space.s8),
        ) {
            Box(
                Modifier
                    .size(20.dp)
                    .clip(RoundedCornerShape(Lx.radius.r6))
                    .background(Lx.colors.Fg),
                contentAlignment = Alignment.Center,
            ) {
                Text(
                    text = "L",
                    style = Lx.type.Button.copy(fontWeight = FontWeight.Bold),
                    color = LxColors.Bg,
                )
            }
            Text(
                text = "LxCode",
                style = Lx.type.ListTitle.copy(fontWeight = FontWeight.SemiBold),
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

        LazyColumn(
            Modifier
                .fillMaxSize()
                .padding(horizontal = Lx.space.s8),
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

            // ① 导航项
            // 纯远控壳：侧栏导航项只留「新对话」（对远程后端开新会话）；
            // Agents/拓展/Git 管理/远程访问/自动化 是桌面端管理本机的面板，不进手机。
            // （未连接时整页已是引导态，到不了这里——新对话只在连接后可用。）
            val navItems = listOf(
                NavEntry("新对话", Icons.Filled.ChatBubbleOutline) {
                    // 项目组内新建：scope 是当前范围的项目 id（未分组 = ""）——
                    // session.new 的 workspace 参数，会话归属该项目（对齐桌面端
                    // Sidebar 的 filter 语义：在哪个范围里点的新对话就归哪个项目）
                    state.real.newSession(
                        workspace = state.scope,
                        onReady = { id -> state.route = Route.Thread(id) },
                    )
                },
            )
            itemsIndexed(navItems) { i, entry ->
                NavRow(entry, Modifier.lxStaggerEnter(count = navItems.size, index = i))
            }

            // ② 搜索框
            item {
                Box(
                    Modifier
                        .lxEnter(
                            spec = LxEnterSpec.StaggerItem,
                            delayMillis = LxStagger.delayMillis(navItems.size + 1, navItems.size),
                        )
                        .padding(horizontal = Lx.space.s4, vertical = Lx.space.s6),
                ) {
                    LxTextField(
                        value = state.query,
                        onValueChange = { state.query = it },
                        placeholder = "搜索对话与项目",
                        trailing = {
                            if (state.query.isEmpty()) {
                                Text(
                                    text = "Ctrl K",
                                    style = Lx.type.Pill.copy(fontSize = Lx.type.Size10),
                                    color = Lx.colors.FgFaint,
                                    modifier = Modifier
                                        .clip(RoundedCornerShape(Lx.radius.r4))
                                        .background(Lx.colors.BorderSoft)
                                        .padding(horizontal = 5.dp, vertical = 1.dp),
                                )
                            } else {
                                Text(
                                    text = "清除",
                                    style = Lx.type.Button.copy(fontSize = Lx.type.Size10_5),
                                    color = Lx.colors.FgFaint,
                                    modifier = Modifier.clickable(
                                        interactionSource = remember { MutableInteractionSource() },
                                        indication = null,
                                    ) { state.query = "" },
                                )
                            }
                        },
                    )
                }
            }

            // ③ 「项目」分组标题（只读：项目属于远程后端，手机端不提供新增入口）
            item { LxSectionHeader("项目") }

            // ④ 项目行 + 未分组
            // 项目来自 project.list（真数据）；会话按 workspace（= 项目 id）归组，
            // 各组计数 = 该项目 id 下的会话数（对齐桌面端 Sidebar 的 count 口径）；
            // 无归属（workspace 空）进「未分组」。
            itemsIndexed(state.real.projects) { i, project ->
                ProjectRow(
                    modifier = Modifier.lxStaggerEnter(count = state.real.projects.size + 1, index = i),
                    name = project.name,
                    count = state.realProjectSessionCount(project.id),
                    runningCount = state.realProjectRunningCount(project.id),
                    active = state.scope == project.id,
                    onClick = { state.scope = project.id },
                )
            }
            item {
                ProjectRow(
                    modifier = Modifier.lxEnter(
                        spec = LxEnterSpec.StaggerItem,
                        delayMillis = LxStagger.delayMillis(state.real.projects.size + 1, state.real.projects.size),
                    ),
                    name = "未分组",
                    count = state.realLooseSessionCount(),
                    runningCount = state.realLooseRunningCount(),
                    active = state.scope.isEmpty(),
                    onClick = { state.scope = "" },
                )
            }

            // ⑤ 「对话」分组标题
            item { LxSectionHeader("对话", topPadding = Lx.space.s12) }

            // ⑥ 会话行（搜索过滤）：列表来自 session.list（真数据）
            val sessions = state.realSessionsInScope().filter {
                state.query.trim().isEmpty() ||
                    it.title.contains(state.query.trim(), ignoreCase = true)
            }
            if (sessions.isEmpty()) {
                item { SmallEmpty(if (state.query.trim().isEmpty()) "还没有对话——点上方「新对话」开始" else "没有匹配「${state.query.trim()}」的对话") }
            } else {
                itemsIndexed(sessions) { i, s ->
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

            // ⑦ 底部「设置」行
            item {
                Row(
                    Modifier
                        .fillMaxWidth()
                        .clip(Lx.radius.RowShape)
                        .clickable(
                            interactionSource = remember { MutableInteractionSource() },
                            indication = null,
                        ) {}
                        .padding(horizontal = Lx.space.s8, vertical = Lx.space.s8),
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(Lx.space.s8),
                ) {
                    SmallIcon(Icons.Filled.Settings, Lx.colors.FgMuted, size = 14)
                    Text(
                        text = "设置",
                        style = Lx.type.ListTitle,
                        color = Lx.colors.Fg,
                    )
                }
            }
        }
    }

    // 重命名弹窗（session.rename；真实模式生效并刷新列表，未连接时如实提示）
    renaming?.let { target ->
        var draft by remember(target.id) { mutableStateOf(target.title) }
        AlertDialog(
            onDismissRequest = { renaming = null },
            containerColor = LxColors.Surface,
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

private data class NavEntry(val label: String, val icon: ImageVector, val onTap: (() -> Unit)?)

/** 导航项一行（图标 + 文字；对齐 .nav-item）。 */
@Composable
private fun NavRow(entry: NavEntry, modifier: Modifier = Modifier) {
    LxListItem(
        title = entry.label,
        modifier = modifier,
        leading = { SmallIcon(entry.icon, Lx.colors.FgMuted, size = 15) },
        onClick = entry.onTap,
    )
}

/** 项目行（文件夹图标 + 名字 + 守则 + 会话数；对齐 .proj-row）。
 *  [runningCount] > 0 时标题旁亮运行中 spinner + 计数（对齐桌面端侧栏项目行的
 *  spinner——项目内任一会话在生成就亮，多端共享进度）。 */
@Composable
private fun ProjectRow(
    name: String,
    count: Int,
    runningCount: Int,
    active: Boolean,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    LxListItem(
        title = name,
        modifier = modifier,
        selected = active,
        leading = {
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(Lx.space.s6),
            ) {
                SmallIcon(
                    if (active) Icons.Filled.FolderOpen else Icons.Filled.Folder,
                    Lx.colors.FgMuted,
                    size = 14,
                )
                if (runningCount > 0) {
                    LxSpinner(size = 10.dp)
                }
            }
        },
        trailing = {
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(Lx.space.s8),
            ) {
                Text(
                    text = "守则",
                    style = Lx.type.Button.copy(fontSize = Lx.type.Size10_5),
                    color = Lx.colors.FgFaint,
                )
                if (runningCount > 0) {
                    Text(
                        text = "$runningCount 运行中",
                        style = Lx.type.Badge.copy(fontSize = Lx.type.Size10_5),
                        color = LxColors.Amber,
                    )
                }
                Text(
                    text = "$count",
                    style = Lx.type.Badge,
                    color = Lx.colors.FgFaint,
                )
            }
        },
        onClick = onClick,
    )
}

/** 会话行（状态点 + 标题 + 时间 + ⋯；对齐 .session-item）。 */
@Composable
private fun SessionRowItem(
    session: SessionMeta,
    modifier: Modifier = Modifier,
    running: Boolean = session.running,
    onClick: () -> Unit,
    onMenu: () -> Unit,
) {
    LxListItem(
        title = session.title,
        modifier = modifier,
        leading = {
            // 运行中 = 琥珀呼吸点（LxStatus.Run），空闲 = 灰点——红色只给失败态，
            // 这里没有失败语义，永远不出现 Danger。
            LxStatusDot(
                status = if (running) LxStatus.Run else LxStatus.Idle,
                size = 7.dp,
            )
        },
        trailing = {
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(Lx.space.s6),
            ) {
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
        },
        onClick = onClick,
    )
}

/** 行操作菜单 Shape —— 对齐 PickerMenus 的 .mp-panel（圆角 12、1px 边、菜单级阴影）。 */
private val SessionOpsShape = RoundedCornerShape(12.dp)

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
                ambientColor = Color(0x14000000),
                spotColor = Color(0x1F000000),
            )
            .clip(SessionOpsShape)
            .background(LxColors.Surface)
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
            .background(LxColors.Surface)
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
 * 启动自动检查发现新版本的确认弹窗（与连接页「检查更新」同一个弹窗形态，
 * 2026-12 用户拍板统一）：含版本/大小/notes，批准 = 应用内下载（走横幅态）。
 * 可关闭——本次关闭后横幅仍在（会话页可手动下载）；下次启动若无更新不再弹。
 * 横幅仍是下载进度/就绪/失败的态载体，只是不再承担「发现新版」的第一呈现。
 */
@Composable
private fun UpdateFoundDialog(state: AppState) {
    val release = state.pendingUpdatePrompt ?: return
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
            state.pendingUpdatePrompt = null
            // 与连接页「下载并安装」同语义：横幅接管下载/校验/安装编排
            state.updateAvailable = release
        },
        onDeny = { state.pendingUpdatePrompt = null },
        onDismissRequest = { state.pendingUpdatePrompt = null },
    )
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
                .clip(RoundedCornerShape(Lx.radius.r10 + 2.dp))
                .background(Lx.colors.Fg),
            contentAlignment = Alignment.Center,
        ) {
            Text(
                text = "L",
                style = Lx.type.ListTitle.copy(fontSize = Lx.type.Size14, fontWeight = FontWeight.Bold),
                color = LxColors.Bg,
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
            leadingIcon = { SmallIcon(Icons.Filled.QrCode2, LxColors.Bg, size = 16) },
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
