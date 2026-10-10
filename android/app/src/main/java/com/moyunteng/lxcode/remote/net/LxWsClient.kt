// LxWsClient —— lxcode 后端的 WebSocket JSON-RPC 传输层（安卓端）。
//
// 协议对齐 internal/protocol + internal/wsclient（Go 参考实现）与
// frontend/src/agent/ws/index.ts（TS 参考实现），wire 细节见 android/PROTOCOL-NOTES.md：
//  * 帧结构：{"jsonrpc":"2.0","id":N,"method":…,"params":…} / 应答同 id / 事件无 id；
//  * 请求-应答按 id 配对（pending 表）；
//  * 断连 fast-fail：连接关闭时所有 pending 立刻失败（对齐 wsclient 的 closed 语义）；
//  * 事件经 listener 回调（Go 版是 channel，安卓用主线程回调）。
//
// 选型：OkHttp WebSocket（业界标准、WS 成熟、传递面仅 okio+kotlin-stdlib，
// 本机 gradle 缓存已有可离线构建；java.net.http 要 API 34 > minSdk 26）。
// JSON 用 org.json（Android 平台自带，帧结构浅，不值得引序列化框架）。
//
// 线程模型：OkHttp 回调在 WS 线程；本类把事件/状态回调统一切到主线程
//（调用方传 Handler 或默认主线程），UI 层不需要再自己切。
package com.moyunteng.lxcode.remote.net

import android.os.Handler
import android.os.Looper
import android.util.Log
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import org.json.JSONObject
import java.util.concurrent.ConcurrentLinkedQueue
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicReference

/** 协议版本（internal/protocol.Version = "2"；不匹配服务端回 1007）。 */
const val PROTOCOL_VERSION = "2"

/** WS 端点路径（internal/protocol.Path）。 */
const val RPC_PATH = "/rpc"

/** 请求超时（对齐 frontend WSAgent 的 REQUEST_TIMEOUT_MS）。 */
private const val REQUEST_TIMEOUT_MS = 10_000L

/** 事件监听：method = 事件名（如 "chat.delta"），params = 载荷。 */
fun interface EventListener {
    fun onEvent(method: String, params: JSONObject)
}

/** 连接状态监听（open/close/failure；回调在主线程）。 */
fun interface StateListener {
    fun onState(connected: Boolean, message: String?)
}

/**
 * WS JSON-RPC 客户端：连接、hello 握手、call 配对、事件分发。
 *
 * 生命周期：[connect] → 握手成功后 [connected] 为 true 并回调 onState(true)；
 * 断开/失败回调 onState(false, 原因)。重连由调用方驱动（本批手动重试，见任务纪律）。
 */
