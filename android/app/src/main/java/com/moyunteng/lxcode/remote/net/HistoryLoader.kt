package com.moyunteng.lxcode.remote.net

import android.util.Log
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.moyunteng.lxcode.remote.model.ChildTool
import com.moyunteng.lxcode.remote.model.DISPATCH_INTERRUPTED
import com.moyunteng.lxcode.remote.model.AttachmentMeta
import com.moyunteng.lxcode.remote.model.ContextUsage
import com.moyunteng.lxcode.remote.model.EFFORT_IDS
import com.moyunteng.lxcode.remote.model.ModelGroup
import com.moyunteng.lxcode.remote.model.ModelMeta
import com.moyunteng.lxcode.remote.model.ProjectMeta
import com.moyunteng.lxcode.remote.model.RunningDispatchInfo
import com.moyunteng.lxcode.remote.model.SessionMeta
import com.moyunteng.lxcode.remote.model.SessionStats
import com.moyunteng.lxcode.remote.model.ThreadBlock
import com.moyunteng.lxcode.remote.model.TodoItem
import com.moyunteng.lxcode.remote.model.todoItemOf
import com.moyunteng.lxcode.remote.model.applyRunningDispatches
import com.moyunteng.lxcode.remote.model.isDispatchFinal
import com.moyunteng.lxcode.remote.model.isDispatchPlaceholder
import com.moyunteng.lxcode.remote.model.mergeChildLiveTail
import com.moyunteng.lxcode.remote.model.mergeChildTodoList
import com.moyunteng.lxcode.remote.model.mergeKnownChildIds
import com.moyunteng.lxcode.remote.model.parseModelList
import org.json.JSONArray
import org.json.JSONObject
import java.time.Instant
import java.time.temporal.ChronoUnit

/** History loading, parsing, cache merging and session-list reduction for the RealBackend facade. */
internal class HistoryLoader(private val owner: RealBackend) {
    // ===== 历史装载 =====

    /** chat.history → 块（含 checkpoints → 压缩块、pending → 挂起确认）。
     *  打开流程（2026-12 缓存秒开）：先把缓存渲染出来（如果有），再拉远端 tail——
     *  远端返回后 tail 窗口替换缓存尾部（分页加载的更早部分保留）。 */
    internal fun loadHistory(id: String) {
        with(owner) {
        // 先切再拉：currentSessionId 必须在**发起请求前**就指向目标会话——否则
        // 回调里的过期响应守卫（id != currentSessionId）会把这次响应整个丢掉，
        // 从 A 切 B 永远切不动（A 开着时点 B，resume 成功但 history 被丢弃，
        // 界面停在 A——本批 e2e 实测抓到）。
        if (id != currentSessionId) switchTo(id)
        // 缓存秒开：有缓存先渲染（快于任何网络往返）；远端回来后按 tailAnchorSeq
        // 合并。缓存块与远端块的「分界」由 anchor 保证——anchor 之前的块是历史
        // 分页段（信任缓存），之后整段以远端为准。
        val cached = sessionCache.load(id)
        cachedBlocks = emptyList()
        cachedOldestSeq = null
        if (cached != null && cached.blocks.isNotEmpty()) {
            blocks.clear()
            blocks.addAll(cached.blocks)
            cached.statsJson?.let { sj ->
                try {
                    sessionStats = parseStats(JSONObject(sj))
                } catch (_: Exception) {
                }
            }
            oldestLoadedSeq = cached.oldestSeq
            hasMoreHistory = cached.hasMore
            // 记住缓存段：远端 tail 回来后 anchor 之前（= 分页段）要合并保留
            cachedBlocks = cached.blocks
            if (cached.oldestSeq < cached.tailAnchorSeq) cachedOldestSeq = cached.oldestSeq
            showingCached = true
            // 缓存装载也算一次「历史就绪」：ThreadScreen 滚到底（进会话停在最底）。
            historyVersion++
            Log.i(RealBackend.TAG, "cache($id): ${cached.blocks.size} 块 anchor=${cached.tailAnchorSeq}")
        }
        // 慢链路优化（2026-10 公网性能排查）：先拉尾部 RealBackend.TAIL_WINDOW 条（公网 frp
        // 隧道限速 10 Mbit/s，大会话全量 1.4~1.7MB 要秒级传输），进会话即刻可见；
        // tail 是可选参数、缺省全量（wire 向后兼容，老后端忽略未知键）。
        client.call("chat.history", JSONObject().put("session_id", id).put("tail", RealBackend.TAIL_WINDOW)) { err, result ->
            if (err != null) {
                Log.w(RealBackend.TAG, "chat.history($id) 失败: ${err.optString("message")}")
                if (id != currentSessionId) return@call
                if (showingCached) {
                    // 缓存已在屏上：不插整屏 Error（那会把秒开的缓存盖掉）——
                    // 只标记离线展示态，UI 顶部出「离线，显示缓存」提示条。
                    showingCached = true
                    return@call
                }
                // 失败不能静默（原实现只 log——用户看到的就是白屏「加载不出来」）：
                // 插一块 Error 给人话反馈；重试 = 返回后重新进入会话（openSession 重拉）。
                blocks.clear()
                blocks.add(ThreadBlock.Error("历史加载失败: ${err.optString("message")}（返回后重新进入可重试）"))
                historyVersion++
                return@call
            }
            // 请求在途时用户又点了别的会话：currentSessionId 已指向新目标，
            // 这份旧响应作废（守卫语义保留，判定对象不变）
            if (id != currentSessionId) return@call
            applyHistory(result as? JSONObject)
        }
    
        }}

