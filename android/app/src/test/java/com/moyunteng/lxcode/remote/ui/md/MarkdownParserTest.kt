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
        val list = blocks[0] as MdBlock.Listing
        assertFalse(list.ordered)
        assertEquals(listOf("第一项", "第二项"), list.items.map { it.spans.single().text })
        assertEquals(listOf(0, 0), list.items.map { it.level })
        assertTrue(blocks[1] is MdBlock.Paragraph)
    }

    @Test
    fun asteriskBulletWorks() {
        val blocks = parseMarkdown("* 项")
        val list = blocks[0] as MdBlock.Listing
        assertEquals("项", list.items.single().spans.single().text)
    }

    @Test
    fun plusBulletWorks() {
        // 桌面端 RE_UL 认 `+ `（[-*+]）——对齐
        val blocks = parseMarkdown("+ 项")
        val list = blocks[0] as MdBlock.Listing
        assertEquals("项", list.items.single().spans.single().text)
    }

    // ===== 有序列表（桌面端 RE_OL 有——补齐）=====

    @Test
    fun orderedListWithDotMarker() {
        val blocks = parseMarkdown("1. 第一\n2. 第二")
        assertEquals(1, blocks.size)
        val list = blocks[0] as MdBlock.Listing
        assertTrue(list.ordered)
        assertEquals(listOf("第一", "第二"), list.items.map { it.spans.single().text })
    }

    @Test
    fun orderedListWithParenMarker() {
        // 桌面端 RE_OL 认 `1)`——对齐
        val blocks = parseMarkdown("1) 甲\n2) 乙")
        val list = blocks[0] as MdBlock.Listing
        assertTrue(list.ordered)
        assertEquals(listOf("甲", "乙"), list.items.map { it.spans.single().text })
    }

    @Test
    fun plainNumberDotIsNotAList() {
        // 「3.14 是圆周率」不是列表：数字后必须跟 . + 空格
        val blocks = parseMarkdown("3.14 是圆周率")
        assertTrue(blocks[0] is MdBlock.Paragraph)
    }

    // ===== 嵌套列表（缩进 2 空格一级——桌面端 MdListItem.level 同口径）=====

    @Test
    fun nestedBulletListIndentLevels() {
        val blocks = parseMarkdown("- 外层\n  - 内层\n    - 更深")
        assertEquals(1, blocks.size)
        val list = blocks[0] as MdBlock.Listing
        assertEquals(listOf(0, 1, 2), list.items.map { it.level })
        assertEquals("外层", list.items[0].spans.single().text)
        assertEquals("内层", list.items[1].spans.single().text)
        assertEquals("更深", list.items[2].spans.single().text)
    }

    @Test
    fun mixedListTypesSplitIntoBlocks() {
        // 异类列表行开新块（对齐桌面端：if (!m) break）
        val blocks = parseMarkdown("- 无序\n1. 有序")
        assertEquals(2, blocks.size)
        assertFalse((blocks[0] as MdBlock.Listing).ordered)
        assertTrue((blocks[1] as MdBlock.Listing).ordered)
    }

    // ===== 分割线（桌面端 RE_HR 有——补齐）=====

    @Test
    fun horizontalRules() {
        for (src in listOf("---", "***", "___", "-----")) {
            val blocks = parseMarkdown("上\n$src\n下")
            assertEquals(3, blocks.size)
            assertTrue("src=$src", blocks[1] is MdBlock.Hr)
        }
    }

    @Test
    fun shortOrMixedDashesAreNotHr() {
        // 不足 3 个 / 混合符号：不是分割线（对齐桌面端 RE_HR）
        assertTrue(parseMarkdown("--\n")[0] is MdBlock.Paragraph)
        assertTrue(parseMarkdown("-*-\n")[0] is MdBlock.Paragraph)
    }

    // ===== 删除线（桌面端 md-del 有——补齐）=====

    @Test
    fun strikethroughRendersAsStrikeSpan() {
        val spans = parseInline("~~不要了~~")
        assertEquals(1, spans.size)
        assertTrue(spans[0].strike)
        assertEquals("不要了", spans[0].text)
    }

    @Test
    fun singleTildeIsLiteral() {
        // 单个 ~ 不是删除线标记（桌面端 RE 要求双 ~）
        val spans = parseInline("近似 ~5kg")
        assertEquals(1, spans.size)
        assertFalse(spans[0].strike)
        assertEquals("近似 ~5kg", spans[0].text)
    }

    @Test
    fun unclosedStrikethroughIsLiteral() {
        // 流式容错：未闭合的 ~~ 按字面
        val spans = parseInline("正在 ~~删除")
        assertEquals(1, spans.size)
        assertFalse(spans[0].strike)
    }

    // ===== 段落段距（gap——桌面端 .md-para 语义）=====

    @Test
    fun paragraphAfterBlankLineCarriesGap() {
        val blocks = parseMarkdown("第一段\n\n第二段")
        assertEquals(2, blocks.size)
        assertFalse((blocks[0] as MdBlock.Paragraph).gap)
        assertTrue((blocks[1] as MdBlock.Paragraph).gap)
    }

    @Test
    fun firstParagraphHasNoGap() {
        val blocks = parseMarkdown("开头段")
        assertFalse((blocks[0] as MdBlock.Paragraph).gap)
    }

    @Test
    fun paragraphAfterHrCarriesGap() {
        // 分割线是块级边界——后续段落带段距（桌面端 parseBlocks 同语义）
        val blocks = parseMarkdown("---\n段")
        assertTrue(blocks[0] is MdBlock.Hr)
        assertTrue((blocks[1] as MdBlock.Paragraph).gap)
    }

    @Test
    fun quoteGroupsConsecutiveLines() {
        val blocks = parseMarkdown("> 引用一\n> 引用二")
        assertEquals(1, blocks.size)
        val q = blocks[0] as MdBlock.Quote
        assertEquals(2, q.paragraphs.size)
        assertEquals("引用一", q.paragraphs[0].single().text)
    }

    // ===== 代码块内换行保留 =====

    @Test
    fun codeBlockPreservesBlankLines() {
        val blocks = parseMarkdown("```\nline1\n\nline3\n```")
        assertEquals("line1\n\nline3", (blocks[0] as MdBlock.CodeBlock).code)
    }

    // ===== 桌面端没有的语法：不做的口径钉住 =====

    @Test
    fun taskListSyntaxIsPlainListItem() {
        // 桌面端 RE_UL 不认 `- [ ]`——按普通列表项渲染，不发明任务列表
        val blocks = parseMarkdown("- [ ] 待办")
        val list = blocks[0] as MdBlock.Listing
        assertEquals("[ ] 待办", list.items.single().spans.single().text)
    }

    @Test
    fun tableRowIsPlainParagraph() {
        // 表格第一版不补（手机窄屏横向滚动体验差，差异已记录）——管道行按普通段落
        val blocks = parseMarkdown("| a | b |\n|---|---|\n| 1 | 2 |")
        assertEquals(1, blocks.size)
        assertTrue(blocks[0] is MdBlock.Paragraph)
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
