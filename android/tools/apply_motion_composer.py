import io, sys

P = r"C:\Users\xzy\Desktop\my\lxcode\android\app\src\main\java\com\moyunteng\lxcode\remote\ui\Composer.kt"
src = io.open(P, encoding="utf-8", newline="").read()

def rep(old, new, count=1):
    global src
    n = src.count(old)
    if n != count:
        print("MISMATCH(%d): %r" % (n, old[:80]))
        sys.exit(1)
    src = src.replace(old, new, count)

rep(
    "import com.moyunteng.lxcode.design.component.LxTextField\r\n",
    "import com.moyunteng.lxcode.design.component.LxTextField\r\n"
    "import com.moyunteng.lxcode.design.motion.LxEnterSpec\r\n"
    "import com.moyunteng.lxcode.design.motion.lxAnimateFloat\r\n"
    "import com.moyunteng.lxcode.design.motion.lxEnter\r\n"
    "import com.moyunteng.lxcode.design.motion.lxPressScale\r\n",
)

# 控件行：pop-in（translateY(-4dp) + scale .98，160ms）
rep(
    "        Row(\r\n"
    "            Modifier\r\n"
    "                .fillMaxWidth()\r\n"
    "                .horizontalScroll(rememberScrollState()),\r\n"
    "            verticalAlignment = Alignment.CenterVertically,\r\n"
    "            horizontalArrangement = Arrangement.spacedBy(Lx.space.s6),\r\n"
    "        ) {\r\n"
    "            ControlPill(\"模型\", MockData.model)",
    "        Row(\r\n"
    "            Modifier\r\n"
    "                // 控件入场：pop-in（translateY(-4dp) + scale .98，160ms 标准曲线）\r\n"
    "                .lxEnter(spec = LxEnterSpec.PopIn)\r\n"
    "                .fillMaxWidth()\r\n"
    "                .horizontalScroll(rememberScrollState()),\r\n"
    "            verticalAlignment = Alignment.CenterVertically,\r\n"
    "            horizontalArrangement = Arrangement.spacedBy(Lx.space.s6),\r\n"
    "        ) {\r\n"
    "            ControlPill(\"模型\", MockData.model)",
)

# 发送 / 停止按钮：按压缩放微交互
rep(
    "private fun SendButton(busy: Boolean, enabled: Boolean, onClick: () -> Unit) {\r\n"
    "    val clickable = busy || enabled\r\n"
    "    Box(\r\n"
    "        Modifier\r\n"
    "            .size(34.dp)",
    "private fun SendButton(busy: Boolean, enabled: Boolean, onClick: () -> Unit) {\r\n"
    "    val clickable = busy || enabled\r\n"
    "    val interaction = remember { MutableInteractionSource() }\r\n"
    "    Box(\r\n"
    "        Modifier\r\n"
    "            // 按压微交互：scale .98 + 120ms 标准曲线（门控关闭时瞬变）\r\n"
    "            .lxPressScale(interaction, enabled = clickable)\r\n"
    "            .size(34.dp)",
)
rep(
    "            .clickable(\r\n"
    "                enabled = clickable,\r\n"
    "                interactionSource = remember { MutableInteractionSource() },\r\n"
    "                indication = null,\r\n"
    "                onClick = onClick,\r\n"
    "            ),",
    "            .clickable(\r\n"
    "                enabled = clickable,\r\n"
    "                interactionSource = interaction,\r\n"
    "                indication = null,\r\n"
    "                onClick = onClick,\r\n"
    "            ),",
)

# 上下文环：数值变化 220ms 过渡（不是瞬变）
rep(
    "    val pct = if (window <= 0) 0f else (used.toFloat() / window).coerceIn(0f, 1f)\r\n",
    "    val target = if (window <= 0) 0f else (used.toFloat() / window).coerceIn(0f, 1f)\r\n"
    "    // 环读数变化 220ms 动画过渡（门控关闭时瞬变）—— 桌面端 ContextIndicator 的环不瞬跳\r\n"
    "    val pct by lxAnimateFloat(target = target)\r\n",
)

io.open(P, "w", encoding="utf-8", newline="").write(src)
print("ok")