    /**
     * 向上翻页：拉取已加载窗口（oldestLoadedSeq）之前更早 RealBackend.TAIL_WINDOW 条
     * （chat.history 的 before_seq 分页——内核 HistoryBefore 负责配平与检查点平移）。
     *
     * 状态机：loadingEarlier 置位（UI spinner）→ 成功后新块**插入头部** +
     * oldestLoadedSeq 推进 + earlierVersion++（ThreadScreen 做不跳屏补偿）；
     * has_more=false → hasMoreHistory=false（「已加载全部历史」）；失败 →
     * loadEarlierError 置位（UI 重试按钮），loadingEarlier 复位。
     * 忙碌会话也可翻页（读路径不冲突）；重复触发被 loadingEarlier 闸住。
     */
    fun loadEarlier() {
        with(owner) {
        if (loadingEarlier) return
        val id = currentSessionId
        val anchor = oldestLoadedSeq
        if (id.isEmpty() || anchor <= 0 || !hasMoreHistory) return
        loadingEarlier = true
        loadEarlierError = null
        client.call(
            "chat.history",
            JSONObject()
                .put("session_id", id)
                .put("tail", RealBackend.TAIL_WINDOW)
                .put("before_seq", anchor),
        ) { err, result ->
            loadingEarlier = false
            if (id != currentSessionId) return@call
            if (err != null) {
                Log.w(RealBackend.TAG, "chat.history(before_seq=$anchor) 失败: ${err.optString("message")}")
                loadEarlierError = err.optString("message").ifBlank { "加载失败" }
                return@call
            }
            val r = result as? JSONObject ?: return@call
            val (parsed, _, _, _) = parseHistoryBlocks(r)
            if (parsed.isEmpty()) {
                // 空窗口 = 已到头（防御：后端 has_more 缺席的老版本）
                hasMoreHistory = false
                earlierVersion++
                return@call
            }
            // 插入头部（LazyColumn 顶部）；seq 薄记推进到新窗口首条（分页应答的
            // first_seq 是权威值——配平回退后窗口首条可能比 anchor-N 更旧）
            blocks.addAll(0, parsed)
            val newFirstSeq = r.optLong("first_seq", 0L)
            if (newFirstSeq > 0) oldestLoadedSeq = newFirstSeq
            hasMoreHistory = r.optBoolean("has_more", false)
            loadEarlierError = null
            earlierVersion++
            Log.i(RealBackend.TAG, "loadEarlier($id): +${parsed.size} 块 has_more=$hasMoreHistory anchor=$oldestLoadedSeq")
            saveHistoryCache(id, currentTodosJson(), currentStatsJson())
        }
        }
    }

    /** 当前 blocks 里的 Todo 块序列化（缓存落盘用；无 = null）。 */
    internal fun currentTodosJson(): String? {
        with(owner) {
        val todo = blocks.firstOrNull { it is ThreadBlock.Todo } as? ThreadBlock.Todo ?: return null
        val arr = JSONArray()
        todo.items.forEach { arr.put(JSONObject().put("content", it.content).put("status", it.status)) }
        return arr.toString()
    
        }}

    /** 当前 stats 序列化（缓存落盘用；无 = null）。 */
    internal fun currentStatsJson(): String? = with(owner) { try {
        sessionStats?.let { s ->
            JSONObject()
                .put("turns", s.turns)
                .put("steps", s.steps)
                .put("llm_ms", s.llmMs)
                .put("tool_ms", s.toolMs)
                .put("ttft_ms", s.ttftMs)
                .put("ttft_steps", s.ttftSteps)
                .put("decode_ms", s.decodeMs)
                .put("decode_tokens", s.decodeTokens)
                .put("input_tokens", s.inputTokens)
                .put("cache_read_tokens", s.cacheReadTokens)
                .put("cache_write_tokens", s.cacheWriteTokens)
                .put("output_tokens", s.outputTokens)
                .put("legacy_tokens", s.legacyTokens)
                .toString()
        }
    } catch (_: Exception) {
        null
    } }

