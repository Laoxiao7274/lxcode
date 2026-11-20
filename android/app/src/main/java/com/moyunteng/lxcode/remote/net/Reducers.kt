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

/** Main-session and child-session event reduction. State remains owned by the RealBackend facade. */
internal class Reducers(private val owner: RealBackend) {
    // ===== 事件归约 =====

    /** 当前流式 assistant 块的下标（-1 = 无；toolCall/done 后归 -1，下一步开新块）。 */
    internal var streamingIdx = -1

    internal fun onEvent(method: String, p: JSONObject) {
        with(owner) {
        // ===== 助手域分流（assistant.*，协议见 internal/protocol/assistant.go）=====
        // payload 与 chat.* 逐字段同构、只换事件名（assistantEventName 的映射表）——
        // 归一成 chat.* 等价名后走**同一份**块语义（assistantRouteEvent），数据桶
        // 换助手域自己的（asst.*）——与主域 currentSessionId 过滤互不污染。
        // 未知 assistant.* 事件名不猜归属（对齐桌面端 mapEvent：表里没有就忽略）。
        if (method.startsWith("assistant.")) {
            val chat = RealBackend.ASSISTANT_TO_CHAT[method]
            if (chat != null) assistantRouteEvent(chat, p)
            return
        }
        val dispatchId = p.optString("dispatch_id", "")
        // 确认门先行：子 Agent 的确认请求**必带 dispatch_id**（协议契约 protocol.ConfirmRequest、
        // dispatch_ws_test.go 钉住），不能进下面的子会话丢弃分支——否则真实模式（主 Agent 唯一
        // 工具是 agent_dispatch，高危确认全来自子派发）确认弹窗永不出现、会话挂死。
        // 归属照桌面端契约（AGENTS.md §2.3）：子确认按 request.dispatch_id 归到本会话的派发卡，
        // 主会话自己的确认（dispatch_id 空）仍按 session_id 归属；裁决经 resolveConfirm 原样回
        // tool.confirm（session_id 取载荷里的子会话 id）。
        // 投屏状态变化（screen.changed，internal/protocol/screen.go）：接收者断开/
        // 壳异常退出时后端会推——投屏页据此把 UI 收回「未投屏」并提示原因。
        if (method == "screen.changed") {
            applyScreenState(p.optJSONObject("state") ?: return)
            return
        }
        if (method == "chat.confirmRequest") {
            if (dispatchId.isNotEmpty()) {
                if (dispatchCardIdx(dispatchId) < 0) {
                    Log.w(RealBackend.TAG, "confirmRequest 无归属派发卡（非当前会话的子派发？）dispatch=$dispatchId")
                    return
                }
            } else if (p.optString("session_id", "") != currentSessionId) {
                return
            }
            confirm = PendingConfirm(
                sessionId = p.optString("session_id", ""),
                id = p.optString("id"),
                name = p.optString("name"),
                arguments = p.optString("arguments"),
                prompt = p.optString("prompt"),
            kind = p.optString("kind"),
            options = p.optJSONArray("options")?.let { a ->
                (0 until a.length()).map { a.optString(it) }
            } ?: emptyList(),
            )
            // 子会话视图打开时双投确认（子会话页内的确认请求正常弹——契约要求）。
            // 归属判定同样要看**卡上的子会话 id**：dispatch_id 是工具调用 id，
            // 与子会话 id 不相等（旧代码直接比两者，普通派发的子确认因此永远
            // 弹不到子会话视图里）。
            if (dispatchId.isNotEmpty() && dispatchChildSessionOf(dispatchId) == viewChildSessionId) {
                childConfirm = confirm
            }
            return
        }
        // 派发卡事件（dispatchStart/End）自身也带 dispatch_id，但归属键是 owner_session_id——
        // 必须先于下面的子会话过滤器处理，否则派发卡建不出来、后续子事件全部无归属。
        if (method == "chat.dispatchStart" || method == "chat.dispatchEnd") {
            if (p.optString("owner_session_id") != currentSessionId) return
            if (method == "chat.dispatchStart") {
                // todos 非空 = 主 Agent 预拆的清单初稿（todo 契约——卡上第一秒显示计划）
                val todos = p.optJSONArray("todos")
                val (tDone, tTotal) = if (todos != null) {
                    val list = (0 until todos.length()).map { todos.getJSONObject(it) }
                    list.count { it.optString("status") == "done" } to list.size
                } else {
                    -1 to 0
                }
                // **建卡与是否有工具调用无关**：dispatchStart 一到就挂卡（一行摘要 +
                // 「查看子会话 ›」）。子 Agent 只输出文字、一个工具都没调的派发同样
                // 需要一个进得去的入口——卡只在 toolCall 里建/显示的话，那种派发在
                // 主时间线上完全不可见（用户报的现象）。
                //
                // 去重：历史回放（parseHistoryBlocks）已经从 assistant 的
                // agent_dispatch 调用建过一张同 dispatch_id 的卡（实时与回放同一张脸）；
                // 本轮实时事件又到一次 dispatchStart 时**复用**它（把"中断"占位改回
                // 运行中 + 回填实时字段），绝不并排长两张卡。dispatch_id 为空的
                // 老载荷没有归属键，只能按「最后一张未完结卡」兜底。
                val cardId = p.optString("dispatch_id", "")
                val idx = if (cardId.isNotEmpty()) {
                    dispatchCardIdx(cardId)
                } else {
                    blocks.indexOfLast { it is ThreadBlock.Dispatch && it.done.not() }
                }
                val fresh = ThreadBlock.Dispatch(
                    agentName = p.optString("agent_name"),
                    task = p.optString("task"),
                    conclusion = "",
                    sessionId = p.optString("session_id", ""),
                    done = false,
                    isError = false,
                    dispatchId = cardId,
                    childTodosDone = tDone,
                    childTodosTotal = tTotal,
                )
                if (idx >= 0) {
                    val prev = blocks[idx] as ThreadBlock.Dispatch
                    blocks[idx] = prev.copy(
                        agentName = fresh.agentName.ifBlank { prev.agentName },
                        task = fresh.task.ifBlank { prev.task },
                        // 子会话 id：实时载荷是权威值（回放时可能只有 arguments.session）
                        sessionId = fresh.sessionId.ifBlank { prev.sessionId },
                        // 重新开跑（续跑同一 dispatch_id）：状态回到运行中，旧结论清掉
                        done = false,
                        isError = false,
                        conclusion = "",
                        dispatchId = cardId.ifBlank { prev.dispatchId },
                        childTodosDone = if (tDone >= 0) tDone else prev.childTodosDone,
                        childTodosTotal = if (tTotal > 0) tTotal else prev.childTodosTotal,
                    )
                } else {
                    blocks.add(fresh)
                }
            } else {
                // 先按 dispatch_id 精确配对（协议契约：dispatchStart/End 的
                // dispatch_id 同源）；找不到（历史重建后的卡没有 dispatch_id、
                // 或跨会话的旧广播）才回落「最后一张未完结卡」并记日志。
                val exact = dispatchCardIdx(dispatchId)
                val i = if (exact >= 0) {
                    exact
                } else {
                    Log.w(RealBackend.TAG, "dispatchEnd 无 dispatch_id 配对卡，回落未完结卡 dispatch=$dispatchId")
                    blocks.indexOfLast { it is ThreadBlock.Dispatch && it.done.not() }
                }
                if (i >= 0) {
                    val d = blocks[i] as ThreadBlock.Dispatch
                    // 清单终态（DispatchEnd.FinalTodos）：定格卡上的「清单 N/M」。
                    // 空 = 子会话没建过清单，保持实时进度不动（不把已有徽标擦成 0）。
                    val finalTodos = p.optJSONArray("final_todos")
                    val (fDone, fTotal) = if (finalTodos != null && finalTodos.length() > 0) {
                        val list = (0 until finalTodos.length()).map { finalTodos.getJSONObject(it) }
                        list.count { it.optString("status") == "done" } to list.size
                    } else {
                        d.childTodosDone to d.childTodosTotal
                    }
                    if (dispatchId.isNotEmpty()) endedDispatches.add(dispatchId)
                    blocks[i] = d.copy(
                        // 空 result = 子 Agent 没产出文本结论（对齐历史路径的既有兜底），
                        // 不兜就是卡上结论行整行空白
                        conclusion = p.optString("result").ifBlank { "（子 Agent 没有产出文本结论）" },
                        // 子会话 id 以 dispatchEnd 的为准（续跑时 arguments.session 可能
                        // 为空，而这里永远是本次真正用的那个）——缺了就保留卡上的。
                        sessionId = p.optString("session_id", "").ifBlank { d.sessionId },
                        done = true,
                        isError = p.optBoolean("is_error"),
                        childTodosDone = fDone,
                        childTodosTotal = fTotal,
                    )
                } else {
                    Log.w(RealBackend.TAG, "dispatchEnd 无配对卡 dispatch=$dispatchId")
                }
            }
            return
        }
        // 子会话事件（dispatch_id 非空）不进主时间线；toolCall/toolResult 归并进派发卡的
        // 子工具实时流（卡内展开可见子工具名/状态/耗时——桌面端「子会话标签页实时流」的
        // 安卓单页等价物）。todo.updated（带 dispatch_id = 子 Agent 的清单更新）：
        // 归并进**归属派发卡**（轻量派发进度——手机上看子 Agent 跑到哪一步，
        // 与桌面端 TodoUpdatedParams.DispatchID 的归属契约一致；卡不存在只记日志）。
        if (dispatchId.isNotEmpty()) {
            // 「这个派发还在跑」的证据：任何带归属的子事件（delta/toolCall/toolResult/
            // todo.updated/busy）都只可能来自**正在执行**的子会话。历史回放建的卡
            // 先按"中断"定格（那时不知道它是死是活），一旦有子事件到达就说明它还活着
            // ——把卡恢复成运行中（否则用户看到的是一张红着的"中断"卡，子 Agent 其实
            // 正在干活；用户报的「子 Agent 在运行时总是红色」就是这条）。
            markDispatchRunning(dispatchId)
            // ===== 子会话视图双投（对齐桌面端 reduceSessionStates）=====
            // 归属判定必须用**卡上的子会话 id**：dispatch_id 是**工具调用 id**
            // （runDispatch 的 call.DispatchID = tc.ID），与子会话 id 是两回事——
            // 旧代码拿 `dispatchId == viewChildSessionId` 比，普通派发永远不相等，
            // 于是「进了子会话视图只有打开那一刻的历史快照、看不到实时流」（文字
            // 流式、工具流全都停在原地）。桌面端同样是按卡反查子会话 id。
            val ownerChild = dispatchChildSessionOf(dispatchId)
            val routeToChild = viewChildSessionId != null &&
                (ownerChild == viewChildSessionId || dispatchId == viewChildSessionId)
            if (routeToChild) childRouteEvent(method, p)
            when (method) {
                "chat.toolCall" -> {
                    upsertChildTool(
                        blocks,
                        dispatchId,
                        id = p.optString("id"),
                        name = p.optString("name"),
                        argsSummary = RealBackend.argsSummary(p.optString("arguments")),
                        running = true,
                        isError = false,
                        durationMs = 0,
                    )
                }
                "chat.toolResult" -> {
                    finishChildTool(blocks, dispatchId, p.optString("id"), p.optBoolean("is_error"))
                }
                "todo.updated" -> {
                    mergeChildTodos(blocks, dispatchId, p.optJSONArray("items"))
                }
            }
            return
        }
        val sid = p.optString("session_id", "")
        // 子会话视图双投：子会话自己的事件（无 dispatch_id、session_id == 子会话 id）
        // 在**不进主时间线**的前提下投进 child 状态——子会话标签页看实时流。
        if (viewChildSessionId != null && sid == viewChildSessionId) {
            childRouteEvent(method, p)
            return
        }
        when (method) {
            "chat.userMessage" -> {
                if (sid != currentSessionId) return
                val msg = p.optJSONObject("message") ?: return
                val text = msg.optString("content")
                // 通告前缀识别（对齐桌面端 shared/notices.ts 的前缀表）：user 角色 ≠
                // 用户说的话——后台任务完成通告渲染成提示条，不进用户气泡。
                // hidden 前缀（[系统通告] ）是写给模型的注记：不产生任何块。
                if (text.startsWith(RealBackend.HIDDEN_NOTICE_PREFIX)) return
                val notice = RealBackend.noticeKind(text)
                if (notice != null) {
                    blocks.add(ThreadBlock.Notice(label = notice.second, text = text.removePrefix(notice.first).trim()))
                    streamingIdx = -1
                    return
                }
                val atts = parseAttachments(msg.optJSONArray("attachments"))
                val last = blocks.lastOrNull()
                if (last !is ThreadBlock.User || last.text != text || last.attachments != atts) {
                    blocks.add(ThreadBlock.User(text, atts, localEpochMs = System.currentTimeMillis()))
                    streamingIdx = -1
                }
            }

            "chat.delta" -> {
                if (sid != currentSessionId) return
                val kind = p.optString("kind", "text")
                val text = p.optString("text")
                if (text.isEmpty()) return
                val i = ensureStreamingBlock()
                val cur = blocks[i] as ThreadBlock.Assistant
                blocks[i] = if (kind == "reasoning") {
                    cur.copy(reasoning = (cur.reasoning ?: "") + text, streaming = true)
                } else {
                    cur.copy(content = cur.content + text, streaming = true)
                }
                streamVersion++
            }

            "chat.toolCall" -> {
                if (sid != currentSessionId) return
                streamingIdx = -1 // 工具轮开始：下一步 delta 开新 assistant 块
                val name = p.optString("name")
                // 调度工具不建工具行（对齐桌面端 reduce.ts）：它的渲染形态就是派发卡
                //（dispatchStart 已建卡）——两处都建 = 同一个调度渲染两遍，且
                // toolResult 会把子会话结论全文塞进 output（4000 字符）。
                if (name == "agent_dispatch") return
                val id = p.optString("id")
                val args = p.optString("arguments")
                blocks.add(
                    ThreadBlock.Tool(
                        id = id,
                        name = name,
                        title = RealBackend.toolTitle(name),
                        argsSummary = RealBackend.argsSummary(args),
                        command = RealBackend.argsCommand(args),
                        cwd = RealBackend.argsCwd(args),
                        output = null,
                        running = true,
                        isError = false,
                        durationMs = 0,
                    ),
                )
            }

            "chat.toolResult" -> {
                if (sid != currentSessionId) return
                // 调度结果不进主时间线（对齐桌面端）：全文是子会话结论，由
                // dispatchEnd 回填派发卡的 conclusion 行（卡上截一行显示）。
                //
                // 但**子会话 id 的权威来源就是这条结果**（后端在 agent_dispatch 的
                // 结果末尾追加 `[子会话 id: …]`，见 internal/agent/tools.go）：只靠
                // dispatchStart/End 的 session_id 的话，续跑（arguments.session 为空）
                // 与老载荷会让卡上 id 为空——那张卡就再也没有「查看子会话」入口了。
                // 所以这里照样把正文剥出来回填卡（卡已定格也不改状态，只补 id/结论）。
                // 附件回流（对齐桌面端 reduceToolResult 的调度分支）：这次派发期间
                // 子会话 share_file 等的产物随这条结果回流（ToolResultParams
                // .attachments——与结果消息同一份元数据），挂到派发卡上渲染成果条。
                if (p.optString("name") == "agent_dispatch") {
                    val (body, childSid) = RealBackend.splitDispatchResult(p.optString("content"))
                    val atts = parseAttachments(p.optJSONArray("attachments"))
                    // 归属键 = 这次调用的 id（后端 DispatchCall.DispatchID = tc.ID），
                    // 与 dispatchStart/End 的 dispatch_id 同源。
                    val i = dispatchCardIdx(p.optString("id"))
                    if (i >= 0) {
                        val d = blocks[i] as ThreadBlock.Dispatch
                        blocks[i] = d.copy(
                            // 结论以 dispatchEnd 为准（它有 is_error 语义）；这里只在
                            // 卡还空着时补，避免把 dispatchEnd 写好的终态覆盖成半成品。
                            conclusion = d.conclusion.ifBlank { body },
                            sessionId = childSid.ifBlank { d.sessionId },
                            attachments = if (atts.isNotEmpty()) atts else d.attachments,
                        )
                    } else {
                        Log.w(RealBackend.TAG, "调度结果无归属派发卡 id=${p.optString("id")}（卡还没建？）")
                    }
                    return
                }
                val id = p.optString("id")
                val i = blocks.indexOfFirst { it is ThreadBlock.Tool && it.id == id }
                if (i >= 0) {
                    val t = blocks[i] as ThreadBlock.Tool
                    val atts = parseAttachments(p.optJSONArray("attachments"))
                    blocks[i] = t.copy(
                        output = p.optString("content").take(4_000),
                        running = false,
                        isError = p.optBoolean("is_error"),
                        attachments = if (atts.isNotEmpty()) atts else t.attachments,
                    )
                } else {
                    Log.w(RealBackend.TAG, "toolResult 无配对调用块 id=$id name=${p.optString("name")}")
                }
            }

            "chat.confirmRequest" -> {
                if (sid != currentSessionId) return
                confirm = PendingConfirm(
                    sessionId = sid,
                    id = p.optString("id"),
                    name = p.optString("name"),
                    arguments = p.optString("arguments"),
                    prompt = p.optString("prompt"),
                kind = p.optString("kind"),
                options = p.optJSONArray("options")?.let { a ->
                    (0 until a.length()).map { a.optString(it) }
                } ?: emptyList(),
                )
            }

            "chat.done" -> {
                if (sid != currentSessionId) return
                // 用最终 message 对齐流式块（修 delta 丢字的尾差）。
                // 覆盖语义对齐桌面端 reduce.ts 的 reduceDone：**content 非空才覆盖**
                //——中间步 done（带 tool_calls、content 空）不得把已流式的正文
                // 清成空串；空 content 时只关 streaming + 填元数据。
                val msg = p.optJSONObject("message")
                if (msg != null) {
                    val i = lastAssistantIndex()
                    if (i >= 0) {
                        val cur = blocks[i] as ThreadBlock.Assistant
                        val content = msg.optString("content")
                        val reasoning = msg.optString("reasoning_content", "")
                        val model = msg.optString("model", cur.model)
                        val ftms = msg.optLong("first_token_ms", 0L)
                        val dur = msg.optLong("duration_ms", 0L)
                        val utok = msg.optInt("usage_tokens", 0)
                        blocks[i] = cur.copy(
                            content = content.ifBlank { cur.content },
                            reasoning = reasoning.ifBlank { cur.reasoning },
                            reasoningMs = ftms,
                            model = model,
                            firstTokenMs = if (ftms > 0) ftms else null,
                            tokPerSec = if (utok > 0 && dur > 0) utok * 1000.0 / dur else null,
                            // 轮结束 = 打字机收尾：整块呈现（streaming=false）
                            streaming = false,
                        )
                    }
                }
                p.optJSONObject("context")?.let { contextUsage = parseContext(it) }
                p.optJSONObject("stats")?.let { sessionStats = parseStats(it) }
                Log.i(RealBackend.TAG, "done: ctx=$contextUsage stats=$sessionStats")
                // 轮结束即刷新缓存快照（断网可看的最新性）：cache 落盘不在关键路径，
                // 失败只影响下次打开的秒开质量，不影响正确性（远端是事实源）。
                if (oldestLoadedSeq > 0) {
                    owner.historyLoader.saveHistoryCache(currentSessionId, owner.historyLoader.currentTodosJson(), owner.historyLoader.currentStatsJson())
                }
                msg?.optString("model", "")?.takeIf { it.isNotEmpty() }?.let { sessionModel = it }
                busy = false
                // 投影同款收敛：done 就是本会话这一轮的结束（chat.busy 广播可能晚到
                // 或在断线窗口里丢掉）——列表状态点不该等它。chat.done **不带**
                // dispatch_id（子轮有），所以这条只可能是主轮，归属 = sid。
                if (sid.isNotEmpty()) {
                    sessionBusy[sid] = false
                    owner.historyLoader.syncRunningDots()
                }
                // 主轮结束重拉会话列表（对齐桌面端 ws/index.ts 的 chat.done →
                // refreshSessionList）：新对话的首条消息落库后侧栏才出得来
                //（后端语义：未发过消息的空会话不进 session.list；而 session.new
                // 建的会话首条消息不发 SessionStarted 事件——唯一刷新信号就是 done）
                refreshSessions()
                streamingIdx = -1
            }

            "chat.error" -> {
                if (sid != currentSessionId) return
                streamingIdx = -1
                val aborted = p.optBoolean("aborted")
                val message = p.optString("message")
                p.optJSONObject("partial")?.let { partial ->
                    val content = partial.optString("content")
                    if (content.isNotEmpty()) {
                        val i = lastAssistantIndex()
                        if (i >= 0) {
                            val cur = blocks[i] as ThreadBlock.Assistant
                            blocks[i] = cur.copy(content = content, streaming = false)
                        } else {
                            blocks.add(ThreadBlock.Assistant(content, null, 0, sessionModel))
                        }
                    }
                }
                blocks.add(ThreadBlock.Error(if (aborted) "已中断" else message))
                busy = false
                // 投影同款收敛（与 chat.done 同一条纪律：错误收尾也是这一轮结束）
                if (sid.isNotEmpty()) {
                    sessionBusy[sid] = false
                    owner.historyLoader.syncRunningDots()
                }
            }

            "chat.busy" -> {
                // 多端共享进度：忙闲广播**先记进会话级投影**（列表运行中状态点，
                // 所有会话都记——不只是当前打开的），当前会话再同步本页的 busy。
                if (sid.isNotEmpty()) {
                    sessionBusy[sid] = p.optBoolean("busy")
                    owner.historyLoader.syncRunningDots()
                }
                if (sid != currentSessionId) return
                busy = p.optBoolean("busy")
            }

            "chat.modelChanged" -> {
                // 会话级模型变更广播（多端同步——本端切模型后的回执也走这条）：
                // 按会话归属，别的会话的变更不改本会话的选中态（不串台）
                if (sid != currentSessionId) return
                p.optString("model_id", "").takeIf { it.isNotEmpty() }?.let { pickedModelId = it }
            }

            "chat.approvalChanged" -> {
                // 权限档变更广播（多端同步——本端改档后的回执也走这条）：按会话
                // 归属，别的会话的变更不改本会话药丸
                if (sid != currentSessionId) return
                p.optString("approval", "").takeIf { it.isNotEmpty() }?.let { approval = it }
            }

            "todo.updated" -> {
                if (sid != currentSessionId) return
                val items = p.optJSONArray("items") ?: return
                val list = (0 until items.length()).map { items.getJSONObject(it) }
                    .map { todoItemOf(it.optString("content"), it.optString("status")) }
                val i = blocks.indexOfFirst { it is ThreadBlock.Todo }
                when {
                    // 空清单 = Agent 把清单清空了：移除既有 Todo 块恢复到无清单态
                    //（照旧替换/追加空块就是「光有框里面空」的空框）
                    list.isEmpty() -> if (i >= 0) blocks.removeAt(i)
                    i >= 0 -> blocks[i] = ThreadBlock.Todo(list)
                    else -> blocks.add(ThreadBlock.Todo(list))
                }
            }

            "session.changed" -> refreshSessions()

            "project.changed" -> refreshProjects()

            // 模型注册表变更（多端同步：桌面端加了模型/改了绑定，手机端重拉对齐）
            "model.changed" -> refreshModels()

            "chat.compacted" -> {
                if (sid != currentSessionId) return
                blocks.add(
                    ThreadBlock.Compaction(
                        summary = p.optString("summary"),
                        shadowed = p.optInt("shadowed"),
                        before = p.optInt("before"),
                        after = p.optInt("after"),
                        manual = p.optBoolean("manual"),
                    ),
                )
            }

            else -> Log.d(RealBackend.TAG, "忽略事件 $method")
        }
    
        }}

