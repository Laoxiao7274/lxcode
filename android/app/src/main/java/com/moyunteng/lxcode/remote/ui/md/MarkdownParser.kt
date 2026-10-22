// 轻量 Markdown 解析器 —— 安卓端自写子集（零新增依赖，与桌面端 markdown.tsx
// 的自写子集语义对齐）。
//
// 支持的子集（**以桌面端 markdown.tsx 为口径对齐**）：
//   * 标题    `# ` ~ `###### `（行级，ATX）
//   * 粗体    `**text**` / `__text__`
//   * 斜体    `*text*` / `_text_`（不与粗体歧义：先匹配粗体）
//   * 删除线  `~~text~~`（桌面端 md-del 有——补齐）
//   * 行内码  `` `code` ``（内部不再解析其它标记）
//   * 代码块  ``` 围栏（行级；语言标记只丢弃，不做高亮）
//   * 分割线  `---` / `***` / `___`（桌面端 md-hr 有——补齐）
//   * 无序列表 `- item` / `* item` / `+ item`（缩进 2 空格一级 = 嵌套，桌面端同口径）
//   * 有序列表 `1. item` / `1) item`（桌面端 ordered list 有——补齐）
//   * 引用    `> text`（行级；连续引用行合并为一段）
//   * 链接    URL 自动识别（http/https，可点击）+ `[text](url)` 形式
//   * 表格    管道表格（表头 + `|---|---|` 分隔行 + 数据行）：行级解析成
//             MdBlock.Table，渲染层横滚 + 边框/斑马线（手机窄屏体验优于
//             桌面端的 HTML table 语义——整表 horizontalScroll 不挤压）。
//             单元格内联格式保留（parseInline）；转义管道 `\|` 不切列；
//             不规则列数按最大列数右补空。
//   * 任务列表 `- [ ]`：桌面端没有（RE_UL 不认）——不做，`- [ ]` 按普通列表项渲染。
//   * 转义    桌面端没有——不做。
//
// 解析输出是**块列表**（MdBlock），每块携带 AnnotatedString 的数据形态
// （Spans：文本 + 样式标记 + 链接）。渲染层（ThreadScreen.MdText）把块映射成
// Compose 组件；解析器本身纯 Kotlin 无 Android 依赖 → 可 JUnit 测试。
//
// 与打字机流式的协同：解析在**已显示文本**（reveal.text）上进行，流式期间每帧
// 重解析整块——对 2~3KB 的 assistant 正文开销是微秒级（纯字符串扫描），GLM
// 200tok/s 实测瓶颈在 layout 不在解析；若出现卡顿由调用方限流（本层无状态）。
package com.moyunteng.lxcode.remote.ui.md

/**
 * 一个 Markdown 块（渲染单元）。
 */
sealed interface MdBlock {
    /** 普通段落（含行内样式与链接）。[gap] = 前面有空行——渲染层给段距
     *  （对齐桌面端 md-para：连续段落间的 16px 呼吸感）。 */
    data class Paragraph(val spans: List<MdSpan>, val gap: Boolean = false) : MdBlock

    /** 标题（level 1~6）。 */
    data class Heading(val level: Int, val spans: List<MdSpan>) : MdBlock

    /** 围栏代码块（语言标记已丢弃；内容原样）。 */
    data class CodeBlock(val code: String) : MdBlock

    /** 分割线（`---` / `***` / `___`，对齐桌面端 md-hr）。 */
    data object Hr : MdBlock

    /** 列表（多行合并为一块；每项独立解析行内样式）。
     *  [ordered] = 有序（`1. `）；[items] 携带缩进级别（2 空格一级，
     *  对齐桌面端 MdListItem.level——嵌套列表渲染层按级缩进）。
     *  名字用 Listing：List 与 kotlin.List 撞名（data class List 声明会
     *  遮蔽标准库泛型 List，同文件的 List<MdSpan> 全部编译失败）。 */
    data class Listing(val ordered: Boolean, val items: List<MdListItem>) : MdBlock

    /** 引用（连续行合并；每段解析行内样式）。 */
    data class Quote(val paragraphs: List<List<MdSpan>>) : MdBlock