    internal fun switchTo(id: String) {
        with(owner) {        if (currentSessionId == id) return
        // 切走前把旧会话防抖窗口里的草稿立即落库（竞态防线：否则 800ms 窗口里的
        // 最后状态会跟着新会话的 flush 一起发错地方）。
        flushDraft()
        currentSessionId = id
        blocks.clear()
        reducers.streamingIdx = -1
        // 已收尾派发集合是"这份时间线"的簿记：切会话即清（否则上一条会话的
        // dispatch_id 会在这里越积越多，且语义上属于另一份时间线）
        owner.reducers.endedDispatches.clear()
        confirm = null
        busy = false
        contextUsage = null
        sessionStats = null
        sessionModel = ""
        // 会话级模型同款：切会话即清（切回时由 chat.history 的 model 重新对齐）
        // ——沿用上一会话的模型显示是串台
        pickedModelId = ""
        // 权限档是会话级状态：切会话即清（切回时由 chat.history 的 approval 重新
        // 对齐）——沿用上一会话的档位显示是串台
        approval = null
        // 历史装载版本也清零：新会话的历史还没装载，滚动触发条件（historyVersion>0）
        // 必须等下一次 applyHistory 才成立——沿用上一会话的版本号会让 ThreadScreen
        // 在 blocks 还是空的时候去滚
        historyVersion = 0
        // 分页薄记同会话复位（上一会话的 anchor/has_more/错误状态不能带过来）
        oldestLoadedSeq = 0
        hasMoreHistory = true
        loadingEarlier = false
        loadEarlierError = null
        showingCached = false
    
        }}

