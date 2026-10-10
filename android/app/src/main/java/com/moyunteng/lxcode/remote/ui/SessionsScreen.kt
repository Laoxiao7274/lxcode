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
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ChatBubbleOutline
import androidx.compose.material.icons.filled.Folder
import androidx.compose.material.icons.filled.FolderOpen
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.moyunteng.lxcode.design.component.LxListItem
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
import com.moyunteng.lxcode.remote.mock.MockData
import com.moyunteng.lxcode.remote.mock.SessionMeta

/** 会话列表页（首页）。 */
@Composable
fun SessionsScreen(state: MockAppState) {
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
                online = state.online,
                name = state.activeName,
                onClick = { state.route = Route.Connections },
            )
        }

        LazyColumn(
            Modifier
                .fillMaxSize()
                .padding(horizontal = Lx.space.s8),
        ) {
            // ① 导航项
            // 纯远控壳：侧栏导航项只留「新对话」（对远程后端开新会话）；
            // Agents/拓展/Git 管理/远程访问/自动化 是桌面端管理本机的面板，不进手机。
            val navItems = listOf(
                NavEntry("新对话", Icons.Filled.ChatBubbleOutline) {
                    state.route = Route.Thread(MockData.currentSessionId)
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
            itemsIndexed(MockData.projects) { i, project ->
                ProjectRow(
                    modifier = Modifier.lxStaggerEnter(count = MockData.projects.size + 1, index = i),
                    name = project.name,
                    count = state.projectSessionCount(project.id),
                    active = state.scope == project.id,
                    onClick = { state.scope = project.id },
                )
            }
            item {
                ProjectRow(
                    modifier = Modifier.lxEnter(
                        spec = LxEnterSpec.StaggerItem,
                        delayMillis = LxStagger.delayMillis(MockData.projects.size + 1, MockData.projects.size),
                    ),
                    name = "未分组",
                    count = state.looseSessionCount(),
                    active = state.scope.isEmpty(),
                    onClick = { state.scope = "" },
                )
            }

            // ⑤ 「对话」分组标题
            item { LxSectionHeader("对话", topPadding = Lx.space.s12) }

            // ⑥ 会话行（搜索过滤）
            val sessions = state.sessionsInScope().filter {
                state.query.trim().isEmpty() ||
                    it.title.contains(state.query.trim(), ignoreCase = true)
            }
            if (sessions.isEmpty()) {
                item { SmallEmpty("没有匹配「${state.query.trim()}」的对话") }
            } else {
                itemsIndexed(sessions) { i, s ->
                    SessionRowItem(s, Modifier.lxStaggerEnter(count = sessions.size, index = i)) {
                        state.route = Route.Thread(s.id)
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

/** 项目行（文件夹图标 + 名字 + 守则 + 会话数；对齐 .proj-row）。 */
@Composable
private fun ProjectRow(
    name: String,
    count: Int,
    active: Boolean,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    LxListItem(
        title = name,
        modifier = modifier,
        selected = active,
        leading = {
            SmallIcon(
                if (active) Icons.Filled.FolderOpen else Icons.Filled.Folder,
                Lx.colors.FgMuted,
                size = 14,
            )
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
private fun SessionRowItem(session: SessionMeta, modifier: Modifier = Modifier, onClick: () -> Unit) {
    LxListItem(
        title = session.title,
        modifier = modifier,
        leading = {
            LxStatusDot(
                status = if (session.running) LxStatus.Run else LxStatus.Idle,
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
                SmallIcon(Icons.Filled.MoreVert, Lx.colors.FgFaint, size = 12)
            }
        },
        onClick = onClick,
    )
}
