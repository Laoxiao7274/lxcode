// 轻量 Markdown 解析器 —— 安卓端自写子集（零新增依赖，与桌面端 markdown.tsx
// 的自写子集语义对齐）。
//
// 支持的子集（用户拍板清单）：
//   * 标题    `# ` ~ `###### `（行级，ATX）
//   * 粗体    `**text**` / `__text__`
//   * 斜体    `*text*` / `_text_`（不与粗体歧义：先匹配粗体）
//   * 行内码  `` `code` ``（内部不再解析其它标记）
//   * 代码块  ``` 围栏（行级；语言标记只丢弃，不做高亮）
//   * 无序列表 `- item` / `* item`（行级，缩进不做嵌套——子集）
//   * 引用    `> text`（行级；连续引用行合并为一段）
//   * 链接    URL 自动识别（http/https，可点击）+ `[text](url)` 形式
//   * 表格不做（拍板：可暂不做）
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
    /** 普通段落（含行内样式与链接）。 */
    data class Paragraph(val spans: List<MdSpan>) : MdBlock

    /** 标题（level 1~6）。 */
    data class Heading(val level: Int, val spans: List<MdSpan>) : MdBlock

    /** 围栏代码块（语言标记已丢弃；内容原样）。 */
    data class CodeBlock(val code: String) : MdBlock

    /** 无序列表（多行合并为一块；每项独立解析行内样式）。 */
    data class BulletList(val items: List<List<MdSpan>>) : MdBlock

    /** 引用（连续行合并；每段解析行内样式）。 */
    data class Quote(val paragraphs: List<List<MdSpan>>) : MdBlock
}

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
            }
            // 标题：1~6 个 # + 空格
            headingLevel(line) > 0 -> {
                val level = headingLevel(line)
                blocks.add(MdBlock.Heading(level, parseInline(line.trimStart().dropWhile { it == '#' }.trim())))
                i++
            }
            // 无序列表：- 或 * + 空格（连续行聚成一块）
            isBullet(line) -> {
                val items = mutableListOf<List<MdSpan>>()
                while (i < lines.size && isBullet(lines[i])) {
                    items.add(parseInline(lines[i].trimStart().drop(1).trim()))
                    i++
                }
                blocks.add(MdBlock.BulletList(items))
            }
            // 引用：> + 可选空格（连续行聚成一块）
            line.trimStart().startsWith(">") -> {
                val paras = mutableListOf<List<MdSpan>>()
                while (i < lines.size && lines[i].trimStart().startsWith(">")) {
                    paras.add(parseInline(lines[i].trimStart().drop(1).trim()))
                    i++
                }
                blocks.add(MdBlock.Quote(paras))
            }
            // 空行：段落分隔（跳过）
            line.isBlank() -> i++
            // 普通段落：连续非空非特殊行聚成一段（中间的换行合成空格——软换行）
            else -> {
                val parts = mutableListOf<String>()
                while (i < lines.size && !isBlockBoundary(lines[i])) {
                    parts.add(lines[i].trim())
                    i++
                }
                blocks.add(MdBlock.Paragraph(parseInline(parts.joinToString("\n"))))
            }
        }
    }
    return blocks
}

/** 是不是块边界（用于段落聚合）：空行 / 围栏 / 标题 / 列表 / 引用开头。 */
private fun isBlockBoundary(line: String): Boolean =
    line.isBlank() ||
        line.trimStart().startsWith("```") ||
        headingLevel(line) > 0 ||
        isBullet(line) ||
        line.trimStart().startsWith(">")

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

/** 无序列表行（`- `/`* ` + 空格；`*` 后必须有空格避免与斜体歧义）。 */
private fun isBullet(line: String): Boolean {
    val t = line.trimStart()
    return (t.startsWith("- ") || t.startsWith("* ") || t.startsWith("-\t") || t.startsWith("*\t")) &&
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
