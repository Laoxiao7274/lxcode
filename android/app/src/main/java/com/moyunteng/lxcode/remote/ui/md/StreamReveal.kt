// 流式两段渲染切分（移植自桌面端 frontend/src/shared/stream-reveal.ts 的
// splitCommitted / safeCutAt，2026-12 渲染性能 Top2 的安卓等价物）。
//
// 问题：打字机（LxTypewriter.rememberStreamReveal）每帧推进 revealed，若整段
// 已显示文本每帧都走 parseMarkdown，每帧成本是 O(全文)。两段渲染把已显示文本
// 切成「已定稿前缀 committed + 尾段 tail」：committed 只在整行完成时才前进
// （前进频率 = 行完成频率，远低于帧率），它的 remember(content) 缓存按字符串
// 命中（零解析零重组）；每帧真正重解析的只有 tail（≤2 行）。
//
// 切点必须落在**块边界**上——切在 quote/list/table/代码围栏中间会把一个块渲染
// 成两个（与整段解析的 DOM 形态分叉），所以候选切点要回退到更早的安全行
// （safeCutAt）。纯 Kotlin 无 Android 依赖 → 可 JVM 单测（StreamRevealTest）。
package com.moyunteng.lxcode.remote.ui.md

/** 两段渲染切分结果：committed + "\n" + tail 恒等于原文（只切行，不丢字符；
 *  committed 为空时 tail === 原文——短文本 / 全不安全 = 单段形态，与原行为一致）。 */
data class SplitCommitted(val committed: String, val tail: String)

/** shown 的已定稿前缀切分（纯函数，可单测）：
 *  - 候选切点从「最后一行之前」起逐行回退（增长中的末行恒在 tail；比「倒数第
 *    2 行之前」多保一行已定稿——尾段更小，每帧重解析更便宜）；
 *  - 候选切点不安全（见 [safeCutAt]）就逐行回退，退到头则 committed 为空、
 *    tail 承接全文——退回空段总是安全的（单段渲染 = 原行为）。
 *
 *  性能纪律：本函数在**每个浇注帧**被调用，不许对全文做 split("\n")/join——
 *  那与 parseMarkdown 同量级，两段渲染的收益会被它吃光。实现：行边界用
 *  lastIndexOf 定位、围栏奇偶用「全文计数 ⊕ tail 计数」，全文只做一次 C 速
 *  扫描（countFences），其余操作都只在末尾几行上。 */
fun splitCommitted(text: String): SplitCommitted {
    var cut = text.lastIndexOf('\n') // committed = text[0, cut)，tail = cut+1 起
    if (cut <= 0) return SplitCommitted("", text)
    val totalFences = countFences(text)
    while (cut > 0) {
        val tail = text.substring(cut + 1)
        if (safeCutAt(text, cut, tail, totalFences)) {
            return SplitCommitted(text.substring(0, cut), tail)
        }
        cut = text.lastIndexOf('\n', cut - 1)
    }
    return SplitCommitted("", text)
}

/** ``` 出现次数（与 Markdown 入口 text.split("```") 同语义——行内出现也计入）。 */
private fun countFences(s: String): Int {
    var n = 0
    var i = s.indexOf("```")
    while (i >= 0) {
        n++
        i = s.indexOf("```", i + 3)
    }
    return n
}

private val RE_UL_LINE = Regex("^(\\s*)[-*+]\\s+")
private val RE_OL_LINE = Regex("^(\\s*)(\\d+)[.)]\\s+")
private val isTableRowLine: (String) -> Boolean = { it.trimStart().startsWith("|") }

/** 是否「普通段落行」（非空、也不是任何块的开头——见 MarkdownParser 的分支表）。
 *  安卓解析器与桌面的语义差异：桌面是**每行一块**（行间切分天然安全），安卓把
 *  连续普通行聚成**一个段落块**（软换行）——切在两个普通行之间会把一段拆成两段。
 *  所以安卓的 safeCutAt 多一条判定：prev 与 next 都是普通行 → 不安全。 */
private fun isPlainLine(line: String): Boolean {
    val t = line.trimStart()
    if (t.isEmpty()) return false
    if (t.startsWith("```")) return false
    if (t.startsWith(">")) return false
    if (isTableRowLine(line)) return false
    if (RE_UL_LINE.containsMatchIn(line) || RE_OL_LINE.containsMatchIn(line)) return false
    if (t.startsWith("#")) return false // 标题（1~6 个 # + 空格；MarkdownParser.headingLevel）
    if (t.length >= 3 && t.all { it == '-' || it == '*' || it == '_' }) return false // 分割线
    return true
}

/** 切点 cut（text[cut] 是 '\n'，tail = cut+1 起）是否安全：committed 与 tail
 *  各自 parseMarkdown 出的块序列拼接后与整段解析逐块一致。四条判定：
 *  ① committed 内 ``` 计数为偶数（切点不在围栏内——围栏内任何切分都会把一段
 *     代码渲染成两段 CodeBlock）；
 *  ② committed 不以空行收尾（否则 tail 首段会带 gap 段距——与整段解析不一致；
 *     回退到更早的非空行）；
 *  ③ tail 首行不得延续 committed 末行的多行块（quote/list/table——这些块是
 *     「连续同类行聚块」，切在中间 = 一个块渲染成两个）。 */
private fun safeCutAt(text: String, cut: Int, tail: String, totalFences: Int): Boolean {
    val committedFences = totalFences - countFences(tail)
    if (committedFences % 2 != 0) return false
    val prevStart = text.lastIndexOf('\n', cut - 1) + 1
    val prev = text.substring(prevStart, cut)
    if (prev.isBlank()) return false
    val next = tail.substringBefore('\n')
    val prevList = RE_UL_LINE.containsMatchIn(prev) || RE_OL_LINE.containsMatchIn(prev)
    val nextList = RE_UL_LINE.containsMatchIn(next) || RE_OL_LINE.containsMatchIn(next)
    if (prevList && nextList) return false
    if (prev.trimStart().startsWith(">") && next.trimStart().startsWith(">")) return false
    if (isTableRowLine(prev) && isTableRowLine(next)) return false
    // 安卓特有：连续普通行聚成一个段落块（软换行）——切在两个普通行之间 = 一段拆两段
    if (isPlainLine(prev) && isPlainLine(next)) return false
    return true
}