    // ===== 工具函数 =====

    internal fun ensureStreamingBlock(): Int {
        with(owner) {
        if (streamingIdx >= 0 && streamingIdx < blocks.size && blocks[streamingIdx] is ThreadBlock.Assistant) {
            return streamingIdx
        }
        // 新流式块 streaming=true：打字机只对实时增量揭示（历史回放的块是 false）
        blocks.add(ThreadBlock.Assistant(content = "", reasoning = null, reasoningMs = 0, model = sessionModel, streaming = true))
        streamingIdx = blocks.size - 1
        return streamingIdx
    
        }}

    internal fun lastAssistantIndex(): Int =
        owner.blocks.indexOfLast { it is ThreadBlock.Assistant }

    /** 当前会话时间线里 dispatch_id 对应的派发卡下标（-1 = 无）。
     *  [list] = 目标域的块列表（主域 blocks / 助手域 asst.blocks——同一套派发卡
     *  归约两域共用，归属判定必须查**各自域**的时间线）。 */
    internal fun dispatchCardIdx(dispatchId: String, list: List<ThreadBlock> = owner.blocks): Int =
        list.indexOfFirst { it is ThreadBlock.Dispatch && it.dispatchId == dispatchId }

    /**
     * dispatch_id → 卡上的**子会话 id**（空 = 没有这张卡 / 卡上没有 id）。
     *
     * 两个 id 是**不同的东西**：dispatch_id = 这次工具调用的 id（后端
     * `DispatchCall.DispatchID = tc.ID`），子会话 id = 独立会话行 id。凡是"这条
     * 子事件属不属于当前打开的子会话视图"的判定都必须走这里（桌面端按卡反查，
     * 见 shared/reduce.ts 的 dispatchChildSession）。
     */
    internal fun dispatchChildSessionOf(dispatchId: String, list: List<ThreadBlock> = owner.blocks): String =
        (list.firstOrNull { it is ThreadBlock.Dispatch && it.dispatchId == dispatchId }
            as? ThreadBlock.Dispatch)?.sessionId.orEmpty()