class LxWsClient(
    private val eventListener: EventListener,
    private val stateListener: StateListener,
) {
    private val main = Handler(Looper.getMainLooper())
    private val client = OkHttpClient.Builder()
        .connectTimeout(5, TimeUnit.SECONDS)
        .readTimeout(0, TimeUnit.MILLISECONDS) // WS 长连接：读不超时
        .pingInterval(30, TimeUnit.SECONDS)
        .build()

    private val nextId = AtomicInteger(0)
    private val pending = ConcurrentLinkedQueue<PendingCall>()
    private val wsRef = AtomicReference<WebSocket?>(null)

    /** 握手是否已成功（hello 应答到达并版本匹配）。 */
    @Volatile var connected: Boolean = false
        private set

    /** 服务端握手应答（server/version/busy）。 */
    @Volatile var helloResult: JSONObject? = null
        private set

    private data class PendingCall(
        val id: Int,
        val method: String,
        val createdAt: Long,
        val callback: (err: JSONObject?, result: Any?) -> Unit,
    )

    /** 连接后端（addr 形如 127.0.0.1:7789；token 可空 = 后端未开远程门）。 */
    fun connect(addr: String, token: String) {
        close()
        val url = buildString {
            append("ws://").append(addr.trim()).append(RPC_PATH)
            if (token.isNotBlank()) append("?token=").append(java.net.URLEncoder.encode(token, "UTF-8"))
        }
        Log.i(TAG, "connect $url")
        val req = Request.Builder().url(url).build()
        val ws = client.newWebSocket(req, object : WebSocketListener() {
            override fun onOpen(webSocket: WebSocket, response: Response) {
                Log.i(TAG, "ws onOpen (http ${response.code})，发送 hello")
                // 握手：connection.hello（版本不匹配服务端回 code 1007）
                call("connection.hello", JSONObject().put("client", "android").put("version", PROTOCOL_VERSION)) { err, result ->
                    if (err != null) {
                        Log.w(TAG, "hello 失败: code=${err.optInt("code")} msg=${err.optString("message")}")
                        close()
                        main.post { stateListener.onState(false, err.optString("message")) }
                        return@call
                    }
                    val hello = result as? JSONObject
                    helloResult = hello
                    connected = true
                    Log.i(TAG, "hello 成功: server=${hello?.optString("server")} version=${hello?.optString("version")}")
                    main.post { stateListener.onState(true, null) }
                }
            }

            override fun onMessage(webSocket: WebSocket, text: String) {
                handleFrame(text)
            }

            override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                val msg = "连接失败: ${t.message ?: t.javaClass.simpleName}"
                Log.w(TAG, msg)
                failAllPending("后端连接已断开")
                connected = false
                main.post { stateListener.onState(false, msg) }
            }

            override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                Log.i(TAG, "ws closed code=$code reason=$reason")
                failAllPending("后端连接已断开")
                connected = false
                main.post { stateListener.onState(false, null) }
            }
        })
        wsRef.set(ws)
    }

    /** 关闭连接（幂等）。 */
    fun close() {
        connected = false
        val ws = wsRef.getAndSet(null) ?: return
        try {
            ws.close(1000, "client close")
        } catch (_: Exception) {
        }
        failAllPending("后端连接已断开")
    }

    /**
     * 发请求并等应答。callback 在**主线程**执行：
     * err != null → 请求失败（error 帧或断连/超时）；否则 result 为应答 result——
     * 可能是 JSONObject / JSONArray（如 session.list 返回裸数组）/ String / null。
     */
    fun call(method: String, params: JSONObject?, callback: (err: JSONObject?, result: Any?) -> Unit) {
        val ws = wsRef.get()
        if (ws == null) {
            main.post { callback(errorObj(-1, "后端未连接"), null) }
            return
        }
        val id = nextId.incrementAndGet()
        pending.add(PendingCall(id, method, System.currentTimeMillis(), callback))
        val frame = JSONObject().put("jsonrpc", "2.0").put("id", id).put("method", method)
        if (params != null) frame.put("params", params)
        val sent = try {
            ws.send(frame.toString())
        } catch (e: Exception) {
            Log.w(TAG, "send $method 失败: $e")
            false
        }
        if (!sent) {
            pending.removeIf { it.id == id }
            main.post { callback(errorObj(-1, "发送请求失败（连接不可用）"), null) }
        }
        // 超时看门狗（对齐 WSAgent 的 10s 请求超时）
        main.postDelayed({
            if (pending.removeIf { it.id == id }) {
                Log.w(TAG, "请求超时: $method")
                callback(errorObj(-1, "请求超时"), null)
            }
        }, REQUEST_TIMEOUT_MS)
    }

    /** 处理一帧：有 id = 应答（按 id 配对）；无 id = 事件（分发 listener）。 */
    private fun handleFrame(text: String) {
        val frame = try {
            JSONObject(text)
        } catch (e: Exception) {
            Log.w(TAG, "非 JSON 帧: ${text.take(120)}")
            return
        }
        if (!frame.has("id")) {
            val method = frame.optString("method")
            val params = frame.optJSONObject("params") ?: JSONObject()
            Log.d(TAG, "event $method ${params.toString().take(200)}")
            main.post { eventListener.onEvent(method, params) }
            return
        }
        val id = frame.optInt("id", -1)
        val err = frame.optJSONObject("error")
        val result = frame.opt("result")
        // 配对取出（线性扫；pending 常态 ≤ 个位数）。未配对 = 超时后迟到的应答，丢弃。
        deliverPending(id, err, result)
    }

    private fun deliverPending(id: Int, err: JSONObject?, result: Any?) {
        var delivered: PendingCall? = null
        val it = pending.iterator()
        while (it.hasNext()) {
            val p = it.next()
            if (p.id == id) {
                it.remove()
                delivered = p
                break
            }
        }
        if (delivered != null) {
            if (err != null) Log.w(TAG, "call ${delivered.method} 失败: ${err.optInt("code")} ${err.optString("message")}")
            else Log.i(TAG, "call ${delivered.method} ok ${result?.toString()?.take(200) ?: "<null>"}")
            main.post { delivered.callback(err, result) }
        }
    }

    private fun failAllPending(reason: String) {
        val snapshot = mutableListOf<PendingCall>()
        while (true) {
            val p = pending.poll() ?: break
            snapshot.add(p)
        }
        for (p in snapshot) {
            Log.w(TAG, "fast-fail pending ${p.method}")
            main.post { p.callback(errorObj(-1, reason), null) }
        }
    }

    private fun errorObj(code: Int, message: String) = JSONObject().put("code", code).put("message", message)

    companion object {
        private const val TAG = "LxNet"
    }
}
