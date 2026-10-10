import io, sys

P = r"C:\Users\xzy\Desktop\my\lxcode\android\app\src\main\java\com\moyunteng\lxcode\remote\ui\SessionsScreen.kt"
src = io.open(P, encoding="utf-8", newline="").read()

def rep(old, new, count=1):
    global src
    n = src.count(old)
    if n != count:
        print("MISMATCH(%d): %s" % (n, old[:70].replace("\r\n", "\\n")))
        sys.exit(1)
    src = src.replace(old, new, count)

# 导入
rep(
    "import androidx.compose.foundation.lazy.items\r\n",
    "import androidx.compose.foundation.lazy.itemsIndexed\r\n",
)
rep(
    "import com.moyunteng.lxcode.design.component.LxListItem\r\n",
    "import com.moyunteng.lxcode.design.component.LxListItem\r\n"
    "import com.moyunteng.lxcode.design.motion.LxEnterSpec\r\n"
    "import com.moyunteng.lxcode.design.motion.LxStagger\r\n"
    "import com.moyunteng.lxcode.design.motion.lxEnter\r\n"
    "import com.moyunteng.lxcode.design.motion.lxStaggerEnter\r\n",
)

# ① 导航项：交错入场
rep(
    "            items(navItems) { entry ->\r\n                NavRow(entry)\r\n            }",
    "            itemsIndexed(navItems) { i, entry ->\r\n"
    "                NavRow(entry, Modifier.lxStaggerEnter(count = navItems.size, index = i))\r\n"
    "            }",
)

# ② 搜索框：接在导航项之后入场
rep(
    "            item {\r\n"
    "                Box(Modifier.padding(horizontal = Lx.space.s4, vertical = Lx.space.s6)) {",
    "            item {\r\n"
    "                Box(\r\n"
    "                    Modifier\r\n"
    "                        .lxEnter(\r\n"
    "                            spec = LxEnterSpec.StaggerItem,\r\n"
    "                            delayMillis = LxStagger.delayMillis(navItems.size + 1, navItems.size),\r\n"
    "                        )\r\n"
    "                        .padding(horizontal = Lx.space.s4, vertical = Lx.space.s6),\r\n"
    "                ) {",
)

# ④ 项目行：交错入场
rep(
    "            items(MockData.projects) { project ->\r\n"
    "                ProjectRow(\r\n"
    "                    name = project.name,",
    "            itemsIndexed(MockData.projects) { i, project ->\r\n"
    "                ProjectRow(\r\n"
    "                    modifier = Modifier.lxStaggerEnter(count = MockData.projects.size + 1, index = i),\r\n"
    "                    name = project.name,",
)
rep(
    "            item {\r\n"
    "                ProjectRow(\r\n"
    "                    name = \"未分组\",",
    "            item {\r\n"
    "                ProjectRow(\r\n"
    "                    modifier = Modifier.lxEnter(\r\n"
    "                        spec = LxEnterSpec.StaggerItem,\r\n"
    "                        delayMillis = LxStagger.delayMillis(MockData.projects.size + 1, MockData.projects.size),\r\n"
    "                    ),\r\n"
    "                    name = \"未分组\",",
)

# ⑥ 会话行：交错入场
rep(
    "                items(sessions) { s ->\r\n"
    "                    SessionRowItem(s) { state.route = Route.Thread(s.id) }\r\n"
    "                }",
    "                itemsIndexed(sessions) { i, s ->\r\n"
    "                    SessionRowItem(s, Modifier.lxStaggerEnter(count = sessions.size, index = i)) {\r\n"
    "                        state.route = Route.Thread(s.id)\r\n"
    "                    }\r\n"
    "                }",
)

# 组件签名：补 modifier 参数（只增不改，默认 Modifier）
rep(
    "private fun NavRow(entry: NavEntry) {\r\n"
    "    LxListItem(\r\n"
    "        title = entry.label,",
    "private fun NavRow(entry: NavEntry, modifier: Modifier = Modifier) {\r\n"
    "    LxListItem(\r\n"
    "        title = entry.label,\r\n"
    "        modifier = modifier,",
)
rep(
    "private fun ProjectRow(name: String, count: Int, active: Boolean, onClick: () -> Unit) {\r\n"
    "    LxListItem(\r\n"
    "        title = name,\r\n"
    "        selected = active,",
    "private fun ProjectRow(\r\n"
    "    name: String,\r\n"
    "    count: Int,\r\n"
    "    active: Boolean,\r\n"
    "    onClick: () -> Unit,\r\n"
    "    modifier: Modifier = Modifier,\r\n"
    ") {\r\n"
    "    LxListItem(\r\n"
    "        title = name,\r\n"
    "        modifier = modifier,\r\n"
    "        selected = active,",
)
rep(
    "private fun SessionRowItem(session: SessionMeta, onClick: () -> Unit) {\r\n"
    "    LxListItem(\r\n"
    "        title = session.title,",
    "private fun SessionRowItem(session: SessionMeta, modifier: Modifier = Modifier, onClick: () -> Unit) {\r\n"
    "    LxListItem(\r\n"
    "        title = session.title,\r\n"
    "        modifier = modifier,",
)

io.open(P, "w", encoding="utf-8", newline="").write(src)
print("ok")
