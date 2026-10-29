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
import okio.ByteString
import org.json.JSONObject
import java.net.URLEncoder
import java.security.SecureRandom
import java.security.cert.X509Certificate
import java.util.concurrent.ConcurrentLinkedQueue
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicReference
import javax.net.ssl.SSLContext
import javax.net.ssl.X509TrustManager

/** 协议版本（internal/protocol.Version = "2"；不匹配服务端回 1007）。 */
const val PROTOCOL_VERSION = "2"

/** WS 端点路径（internal/protocol.Path）。 */
const val RPC_PATH = "/rpc"

/** 请求超时（对齐 frontend WSAgent 的 REQUEST_TIMEOUT_MS）。 */
private const val REQUEST_TIMEOUT_MS = 10_000L

/** 重方法超时（对齐 frontend connection.ts 的 HEAVY_REQUEST_TIMEOUT_MS）：
 *  session.resume / chat.history 要全量加载大会话历史（后端磁盘 IO + JSON
 *  反序列化 + 折叠统计），公网 frp 隧道限速 10 Mbit/s 下 1.4~1.7MB 载荷传输
 *  本身就要秒级——10s 会偶发命中（用户报的「历史加载不出来」）。放宽到 30s，
 *  其余请求保持 10s 快判。 */
private const val HEAVY_REQUEST_TIMEOUT_MS = 30_000L

/** 重方法名单（与 frontend HEAVY_METHODS 同一份语义）。 */
private val HEAVY_METHODS = setOf(
    "session.resume",
    "chat.history",
    "chat.compact",
    "chat.rewind",
    "session.list",
)

/** 事件监听：method = 事件名（如 "chat.delta"），params = 载荷。 */
fun interface EventListener {
    fun onEvent(method: String, params: JSONObject)
}

/** 二进制帧监听（投屏视频；internal/protocol/screen.go FrameHeader 分路——
 *  文本帧 = JSON-RPC，二进制帧 = 视频，同一条 WS）。回调在 WS 线程，
 *  接收方自行切换线程（解码器回调要进自己的线程，不能占 WS 线程）。 */
fun interface FrameListener {
    fun onFrame(frame: ByteString)
}

/** 连接状态监听（open/close/failure；回调在主线程）。 */
fun interface StateListener {
    fun onState(connected: Boolean, message: String?)
}

/** 升级请求被 401 拒（token 错/被吊销）——调用方停重连并进「配对已失效」态。 */
fun interface UnauthorizedListener {
    fun onUnauthorized()
}

/**
 * WS JSON-RPC 客户端：连接、hello 握手、call 配对、事件分发、心跳与自动重连。
 *
 * 生命周期：[connect] → 握手成功后 [connected] 为 true 并回调 onState(true)；
 * 断开/失败回调 onState(false, 原因)。断线后自动重连（指数退避，封顶 30s，
 * 调用方 disconnect() 显式断开则不重连）。多端共享进度依赖它：手机息屏/
 * 切后台回来时连接可能已被断开——心跳主动发现断线、重连后事件流续上。
 */
