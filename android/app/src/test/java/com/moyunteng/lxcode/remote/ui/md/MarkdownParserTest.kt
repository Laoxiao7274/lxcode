// Markdown 解析器 JUnit 测试 —— 纯 Kotlin（无 Android 依赖），android/app/src/test。
// 覆盖：标题/粗斜体/行内码/代码块/列表/引用/链接/裸 URL/流式容错（未闭合标记）。
package com.moyunteng.lxcode.remote.ui.md

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class MarkdownParserTest {

    // ===== 标题 =====

    @Test
    fun headingLevel() {
        val blocks = parseMarkdown("## 二级标题")
        assertEquals(1, blocks.size)
        val h = blocks[0] as MdBlock.Heading
        assertEquals(2, h.level)
        assertEquals("二级标题", h.spans.single().text)
    }

    @Test
    fun hashWithoutSpaceIsNotHeading() {
        val blocks = parseMarkdown("#tag 不是标题")
        assertTrue(blocks[0] is MdBlock.Paragraph)
        assertEquals("#tag 不是标题", (blocks[0] as MdBlock.Paragraph).spans.single().text)
    }

    @Test
    fun moreThanSixHashesIsPlainText() {
        val blocks = parseMarkdown("####### 七个")
        assertTrue(blocks[0] is MdBlock.Paragraph)
    }

    // ===== 粗体 / 斜体 =====

    @Test
    fun boldWithDoubleAsterisk() {
        val spans = parseInline("前 **粗体** 后")
        assertEquals(3, spans.size)
        assertTrue(spans[1].bold)
        assertFalse(spans[1].italic)
        assertEquals("粗体", spans[1].text)
    }

    @Test
    fun italicWithSingleAsterisk() {
        val spans = parseInline("*斜体*")
        assertEquals(1, spans.size)
        assertTrue(spans[0].italic)
        assertFalse(spans[0].bold)
    }

    @Test
    fun tripleAsteriskIsBoldItalic() {
        val spans = parseInline("***x***")
        assertEquals(1, spans.size)
        assertTrue(spans[0].bold)
        assertTrue(spans[0].italic)
    }

    @Test
    fun unclosedBoldRendersLiterally() {
        // 流式容错：半截标记按字面渲染
        val spans = parseInline("正在 **加粗")
        assertEquals(1, spans.size)
        assertEquals("正在 **加粗", spans[0].text)
        assertFalse(spans[0].bold)
    }

    // ===== 行内 code =====

    @Test
    fun inlineCodeNotFurtherParsed() {
        val spans = parseInline("`**不是粗体**`")
        assertEquals(1, spans.size)
        assertTrue(spans[0].code)
        assertEquals("**不是粗体**", spans[0].text)
        assertFalse(spans[0].bold)
    }

    @Test
    fun unclosedBacktickRendersLiterally() {
        val spans = parseInline("流式中的 `code")
        assertEquals("流式中的 `code", spans.single().text)
        assertFalse(spans.single().code)
    }

    // ===== 代码块 =====

    @Test
    fun fencedCodeBlockWithLanguageTag() {
        val blocks = parseMarkdown("```kotlin\nval x = 1\n```")
        assertEquals(1, blocks.size)
        assertEquals("val x = 1", (blocks[0] as MdBlock.CodeBlock).code)
    }

    @Test
    fun unclosedFenceConsumesToEnd() {
        val blocks = parseMarkdown("```bash\nls -la")
        assertEquals(1, blocks.size)
        assertEquals("ls -la", (blocks[0] as MdBlock.CodeBlock).code)
    }

    // ===== 列表 / 引用 =====

    @Test
    fun bulletListGroupsConsecutiveLines() {
        val blocks = parseMarkdown("- 第一项\n- 第二项\n\n普通段落")
        assertEquals(2, blocks.size)
        val list = blocks[0] as MdBlock.BulletList
        assertEquals(listOf("第一项", "第二项"), list.items.map { it.single().text })
        assertTrue(blocks[1] is MdBlock.Paragraph)
    }

    @Test
    fun asteriskBulletWorks() {
        val blocks = parseMarkdown("* 项")
        val list = blocks[0] as MdBlock.BulletList
        assertEquals("项", list.items.single().single().text)
    }

    @Test
    fun quoteGroupsConsecutiveLines() {
        val blocks = parseMarkdown("> 引用一\n> 引用二")
        assertEquals(1, blocks.size)
        val q = blocks[0] as MdBlock.Quote
        assertEquals(2, q.paragraphs.size)
        assertEquals("引用一", q.paragraphs[0].single().text)
    }

    // ===== 链接 =====

    @Test
    fun markdownLinkSyntax() {
        val spans = parseInline("看 [文档](https://example.com/a) 明白")
        assertEquals(3, spans.size)
        assertEquals("文档", spans[1].text)
        assertEquals("https://example.com/a", spans[1].link)
        assertNullLink(spans[0])
        assertNullLink(spans[2])
    }

    @Test
    fun bareUrlBecomesLink() {
        val spans = parseInline("访问 https://example.com/x?q=1 即可")
        assertEquals(3, spans.size)
        assertEquals("https://example.com/x?q=1", spans[1].link)
        assertEquals(spans[1].link, spans[1].text)
    }

    @Test
    fun trailingPunctuationNotPartOfUrl() {
        val spans = parseInline("见 https://example.com/a。")
        val link = spans.first { it.link != null }
        assertEquals("https://example.com/a", link.link)
    }

    @Test
    fun nonHttpLinkSyntaxStaysLiteral() {
        val spans = parseInline("[x](ftp://y)")
        assertEquals("[x](ftp://y)", spans.single().text)
    }

    // ===== 段落聚合 =====

    @Test
    fun paragraphJoinsConsecutiveLines() {
        val blocks = parseMarkdown("第一行\n第二行")
        assertEquals(1, blocks.size)
        val p = blocks[0] as MdBlock.Paragraph
        // 软换行保留 \n（渲染层 Text 原样换行）
        assertEquals("第一行\n第二行", p.spans.single().text)
    }

    @Test
    fun emptyInputGivesEmptyList() {
        assertTrue(parseMarkdown("").isEmpty())
    }

    private fun assertNullLink(s: MdSpan) {
        assertTrue(s.link == null)
    }
}