    /**
     * 已经收尾（收到过 dispatchEnd）的 dispatch_id：迟到的子事件不许把卡翻回运行中。
     *
     * 生命周期 = 当前这一份时间线：历史重载（[applyHistory]）时清空——重载之后
     * 的卡是新的，旧的"已收尾"记录没有意义（也顺手把内存账收掉）。
     */
    internal val endedDispatches = mutableSetOf<String>()

    /**
     * 按**子会话 id** 找派发卡并恢复成运行中（`chat.busy` 无归属键的那条走这里：
     * 普通派发的 dispatch_id 是工具调用 id，与子会话 id 不同，不能拿它当键）。
     */
    internal fun markChildCardRunning(childSessionId: String, list: MutableList<ThreadBlock> = owner.blocks) {
        if (childSessionId.isEmpty()) return
        val i = list.indexOfLast { it is ThreadBlock.Dispatch && it.sessionId == childSessionId }
        if (i < 0) return
        val d = list[i] as ThreadBlock.Dispatch
        if (!isDispatchPlaceholder(d.done, d.isError, d.conclusion)) return
        list[i] = d.copy(conclusion = "", done = false, isError = false)
    }

    /**
     * 「这个派发还活着」的信号：带 dispatch_id 的子事件（delta/toolCall/toolResult/
     * todo.updated）只可能来自**正在执行**的子会话。
     *
     * 为什么需要它：历史回放建的卡先按"中断"定格（`parseHistoryBlocks` 里那条
     * 占位结论——那时不知道它是死是活），而**实时事件里并没有 dispatchStart 可吃**
     * 的情形真实存在（回放刚建完卡、子事件紧接着到；或连接建立时那轮已经在跑）。
     * 不翻回来的话，用户看到的是一张红色的"中断"卡，而子 Agent 明明正在干活
     * （用户报的「子 Agent 在运行时总是红色，看起来像中断了」）。
     *
     * 判据刻意收窄：只翻**占位态**（done + isError + 结论就是那句占位文案）——
     * 正常结束的卡（成功或真失败）绝不会被迟到事件改成运行中；收到过 dispatchEnd
     * 的 id 直接跳过（`endedDispatches`）。
     */
    internal fun markDispatchRunning(dispatchId: String, list: MutableList<ThreadBlock> = owner.blocks) {
        if (dispatchId.isEmpty() || dispatchId in endedDispatches) return
        val i = dispatchCardIdx(dispatchId, list)
        if (i < 0) return
        val d = list[i] as ThreadBlock.Dispatch
        if (!isDispatchPlaceholder(d.done, d.isError, d.conclusion)) return
        list[i] = d.copy(conclusion = "", done = false, isError = false)
    }