    /** 解析 chat.history 的 messages → 块列表 + todos JSON + 首条消息 seq（主会话与
     *  子会话视图共用）。首条 seq 是分页薄记的 anchor（老后端缺 seq = 0）。
     *  第四个返回值 = 「窗口外派发结果剥出的子会话 id」（callId → childSid）：
     *  派发卡被 tail 窗口截掉时，结果正文里的「[子会话 id: …]」提示行是卡重建后
     *  兜底补 id 的最后一道来源（applyHistory 里并进 mergeKnownChildIds 的口径）。 */
    internal fun parseHistoryBlocks(r: JSONObject?): Quad<List<ThreadBlock>, org.json.JSONArray?, Long, Map<String, String>> {
        with(owner) {
        if (r == null) return Quad(emptyList(), null, 0L, emptyMap())
        val msgs = r.optJSONArray("messages") ?: return Quad(emptyList(), null, 0L, emptyMap())
        val firstSeq = msgs.optJSONObject(0)?.optLong("seq", 0L) ?: 0L
        val checkpoints = r.optJSONArray("checkpoints")
            ?.let { cp -> (0 until cp.length()).map { cp.optInt(it) } }
            ?.toSet() ?: emptySet()
        // tool 消息先到调用块后到结果的配对表（历史里调用在前、结果紧随）
        val toolCallIdx = mutableMapOf<String, Int>()
        val dispatchCallIdx = mutableMapOf<String, Int>()
        // 窗口外的派发结果剥出的子会话 id（callId → childSid）：卡被 tail 截掉时
        // dispatchCallIdx 里没有它的调用——applyRunningDispatches 的兜底第三来源。
        val orphanChildIds = mutableMapOf<String, String>()
        val newBlocks = mutableListOf<ThreadBlock>()
        for (i in 0 until msgs.length()) {
            val m = msgs.getJSONObject(i)
            val role = m.optString("role")
            if (i in checkpoints) {
                newBlocks.add(
                    ThreadBlock.Compaction(
                        summary = m.optString("content"),
                        shadowed = 0,
                        before = 0,
                        after = 0,
                        manual = false,
                    ),
                )
                continue
            }
            when (role) {
                "user" -> {
                    // 通告前缀识别（对齐桌面端 history.ts 的 notices 分支）：
                    // hidden 注记不产生任何块；通告渲染成提示条，其余才是用户气泡。
                    val content = m.optString("content")
                    if (content.startsWith(RealBackend.HIDDEN_NOTICE_PREFIX)) continue
                    val notice = RealBackend.noticeKind(content)
                    if (notice != null) {
                        newBlocks.add(ThreadBlock.Notice(label = notice.second, text = content.removePrefix(notice.first).trim()))
                    } else {
                        newBlocks.add(ThreadBlock.User(content, parseAttachments(m.optJSONArray("attachments"))))
                    }
                }
                "assistant" -> {
                    val ftms = m.optLong("first_token_ms", 0L)
                    val dur = m.optLong("duration_ms", 0L)
                    val utok = m.optInt("usage_tokens", 0)
                    if (m.optString("content").isNotEmpty() || !m.optString("reasoning_content").isBlank()) {
                        newBlocks.add(
                            ThreadBlock.Assistant(
                                content = m.optString("content"),
                                reasoning = m.optString("reasoning_content").ifBlank { null },
                                reasoningMs = ftms,
                                model = m.optString("model", ""),
                                firstTokenMs = if (ftms > 0) ftms else null,
                                tokPerSec = if (utok > 0 && dur > 0) utok * 1000.0 / dur else null,
                            ),
                        )
                    }
                    val calls = m.optJSONArray("tool_calls")
                    if (calls != null) {
                        for (k in 0 until calls.length()) {
                            val c = calls.getJSONObject(k)
                            val fn = c.optJSONObject("function") ?: continue
                            val callId = c.optString("id")
                            val name = fn.optString("name")
                            val args = fn.optString("arguments")
                            // 调度调用重建的是**派发卡**，不是工具行（对齐桌面端 history.ts
                            // 的 DISPATCH_TOOL_NAME 分支）——回放建工具行的后果：每次刷新
                            // 派发都退化成一张 4000 字 output 的全文工具卡，卡、Agent 名、
                            // 子会话 id 全没了。先按"中断"定格，配对结果到了再回填。
                            if (name == "agent_dispatch") {
                                val parsed = try {
                                    JSONObject(args)
                                } catch (_: Exception) {
                                    null
                                }
                                dispatchCallIdx[callId] = newBlocks.size
                                newBlocks.add(
                                    ThreadBlock.Dispatch(
                                        agentName = parsed?.optString("agent").orEmpty().ifBlank { "子 Agent" },
                                        task = parsed?.optString("task").orEmpty(),
                                        // 先按"中断"定格：历史里没有配对结果 = 那次派发在
                                        // 结束前就断了，不是"正在跑"（不许假装 running）。
                                        // 结果到了（tool 分支）再回填成 done + 正文。
                                        conclusion = DISPATCH_INTERRUPTED,
                                        sessionId = parsed?.optString("session").orEmpty(),
                                        done = true,
                                        isError = true,
                                        // 归属键 = 这次工具调用的 id（后端 DispatchCall.DispatchID
                                        // = tc.ID，与 dispatchStart/End 的 dispatch_id 同源）：
                                        // 有了它，本轮稍后到达的实时 dispatchStart 才能按 id
                                        // **复用**这张卡（而不是并排再长一张），tool 结果分支也
                                        // 才能精确回填。老代码这里留空 = 每次刷新后卡与实时事件
                                        // 断链。
                                        dispatchId = callId,
                                    ),
                                )
                                continue
                            }
                            toolCallIdx[callId] = newBlocks.size
                            newBlocks.add(
                                ThreadBlock.Tool(
                                    id = callId,
                                    name = name,
                                    title = RealBackend.toolTitle(name),
                                    argsSummary = RealBackend.argsSummary(args),
                                    command = RealBackend.argsCommand(args),
                                    cwd = RealBackend.argsCwd(args),
                                    output = null,
                                    running = false,
                                    isError = false,
                                    durationMs = m.optLong("duration_ms", 0L),
                                ),
                            )
                        }
                    }
                }

                "tool" -> {
                    val callId = m.optString("tool_call_id")
                    // ① 调度卡回填：结果正文剥掉「[子会话 id: …]」提示行（给模型看的
                    //    续跑线索），子会话 id 从提示行取（权威值）；正文空 = 没有文本结论。
                    val dIdx = dispatchCallIdx[callId]
                    if (dIdx != null && dIdx < newBlocks.size) {
                        val d = newBlocks[dIdx] as ThreadBlock.Dispatch
                        val (body, childSid) = RealBackend.splitDispatchResult(m.optString("content"))
                        val atts = parseAttachments(m.optJSONArray("attachments"))
                        newBlocks[dIdx] = d.copy(
                            conclusion = body.ifBlank { "（子 Agent 没有产出文本结论）" },
                            sessionId = childSid.ifBlank { d.sessionId },
                            done = true,
                            isError = false,
                            // 子会话共享到对话的附件（调度结果消息上的元数据——与
                            // 实时 toolResult 同一份形状）：卡上渲染成果条（桌面端
                            // historyBlocks 的 card.attachments 同语义）。
                            attachments = if (atts.isNotEmpty()) atts else d.attachments,
                        )
                    } else if (dispatchCallIdx.containsKey(callId)) {
                        // ①b 派发卡被 tail 窗口截掉、只剩这条结果：从正文剥出子会话 id
                        //    兜底记进配对表——稍后 applyRunningDispatches 用它补回放卡
                        //    （老后端快照键缺席时的最后一道兜底；不编造，提示行缺失不动）。
                        val (_, childSid) = RealBackend.splitDispatchResult(m.optString("content"))
                        if (childSid.isNotEmpty()) orphanChildIds[callId] = childSid
                    } else {
                        // ② 普通工具行：调用在前、结果紧随，按 id 配对回填
                        val idx = toolCallIdx[callId]
                        if (idx != null && idx < newBlocks.size) {
                            val t = newBlocks[idx] as ThreadBlock.Tool
                            val atts = parseAttachments(m.optJSONArray("attachments"))
                            newBlocks[idx] = t.copy(
                                output = m.optString("content").take(4_000),
                                isError = false,
                                durationMs = m.optLong("duration_ms", t.durationMs),
                                // share_file 共享的附件（结果消息上的元数据——与实时
                                // toolResult 同一份形状，桌面端 historyBlocks 同语义）
                                attachments = if (atts.isNotEmpty()) atts else t.attachments,
                            )
                        }
                    }
                }
            }
        }
        // 历史快照的任务清单（todos 原样返回，调用方决定插不插 Todo 块）
        val todosArr = r.optJSONArray("todos")?.takeIf { it.length() > 0 }
        return Quad(newBlocks.toList(), todosArr, firstSeq, orphanChildIds.toMap())
    
        }}