class LxWsClient(
    private val eventListener: EventListener,
    private val stateListener: StateListener,
    private val unauthorized: UnauthorizedListener = UnauthorizedListener { },
    /** 二进制帧（投屏视频）监听；默认空实现——非投屏页面零开销。 */
    private val frameListener: FrameListener = FrameListener { },
) {
    private val main = Handler(Looper.getMainLooper())

    /** delta 合帧器：chat.delta 同键聚合、拍末一次投递（非 delta 事件先冲刷——保序）。
     *  emit 语义与合帧前的 eventListener.onEvent 完全一致（method/params 直通）。 */
    private val coalescer = DeltaCoalescer(
        emitRaw = { m, p -> eventListener.onEvent(m, p) },
        schedule = { flush ->
            val r = Runnable { flush() }
            main.postDelayed(r, DELTA_COALESCE_MS)
            return@DeltaCoalescer { main.removeCallbacks(r) }
        },
    )

    /** 局域网/本机直连用：默认 OkHttp（系统信任链，行为不变）。 */
    private val client = OkHttpClient.Builder()
        .connectTimeout(5, TimeUnit.SECONDS)
        .readTimeout(0, TimeUnit.MILLISECONDS) // WS 长连接：读不超时
        .pingInterval(30, TimeUnit.SECONDS)
        .build()

    /**
     * wss（樱花frp 隧道）专用 client：放行 frpc 节点的**自签**证书。
     *
     * 为什么放行：隧道 extra 是 auto_https=auto，节点对外 TLS 终结用 frpc 自签
     * 证书（无 CA 签发、主机名也是节点域名而非证书 SAN），系统信任链必然拒绝；
     * 明文 ws 又会被节点回 501——自签是这条入口唯一可用的 TLS 形态。
     * 为什么可接受：这是**点对点的配对/隧道连接**，token 已是唯一凭据
     * （internal/server/remote.go 对每次 /rpc 升级验 token），不依赖证书身份；
     * 宽松信任只挂在本 client 实例上——UpdateChecker / Attachments 等其他
     * HTTPS 调用走各自默认 client，不受影响。
     */
    private val clientTrustAll: OkHttpClient by lazy {
        val tm = object : X509TrustManager {
            override fun checkClientTrusted(chain: Array<X509Certificate>, authType: String) {}
            override fun checkServerTrusted(chain: Array<X509Certificate>, authType: String) {}
            override fun getAcceptedIssuers(): Array<X509Certificate> = arrayOf()
        }
        val ssl = SSLContext.getInstance("TLS").apply { init(null, arrayOf(tm), SecureRandom()) }
        OkHttpClient.Builder()
            .connectTimeout(5, TimeUnit.SECONDS)
            .readTimeout(0, TimeUnit.MILLISECONDS)
            .pingInterval(30, TimeUnit.SECONDS)
            .sslSocketFactory(ssl.socketFactory, tm)
            // 自签证书的 SAN 不含节点域名（或干脆没有 SAN），hostname 校验必然失败
            .hostnameVerifier { _, _ -> true }
            .build()
    }

    /** 按 addr 形态选 client：wss = 隧道入口（自签放行），其余 = 默认信任链。 */
    private fun clientFor(addr: String): OkHttpClient =
        if (addr.trim().startsWith("wss://", ignoreCase = true)) clientTrustAll else client

    private val nextId = AtomicInteger(0)
    private val pending = ConcurrentLinkedQueue<PendingCall>()
    private val wsRef = AtomicReference<WebSocket?>(null)

    /** 握手是否已成功（hello 应答到达并版本匹配）。 */
    @Volatile var connected: Boolean = false
        private set

    /** 服务端握手应答（server/version/busy）。 */
    @Volatile var helloResult: JSONObject? = null
        private set

    // ===== 心跳与自动重连（对齐桌面端 connection.ts 的 HEARTBEAT_* 常量语义）=====

    /** 心跳间隔（15s——OKHttp 协议层 ping 是 30s，应用层这条覆盖「连接假活」：*
     *  TCP 没断但后端已不回帧的场景，协议层 ping 发现不了）。 */
    private val heartbeatIntervalMs = 15_000L

    /** 心跳超时（3s）与连续失败上限（2 次）——超限主动 close 触发重连。 */
    private val heartbeatTimeoutMs = 3_000L
    private val maxHeartbeatFailures = 2
    private var heartbeatFailures = 0
    private var heartbeatRunnable: Runnable? = null

    /** 自动重连退避（指数：1s → 2s → 4s … 封顶 30s；成功握手后归 1）。 */
    private var reconnectBackoffMs = 1_000L
    private var reconnectRunnable: Runnable? = null

    /**
     * 自动重连进行中（断线后自动重试已排程/尝试中）。UI 显示「正在重连…」的依据：
     * 自动重连其实一直在后台跑，不表达它用户会以为必须手动去连接页重连。
     * 置位 = scheduleReconnect（断线事件 → 排程下一次重试）；复位 = 握手成功
     * （onOpen 的 hello 应答）或用户显式 close()。
     */
    @Volatile var autoReconnecting: Boolean = false
        private set

    /** 用户显式断开（disconnect()）= 不自动重连；其余断线（网络抖动/后端重启）重连。 */
    @Volatile private var userClosed = false

    /** 最近一次连接的凭证（自动重连复用）。 */
    private var currentAddr: String = ""
    private var currentToken: String = ""

    private data class PendingCall(
        val id: Int,
        val method: String,
        val createdAt: Long,
        val callback: (err: JSONObject?, result: Any?) -> Unit,
    )

    /**
     * 连接后端（addr 形如 127.0.0.1:7789；token 可空 = 后端未开远程门）。
     *
     * [resetBackoff] 只在**用户显式发起**时为 true（开关/重试按钮/亮屏即时重连）：
     * 自动重连路径（connectAuto）传 false——否则每次重试都把退避拍回 1s，
     * 「1s → 2s → … 封顶 30s」的指数退避永不生效，断线期间恒 1s 轰炸后端。
     * 退避归 1 的另一个时机 = 握手成功（onOpen 的 hello 应答处）。
     */
    fun connect(addr: String, token: String, resetBackoff: Boolean = true) {
        userClosed = false
        if (resetBackoff) reconnectBackoffMs = 1_000L
        stopHeartbeat()
        stopReconnect()
        connectOnce(addr, token)
    }

    /** 按 addr 形态构造升级请求（对齐桌面端 connection.ts 的 wsURL 自适应）：
     *  `wss://` 前缀原样（樱花frp 隧道——节点对外 TLS，明文 ws 会被节点回 501）；
     *  `ws://` / 裸 host:port → ws://（局域网/本机直连，行为不变）；
     *  `https://` → wss://（远端 HTTPS 反代同款语义）。路径统一 /rpc，token 挂查询串。
     *  地址只取 host:port（载荷地址不带路径；即便带了也收敛到 /rpc）。
     *  非法地址（用户输入错乱 / 缺 host）不 FATAL：返回 null 走失败态 UI
     * （实测路径：输入框残留 "1270..01.7:790" 时点重试曾直接崩进程）。 */
    private fun request(addr: String, token: String): Request? {
        val trimmed = addr.trim()
        val base = when {
            trimmed.startsWith("wss://", true) -> "wss://" + trimmed.substring(6)
            trimmed.startsWith("ws://", true) -> "ws://" + trimmed.substring(5)
            trimmed.startsWith("https://", true) -> "wss://" + trimmed.substring(8)
            trimmed.startsWith("http://", true) -> "ws://" + trimmed.substring(7)
            else -> "ws://" + trimmed
        }
        // scheme://host:port 之后是路径/查询——全部剥掉，路径统一 /rpc
        val authority = base.substringAfter("://").substringBefore('/').substringBefore('?')
        val url = "${base.substringBefore("://")}://$authority$RPC_PATH" +
            if (token.isNotBlank()) "?token=${URLEncoder.encode(token, "UTF-8")}" else ""
        Log.i(TAG, "connect $url")
        return try {
            Request.Builder().url(url).build()
        } catch (e: IllegalArgumentException) {
            Log.w(TAG, "非法地址 $url: ${e.message}")
            main.post { stateListener.onState(false, "地址不合法: ${e.message}") }
            null
        }
    }

    /** WS 事件监听（连接/心跳/重连逻辑与 scheme 无关，抽出共用）。 */
    private fun listener(addr: String, token: String): WebSocketListener =
        object : WebSocketListener() {
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
                    heartbeatFailures = 0
                    reconnectBackoffMs = 1_000L
                    autoReconnecting = false
                    startHeartbeat()
                    Log.i(TAG, "hello 成功: server=${hello?.optString("server")} version=${hello?.optString("version")}")
                    main.post { stateListener.onState(true, null) }
                }
            }

            override fun onMessage(webSocket: WebSocket, text: String) {
                handleFrame(text)
            }

            override fun onMessage(webSocket: WebSocket, bytes: ByteString) {
                // 二进制帧 = 投屏视频（协议分路：文本 = JSON-RPC）。直接透传给
                // frameListener（WS 线程回调——解码侧自己排队，不在这里切线程/
                // 不在这里拷贝两份）。
                frameListener.onFrame(bytes)
            }

            override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                val msg = "连接失败: ${t.message ?: t.javaClass.simpleName}"
                Log.w(TAG, msg)
                failAllPending("后端连接已断开")
                connected = false
                stopHeartbeat()
                // 401 = token 错/被吊销：重连必再 401，上报调用方停自动重连
                //（close 语义由调用方决定；这里先把排程停掉，避免退避空转）。
                if (response?.code == 401) {
                    stopReconnect()
                    autoReconnecting = false
                    main.post { unauthorized.onUnauthorized() }
                    return
                }
                main.post { stateListener.onState(false, msg) }
                scheduleReconnect(addr, token)
            }

            override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                Log.i(TAG, "ws closed code=$code reason=$reason")
                failAllPending("后端连接已断开")
                connected = false
                stopHeartbeat()
                main.post { stateListener.onState(false, null) }
                scheduleReconnect(addr, token)
            }
        }

    /** 连接后端（addr 形如 127.0.0.1:7789 或 wss://公网入口；token 可空 = 后端未开远程门）。 */
    private fun connectOnce(addr: String, token: String) {
        currentAddr = addr
        currentToken = token
        closeQuietly()
        val req = request(addr, token) ?: run {
            // request 返回 null（地址不合法）时失败态已上报，但**不能就此永久卡死**：
            // 自动重连路径里就此 return = 没有任何下一次重试（UI 永远停在失败态，
            // 只能手动重试）。继续排退避重试——地址不合法重试也无害（失败态重复
            // 上报），而暂时性故障（DNS 未就绪等）靠它自愈。
            scheduleReconnect(addr, token)
            return
        }
        val ws = clientFor(addr).newWebSocket(req, listener(addr, token))
        wsRef.set(ws)
    }

    /**
     * 自动重连的统一入口：走 [connect] 的完整语义（清 userClosed、重新发起一次
     * newWebSocket——每次重试都是新的一次「明确要连」）。**不重置退避**：
     * 重置只属于用户显式 connect；指数退避靠 scheduleReconnect 里自增 + 握手
     * 成功归 1（见 connect 的 KDoc）。
     */
    private fun connectAuto(addr: String, token: String) {
        connect(addr, token, resetBackoff = false)
    }

    /** 关闭连接（幂等；用户显式断开 = 不自动重连）。 */
    fun close() {
        userClosed = true
        autoReconnecting = false
        stopHeartbeat()
        stopReconnect()
        // 清账：挂起的 delta 没有投递对象了（断连后再投只会把半截文本归约进
        // 旧状态）。
        coalescer.reset()
        closeQuietly()
    }

    /** 只关连接不清重连标记（connectOnce 的开场清账用）。 */
    private fun closeQuietly() {
        connected = false
        val ws = wsRef.getAndSet(null) ?: return
        try {
            ws.close(1000, "client close")
        } catch (_: Exception) {
        }
        failAllPending("后端连接已断开")
    }

    // ===== 心跳（connection.ping：主动发现「TCP 没断但后端不回帧」的假活连接）=====

    private fun startHeartbeat() {
        stopHeartbeat()
        val r = object : Runnable {
            override fun run() {
                if (!connected) return
                pingOnce { ok ->
                    if (ok) {
                        heartbeatFailures = 0
                    } else {
                        heartbeatFailures++
                        Log.w(TAG, "心跳失败 $heartbeatFailures/$maxHeartbeatFailures")
                        if (heartbeatFailures >= maxHeartbeatFailures) {
                            // 主动断开触发重连路径（onFailure/onClosed → scheduleReconnect）
                            Log.w(TAG, "心跳连续失败，主动断开重连")
                            closeQuietly()
                            stopHeartbeat()
                            main.post { stateListener.onState(false, "心跳超时，正在重连…") }
                            scheduleReconnect(currentAddr, currentToken)
                            return@pingOnce
                        }
                    }
                    heartbeatRunnable = this
                    main.postDelayed(this, heartbeatIntervalMs)
                }
            }
        }
        heartbeatRunnable = r
        main.postDelayed(r, heartbeatIntervalMs)
    }

    private fun stopHeartbeat() {
        heartbeatRunnable?.let { main.removeCallbacks(it) }
        heartbeatRunnable = null
    }

    /** 发一次 connection.ping；ok = 超时前收到应答（3s，独立于普通请求的 10s）。
     *  应答到达走 deliverPending（按 id 配对）→ 回调在 pending 表上核对。 */
    private fun pingOnce(done: (Boolean) -> Unit) {
        val ws = wsRef.get()
        if (ws == null) {
            done(false)
            return
        }
        val id = nextId.incrementAndGet()
        val frame = JSONObject()
            .put("jsonrpc", "2.0")
            .put("id", id)
            .put("method", "connection.ping")
        val sent = try {
            ws.send(frame.toString())
        } catch (_: Exception) {
            false
        }
        if (!sent) {
            done(false)
            return
        }
        pending.add(
            PendingCall(
                id = id,
                method = "connection.ping",
                createdAt = System.currentTimeMillis(),
                // 应答到达：心跳回调（deliverPending 在主线程执行本回调）
                callback = { _, _ -> pingAnsweredId = id },
            ),
        )
        main.postDelayed({
            // 超时判定：**本次** ping 的应答没到（按请求 id 限定——共享布尔会被
            // 上一次心跳的迟到应答置真，让本次超时误判为成功）。
            // **连接 epoch 守卫**：按发送时的 ws 实例限定——超时回调触发前连接若已
            // 重建（重连成功换新 ws），旧 ping 的失败不算数（done(false) 会把
            // 心跳失败计数加到新连接头上 / 体检会把刚建好的连接误杀）。
            done(wsRef.get() === ws && pingAnsweredId == id)
        }, heartbeatTimeoutMs)
    }

    /** 最近一次收到应答的心跳请求 id（pingOnce 的超时判定读它；按 id 限定，
     *  不会被上一轮心跳的迟到应答污染）。 */
    @Volatile private var pingAnsweredId = 0

    // ===== 自动重连（指数退避；用户显式 close 不重连）=====

    private fun scheduleReconnect(addr: String, token: String) {
        if (userClosed) return
        autoReconnecting = true
        stopReconnect()
        val delay = reconnectBackoffMs
        reconnectBackoffMs = (reconnectBackoffMs * 2).coerceAtMost(30_000L)
        Log.i(TAG, "将在 ${delay}ms 后重连 $addr")
        val r = Runnable {
            reconnectRunnable = null
            // 必须走 connectAuto（= connect 的完整语义），不能直接 connectOnce：
            // ① 重连自失败后的下一轮要清 userClosed（connectOnce 不清账）；
            // ② connectOnce 复用当前 wsRef 关联的客户端形态，失败后的重试要重建。
            // 退避不重置（connectAuto 传 resetBackoff=false）：指数退避靠本函数
            // 自增 + 握手成功归 1。
            if (!userClosed && !connected) connectAuto(addr, token)
        }
        reconnectRunnable = r
        main.postDelayed(r, delay)
    }

    private fun stopReconnect() {
        reconnectRunnable?.let { main.removeCallbacks(it) }
        reconnectRunnable = null
    }

    /**
     * 亮屏即时重连（MainActivity onStart/onResume 调）：掉线且用户没显式断开时
     * 立即重连，不等退避定时器走完——退避最长 30s，回手机看一眼要等
     * 半分钟才重连是不可接受的。已连接 / 用户主动断开（close）时不动作。
     */
    fun reconnectIfDown() {
        if (userClosed || connected) return
        Log.i(TAG, "亮屏重连：立即 connect $currentAddr（不等退避）")
        stopReconnect()
        // 用户显式动作（回到前台）→ 退避重置（与开关/重试同语义）。
        connect(currentAddr, currentToken)
    }

    /**
     * 连接体检（RealBackend.onForeground 在已连接时调）：发一次 connection.ping，
     * 3s 无应答 = 死连接（TCP 没断但后端不回帧——心跳间隔 15s 内回前台的空窗，
     * 心跳还没来得及发现）。主动 close 走 onFailure → 自动重连照常排程。
     * 已连接才发；应答正常则无事发生。
     */
    fun healthCheck() {
        if (!connected) return
        Log.i(TAG, "回前台体检：ping 当前连接")
        pingOnce { ok ->
            if (!ok && connected) {
                Log.w(TAG, "体检超时，主动断开重连")
                closeQuietly()
                stopHeartbeat()
                main.post { stateListener.onState(false, "连接无响应，正在重连…") }
                scheduleReconnect(currentAddr, currentToken)
            }
        }
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
        // 超时看门狗（普通 10s；重方法 30s——对齐 frontend 的 HEAVY_METHODS）
        val timeoutMs = if (method in HEAVY_METHODS) HEAVY_REQUEST_TIMEOUT_MS else REQUEST_TIMEOUT_MS
        main.postDelayed({
            if (pending.removeIf { it.id == id }) {
                Log.w(TAG, "请求超时(${timeoutMs / 1000}s): $method")
                callback(errorObj(-1, "请求超时"), null)
            }
        }, timeoutMs)
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
            if (Log.isLoggable(TAG, Log.DEBUG)) {
                Log.d(TAG, "event $method ${params.toString().take(200)}")
            }
            main.post { coalescer.emit(method, params) }
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