    /** chat.history 的 running_dispatches → 观察快照列表（键缺席/空数组 = 没有 → null，
     *  让 applyRunningDispatches 的「原样返回」路径生效）。残缺条目（无 dispatch_id）丢弃。 */
    internal fun parseRunningDispatches(arr: org.json.JSONArray?): List<com.moyunteng.lxcode.remote.model.RunningDispatchInfo>? {
        with(owner) {
        if (arr == null || arr.length() == 0) return null
        val out = mutableListOf<com.moyunteng.lxcode.remote.model.RunningDispatchInfo>()
        for (k in 0 until arr.length()) {
            val o = arr.optJSONObject(k) ?: continue
            val id = o.optString("dispatch_id")
            if (id.isEmpty()) continue
            out.add(
                com.moyunteng.lxcode.remote.model.RunningDispatchInfo(
                    dispatchId = id,
                    childSessionId = o.optString("child_session_id"),
                    agentName = o.optString("agent_name"),
                    task = o.optString("task"),
                ),
            )
        }
        return out.ifEmpty { null }
    
        }}

    /** 子会话工具实时流：调用到达即插行/标记运行中（归属派发卡；卡不存在只记日志）。
     *  [list] = 目标域的块列表（主域 / 助手域——两域的派发卡归约共用同一套）。 */
    internal fun upsertChildTool(
        list: MutableList<ThreadBlock>,
        dispatchId: String,
        id: String,
        name: String,
        argsSummary: String,
        running: Boolean,
        isError: Boolean,
        durationMs: Long,
    ) {
        with(owner) {
        val i = dispatchCardIdx(dispatchId, list)
        if (i < 0) {
            Log.w(RealBackend.TAG, "子会话工具事件无归属派发卡 dispatch=$dispatchId")
            return
        }
        val d = list[i] as ThreadBlock.Dispatch
        if (running) childStarts[id] = android.os.SystemClock.elapsedRealtime()
        val existing = d.childRuns.indexOfFirst { it.id == id }
        val entry = ChildTool(
            id = id,
            name = name,
            title = RealBackend.toolTitle(name),
            argsSummary = argsSummary,
            running = running,
            isError = isError,
            durationMs = durationMs,
        )
        val runs = if (existing >= 0) {
            d.childRuns.toMutableList().also { it[existing] = entry }
        } else {
            d.childRuns + entry
        }
        list[i] = d.copy(childRuns = runs, childTools = runs.size)
    
        }}

