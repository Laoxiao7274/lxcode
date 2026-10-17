// 会话历史本地缓存（视图加速，不是数据源——远端 history 是事实源，清缓存不丢任何
// 数据）。与 ConnStore（连接信息，SharedPreferences）分开存储：本缓存写 app 私有
// 文件目录 `cache/sessions/<sessionId>.json`，序列化 ThreadBlock + todos + stats。
//
// 策略（2026-12 分页加载配套）：
//   - 打开会话 → 先渲染缓存（秒开）→ 后台拉 chat.history tail:50 → 对比/合并：
//     远端 tail 窗口替换缓存对应段（以缓存记录的 tailAnchorSeq 为界——界之前是
//     已分页加载的更早历史，信任缓存；界之后以远端为准）；
//   - 远端返回即更新缓存（tail 窗口覆盖 + anchor 推进），分页加载的更早部分追加；
//   - 撤回/压缩后的失效语义：以远端为事实源——tail 窗口整段替换缓存尾部；更早的
//     分页段信任缓存（简单口径：后端撤回广播时 Android 端目前没有撤回 UI，压缩
//     检查点作为普通块渲染，两端一致性由「远端返回覆盖 tail 段」保证）；
//   - LRU 淘汰：每会话 2MB / 总量 50MB，超出按 lastAccess 最旧淘汰（lru.json 记
//     lastAccess 时间戳）；
//   - 断网时打开会话：读缓存直接渲染（顶部由 UI 显示「离线，显示缓存」）。
package com.moyunteng.lxcode.remote.net

import android.util.Log
import com.moyunteng.lxcode.remote.model.ThreadBlock
import org.json.JSONArray
import org.json.JSONObject
import java.io.File

/** 一个会话的缓存载荷（blocks + todos + stats + 分页簿记）。 */
data class SessionCacheEntry(
    val sessionId: String,
    val blocks: List<ThreadBlock>,
    /** 历史快照的 todos JSON（缺省 = 没建过清单）。 */
    val todosJson: String? = null,
    /** stats 键的原始 JSON（缺省 = 还没有任何一步）。 */
    val statsJson: String? = null,
    /** tail 窗口最旧一条消息的 seq（「分页加载段 | tail 段」的分界锚点；
     *  0 = 缓存里只有 tail 段 / 还没装载过）。 */
    val tailAnchorSeq: Long = 0,
    /** 已加载到更早多远（最旧一条消息的 seq；0 = 只有 tail 段）。 */
    val oldestSeq: Long = 0,
    /** anchor 之前还有没有更早的历史（远端 has_more；false = 已加载全部）。 */
    val hasMore: Boolean = true,
    /** 缓存写入时间（epoch ms；LRU 展示用，不参与判定）。 */
    val savedAt: Long = 0,
)

/**
 * 会话缓存存取（文件缓存；org.json 序列化——与 wire 层同一套、零新依赖）。
 * 一个实例挂在 RealBackend 上（AppContextHolder 注入 Context；单元测试传 null context
 * 用内存模式——读写都是 no-op，纯逻辑测试打 [SessionCacheCodec]）。
 */
class SessionCache(context: android.content.Context?) {

    private val root: File? = context?.let {
        // 应用私有 cache 目录下的 sessions/：系统空间紧张时可回收（视图加速的定位
        // 与之一致——丢了也只是重新拉一次 history，不丢数据）。
        File(it.cacheDir, "sessions").apply { mkdirs() }
    }

    private val lruFile: File? = root?.let { File(it, "lru.json") }

    // ===== 读 =====

    /** 读一个会话的缓存；没有 / 损坏返回 null（损坏档删掉重拉——不崩、不猜）。 */
    fun load(sessionId: String): SessionCacheEntry? {
        val f = fileOf(sessionId) ?: return null
        if (!f.exists()) return null
        return try {
            SessionCacheCodec.decode(sessionId, f.readText())
        } catch (e: Exception) {
            Log.w(TAG, "会话缓存损坏，丢弃重拉: $sessionId: $e")
            f.delete()
            null
        }
    }

    // ===== 写 =====