    /** 四元组（Triple 的扩展——parseHistoryBlocks 还要带「窗口外剥出的子会话 id」）。 */
    internal data class Quad<A, B, C, D>(val first: A, val second: B, val third: C, val fourth: D)

    internal fun applyHistory(r: JSONObject?) {
        with(owner) {
        if (r == null) return
        val (parsed, todosArr, firstSeq, orphanChildIds) = parseHistoryBlocks(r)
        // 分页薄记：tail 段首条消息的 seq 是「tail 窗口」的锚点——缓存合并时 anchor
        // 之前的块（历史分页段）保留、之后整段以远端为准。缺 seq（老后端）= 记 0
        //（缓存退化为整段替换——正确性不受影响，只是丢掉分页复用）。
        val tailAnchor = firstSeq
        val cacheTodos = todosArr?.toString()
        val cacheStats = r.optJSONObject("stats")?.toString()
        val hadEarlierBlocks = cachedOldestSeq != null && tailAnchor > 0 &&
            (cachedOldestSeq ?: 0L) < tailAnchor
        // 子会话 id 的跨重载记忆：历史里只有消息（没有 dispatchStart），正在跑
        // 的派发在历史里既没有结果提示行、也没有子会话 id——直接整段替换的话，
        // 用户打开这个会话就看不到任何能进子会话的入口（问题 2）。把上一份块里
        // 同一 dispatch_id 已知的 id 合并回来（只补空值，不编造）。
        // 第三来源：窗口外的派发结果正文剥出的 id（parseHistoryBlocks 的
        // orphanChildIds）——卡被 tail 截掉时它可能是唯一线索，同口径「只补空缺」。
        val knownChildIds = blocks.toList()
        val previousBlocks = if (orphanChildIds.isEmpty()) knownChildIds else {
            knownChildIds + orphanChildIds.map { (callId, sid) ->
                ThreadBlock.Dispatch(
                    agentName = "", task = "", conclusion = "",
                    sessionId = sid, done = true, isError = false, dispatchId = callId,
                )
            }
        }
        val merged = mergeKnownChildIds(parsed, previousBlocks)
        // 观察端回填（chat.history 的 running_dispatches，2026-12 用户报「安卓
        // 点不开电脑发起的子会话」的主修）：历史回放扫不出正在跑的派发（结果
        // 提示行要派发结束才落库、dispatchStart 是本机不在场时错过的事件）——
        // 后端按**此刻的运行中登记**导出快照，按 dispatch_id 补卡/补 id。
        // 回放占位卡被恢复成运行中（后端事实，比「只认最后一张」的 busy 启发式
        // 强——多派发并行时每张都能恢复）；已收尾的真终态卡绝不被翻回。
        val runningDispatches = owner.reducers.parseRunningDispatches(r.optJSONArray("running_dispatches"))
        val filled = applyRunningDispatches(merged, runningDispatches, previousBlocks)
        // 已收尾集合属于"当前这一份时间线"：重载之后旧记录无意义（且不清理会
        // 随会话长度无限增长）
        owner.reducers.endedDispatches.clear()
        blocks.clear()
        blocks.addAll(filled)
        reducers.streamingIdx = -1
        // 历史快照的任务清单（ChatHistoryResult.todos——对齐桌面端 history-snapshot
        // 的 todos 装载）：非空插一块 Todo（当前清单的定格视图，随后的 todo.updated
        // 会找到并替换它）；缺席/空 = 不插（老后端或没建过清单——不编空块）。
        todosArr?.let { arr ->
            val list = (0 until arr.length()).map { arr.getJSONObject(it) }
                .map { todoItemOf(it.optString("content"), it.optString("status")) }
            blocks.add(ThreadBlock.Todo(list))
        }
        r.optJSONObject("context")?.let { contextUsage = owner.reducers.parseContext(it) }
        r.optJSONObject("stats")?.let { sessionStats = parseStats(it) }
        sessionModel = r.optString("model", "")
        // 模型菜单的选中态：快照里带的 model 是**后端解析后**的（会话选择 > Agent
        // 绑定 > default 角色）。老后端不带键 = 未知，保持空（显示回落 default）。
        pickedModelId = sessionModel
        busy = r.optBoolean("busy")
        // 历史的 busy 键是服务端事实：同步进会话级忙闲投影（列表状态点对齐）
        if (currentSessionId.isNotEmpty()) {
            sessionBusy[currentSessionId] = busy
            syncRunningDots()
        }
        // 卡里的占位态同样按快照修正：本会话在跑 + **最后一张**派发卡还是"中断"
        // 占位 = 那次派发其实正在跑（子事件可能还没到，而 history 的 busy 已经是
        // 服务端事实）。只认最后一张：更早的占位卡是历史上真断掉的派发，本会话
        // 后来的忙碌与它无关——把它们一起点亮就是"把中断说成在跑"。
        if (busy) {
            blocks.indexOfLast { it is ThreadBlock.Dispatch && (it as ThreadBlock.Dispatch).done }
                .takeIf { it >= 0 }
                ?.let { owner.reducers.markDispatchRunning((blocks[it] as ThreadBlock.Dispatch).dispatchId) }
        }
        // 该会话此刻的权限档（后端 LiveApproval——会话级状态持久化的显示依据）；
        // 空串 = 老后端没报，保持 null（UI 回落「默认」，不猜）
        r.optString("approval", "").takeIf { it.isNotEmpty() }?.let { approval = it }
        // 落库草稿（chat.draft 写的，多端共享）：**键有值且内存无该会话记账**时才
        // 预填——缺席/空串 = 后端没存过，不能清掉本端内存里还没落库的草稿（后写
        // 覆盖语义下，缺席 = 后端没话语权）。发送清空路径已经把内存记账清了，
        // 这里只在「换端/重启捡回别人的草稿」时生效。
        r.optString("draft", "").takeIf { it.isNotEmpty() }?.let { remote ->
            if ((sessionDrafts[currentSessionId] ?: "").isEmpty()) {
                sessionDrafts[currentSessionId] = remote
            }
        }
        r.optJSONObject("pending")?.let { pend ->
            confirm = PendingConfirm(
                sessionId = pend.optString("session_id", currentSessionId),
                id = pend.optString("id"),
                name = pend.optString("name"),
                arguments = pend.optString("arguments"),
                prompt = pend.optString("prompt"),
            kind = pend.optString("kind"),
            options = pend.optJSONArray("options")?.let { a ->
                (0 until a.length()).map { a.optString(it) }
            } ?: emptyList(),
            dispatchId = pend.optString("dispatch_id", ""),
            )
        }
        Log.i(RealBackend.TAG, "history($currentSessionId): ${parsed.size} 条消息 → ${blocks.size} 块 busy=$busy")
        // 分页薄记 + 缓存落盘（在 historyVersion++ 之前——UI 读到的状态要一次到位）：
        // 远端 tail 窗口替换缓存尾部（tailAnchor 之后整段以远端为准），anchor 之前的
        // 分页段（打开会话时缓存秒开的部分）保留——缓存与远端合并口径。
        if (hadEarlierBlocks) {
            val earlier = cachedBlocksBefore(tailAnchor)
            if (earlier.isNotEmpty()) {
                blocks.addAll(0, earlier)
            }
            oldestLoadedSeq = cachedOldestSeq ?: tailAnchor
        } else {
            oldestLoadedSeq = tailAnchor
        }
        hasMoreHistory = oldestLoadedSeq > 0 // tail 首条 > 0 即可能还有更早的（hasMore 由分页应答精化）
        showingCached = false
        saveHistoryCache(currentSessionId, cacheTodos, cacheStats)
        // 历史装载完成：ThreadScreen 据此滚动到底（进会话停在最底，对齐桌面端）
        historyVersion++
    
        }}