    /**
     * 子会话工具结果到达：标记完成/出错，耗时按本地计时补齐（协议 toolResult 不带
     * duration_ms；[childStarts] 记录调用到达时刻，回放历史时才用消息里的耗时）。
     */
    internal val childStarts = mutableMapOf<String, Long>()

    internal fun finishChildTool(list: MutableList<ThreadBlock>, dispatchId: String, id: String, isError: Boolean) {
        with(owner) {
        val i = dispatchCardIdx(dispatchId, list)
        if (i < 0) {
            Log.w(RealBackend.TAG, "子会话工具结果无归属派发卡 dispatch=$dispatchId")
            return
        }
        val d = list[i] as ThreadBlock.Dispatch
        val idx = d.childRuns.indexOfFirst { it.id == id }
        if (idx < 0) {
            Log.w(RealBackend.TAG, "子会话工具结果无配对调用行 id=$id dispatch=$dispatchId")
            return
        }
        val start = childStarts.remove(id) ?: 0L
        val dur = if (start > 0) android.os.SystemClock.elapsedRealtime() - start else 0L
        val runs = d.childRuns.toMutableList()
        runs[idx] = runs[idx].copy(running = false, isError = isError, durationMs = dur)
        list[i] = d.copy(childRuns = runs, childTools = runs.size)
    
        }}