    /** 写缓存（原子替换：先写 tmp 再 rename）。写完跑一次 LRU 淘汰。 */
    fun save(entry: SessionCacheEntry) {
        val f = fileOf(entry.sessionId) ?: return
        try {
            val text = SessionCacheCodec.encode(entry)
            if (text.length > PER_SESSION_LIMIT_BYTES) {
                // 超限不写这份缓存（正常会话远小于 2MB；超限说明异常膨胀——直接丢，
                // 下次打开重拉 history，正确性不受影响）。
                Log.w(TAG, "会话缓存超限丢弃: ${entry.sessionId} ${text.length}B")
                return
            }
            val tmp = File(f.parentFile, f.name + ".tmp")
            tmp.writeText(text)
            if (!tmp.renameTo(f)) {
                // rename 失败（Windows 偶发占用）：直接覆盖写，缓存不是关键路径
                f.writeText(text)
                tmp.delete()
            }
            touchLru(entry.sessionId)
            evictIfNeeded()
        } catch (e: Exception) {
            Log.w(TAG, "会话缓存写入失败: ${entry.sessionId}: $e")
        }
    }

    /** 删除一个会话的缓存（会话归档时调用；不删也不影响正确性——远端是事实源）。 */
    fun delete(sessionId: String) {
        fileOf(sessionId)?.delete()
        val lru = readLru().toMutableMap()
        lru.remove(sessionId)
        writeLru(lru)
    }

    // ===== LRU =====

    private fun readLru(): Map<String, Long> {
        val f = lruFile ?: return emptyMap()
        if (!f.exists()) return emptyMap()
        return try {
            val o = JSONObject(f.readText())
            val out = mutableMapOf<String, Long>()
            for (k in o.keys()) out[k] = o.optLong(k, 0L)
            out
        } catch (_: Exception) {
            emptyMap()
        }
    }

    private fun writeLru(m: Map<String, Long>) {
        val f = lruFile ?: return
        try {
            val o = JSONObject()
            m.forEach { (k, v) -> o.put(k, v) }
            f.writeText(o.toString())
        } catch (_: Exception) {
        }
    }

    private fun touchLru(sessionId: String) {
        val m = readLru().toMutableMap()
        m[sessionId] = System.currentTimeMillis()
        writeLru(m)
    }

    /** 总量超 [TOTAL_LIMIT_BYTES] 时按 lastAccess 最旧淘汰（LRU）。 */
    private fun evictIfNeeded() {
        val dir = root ?: return
        var total = dir.listFiles()?.sumOf { it.length() } ?: 0L
        if (total <= TOTAL_LIMIT_BYTES) return
        val lru = readLru().toMutableMap()
        // 按 lastAccess 升序逐个淘汰，直到回到限额内
        val ordered = lru.entries.sortedBy { it.value }
        for ((sid, _) in ordered) {
            if (total <= TOTAL_LIMIT_BYTES) break
            val f = fileOf(sid) ?: continue
            val size = f.length()
            if (f.delete()) {
                total -= size
                lru.remove(sid)
                Log.i(TAG, "LRU 淘汰会话缓存: $sid（省 ${formatBytes(size)}）")
            }
        }
        writeLru(lru)
    }

    private fun fileOf(sessionId: String): File? {
        // sessionId 是后端生成的 `yyyyMMdd-HHmmss-xxxx` 形态（store 侧纪律），不含
        // 路径分隔符；防御性仍拒绝任何分隔符/穿越（缓存文件名 = 会话 id）。
        if (sessionId.isEmpty() || sessionId.contains('/') || sessionId.contains('\\') ||
            sessionId.contains("..")
        ) {
            return null
        }
        val dir = root ?: return null
        return File(dir, "$sessionId.json")
    }

    companion object {
        private const val TAG = "LxSessionCache"

        /** 每会话缓存上限（2MB——超出直接丢弃不写）。 */
        const val PER_SESSION_LIMIT_BYTES = 2L * 1024 * 1024

        /** 全部会话缓存总量上限（50MB；LRU 淘汰）。 */
        const val TOTAL_LIMIT_BYTES = 50L * 1024 * 1024

        /** 字节数 → 人话大小（与 RealBackend.formatBytes 同口径，避免 cyclic 依赖）。 */
        fun formatBytes(n: Long): String = when {
            n < 1024 -> "$n B"
            n < 1024 * 1024 -> "${n / 1024} KB"
            else -> String.format(java.util.Locale.ROOT, "%.1f MB", n / (1024.0 * 1024.0))
        }
    }
}