    /** 管道表格（表头 + 分隔行 + 数据行；渲染层横滚，见 ThreadScreen.MdTable）。
     *  [header] = 表头单元格；[rows] = 数据行（不含分隔行）；不规则列数
     *  已在解析期按最大列数右补空（渲染层不需要再对齐）。 */
    data class Table(val header: List<List<MdSpan>>, val rows: List<List<List<MdSpan>>>) : MdBlock
}

/** 列表项：[level] = 缩进级数（2 空格一级，tab 记 2——桌面端同口径）。 */
data class MdListItem(val level: Int, val spans: List<MdSpan>)

/**
 * 行内 span。
 *
 * @property text 文本内容
 * @property styles 叠加的样式标记（bold/italic/code 可叠加，如 `***x***`）
 * @property link 非空 = 可点击链接（http/https URL）
 */
data class MdSpan(
    val text: String,
    val bold: Boolean = false,
    val italic: Boolean = false,
    val code: Boolean = false,
    /** 删除线（`~~x~~`，对齐桌面端 md-del：opacity 0.55 的删除线）。 */
    val strike: Boolean = false,
    val link: String? = null,
)

/**
 * 解析 Markdown 文本为块列表。空文本 → 空列表。
 * 容错纪律：任何不成对的标记（未闭合的 `` ` ``、`**`）按**字面文本**渲染，
 * 不抛异常——流式期间的半截标记（`**正在加粗`）是常态，必须稳定渲染。
 */
fun parseMarkdown(src: String): List<MdBlock> {
    if (src.isEmpty()) return emptyList()
    val blocks = mutableListOf<MdBlock>()
    val lines = src.split('\n')
    var i = 0
    var gap = false // 空行标记：下一个段落块带段距（对齐桌面端 parseBlocks 的 gap）
    while (i < lines.size) {
        val line = lines[i]
        when {
            // 围栏代码块：``` 开头（允许缩进与语言标记）；未闭合 = 吃到结尾（容错）
            line.trimStart().startsWith("```") -> {
                val body = StringBuilder()
                i++
                while (i < lines.size && !lines[i].trimStart().startsWith("```")) {
                    body.append(lines[i]).append('\n')
                    i++
                }
                if (i < lines.size) i++ // 吃掉闭合围栏
                blocks.add(MdBlock.CodeBlock(body.toString().trimEnd('\n')))
                gap = false
            }
            // 标题：1~6 个 # + 空格
            headingLevel(line) > 0 -> {
                val level = headingLevel(line)
                blocks.add(MdBlock.Heading(level, parseInline(line.trimStart().dropWhile { it == '#' }.trim())))
                i++
                gap = false
            }
            // 分割线：--- / *** / ___（整行只有 3+ 个同一符号；对齐桌面端 RE_HR）
            isHr(line) -> {
                blocks.add(MdBlock.Hr)
                i++
                gap = true // 分割线是块级边界——后续段落带段距（桌面端同语义）
            }
            // 列表：无序（- / *）或有序（1. / 1)）；连续同类行聚成一块，
            // 缩进按 2 空格一级记进 item.level（嵌套列表渲染层缩进——桌面端同口径）
            isBullet(line) || isOrderedItem(line) -> {
                val ordered = !isBullet(line)
                val items = mutableListOf<MdListItem>()
                while (i < lines.size) {
                    val l = lines[i]
                    val lvl = if (ordered) orderedIndentLevel(l) else bulletIndentLevel(l)
                    // 异类列表行 / 非列表行 → 开新块（对齐桌面端：if (!m) break）
                    if (lvl < 0) break
                    items.add(
                        MdListItem(
                            level = lvl,
                            spans = parseInline(
                                if (ordered) orderedItemText(l) else l.trimStart().drop(1).trim(),
                            ),
                        ),
                    )
                    i++
                }
                blocks.add(MdBlock.Listing(ordered, items))
                gap = false
            }
            // 引用：> + 可选空格（连续行聚成一块）
            line.trimStart().startsWith(">") -> {
                val paras = mutableListOf<List<MdSpan>>()
                while (i < lines.size && lines[i].trimStart().startsWith(">")) {
                    paras.add(parseInline(lines[i].trimStart().drop(1).trim()))
                    i++
                }
                blocks.add(MdBlock.Quote(paras))
                gap = false
            }
            // 空行：段落分隔（记 gap，跳过）
            line.isBlank() -> {
                gap = true
                i++
            }
            // 管道表格：本行是表头且下一行是分隔行（|---|---|）才认作表格——
            // 孤立的管道行（如 `- [ ]` 之外的普通含管道文本）仍按段落渲染。
            isTableRow(line) && i + 1 < lines.size && isTableDivider(lines[i + 1]) -> {
                val header = splitTableRow(line)
                val rawRows = mutableListOf<List<List<MdSpan>>>()
                i += 2 // 吃掉表头 + 分隔行
                while (i < lines.size && isTableRow(lines[i])) {
                    rawRows.add(splitTableRow(lines[i]))
                    i++
                }
                // 不规则列数：按全局最大列数对齐（右补空 / 截断到 max——渲染层每行等列）
                val ncols = maxOf(header.size, rawRows.maxOfOrNull { it.size } ?: 0)
                fun pad(cells: List<List<MdSpan>>) =
                    List(ncols) { c -> cells.getOrElse(c) { emptyList() } }
                blocks.add(MdBlock.Table(pad(header), rawRows.map(::pad)))
                gap = true // 表格是块级边界——后续段落带段距（对齐 Hr 的语义）
            }
            // 普通段落：连续非空非特殊行聚成一段（中间的换行保留——软换行）
            else -> {
                val parts = mutableListOf<String>()
                while (i < lines.size && !isBlockBoundary(lines[i], lines.getOrNull(i + 1))) {
                    parts.add(lines[i].trim())
                    i++
                }
                blocks.add(MdBlock.Paragraph(parseInline(parts.joinToString("\n")), gap))
                gap = false
            }
        }
    }
    return blocks
}

