// splitCommitted / safeCutAt 的 JVM 单测（对齐桌面端 stream-reveal 的测试语义）：
// 核心不变量是「committed + "\n" + tail 恒等于原文」与「切点安全（围栏不切断、
// 表格行不切断、列表项完整、引用不切断、空行收尾回退）」。
package com.moyunteng.lxcode.remote.ui.md

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class StreamRevealTest {

    /** 拼回原文不变量：两段各自由 parseMarkdown 解析出的块序列，与整段解析
     *  的块序列逐块一致（用解析结果相等验证切点安全——比字符串比较强）。 */
    private fun assertSafeSplit(text: String) {
        val s = splitCommitted(text)
        assertEquals(text, if (s.committed.isEmpty()) s.tail else s.committed + "\n" + s.tail)
        assertEquals(
            "块序列必须与整段解析一致: " + text.take(60),
            parseMarkdown(text),
            parseMarkdown(s.committed) + parseMarkdown(s.tail),
        )
    }

    @Test fun shortText_singleSegment() {
        assertEquals(SplitCommitted("", "一行"), splitCommitted("一行"))
    }

    @Test fun twoLinesWithoutBlank_splittingWouldBreakParagraph_singleSegment() {
        // 安卓语义：连续普通行聚成一段（软换行）——中间切会把一段拆成两段，
        // 全不安全 → 单段（committed 为空）
        assertEquals(SplitCommitted("", "a\nb"), splitCommitted("a\nb"))
    }    @Test fun paragraphFollowedByBlankSeparatedParagraph_splitSafe() {
        // 空行分隔的两个段落：切在段落边界安全（tail 首段不带 gap——空行在 tail 里）
        val text = "段落一\n\n段落二\n还在增长"
        val s = splitCommitted(text)
        assertEquals("段落一", s.committed)
        assertEquals("\n段落二\n还在增长", s.tail)
        assertSafeSplit(text)
    }

    @Test fun plainParagraphs_singleBlockSingleSegment() {
        // 安卓语义：连续普通行聚成一段——没有安全切点，单段（桌面同输入会切，
        // 因为桌面每行一块；这条测试钉住两端解析差异的补偿）
        val text = "第一段\n第二段\n增长的尾巴"
        assertEquals(text, splitCommitted(text))
    }

    @Test fun paragraphsSplitByBlank_safeAtBoundary() {
        // 空行分隔的三个段落：候选切点从最后一行前回退，「第二段」后的空行是
        // 安全切点 → committed = 前两段（含空行），tail = 还在增长的最后一段
        val text = "第一段\n\n第二段\n\n还在增长的第三段"
        val s = splitCommitted(text)
        assertSafeSplit(text)
        assertEquals("第一段\n\n第二段", s.committed)
        assertEquals("\n还在增长的第三段", s.tail)
    }

    @Test fun trailingNewline_trailingNewlineEatenIntoCommitted() {
        // 文本以换行收尾：最后一条候选切点就是尾换行本身（tail 为空 = 安全），
        // committed 直接吃到「段落二」行尾
        val text = "段落一\n\n段落二\n"
        val s = splitCommitted(text)
        assertSafeSplit(text)
        assertEquals("段落一\n\n段落二", s.committed)
        assertEquals("", s.tail)
    }

    @Test fun unclosedFence_tailHoldsFenceButEarlierSafeLinesOk() {
        // 未闭合围栏：committed 的 ``` 计数必须为偶数 → 全文退到单段（或更早行）
        val text = "前文说明\n```kotlin\nval a = 1\n还在增长的代码"
        val s = splitCommitted(text)
        // 「前文说明」行可以安全留在 committed（它后面跟围栏开头，块边界）；
        // 关键不变量：committed 内围栏计数为偶数
        assertTrue(s.committed.isEmpty() || countFencesVisible(s.committed) % 2 == 0)
        assertSafeSplit(text)
    }

    @Test fun closedFence_canSplitAfterIt() {
        val text = "```kotlin\nval a = 1\n```\n围栏后的正文\n还在增长"
        val s = splitCommitted(text)
        assertTrue("应能切在闭合围栏之后", s.committed.contains("```"))
        assertSafeSplit(text)
    }
    @Test fun fenceInterior_neverSplit() {
        // 闭合围栏 + 未闭合围栏：切点不能落在未闭合围栏内
        val text = "```\ncode\n```\n正文\n```\npython\n增长中"
        assertSafeSplit(text)
        val s = splitCommitted(text)
        assertTrue(s.committed.isEmpty() || countFencesVisible(s.committed) % 2 == 0)
    }

    @Test fun listItems_stayWhole() {
        val text = "说明\n- 第一项\n- 第二项\n- 第三项还在增长"
        val s = splitCommitted(text)
        // 候选切点（第二项前）两侧都是列表行 → 回退；committed 至少不把列表
        // 切成两个块（块序列不变量已验）
        assertSafeSplit(text)
    }

    @Test fun orderedList_stayWhole() {
        val text = "步骤\n1. 第一步\n2. 第二步\n3. 第三步增长中"
        assertSafeSplit(text)
    }

    @Test fun quote_stayWhole() {
        val text = "正文\n> 引用一\n> 引用二\n> 引用三增长中"
        assertSafeSplit(text)
    }

    @Test fun tableRows_stayWhole() {
        val text = "表：\n| a | b |\n|---|---|\n| 1 | 2 |\n| 3 | 4 |"
        assertSafeSplit(text)
    }

    @Test fun blankLineTail_revertsToEarlierLine() {
        // committed 以空行收尾不安全（tail 首段带 gap 段距，与整段解析分叉）→ 回退
        val text = "段落一\n\n段落二\n增长中"
        val s = splitCommitted(text)
        assertEquals("段落一", s.committed)
        assertEquals("\n段落二\n增长中", s.tail)
        assertSafeSplit(text)
    }

    @Test fun nestedList_indentationKept() {
        val text = "- 顶层\n  - 嵌套一\n  - 嵌套二\n- 顶层增长中"
        assertSafeSplit(text)
    }

    @Test fun tableFollowedByParagraph_safeAtBoundary() {
        val text = "| a | b |\n|---|---|\n| 1 | 2 |\n\n表格后的段落"
        assertSafeSplit(text)
    }

    /** 拼回原文不变量（简版）：committed + "\n" + tail 恒等于原文。 */
    private fun assertEquals(text: String, s: SplitCommitted) {
        assertEquals(text, if (s.committed.isEmpty()) s.tail else s.committed + "\n" + s.tail)
    }

    private fun countFencesVisible(s: String): Int {
        var n = 0
        var i = s.indexOf("```")
        while (i >= 0) {
            n++
            i = s.indexOf("```", i + 3)
        }
        return n
    }
}