    /** 缓存合并薄记：loadHistory 渲染缓存时记住「分页段块列表」与「分页段最旧
     *  seq」——远端 tail 回来后把分页段合并回头部（tail 段以远端为准）。 */
    internal var cachedBlocks: List<ThreadBlock> = emptyList()
    internal var cachedOldestSeq: Long? = null

    internal fun cachedBlocksBefore(anchor: Long): List<ThreadBlock> {
        with(owner) {
        if (cachedBlocks.isEmpty() || anchor <= 0) return emptyList()
        // 分页段 = 缓存里 anchor 之前的部分（缓存的 blocks 布局就是「分页段 + tail 段」，
        // tail 段的长度 == 本次远端 tail 装载的块数——按位置从尾部对齐切除 tail 段）
        val tailCount = blocks.size
        return if (cachedBlocks.size > tailCount) {
            cachedBlocks.subList(0, cachedBlocks.size - tailCount)
        } else {
            emptyList()
        }
    
        }}

    /** 当前 blocks 落缓存（会话 id + 分页薄记一起存）。Todo 块不缓存（todos 单独
     *  存 JSON——它的更新走 todo.updated 事件，块列表快照里的 Todo 是定格视图）。 */
    internal fun saveHistoryCache(sessionId: String, todosJson: String?, statsJson: String?) {
        with(owner) {
        if (sessionId.isEmpty()) return
        val snapshot = blocks.toList().filterNot { it is ThreadBlock.Todo || it is ThreadBlock.Error }
        // tailAnchor = 分页段与 tail 段的分界（最旧已加载 seq）：缓存合并按
        // 「缓存块数 − 远端 tail 块数」的位置切分——anchor 语义等价记录
        val anchor = oldestLoadedSeq
        sessionCache.save(
            SessionCacheEntry(
                sessionId = sessionId,
                blocks = snapshot.filterNot { it is ThreadBlock.Todo },
                todosJson = todosJson,
                statsJson = statsJson,
                tailAnchorSeq = anchor,
                oldestSeq = anchor,
                hasMore = hasMoreHistory,
                savedAt = System.currentTimeMillis(),
            ),
        )
    
        }}