/** 分割线行：整行（去空白）只有 3+ 个 - / * / _（对齐桌面端 RE_HR）。 */
private fun isHr(line: String): Boolean {
    val t = line.trim()
    if (t.length < 3) return false
    val c = t[0]
    if (c != '-' && c != '*' && c != '_') return false
    return t.all { it == c }
}

// ===== 表格解析 =====

/** 表格行：至少有一个未转义的管道 `|`（容错：首尾管道可省——GFM 同款）。 */
private fun isTableRow(line: String): Boolean {
    val t = line.trim()
    if (t.isEmpty()) return false
    // 行内 code 里的管道不算列分隔（`a|b` 在 code 单元格里是文本）
    var inCode = false
    var i = 0
    while (i < t.length) {
        when {
            t[i] == '\\' -> i += 2 // 转义：跳过下一字符（`\|` 不算管道）
            t[i] == '`' -> { inCode = !inCode; i++ }
            t[i] == '|' && !inCode -> return true
            else -> i++
        }
    }
    return false
}

/** 分隔行（`| --- | --- |` / `|---|---|` 等）：去掉首尾管道后每格都是空/冒号/连字符
 *  且至少含一个 `-`——对齐 GFM 对齐标记（`:---` `:--:` `---:`，对齐信息只丢弃）。 */
private fun isTableDivider(line: String): Boolean {
    if (!isTableRow(line)) return false
    val cells = splitTableRowCells(line)
    if (cells.isEmpty()) return false
    return cells.all { c ->
        val t = c.trim().removePrefix(":").removeSuffix(":").trim()
        t.isNotEmpty() && t.all { it == '-' }
    }
}

/** 表格行 → 单元格文本列表（未转义管道切列；`\|` 还原成 `|`；首尾管道可省）。
 *  与 [isTableRow]/[isTableDivider] 共享同一套切列逻辑。 */
private fun splitTableRowCells(line: String): List<String> {
    val t = line.trim()
    // 剥掉首尾孤立管道（GFM 容错）——只剥一个，防误伤 `||`
    val body = if (t.startsWith("|")) t.removePrefix("|") else t
    val trimmedBody = if (body.endsWith("|") && !body.endsWith("\\|")) body.removeSuffix("|") else body
    val cells = mutableListOf<String>()
    val cur = StringBuilder()
    var inCode = false
    var i = 0
    while (i < trimmedBody.length) {
        val c = trimmedBody[i]
        when {
            c == '\\' && i + 1 < trimmedBody.length && trimmedBody[i + 1] == '|' -> {
                cur.append('|'); i += 2 // 转义管道还原成字面 `|`
            }
            c == '\\' -> { cur.append(c); i++ } // 其它转义原样保留（行内解析器不处理转义）
            c == '`' -> { inCode = !inCode; cur.append(c); i++ }
            c == '|' && !inCode -> { cells.add(cur.toString()); cur.clear(); i++ }
            else -> { cur.append(c); i++ }
        }
    }
    cells.add(cur.toString())
    return cells
}