/** 序列化编解码（纯 JSON、无 Android 依赖——单元测试直接打它）。 */
object SessionCacheCodec {

    fun encode(entry: SessionCacheEntry): String {
        val o = JSONObject()
        o.put("session_id", entry.sessionId)
        o.put("tail_anchor_seq", entry.tailAnchorSeq)
        o.put("oldest_seq", entry.oldestSeq)
        o.put("has_more", entry.hasMore)
        o.put("saved_at", entry.savedAt)
        entry.todosJson?.let { o.put("todos", it) }
        entry.statsJson?.let { o.put("stats", it) }
        val arr = JSONArray()
        entry.blocks.forEach { arr.put(BlockCodec.encode(it)) }
        o.put("blocks", arr)
        return o.toString()
    }

    fun decode(sessionId: String, text: String): SessionCacheEntry {
        val o = JSONObject(text)
        // 存档里的 session_id 与文件名不一致 = 串档（防御性拒绝）
        if (o.optString("session_id") != sessionId) {
            throw IllegalStateException("session_id 不匹配")
        }
        val arr = o.optJSONArray("blocks") ?: JSONArray()
        val blocks = mutableListOf<ThreadBlock>()
        for (i in 0 until arr.length()) {
            blocks.add(BlockCodec.decode(arr.getJSONObject(i)))
        }
        return SessionCacheEntry(
            sessionId = sessionId,
            blocks = blocks,
            todosJson = o.optString("todos", "").takeIf { it.isNotEmpty() },
            statsJson = o.optString("stats", "").takeIf { it.isNotEmpty() },
            tailAnchorSeq = o.optLong("tail_anchor_seq", 0L),
            oldestSeq = o.optLong("oldest_seq", 0L),
            hasMore = o.optBoolean("has_more", true),
            savedAt = o.optLong("saved_at", 0L),
        )
    }
}

/** ThreadBlock ↔ JSON（缓存专用；kind 字段判别 + 逐类字段编解码）。 */
object BlockCodec {

    private const val K_USER = "user"
    private const val K_ASSISTANT = "assistant"
    private const val K_TOOL = "tool"
    private const val K_DISPATCH = "dispatch"
    private const val K_COMPACTION = "compaction"
    private const val K_TODO = "todo"
    private const val K_NOTICE = "notice"
    private const val K_ERROR = "error"

    fun encode(b: ThreadBlock): JSONObject {
        val o = JSONObject()
        when (b) {
            is ThreadBlock.User -> {
                o.put("kind", K_USER)
                o.put("text", b.text)
                o.put("attachments", attachmentsToJson(b.attachments))
            }
            is ThreadBlock.Assistant -> {
                o.put("kind", K_ASSISTANT)
                o.put("content", b.content)
                b.reasoning?.let { o.put("reasoning", it) }
                o.put("reasoning_ms", b.reasoningMs)
                o.put("model", b.model)
                b.firstTokenMs?.let { o.put("first_token_ms", it) }
                b.tokPerSec?.let { o.put("tok_per_sec", it) }
                // streaming 不缓存（缓存是历史定格，恢复时恒 false）
                o.put("streaming", false)
            }
            is ThreadBlock.Tool -> {
                o.put("kind", K_TOOL)
                o.put("id", b.id)
                o.put("name", b.name)
                o.put("title", b.title)
                o.put("args_summary", b.argsSummary)
                o.put("command", b.command)
                o.put("cwd", b.cwd)
                b.output?.let { o.put("output", it) }
                o.put("running", b.running)
                o.put("is_error", b.isError)
                o.put("duration_ms", b.durationMs)
            }
            is ThreadBlock.Dispatch -> {
                o.put("kind", K_DISPATCH)
                o.put("agent_name", b.agentName)
                o.put("task", b.task)
                o.put("conclusion", b.conclusion)
                o.put("session_id", b.sessionId)
                o.put("done", b.done)
                o.put("is_error", b.isError)
                o.put("dispatch_id", b.dispatchId)
                o.put("child_tools", b.childTools)
                o.put("child_todos_done", b.childTodosDone)
                o.put("child_todos_total", b.childTodosTotal)
            }
            is ThreadBlock.Compaction -> {
                o.put("kind", K_COMPACTION)
                o.put("summary", b.summary)
                o.put("shadowed", b.shadowed)
                o.put("before", b.before)
                o.put("after", b.after)
                o.put("manual", b.manual)
            }
            is ThreadBlock.Todo -> {
                o.put("kind", K_TODO)
                val arr = JSONArray()
                b.items.forEach { arr.put(JSONObject().put("content", it.content).put("status", it.status)) }
                o.put("items", arr)
            }
            is ThreadBlock.Notice -> {
                o.put("kind", K_NOTICE)
                o.put("label", b.label)
                o.put("text", b.text)
            }
            is ThreadBlock.Error -> {
                o.put("kind", K_ERROR)
                o.put("message", b.message)
            }
        }
        return o
    }