    /**
     * 子 Agent 的清单进度归并（todo.updated 带 dispatch_id 的广播，多端共享进度）：
     * 完成数/总数归进归属派发卡（卡上「清单 2/4」一行的数据源）；卡不存在只记日志
     * （跨会话的旧广播、历史里没有卡的派发——丢弃是正确行为，不建孤儿卡）。
     */
    internal fun mergeChildTodos(tl: MutableList<ThreadBlock>, dispatchId: String, items: org.json.JSONArray?) {
        with(owner) {
        if (items == null) return
        val i = dispatchCardIdx(dispatchId, tl)
        if (i < 0) {
            Log.w(RealBackend.TAG, "子会话清单事件无归属派发卡 dispatch=$dispatchId")
            return
        }
        val d = tl[i] as ThreadBlock.Dispatch
        // 迟到守卫只挡**真终态**（成功 / 带真实错误正文的失败）：终态以 dispatchEnd
        // 的 final_todos 为准。回放占位态（done + isError + 那句「中断」文案）不是
        // 终态——会话仍在跑时它刚被 markDispatchRunning 翻回运行中，清单必须能写上去
        //（用户报的「任务列表有时不显示」：刷新后清单永远停在初稿）。
        if (isDispatchFinal(d.done, d.isError, d.conclusion)) return
        val list = (0 until items.length()).map { items.getJSONObject(it) }
            .map { todoItemOf(it.optString("content"), it.optString("status")) }
        tl[i] = d.copy(
            childTodosDone = list.count { it.status == "done" },
            childTodosTotal = list.size,
        )
    
        }}