/** 表格行 → 单元格行内 span 列表（每格独立 parseInline；空格 trim）。 */
private fun splitTableRow(line: String): List<List<MdSpan>> =
    splitTableRowCells(line).map { parseInline(it.trim()) }

/** 有序列表行（`1. ` / `1)`；对齐桌面端 RE_OL）。 */
private fun isOrderedItem(line: String): Boolean = ORDERED_RE.containsMatchIn(line.trimStart())

private val ORDERED_RE = Regex("""^(\s*)(\d+)[.)]\s+(.*)$""")
private val UL_RE = Regex("""^(\s*)[-*+]\s+(.*)$""")

/** 有序项缩进级数（前导空白 2 格一级，tab 记 2）；-1 = 不是有序项。 */
private fun orderedIndentLevel(line: String): Int =
    ORDERED_RE.find(line)?.let { indentLevel(it.groupValues[1]) } ?: -1

/** 无序项缩进级数；-1 = 不是无序项。 */
private fun bulletIndentLevel(line: String): Int =
    UL_RE.find(line)?.let { indentLevel(it.groupValues[1]) } ?: -1

private fun indentLevel(ws: String): Int = ws.replace("\t", "  ").length / 2

/** 有序项的文本部分（剥掉 `1. ` 前缀）。 */
private fun orderedItemText(line: String): String =
    ORDERED_RE.find(line)!!.groupValues[3]

/** 是不是块边界（用于段落聚合）：空行 / 围栏 / 标题 / 列表 / 引用 / 分割线 / 表格开头。 */
private fun isBlockBoundary(line: String, next: String?): Boolean =
    line.isBlank() ||
        line.trimStart().startsWith("```") ||
        headingLevel(line) > 0 ||
        isHr(line) ||
        isBullet(line) ||
        isOrderedItem(line) ||
        line.trimStart().startsWith(">") ||
        // 表格：本行是表头行且下一行是分隔行才算（普通含管道的段落文本不拆段）
        (isTableRow(line) && next != null && isTableDivider(next))

/** ATX 标题级别（0 = 不是标题；# 后必须有空格或行尾）。 */
private fun headingLevel(line: String): Int {
    val t = line.trimStart()
    if (!t.startsWith("#")) return 0
    var n = 0
    while (n < t.length && t[n] == '#') n++
    if (n > 6) return 0
    if (n == t.length) return n // 整行都是 # 也算标题（空标题）
    return if (t[n] == ' ' || t[n] == '\t') n else 0
}

/** 无序列表行（`- `/`* `/`+ ` + 空格；`*` 后必须有空格避免与斜体歧义）。
 *  `+` 是桌面端 RE_UL 认的第三个 marker（[-*+]）——对齐。 */
private fun isBullet(line: String): Boolean {
    val t = line.trimStart()
    return (t.startsWith("- ") || t.startsWith("* ") || t.startsWith("+ ") ||
        t.startsWith("-\t") || t.startsWith("*\t") || t.startsWith("+\t")) &&
        t.length > 2
}

// ===== 行内解析 =====

/**
 * 行内解析：一遍扫描，处理 `code` / **bold** / *italic* / [text](url) / 裸 URL。
 *
 * 优先级：行内 code 最高（内部不再解析）；其后链接语法；其后粗体；最后斜体。
 * 未闭合标记按字面渲染（流式容错，见 parseMarkdown 的纪律）。
 */