    fun decode(o: JSONObject): ThreadBlock {
        return when (o.optString("kind")) {
            K_USER -> ThreadBlock.User(
                text = o.optString("text"),
                attachments = attachmentsFromJson(o.optJSONArray("attachments")),
            )
            K_ASSISTANT -> ThreadBlock.Assistant(
                content = o.optString("content"),
                reasoning = o.optString("reasoning", "").takeIf { it.isNotEmpty() },
                reasoningMs = o.optLong("reasoning_ms", 0L),
                model = o.optString("model", ""),
                firstTokenMs = o.optLong("first_token_ms", 0L).takeIf { it > 0 },
                tokPerSec = o.optDouble("tok_per_sec", 0.0).takeIf { it > 0 },
                streaming = false,
            )
            K_TOOL -> ThreadBlock.Tool(
                id = o.optString("id"),
                name = o.optString("name"),
                title = o.optString("title"),
                argsSummary = o.optString("args_summary"),
                command = o.optString("command"),
                cwd = o.optString("cwd"),
                output = o.optString("output", "").takeIf { it.isNotEmpty() },
                running = o.optBoolean("running"),
                isError = o.optBoolean("is_error"),
                durationMs = o.optLong("duration_ms", 0L),
            )
            K_DISPATCH -> ThreadBlock.Dispatch(
                agentName = o.optString("agent_name"),
                task = o.optString("task"),
                conclusion = o.optString("conclusion"),
                sessionId = o.optString("session_id"),
                done = o.optBoolean("done"),
                isError = o.optBoolean("is_error"),
                dispatchId = o.optString("dispatch_id"),
                childTools = o.optInt("child_tools", 0),
                childTodosDone = o.optInt("child_todos_done", -1),
                childTodosTotal = o.optInt("child_todos_total", 0),
            )
            K_COMPACTION -> ThreadBlock.Compaction(
                summary = o.optString("summary"),
                shadowed = o.optInt("shadowed"),
                before = o.optInt("before"),
                after = o.optInt("after"),
                manual = o.optBoolean("manual"),
            )
            K_TODO -> {
                val arr = o.optJSONArray("items") ?: JSONArray()
                val items = (0 until arr.length()).map {
                    val it = arr.getJSONObject(it)
                    com.moyunteng.lxcode.remote.model.TodoItem(
                        it.optString("content"), it.optString("status"),
                    )
                }
                ThreadBlock.Todo(items)
            }
            K_NOTICE -> ThreadBlock.Notice(
                label = o.optString("label"),
                text = o.optString("text"),
            )
            K_ERROR -> ThreadBlock.Error(message = o.optString("message"))
            else -> throw IllegalStateException("未知缓存块类型: ${o.optString("kind")}")
        }
    }

    private fun attachmentsToJson(list: List<com.moyunteng.lxcode.remote.model.AttachmentMeta>): JSONArray {
        val arr = JSONArray()
        list.forEach { a ->
            arr.put(
                JSONObject()
                    .put("path", a.path)
                    .put("name", a.name)
                    .put("size", a.size)
                    .put("mime", a.mime)
                    .put("kind", a.kind)
                    .put("caption", a.caption),
            )
        }
        return arr
    }

    private fun attachmentsFromJson(arr: JSONArray?): List<com.moyunteng.lxcode.remote.model.AttachmentMeta> {
        if (arr == null) return emptyList()
        return (0 until arr.length()).map {
            val o = arr.getJSONObject(it)
            com.moyunteng.lxcode.remote.model.AttachmentMeta(
                path = o.optString("path"),
                name = o.optString("name"),
                size = o.optLong("size", 0L),
                mime = o.optString("mime"),
                kind = o.optString("kind"),
                caption = o.optString("caption"),
            )
        }
    }
}