    // ===== 子会话视图的事件归约（主会话 reducer 的 child 影子；块语义同款）=====

    /**
     * 子会话自己的事件（session_id == viewChildSessionId、无 dispatch_id）归约进
     * child 状态。类型集 = 子会话会产生的全部事件（delta/toolCall/toolResult/
     * done/error/busy/todo.updated/compacted）；块构建逻辑与主会话 reducer 逐条
     * 同源——子会话是真 Session，协议上与主会话发同一套事件。
     */
    internal fun childRouteEvent(method: String, p: JSONObject) {
        with(owner) {
        when (method) {
            "chat.userMessage" -> {
                val msg = p.optJSONObject("message") ?: return
                val text = msg.optString("content")
                if (text.startsWith(RealBackend.HIDDEN_NOTICE_PREFIX)) return
                val notice = RealBackend.noticeKind(text)
                if (notice != null) {
                    childBlocks.add(ThreadBlock.Notice(label = notice.second, text = text.removePrefix(notice.first).trim()))
                    childStreamingIdx = -1
                    return
                }
                val atts = parseAttachments(msg.optJSONArray("attachments"))
                val last = childBlocks.lastOrNull()
                if (last !is ThreadBlock.User || last.text != text) {
                    childBlocks.add(ThreadBlock.User(text, atts, localEpochMs = System.currentTimeMillis()))
                    childStreamingIdx = -1
                }
            }

            "chat.delta" -> {
                val kind = p.optString("kind", "text")
                val text = p.optString("text")
                if (text.isEmpty()) return
                val i = childEnsureStreamingBlock()
                val cur = childBlocks[i] as ThreadBlock.Assistant
                childBlocks[i] = if (kind == "reasoning") {
                    cur.copy(reasoning = (cur.reasoning ?: "") + text, streaming = true)
                } else {
                    cur.copy(content = cur.content + text, streaming = true)
                }
                childStreamVersion++
            }

            "chat.toolCall" -> {
                childStreamingIdx = -1
                val name = p.optString("name")
                if (name == "agent_dispatch") return // 子会话再派发不会发生（深度恒 1），防御性丢弃
                childBlocks.add(
                    ThreadBlock.Tool(
                        id = p.optString("id"),
                        name = name,
                        title = RealBackend.toolTitle(name),
                        argsSummary = RealBackend.argsSummary(p.optString("arguments")),
                        command = RealBackend.argsCommand(p.optString("arguments")),
                        cwd = RealBackend.argsCwd(p.optString("arguments")),
                        output = null,
                        running = true,
                        isError = false,
                        durationMs = 0,
                    ),
                )
            }

            "chat.toolResult" -> {
                if (p.optString("name") == "agent_dispatch") return
                val id = p.optString("id")
                val i = childBlocks.indexOfFirst { it is ThreadBlock.Tool && it.id == id }
                if (i >= 0) {
                    val t = childBlocks[i] as ThreadBlock.Tool
                    val atts = parseAttachments(p.optJSONArray("attachments"))
                    childBlocks[i] = t.copy(
                        output = p.optString("content").take(4_000),
                        running = false,
                        isError = p.optBoolean("is_error"),
                        // 子会话自己 share_file 的附件（子会话视图的工具行同样渲染附件条
                        // ——与主会话 reducer 同一条口径）
                        attachments = if (atts.isNotEmpty()) atts else t.attachments,
                    )
                }
            }

            "chat.done" -> {
                val msg = p.optJSONObject("message")
                if (msg != null) {
                    val i = childBlocks.indexOfLast { it is ThreadBlock.Assistant }
                    if (i >= 0) {
                        val cur = childBlocks[i] as ThreadBlock.Assistant
                        val content = msg.optString("content")
                        val reasoning = msg.optString("reasoning_content", "")
                        val model = msg.optString("model", cur.model)
                        val ftms = msg.optLong("first_token_ms", 0L)
                        val dur = msg.optLong("duration_ms", 0L)
                        val utok = msg.optInt("usage_tokens", 0)
                        childBlocks[i] = cur.copy(
                            content = content.ifBlank { cur.content },
                            reasoning = reasoning.ifBlank { cur.reasoning },
                            reasoningMs = ftms,
                            model = model,
                            firstTokenMs = if (ftms > 0) ftms else null,
                            tokPerSec = if (utok > 0 && dur > 0) utok * 1000.0 / dur else null,
                            streaming = false,
                        )
                    }
                }
                childBusy = false
                childStreamingIdx = -1
            }

            "chat.error" -> {
                childStreamingIdx = -1
                val aborted = p.optBoolean("aborted")
                val message = p.optString("message")
                p.optJSONObject("partial")?.let { partial ->
                    val content = partial.optString("content")
                    if (content.isNotEmpty()) {
                        val i = childBlocks.indexOfLast { it is ThreadBlock.Assistant }
                        if (i >= 0) {
                            val cur = childBlocks[i] as ThreadBlock.Assistant
                            childBlocks[i] = cur.copy(content = content, streaming = false)
                        }
                    }
                }
                childBlocks.add(ThreadBlock.Error(if (aborted) "已中断" else message))
                childBusy = false
            }

            // 子会话自己的忙闲（无归属键、session_id == 子会话 id 的那条）。
            // 带 dispatch_id 的那条走主 reducer 的子会话分支（同样双投到 childBusy）。
            "chat.busy" -> {
                childBusy = p.optBoolean("busy")
                if (childBusy) markChildCardRunning(viewChildSessionId ?: "")
            }

            // 子会话自己的模型变更广播（子会话页切模型的回执，多端同步）：
            // 按 session_id 归属——别的子会话的变更不改本视图的选中态。
            "chat.modelChanged" -> {
                p.optString("model_id", "").takeIf { it.isNotEmpty() }?.let { childModelId = it }
            }

            "todo.updated" -> childMergeTodos(p.optJSONArray("items"))

            "chat.compacted" -> childBlocks.add(
                ThreadBlock.Compaction(
                    summary = p.optString("summary"),
                    shadowed = p.optInt("shadowed"),
                    before = p.optInt("before"),
                    after = p.optInt("after"),
                    manual = p.optBoolean("manual"),
                ),
            )
        }
    
        }}