fun parseInline(src: String): List<MdSpan> {
    val spans = mutableListOf<MdSpan>()
    var i = 0
    val plain = StringBuilder()

    fun flushPlain() {
        if (plain.isNotEmpty()) {
            emitWithLinks(plain.toString(), spans)
            plain.clear()
        }
    }

    while (i < src.length) {
        val c = src[i]
        when {
            // 行内 code：闭合的 `...`；未闭合按字面
            c == '`' -> {
                val end = src.indexOf('`', i + 1)
                if (end > i + 1) {
                    flushPlain()
                    spans.add(MdSpan(text = src.substring(i + 1, end), code = true))
                    i = end + 1
                } else {
                    plain.append(c); i++
                }
            }
            // 粗体 `**` 或 `__`（先于斜体判）；闭合才算，未闭合按字面
            (c == '*' && src.startsWith("**", i)) || (c == '_' && src.startsWith("__", i)) -> {
                val marker = src.substring(i, i + 2)
                // 粗斜体 `***x***`：三连标记按整体吃掉（否则两侧各剩一个孤立 *）
                val triple = src.startsWith(marker + marker[0], i)
                val endMarker = if (triple) marker + marker[0] else marker
                val end = src.indexOf(endMarker, i + endMarker.length)
                if (end > i + endMarker.length) {
                    flushPlain()
                    // 内部再跑一遍行内解析（支持嵌套叠加）。粗斜体 `***x***` 时
                    // 内容补一对单标记再内解析——语义 = 粗 + 斜（CommonMark 同款）。
                    val inner = src.substring(i + endMarker.length, end)
                    val innerSrc = if (triple) "*$inner*" else inner
                    for (s in parseInline(innerSrc)) {
                        spans.add(s.copy(bold = true))
                    }
                    i = end + endMarker.length
                } else {
                    plain.append(c); i++
                }
            }
            // 删除线 `~~text~~`（先于斜体判；对齐桌面端 md-del）
            c == '~' && src.startsWith("~~", i) -> {
                val end = src.indexOf("~~", i + 2)
                if (end > i + 2) {
                    flushPlain()
                    spans.add(MdSpan(text = src.substring(i + 2, end), strike = true))
                    i = end + 2
                } else {
                    plain.append(c); i++
                }
            }
            // 斜体 `*` / `_`：闭合且内容非空才算
            (c == '*' || c == '_') && i + 1 < src.length && src[i + 1] != c -> {
                val end = src.indexOf(c, i + 1)
                if (end > i + 1 && src.getOrNull(end + 1) != c) {
                    flushPlain()
                    spans.add(MdSpan(text = src.substring(i + 1, end), italic = true))
                    i = end + 1
                } else {
                    plain.append(c); i++
                }
            }
            // 链接 [text](url)
            c == '[' -> {
                val close = src.indexOf("](", i + 1)
                if (close > i) {
                    val urlEnd = src.indexOf(')', close + 2)
                    if (urlEnd > close + 2) {
                        flushPlain()
                        val url = src.substring(close + 2, urlEnd)
                        val label = src.substring(i + 1, close)
                        if (isHttpUrl(url)) {
                            spans.add(MdSpan(text = label.ifBlank { url }, link = url))
                            i = urlEnd + 1
                        } else {
                            plain.append(src.substring(i, urlEnd + 1))
                            i = urlEnd + 1
                        }
                        continue
                    }
                }
                plain.append(c); i++
            }
            else -> {
                plain.append(c); i++
            }
        }
    }
    flushPlain()
    return spans
}

/** 纯文本段里的裸 URL 识别（http/https），切成 [link span + 文本] 序列。 */
private fun emitWithLinks(text: String, out: MutableList<MdSpan>) {
    var rest = text
    while (true) {
        val m = URL_RE.find(rest) ?: break
        val url = m.value
        if (m.range.first > 0) out.add(MdSpan(rest.substring(0, m.range.first)))
        out.add(MdSpan(text = url, link = url))
        rest = rest.substring(m.range.last + 1)
    }
    if (rest.isNotEmpty()) out.add(MdSpan(rest))
}

/** http/https URL 判定（链接语法里只认这两个 scheme）。 */
private fun isHttpUrl(url: String): Boolean = url.startsWith("http://") || url.startsWith("https://")

/** 裸 URL 正则（http/https 开头，吃非空白与不成对的右括号；中英文标点都作终止符）。 */
private val URL_RE = Regex("""https?://[^\s<>()　]+[^\s<>().,;:!?，。；：！？、　]""")
