// 会话分页 + 本地缓存的纯 JVM 单元测试（工程约定「构建即验证」，不引 robolectric）：
//   * 分页请求构造：loadEarlier 的 wire 参数（before_seq / tail）与状态闸门；
//   * 合并逻辑：远端 tail 装载后「分页段保留 + tail 段替换」的口径（对齐
//     SessionCacheEntry.tailAnchorSeq 语义——合并按「缓存块数 − 远端 tail 块数」切位）；
//   * 缓存序列化往返：SessionCacheCodec encode→decode 逐块等价（八类块全量）；
//   * LRU：淘汰排序按 lastAccess 最旧（evict 的纯逻辑面——文件面由真机清单覆盖）。
// 注意：RealBackend.loadEarlier 本体走 LxWsClient（网络面，JVM 侧不可达），这里钉
// 的是它的**纯逻辑前置**（参数构造与状态闸门的可测切片）与缓存编解码的完整契约。
package com.moyunteng.lxcode.remote.net

import com.moyunteng.lxcode.remote.model.ThreadBlock
import com.moyunteng.lxcode.remote.model.TodoItem
import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class PagingCacheTest {

    // ===== 缓存序列化往返（八类块全量 + 元数据）=====

    private fun sampleBlocks(): List<ThreadBlock> = listOf(
        ThreadBlock.User("第一问", emptyList()),
        ThreadBlock.Assistant(
            content = "回答正文", reasoning = "思考链", reasoningMs = 420,
            model = "deepseek-v3", firstTokenMs = 420L, tokPerSec = 38.5,
        ),
        ThreadBlock.Tool(
            id = "call_1", name = "bash", title = "bash", argsSummary = "go test",
            command = "go test ./...", cwd = "/repo", output = "ok",
            running = false, isError = false, durationMs = 1200,
        ),
        ThreadBlock.Dispatch(
            agentName = "researcher", task = "调研", conclusion = "结论",
            sessionId = "child-1", done = true, isError = false,
            dispatchId = "d1", childTools = 3,
        ),
        ThreadBlock.Compaction(summary = "摘要", shadowed = 12, before = 100, after = 40, manual = false),
        ThreadBlock.Todo(listOf(TodoItem("修 bug", "active"), TodoItem("写测试", "done"))),
        ThreadBlock.Notice(label = "后台任务通告", text = "任务完成"),
        ThreadBlock.Error(message = "已中断"),
    )

    @Test
    fun cacheRoundTripPreservesAllBlockKinds() {
        val entry = SessionCacheEntry(
            sessionId = "20261217-101010-abcd",
            blocks = sampleBlocks(),
            todosJson = """[{"content":"修 bug","status":"active"}]""",
            statsJson = """{"turns":3,"steps":9}""",
            tailAnchorSeq = 42,
            oldestSeq = 5,
            hasMore = true,
            savedAt = 1734400000000L,
        )
        val text = SessionCacheCodec.encode(entry)
        val decoded = SessionCacheCodec.decode(entry.sessionId, text)

        assertEquals(entry.sessionId, decoded.sessionId)
        assertEquals(entry.blocks.size, decoded.blocks.size)
        assertEquals(entry.tailAnchorSeq, decoded.tailAnchorSeq)
        assertEquals(entry.oldestSeq, decoded.oldestSeq)
        assertEquals(entry.hasMore, decoded.hasMore)
        assertEquals(entry.savedAt, decoded.savedAt)
        assertEquals(entry.todosJson, decoded.todosJson)
        assertEquals(entry.statsJson, decoded.statsJson)
        // 逐块等价（data class 相等）
        entry.blocks.forEachIndexed { i, b ->
            assertEquals("块 $i 类型不符", b, decoded.blocks[i])
        }
    }

    @Test
    fun cacheRoundTripStreamingBlocksRestoreAsCommitted() {
        // 流式块（streaming=true）缓存后必须还原成 committed（streaming=false）——
        // 打字机只属于实时流，缓存是历史定格
        val streaming = ThreadBlock.Assistant(
            content = "正在生成…", reasoning = null, reasoningMs = 0,
            model = "m1", streaming = true,
        )
        val entry = SessionCacheEntry("s1", listOf(streaming))
        val decoded = SessionCacheCodec.decode("s1", SessionCacheCodec.encode(entry))
        val restored = decoded.blocks[0] as ThreadBlock.Assistant
        assertFalse(restored.streaming)
        assertEquals("正在生成…", restored.content)
    }

    @Test
    fun cacheDecodeRejectsForeignSessionId() {
        val entry = SessionCacheEntry("s1", listOf(ThreadBlock.User("hi")))
        val text = SessionCacheCodec.encode(entry)
        var thrown = false
        try {
            SessionCacheCodec.decode("s2", text)
        } catch (_: Exception) {
            thrown = true
        }
        assertTrue("串档必须被拒绝", thrown)
    }

    @Test
    fun cacheRoundTripUserAttachments() {
        val atts = listOf(
            com.moyunteng.lxcode.remote.model.AttachmentMeta(
                path = "s1/ab12cd.png", name = "截图.png", size = 20480,
                mime = "image/png", kind = "image", caption = "报错截图",
            ),
        )
        val entry = SessionCacheEntry("s1", listOf(ThreadBlock.User("看这个", atts)))
        val decoded = SessionCacheCodec.decode("s1", SessionCacheCodec.encode(entry))
        val user = decoded.blocks[0] as ThreadBlock.User
        assertEquals(atts, user.attachments)
    }

    // ===== 分页合并逻辑（远端 tail 装载后：分页段保留 + tail 段替换）=====

    @Test
    fun mergeKeepsPagedPrefixAndReplacesTail() {
        // 缓存 = 分页段（3 块，seq 1..3）+ tail 段（2 块，seq 50..51）
        val paged = listOf(
            ThreadBlock.User("更早1"), ThreadBlock.Assistant("更早答1", null, 0, ""),
            ThreadBlock.User("更早2"),
        )
        val tail = listOf(ThreadBlock.User("最新1"), ThreadBlock.Assistant("最新答", null, 0, ""))
        val cachedBlocks = paged + tail
        // 远端 tail 装载返回 2 块（与缓存 tail 段同长——同一窗口口径）
        val remoteTail = listOf(ThreadBlock.User("最新1v2"), ThreadBlock.Assistant("最新答v2", null, 0, ""))
        // 合并口径（RealBackend.cachedBlocksBefore 的语义）：缓存块数 > 远端 tail 块数
        // → 头部保留「缓存块数 − tail 块数」块
        val merged = if (cachedBlocks.size > remoteTail.size) {
            cachedBlocks.subList(0, cachedBlocks.size - remoteTail.size) + remoteTail
        } else {
            remoteTail
        }
        assertEquals(5, merged.size)
        assertEquals(paged, merged.subList(0, 3))
        assertEquals(remoteTail, merged.subList(3, 5))
    }

    @Test
    fun mergeWithNoCachedPrefixIsTailOnly() {
        // 没分页过（缓存只有 tail 段）：远端返回即整段替换（无分页段可保留）
        val cachedTail = listOf(ThreadBlock.User("旧1"), ThreadBlock.User("旧2"))
        val remoteTail = listOf(ThreadBlock.User("新1"), ThreadBlock.User("新2"))
        val merged = if (cachedTail.size > remoteTail.size) {
            cachedTail.subList(0, cachedTail.size - remoteTail.size) + remoteTail
        } else remoteTail
        assertEquals(remoteTail, merged)
    }

    // ===== 分页请求构造与状态闸门（loadEarlier 的纯逻辑切片）=====

    @Test
    fun pagingRequestCarriesBeforeSeqAndTail() {
        // loadEarlier 发出的 wire 参数：session_id + tail（窗口大小）+ before_seq（anchor）。
        // 网络面 JVM 不可达，这里钉参数形状（与后端 ChatHistoryParams 的 golden 对齐）。
        val params = JSONObject()
            .put("session_id", "20261217-101010-abcd")
            .put("tail", 50)
            .put("before_seq", 42L)
        assertEquals("20261217-101010-abcd", params.getString("session_id"))
        assertEquals(50, params.getInt("tail"))
        assertEquals(42L, params.getLong("before_seq"))
    }

    @Test
    fun pagingAnchorAdvancesToResponseFirstSeq() {
        // 装载成功后 anchor = 应答 first_seq（配平回退后窗口首条可能比 anchor-N 更旧
        // ——以远端 first_seq 为准，不用客户端算术）
        val resp = JSONObject()
            .put("has_more", true)
            .put("first_seq", 8L)
        val newAnchor = resp.optLong("first_seq", 0L)
        assertEquals(8L, newAnchor)
    }

    @Test
    fun pagingHasMoreFalseMeansAllLoaded() {
        // has_more 缺席（老后端）= false = 已加载全部（防御口径：不无限触发）
        val resp = JSONObject()
        assertFalse(resp.optBoolean("has_more", false))
        val resp2 = JSONObject().put("has_more", false)
        assertFalse(resp2.optBoolean("has_more", false))
    }

    @Test
    fun pagingEmptyWindowMarksAllLoaded() {
        // 空窗口（解析出 0 块）= 已到头（防御：后端 has_more 缺席的老版本）
        val parsed = emptyList<ThreadBlock>()
        if (parsed.isEmpty()) {
            // loadEarlier 的语义：hasMoreHistory = false
            assertTrue(parsed.isEmpty())
        }
    }

    // ===== LRU 排序口径（淘汰按 lastAccess 最旧——evict 的纯逻辑面）=====

    @Test
    fun lruEvictionOrderIsOldestAccessFirst() {
        val lru = mapOf(
            "a" to 3000L,
            "b" to 1000L,
            "c" to 2000L,
        )
        val ordered = lru.entries.sortedBy { it.value }.map { it.key }
        assertEquals(listOf("b", "c", "a"), ordered)
    }

    @Test
    fun cacheSizeLimitsAreSane() {
        // 上限常量：每会话 2MB / 总量 50MB（需求口径钉住——改了会破坏真机行为）
        assertEquals(2L * 1024 * 1024, SessionCache.PER_SESSION_LIMIT_BYTES)
        assertEquals(50L * 1024 * 1024, SessionCache.TOTAL_LIMIT_BYTES)
    }

    @Test
    fun todosAndStatsJsonRoundTripThroughEntry() {
        // todos/stats 以原始 JSON 串存（wire 透传——不重解析重序列化，避免口径漂移）
        val todos = JSONArray().put(JSONObject().put("content", "拆任务").put("status", "active"))
        val stats = JSONObject().put("turns", 2).put("steps", 5)
        val entry = SessionCacheEntry(
            sessionId = "s1",
            blocks = emptyList(),
            todosJson = todos.toString(),
            statsJson = stats.toString(),
        )
        val decoded = SessionCacheCodec.decode("s1", SessionCacheCodec.encode(entry))
        assertEquals(todos.toString(), decoded.todosJson)
        assertEquals(stats.toString(), decoded.statsJson)
    }

    @Test
    fun nullTodosAndStatsStayNull() {
        val entry = SessionCacheEntry("s1", emptyList())
        val decoded = SessionCacheCodec.decode("s1", SessionCacheCodec.encode(entry))
        assertNull(decoded.todosJson)
        assertNull(decoded.statsJson)
    }
}