    /** 子会话视图的历史装载（与主会话同一解析器；副作用落在 child* 状态上）。 */
    internal fun applyChildHistory(r: JSONObject?) {
        with(owner) {
        val (parsed, todosArr, _, _) = parseHistoryBlocks(r)
        // 模型选中态回读（对齐桌面端 history.ts 读 h.model 的口径 / 主会话
        // applyHistory 的 sessionModel 落法）：chat.history 顶层的 model 键是
        // 后端**解析后**的模型（会话选择 > Agent 绑定 > default 角色）。不读它的
        // 后果：重开子会话页 childModelId 恒空起步，药丸回落 default 角色——
        // 落库的显式选择显示不出来（用户看到「没切上」）。老后端不带键 = 未知，
        // 保持现状不编。
        r?.optString("model", "")?.takeIf { it.isNotEmpty() }?.let { childModelId = it }
        // 重载**不许抹掉本地实时内容**（2026-12 用户报「子 Agent 正文输出后消失」）：
        // assistant 是整轮结束才落库的，正在流式的那段永远不在 chat.history 里；
        // 而子会话的 chat.history 走「现建运行时 + AttachTo」，busy 恒 false——
        // 不能拿快照的 busy 判「本地有没有实时块」。判定与去重见 ChildHistoryMerge.kt。
        val prevBlocks = childBlocks.toList()
        val snapshotTodos = todosArr?.let { arr ->
            (0 until arr.length()).map { arr.getJSONObject(it) }
                .map { todoItemOf(it.optString("content"), it.optString("status")) }
        }
        val merged = mergeChildLiveTail(parsed, prevBlocks)
        childBlocks.clear()
        childBlocks.addAll(merged)
        // 流式块的续写下标：接回来的尾巴就是尾块（否则后续 delta 会另开一块）。
        childStreamingIdx = if ((merged.lastOrNull() as? ThreadBlock.Assistant)?.streaming == true) {
            merged.size - 1
        } else {
            -1
        }
        // 清单：空快照 = 未知（omitempty），沿用上一份——否则「重开视图清单消失」。
        // 落**会话级状态**（置底清单条数据源），不再往时间线插 Todo 块。
        // 沿用口径收窄到**同一子会话**（childTodoOwner）：切换子会话视图时上一份
        // 清单不属于这个子会话，不许串台。
        childTodos = mergeChildTodoList(snapshotTodos, childTodos.takeIf { childTodoOwner == viewChildSessionId })
        childTodoOwner = viewChildSessionId ?: ""
        // 忙闲：快照的 busy 键对子会话**不可信**（chat.history 是现建运行时 + AttachTo，
        // busy 恒 false）。接回了流式尾部 = 它确实在跑——别把「生成中」指示抹掉。
        // 翻回 false 仍由轮末那条 chat.busy=false 负责（它无条件发，不会漏）。
        val liveTail = childStreamingIdx >= 0
        childBusy = (r?.optBoolean("busy") ?: false) || liveTail
        r?.optJSONObject("pending")?.let { pend ->
            childConfirm = PendingConfirm(
                sessionId = pend.optString("session_id", viewChildSessionId ?: ""),
                id = pend.optString("id"),
                name = pend.optString("name"),
                arguments = pend.optString("arguments"),
                prompt = pend.optString("prompt"),
            kind = pend.optString("kind"),
            options = pend.optJSONArray("options")?.let { a ->
                (0 until a.length()).map { a.optString(it) }
            } ?: emptyList(),
            dispatchId = pend.optString("dispatch_id", ""),
            )
        }
        Log.i(RealBackend.TAG, "child history($viewChildSessionId): ${parsed.size} 条消息 → ${childBlocks.size} 块 busy=$childBusy")
        // 装载完成：ThreadScreen 据此把子会话视图滚到底
        childHistoryVersion++
    
        }}