    internal fun childEnsureStreamingBlock(): Int {
        with(owner) {
        if (childStreamingIdx >= 0 && childStreamingIdx < childBlocks.size && childBlocks[childStreamingIdx] is ThreadBlock.Assistant) {
            return childStreamingIdx
        }
        childBlocks.add(ThreadBlock.Assistant(content = "", reasoning = null, reasoningMs = 0, model = "", streaming = true))
        childStreamingIdx = childBlocks.size - 1
        return childStreamingIdx
    
        }}

    /** 子会话视图的实时工具行（派发卡 childRuns 的同源投影，进子会话时间线）。 */
    internal fun childUpsertTool(
        id: String,
        name: String,
        argsSummary: String,
        running: Boolean,
        isError: Boolean,
        durationMs: Long,
    ) {
        with(owner) {
        val i = childBlocks.indexOfFirst { it is ThreadBlock.Tool && it.id == id }
        val entry = ThreadBlock.Tool(
            id = id,
            name = name,
            title = RealBackend.toolTitle(name),
            argsSummary = argsSummary,
            command = "",
            cwd = "",
            output = null,
            running = running,
            isError = isError,
            durationMs = durationMs,
        )
        if (i >= 0) childBlocks[i] = entry else childBlocks.add(entry)
        childStarts[id] = android.os.SystemClock.elapsedRealtime()
    
        }}

    internal fun childFinishTool(id: String, isError: Boolean) {
        with(owner) {
        val i = childBlocks.indexOfFirst { it is ThreadBlock.Tool && it.id == id }
        if (i < 0) return
        val t = childBlocks[i] as ThreadBlock.Tool
        val start = childStarts.remove(id) ?: 0L
        val dur = if (start > 0) android.os.SystemClock.elapsedRealtime() - start else 0L
        childBlocks[i] = t.copy(running = false, isError = isError, durationMs = dur)
    
        }}

    internal fun childMergeTodos(items: org.json.JSONArray?) {
        with(owner) {
        if (items == null) return
        val list = (0 until items.length()).map { items.getJSONObject(it) }
            .map { todoItemOf(it.optString("content"), it.optString("status")) }
        // 落**会话级状态**（置底清单条数据源），不再往时间线插 Todo 块。
        childTodos = list
    
        }}

    internal fun parseContext(o: JSONObject): ContextUsage = ContextUsage(
        used = o.optInt("used"),
        window = o.optInt("window"),
        system = o.optInt("system"),
        tools = o.optInt("tools"),
        toolResults = o.optInt("tool_results"),
        messages = o.optInt("messages"),
        reasoning = o.optInt("reasoning"),
        estimated = o.optBoolean("estimated"),
    )


}