    internal fun applySessionList(r: Any?) {
        with(owner) {
        // session.list 的 result 是**裸数组**（toProtocolSessionList 直接回 []SessionMeta）
        val arr: org.json.JSONArray? = when (r) {
            is org.json.JSONArray -> r
            is JSONObject -> r.optJSONArray("sessions")
            else -> null
        } ?: return
        val list = (0 until arr!!.length()).mapNotNull { idx ->
            val o = arr.getJSONObject(idx)
            val id = o.optString("id")
            if (id.isEmpty()) return@mapNotNull null
            // 快照说在跑 → 顺手把投影补成 true：否则下一次 syncRunningDots 会按
            // 空投影把它抹掉（状态点闪一下就灭），也覆盖不到「连上时已经在跑」。
            if (o.optBoolean("running")) sessionBusy[id] = true
            SessionMeta(
                id = id,
                title = o.optString("title").ifBlank { "新对话" },
                updatedAt = RealBackend.relativeTime(o.optString("updated_at")),
                messages = o.optInt("messages"),
                archived = o.optBoolean("archived"),
                workspace = o.optString("workspace", ""),
                // 运行中状态点 = **服务端事实**（session.list 的 running 位，
                // 与 hello 的 busy_sessions 同源，见 internal/server/dispatch_sessions.go）
                // **或** chat.busy 广播的会话级投影。
                //
                // 两个来源都要认：running 位覆盖「连上时那轮已经在跑」（客户端错过
                // 了 busy=true 广播）；投影覆盖「列表拉到之后才开跑/跑完」（新广播
                // 到，列表不必重拉）。只认投影 = 冷启动/重连后状态点不亮（用户报的
                // 「必须点进去再退出才亮」的其中一条路径）；只认快照 = 广播到了
                // 状态点不动。
                running = o.optBoolean("running") || sessionBusy[id] == true,
            )
        }.filter { !it.archived }
        sessions.clear()
        sessions.addAll(list)
        Log.i(RealBackend.TAG, "session.list → ${list.size} 条")
    
        }}

    internal fun applyProjectList(r: Any?) {
        with(owner) {
        // project.list 的 result 是**裸数组**（dispatchProjects 直接回 []ProjectMeta：
        // {id,name,path}；workspace 字段是项目 id 而不是路径——见 SessionMeta.workspace）
        val arr: org.json.JSONArray? = when (r) {
            is org.json.JSONArray -> r
            is JSONObject -> r.optJSONArray("projects")
            else -> null
        } ?: return
        val list = (0 until arr!!.length()).mapNotNull { idx ->
            val o = arr.getJSONObject(idx)
            val id = o.optString("id")
            if (id.isEmpty()) return@mapNotNull null
            ProjectMeta(id = id, name = o.optString("name"), path = o.optString("path"))
        }
        projects.clear()
        projects.addAll(list)
        Log.i(RealBackend.TAG, "project.list → ${list.size} 个: ${list.joinToString { it.name }}")
    
        }}

    /** 会话列表的运行中状态点随 busy 事件刷新（SessionMeta 是不可变值，重建条目）。
     *
     *  两条路径都靠它：
     *   ① `chat.busy`（任何会话，不只当前打开的）——投影变了就重算；
     *   ② `session.list` 回来时（[applySessionList] 直接按投影填 running）。
     *  投影本身是 Compose 可观察的（mutableStateMapOf），所以「列表还没拉到 / 该会话
     *  还不在列表里」时投影变化照样能驱动读它的 UI（项目行计数等），不靠这里重建条目。 */
    internal fun syncRunningDots() {
        with(owner) {
        for (i in sessions.indices) {
            val s = sessions[i]
            val running = sessionBusy[s.id] == true
            if (s.running != running) sessions[i] = s.copy(running = running)
        }
    
        }}


}
