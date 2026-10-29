// ScreenDecoder —— 投屏视频的 MediaCodec 硬解（AVC，Surface 输出）。
//
// 输入：LxWsClient 的二进制帧（internal/protocol/screen.go FrameHeader 分路：
// 文本帧 = JSON-RPC，二进制帧 = 视频）。每帧 = 13 字节头 + 一个访问单元的
// H.264 Annex B（SPS/PPS 与 IDR 同帧——壳侧按 NAL 分组封装，见 shell/src/screen-au.ts）。
//
// csd 处理：MediaCodec 要求配置阶段给 csd-0（SPS）/csd-1（PPS）。壳在**每个**
// 关键帧 AU 前缀 SPS/PPS（libx264 repeat-headers / NVENC 关键帧自带头），所以
// 首个关键帧到达时从 AU 里抽 SPS/PPS 作为 csd 提交给 configure；此后各关键帧
// 的 SPS/PPS 随 AU 原样喂给解码器（解码器自行处理重复参数集）。
//
// 低延迟（Android 11+）：KEY_LOW_LATENCY 关解码端重排序缓冲；配合编码端
// bf=0（无 B 帧）实现端到端低延迟。
//
// 线程模型：异步模式（MediaCodec.AsyncCallback）——输入排队在 WS 线程之外
//（dequeueInputId 队列），渲染回调在解码器线程。压帧线程从 frame 队列取帧
// （有界 30 帧，满 = 丢最旧非关键帧）。
package com.moyunteng.lxcode.remote.ui

import android.media.MediaCodec
import android.media.MediaCodecList
import android.media.MediaFormat
import android.util.Log
import android.view.Surface
import okio.ByteString
import okio.ByteString.Companion.toByteString

private const val TAG = "ScreenDecoder"

/** 帧头（internal/protocol/screen.go FrameHeader）：13 字节。 */
const val FRAME_HEADER_SIZE = 13
const val FRAME_VERSION = 1
const val FRAME_FLAG_KEYFRAME = 0x01
const val FRAME_FLAG_STOP = 0x02

/** 解析帧头。返回 null = 非法帧（版本不符/过短）。 */
data class FrameMeta(val flags: Int, val tsMs: Long, val au: ByteArray, val payloadLen: Int)

/** 解码器诊断快照（投屏页显示；黑屏排查的数据来源——手机端到底收没收到帧、
 *  解码器走到哪一步、最近错误是什么）。 */
data class DecoderStats(
    /** WS 层收到的二进制帧数（>0 = 帧到达手机了；恒 0 = 断点在上游）。 */
    val framesReceived: Int,
    /** 收到的关键帧数。 */
    val keyframesReceived: Int,
    /** 真正压进解码器的帧数。 */
    val framesFed: Int,
    /** gate 丢弃的非关键帧数（等关键帧期间的正常丢弃）。 */
    val framesDroppedGate: Int,
    /** 队列满丢弃的帧数（解码跟不上）。 */
    val framesDroppedQueue: Int,
    /** 压入失败（无输入缓冲超时/异常）丢弃的帧数。 */
    val framesDroppedFeed: Int,
    /** 解码器是否已用关键帧 csd 配置完成。 */
    val configured: Boolean,
    /** 当前流分辨率（SPS 解析或 onOutputFormatChanged；0×0 = 未知）。 */
    val width: Int,
    val height: Int,
    /** 最近一次错误（null = 无）。 */
    val lastError: String?,
    /** 最近一帧到达的墙钟（ms；0 = 从未收到帧）。 */
    val lastFrameAtMs: Long,
    /** SPS 解析出的 profile_idc（-1 = 未知/未解析）。 */
    val profileIdc: Int,
    /** SPS 解析出的 level_idc（-1 = 未知/未解析；H.264 level ×10，如 41=4.1）。 */
    val levelIdc: Int,
)

/** WS 二进制消息的帧切分器：后端按帧边界转发（screenDeframer），但单条 WS
 *  消息理论上仍可能粘连/缺尾（中间层缓冲语义差异），且历史后端整块透传——
 *  接收端按「头 13 字节 + len24 载荷」精确切分，完整帧逐帧回调，残尾留存拼
 *  下一块。此前按「一帧一消息」假设解析：粘连帧除首帧外全部静默丢弃（丢的
 *  若是关键帧 → 拿不到 csd → 黑屏 2s），半帧整条丢弃——重连后首关键帧正好
 *  被切进残尾时直接丢，黑屏拉长到下一 GOP。 */
class FrameSplitter {
    private var buf: ByteArray? = null

    /** 把一条 WS 二进制消息并进切分器，产出的每个完整帧回调 onFrame。
     *  返回切出的完整帧数。 */
    fun feed(msg: ByteArray, onFrame: (ByteArray) -> Int): Int {
        var acc = buf?.let { it + msg } ?: msg
        buf = null
        var sent = 0
        while (true) {
            if (acc.size < FRAME_HEADER_SIZE) break
            val len = ((acc[2].toInt() and 0xff) shl 16) or ((acc[3].toInt() and 0xff) shl 8) or (acc[4].toInt() and 0xff)
            if (len < 0 || len > FRAME_MAX_PAYLOAD) { acc = ByteArray(0); break } // 坏流：丢弃
            val total = FRAME_HEADER_SIZE + len
            if (acc.size < total) break // 半帧：留存拼下一块
            sent += onFrame(acc.copyOfRange(0, total))
            acc = acc.copyOfRange(total, acc.size)
        }
        if (acc.size > 0) {
            buf = if (acc.size > FRAME_MAX_PAYLOAD + FRAME_HEADER_SIZE) null else acc // 超限坏流：丢
        }
        return sent
    }
}

/** 单帧载荷上限（与 Go protocol.FrameMaxPayload 一致：len24 24bit 上限）。 */
const val FRAME_MAX_PAYLOAD = (1 shl 24) - 1

/** 解析帧头。返回 null = 非法帧（版本不符/过短）。 */
fun parseFrame(frame: ByteString): FrameMeta? {
    if (frame.size < FRAME_HEADER_SIZE) return null
    val b = frame.toByteArray()
    if (b[0] != FRAME_VERSION.toByte()) return null
    val len = ((b[2].toInt() and 0xff) shl 16) or ((b[3].toInt() and 0xff) shl 8) or (b[4].toInt() and 0xff)
    if (frame.size < FRAME_HEADER_SIZE + len) return null
    var ts = 0L
    for (i in 5 until 13) ts = (ts shl 8) or (b[i].toLong() and 0xff)
    return FrameMeta(b[1].toInt(), ts, b.copyOfRange(FRAME_HEADER_SIZE, FRAME_HEADER_SIZE + len), len)
}

/**
 * needKeyframe 状态机（纯逻辑，JVM 单测覆盖）：丢过 IDR 帧（队列满丢最旧恰好丢
 * 到关键帧、或 feedOne 超时丢当前帧为 IDR）或解码器 onError 之后，后续 P 帧参考
 * 链已断——继续喂数只会花屏/绿屏，必须丢弃所有非关键帧直到下一个 IDR（关键帧
 * AU 自带 SPS/PPS，顺带完成 csd 重配置）。RustDesk discard_queue 同款策略。
 */
class KeyframeGate {
    /** true = 等关键帧（丢弃非关键帧）。 */
    @Volatile var needKeyframe: Boolean = true
        private set

    /** 丢了一帧关键帧（丢最旧/超时/解码器错误重建）：参考链断裂，等下一个 IDR。 */
    fun onKeyframeLost() { needKeyframe = true }

    /** 关键帧通过（入队或压入）：链已重建，恢复喂数。 */
    fun onKeyframeAccepted() { needKeyframe = false }

    /** 重置到等关键帧（新流/重建：从关键帧开始）。 */
    fun reset() { needKeyframe = true }

    /** 该帧是否允许喂给解码器（needKeyframe 时只放关键帧）。 */
    fun allow(flags: Int): Boolean = !needKeyframe || flags and FRAME_FLAG_KEYFRAME != 0
}

/** 从 AU（Annex B）里抽出 SPS 与 PPS（nal type 7/8），供 csd 配置。 */
fun extractSpsPps(au: ByteArray): Pair<ByteArray?, ByteArray?> {
    var sps: ByteArray? = null
    var pps: ByteArray? = null
    var i = 0
    fun startCodeLen(b: ByteArray, off: Int): Int = when {
        off + 4 <= b.size && b[off] == 0.toByte() && b[off + 1] == 0.toByte() && b[off + 2] == 0.toByte() && b[off + 3] == 1.toByte() -> 4
        off + 3 <= b.size && b[off] == 0.toByte() && b[off + 1] == 0.toByte() && b[off + 2] == 1.toByte() -> 3
        else -> 0
    }
    while (i < au.size) {
        val sc = startCodeLen(au, i)
        if (sc == 0) { i++; continue }
        val nalStart = i + sc
        if (nalStart >= au.size) break
        val type = au[nalStart].toInt() and 0x1f
        // NAL 结束 = 下一个 start code
        var j = nalStart + 1
        while (j < au.size && startCodeLen(au, j) == 0) j++
        when (type) {
            7 -> sps = au.copyOfRange(i, j)
            8 -> pps = au.copyOfRange(i, j)
        }
        i = j
    }
    return sps to pps
}

/**
 * Surface 解码器。生命周期：start(surface) → onFrame(frame) 喂帧 → stop()。
 * 解码器配置在**第一个关键帧**到达时才做（在那之前缓存/丢弃非关键帧）。
 */
class ScreenDecoder(
    private val onError: (String) -> Unit,
) {
    @Volatile private var codec: MediaCodec? = null
    private var configured = false
    @Volatile private var started = false

    // ===== 诊断计数（@Volatile：WS 线程写 / UI 线程读）=====
    @Volatile private var framesReceived = 0
    @Volatile private var keyframesReceived = 0
    @Volatile private var framesFed = 0
    @Volatile private var framesDroppedGate = 0
    @Volatile private var framesDroppedQueue = 0
    @Volatile private var framesDroppedFeed = 0
    @Volatile private var lastError: String? = null
    @Volatile private var lastFrameAtMs = 0L
    @Volatile private var streamWidth = 0
    @Volatile private var streamHeight = 0
    @Volatile private var streamProfileIdc = -1
    @Volatile private var streamLevelIdc = -1

    /** 诊断快照（UI 线程随时可读；不锁——各计数独立递增，快照允许轻微不一致）。 */
    fun stats(): DecoderStats = DecoderStats(
        framesReceived = framesReceived,
        keyframesReceived = keyframesReceived,
        framesFed = framesFed,
        framesDroppedGate = framesDroppedGate,
        framesDroppedQueue = framesDroppedQueue,
        framesDroppedFeed = framesDroppedFeed,
        configured = configured,
        width = streamWidth,
        height = streamHeight,
        lastError = lastError,
        lastFrameAtMs = lastFrameAtMs,
        profileIdc = streamProfileIdc,
        levelIdc = streamLevelIdc,
    )

    /** 待压帧队列（帧 → flags；有界）。压帧线程消费。 */
    private val queue = ArrayDeque<Pair<ByteArray, Int>>()
    private val queueLock = Object()
    /** WS 消息 → 单帧切分器（容忍粘连/半帧，见 FrameSplitter）。 */
    @Volatile private var splitter = FrameSplitter()
    private var feederThread: Thread? = null
    @Volatile private var stopping = false

    /** 可复用的输入缓冲队列（异步模式 dequeueInput 产出）。 */
    private val inputIds = java.util.ArrayDeque<Int>()
    private val inputIdsLock = Object()

    /** 丢帧后等关键帧的状态机（见 KeyframeGate）——花屏防护。 */
    private val gate = KeyframeGate()
    /** 生命周期代次：onError 回调是异步的（codec 线程），用户可能在回调投递前
     *  已 stop()/start() 重建——迟到的重建 runnable 必须能识别「自己那一代已被
     *  越过」，否则会把刚建好的新解码器又停掉。 */
    private val generation = java.util.concurrent.atomic.AtomicInteger(0)

    /**
     * Surface 热重绑（旋转时 Surface 销毁重建，避免整解码器重建的黑窗）：
     * mediaCodec.setOutputSurface（API 23+）把已配置的 codec 切到新 surface。
     * feederLoop 的压帧不停（帧继续 render 到新 surface）；失败（codec 已 stop/
     * release 或 vendor 不支持）返回 false，调用方走整解码器重建——KeyframeGate
     * 保证重建后等下一个 IDR ≤2s 恢复画面。
     */
    fun rebindSurface(newSurface: Surface): Boolean {
        if (!started || stopping) return false
        val c = codec ?: return false
        return try {
            c.setOutputSurface(newSurface)
            Log.i(TAG, "surface 热重绑成功（旋转，无解码中断）")
            true
        } catch (e: Exception) {
            Log.w(TAG, "setOutputSurface 失败，回落整解码器重建: ${e.message}")
            false
        }
    }

    fun start(surface: Surface) {
        stop()        // stop() 把诊断计数清零了——这里恢复流级归零后的干净起点（start 即新流）
        generation.incrementAndGet() // 新一代：onError 触发的旧重建计划作废
        stopping = false
        configured = false
        gate.reset()
        started = true
        feederThread = Thread({
            feederLoop(surface)
        }, "screen-decoder-feeder").apply {
            priority = Thread.MAX_PRIORITY - 1
            start()
        }
    }

    fun stop() {
        stopping = true
        generation.incrementAndGet() // 任何在途的重建计划作废
        synchronized(queueLock) { queue.clear(); queueLock.notifyAll() }
        synchronized(inputIdsLock) { inputIds.clear() }
        feederThread?.interrupt()
        feederThread = null
        try {
            codec?.let { c ->
                c.stop()
                c.release()
            }
        } catch (e: Exception) {
            Log.w(TAG, "codec release: ${e.message}")
        }
        codec = null
        started = false
        // 切分器的残尾属于旧流：新流（重连/重启投屏）帧边界从头开始
        splitter = FrameSplitter()
        // 诊断计数归零：面板显示的是「本流」的收帧/解码情况，不是进程累计
        framesReceived = 0
        keyframesReceived = 0
        framesFed = 0
        framesDroppedGate = 0
        framesDroppedQueue = 0
        framesDroppedFeed = 0
        lastError = null
        lastFrameAtMs = 0L
        streamWidth = 0
        streamHeight = 0
        streamProfileIdc = -1
        streamLevelIdc = -1
    }

    /** WS 二进制帧入口（WS 线程回调）。先按帧边界切分（一条消息可能含多帧
     *  或半帧——粘住的帧里丢关键帧会黑屏一整个 GOP），再逐帧入队；满则丢最旧
     *  非关键帧。 */
    fun onFrame(msg: ByteString) {
        if (!started || stopping) return
        splitter.feed(msg.toByteArray(), ::onParsedFrame)
    }

    /** 单帧入口（已按帧边界切分）。 */
    private fun onParsedFrame(frame: ByteArray): Int {
        if (!started || stopping) return 0
        val meta = parseFrame(frame.toByteString()) ?: return 0
        framesReceived++
        lastFrameAtMs = android.os.SystemClock.elapsedRealtime()
        if (meta.flags and FRAME_FLAG_STOP != 0) {
            val reason = String(meta.au, Charsets.UTF_8)
            Log.i(TAG, "远端停止投屏: $reason")
            onError(reason)
            return 0
        }
        if (meta.flags and FRAME_FLAG_KEYFRAME != 0) keyframesReceived++
        // 未配置（还没见过关键帧）或丢过 IDR 等关键帧：非关键帧直接丢——
        // 参考链已断，继续喂数只会花屏。
        if (!gate.allow(meta.flags)) {
            framesDroppedGate++
            return 0
        }
        if (meta.flags and FRAME_FLAG_KEYFRAME != 0) gate.onKeyframeAccepted()
        synchronized(queueLock) {
            if (queue.size >= MAX_QUEUE) {
                // 丢最旧非关键帧；全是关键帧就丢最旧（极端情形）——丢到关键帧
                // 要等下一个 IDR（P 帧参考链断了，RustDesk discard_queue 同款）。
                val dropIdx = queue.indexOfFirst { it.second and FRAME_FLAG_KEYFRAME == 0 }
                if (dropIdx >= 0) {
                    queue.removeAt(dropIdx)
                    framesDroppedQueue++
                } else gate.onKeyframeLost()
            }
            queue.addLast(meta.au to meta.flags)
            queueLock.notifyAll()
        }
        return 1
    }

    private fun feederLoop(surface: Surface) {
        // 压帧循环：等关键帧 → configure → 循环 queue → codec
        val first = synchronized(queueLock) {
            while (queue.isEmpty() && !stopping) {
                try { queueLock.wait(500) } catch (_: InterruptedException) { return }
            }
            if (stopping) return
            queue.removeFirst()
        }
        val (firstAu, firstFlags) = first
        if (!configureWith(firstAu, surface)) {
            onError("解码器初始化失败（该设备不支持此流参数）")
            return
        }
        configured = true
        codec?.start()
        // 把首帧交回队列头（configure 之后统一压入）
        synchronized(queueLock) { queue.addFirst(firstAu to firstFlags) }
        while (!stopping) {
            val item = synchronized(queueLock) {
                while (queue.isEmpty() && !stopping) {
                    try { queueLock.wait(500) } catch (_: InterruptedException) { return }
                }
                if (stopping) return
                queue.removeFirst()
            }
            val (au, flags) = item
            if (!gate.allow(flags)) continue // 丢过 IDR 等关键帧：非关键帧不入解码器
            if (feedOne(au, flags)) {
                if (flags and FRAME_FLAG_KEYFRAME != 0) gate.onKeyframeAccepted()
            } else if (flags and FRAME_FLAG_KEYFRAME != 0) {
                // 关键帧压失败（超时/异常大帧）= 丢关键帧：参考链断，等下一个 IDR。
                gate.onKeyframeLost()
            }
        }
    }

    /** onError 后的整解码器重建：stop（释放死掉的 codec）→ start（起新压帧线程，
     *  needKeyframe=true 等下一个关键帧重新 configure）。回调线程不能直接 release
     *  它自己所在的 codec——切独立线程；重建计划经 generation 守卫（用户若已
     *  stop/重启，迟到的重建自动作废）。重建期间 onFrame 仍会入队（新 start 的
     *  压帧线程会先等关键帧，非关键帧被 gate 丢弃——与首启动同一路径）。 */
    private fun rebuildDecoder(surface: Surface) {
        val gen = generation.get()
        Thread({
            if (generation.get() != gen) return@Thread // 期间已 stop/start：作废
            Log.i(TAG, "解码器 onError：整解码器重建（等下一个关键帧）")
            start(surface)
        }, "screen-decoder-rebuild").apply { priority = Thread.MAX_PRIORITY - 1; start() }
    }

    /** 用关键帧 AU 的 SPS/PPS 配置解码器。分辨率从 SPS 解析（0×0 = 解析失败
     *  回落 1920×1080 占位——MediaCodec 的实际输出以 csd 为准，占位值只影响
     *  configure 校验；真机非 1080p 时硬编码占位不再与 SPS 冲突）。
     *
     *  降级链（真机「该设备不支持此流参数」的防御，2026-12）：configure 参数
     *  越多，个别 vendor 解码器对非预期键/缓冲尺寸的校验越脆。逐级退化：
     *    1) 全参数（csd + SPS 尺寸 + LOW_LATENCY + MAX_INPUT_SIZE）；
     *    2) 去 LOW_LATENCY 与 MAX_INPUT_SIZE（未知键兼容性问题——低版本把
     *       KEY_LOW_LATENCY 当普通 int 键时个别实现按未声明键拒绝）；
     *    3) 软编解码器 c2.android.avc.decoder 兜底（不吃 vendor 私货，几乎
     *       只要求 csd 与尺寸自洽）——CPU 占用变高、功耗变大，但能出画面。
     *  每级失败都把 SPS profile/level 并进错误文案（诊断面板可见，用户回报
     *  一次就能对上设备的 profileLevels 支持表）。 */
    private fun configureWith(keyframeAu: ByteArray, surface: Surface): Boolean {
        val (sps, pps) = extractSpsPps(keyframeAu)
        if (sps == null || pps == null) {
            Log.w(TAG, "关键帧 AU 里没找到 SPS/PPS")
            onError("关键帧 AU 里没找到 SPS/PPS（编码流异常）")
            return false
        }
        val (w, h) = parseSpsSize(sps)
        val (profile, level) = parseSpsProfileLevel(sps)
        streamProfileIdc = profile
        streamLevelIdc = level
        val dimsDesc = if (w > 0) "${w}x${h}" else "未知分辨率"
        val capsDesc = "profile_idc=$profile level=$level"
        if (w > 0 && h > 0) {
            streamWidth = w
            streamHeight = h
            Log.i(TAG, "SPS 分辨率: ${w}x${h}, $capsDesc")
        }

        fun baseFormat(width: Int, height: Int): MediaFormat =
            MediaFormat.createVideoFormat(MediaFormat.MIMETYPE_VIDEO_AVC, width, height).apply {
                setByteBuffer("csd-0", java.nio.ByteBuffer.wrap(sps))
                setByteBuffer("csd-1", java.nio.ByteBuffer.wrap(pps))
            }

        fun tryConfigure(name: String?, minimal: Boolean): Boolean {
            val format = baseFormat(if (w > 0) w else 1920, if (h > 0) h else 1080)
            if (!minimal) {
                // 低延迟：KEY_LOW_LATENCY（API 30+ 的键；配合编码端 bf=0 端到端低延迟）。
                format.setInteger(MediaFormat.KEY_LOW_LATENCY, 1)
                // max-input-size 防大帧 OOM（1080p 关键帧典型 <512KB）
                format.setInteger(MediaFormat.KEY_MAX_INPUT_SIZE, 2 shl 20)
            }
            val c = if (name != null) {
                MediaCodec.createByCodecName(name)
            } else {
                MediaCodec.createDecoderByType(MediaFormat.MIMETYPE_VIDEO_AVC)
            }
            try {
                c.configure(format, surface, null, 0)
                c.setCallback(object : MediaCodec.Callback() {
                    override fun onInputBufferAvailable(mc: MediaCodec, index: Int) {
                        synchronized(inputIdsLock) { inputIds.add(index); inputIdsLock.notifyAll() }
                    }
                    override fun onOutputBufferAvailable(mc: MediaCodec, index: Int, info: MediaCodec.BufferInfo) {
                        // Surface 渲染模式：直接 release（render=true），不碰缓冲
                        try { mc.releaseOutputBuffer(index, true) } catch (e: Exception) { Log.w(TAG, "release: ${e.message}") }
                    }
                    override fun onError(mc: MediaCodec, e: MediaCodec.CodecException) {
                        Log.w(TAG, "codec error: ${e.message}")
                        lastError = "解码器错误: ${e.message ?: e.javaClass.simpleName}，正在重建解码器"
                        onError(lastError!!)
                        // 解码器已死（CodecException 后 codec 不可复用）：整解码器重建
                        // ——stop→start 复用既有路径；重建后 needKeyframe=true，等下一个
                        // 关键帧重新 configure（在回调线程外重建，避免在 codec 自己的
                        // 线程里 release 它；start/stop 期间的 generation 守卫防止
                        // 迟到的重建覆盖用户的 stop/重启意图）。
                        rebuildDecoder(surface)
                    }
                    override fun onOutputFormatChanged(mc: MediaCodec, format: MediaFormat) {
                        // 输出格式落地：实际解码分辨率以这里为准（SPS 解析失败的兜底；
                        // 也供诊断面板显示）。Surface 模式下 renderer 会按它缩放。
                        val ow = format.getInteger(MediaFormat.KEY_WIDTH)
                        val oh = format.getInteger(MediaFormat.KEY_HEIGHT)
                        if (ow > 0 && oh > 0) {
                            streamWidth = ow
                            streamHeight = oh
                            Log.i(TAG, "解码输出分辨率: ${ow}x${oh}")
                        }
                    }
                })
                codec = c
                configured = true
                return true
            } catch (e: Exception) {
                // 本级失败：先释放这个没 configure 成功的 codec，再由调用方降级
                Log.w(TAG, "configure($name${if (minimal) ", minimal" else ""}) failed: ${e.message}")
                try { c.release() } catch (e2: Exception) { Log.w(TAG, "release after configure fail: ${e2.message}") }
                lastError = "解码器配置失败(${name ?: "默认解码器"}${if (minimal) ", 精简参数" else ""}): " +
                    "${e.message ?: e.javaClass.simpleName} [$dimsDesc, $capsDesc]"
                return false
            }
        }

        // 1) 默认硬解 + 全参数；2) 默认硬解 + 精简参数（去 LOW_LATENCY/MAX_INPUT_SIZE）；
        // 3) 软编 c2.android.avc.decoder 兜底。
        if (tryConfigure(null, minimal = false)) return true
        if (tryConfigure(null, minimal = true)) return true
        if (tryConfigure(SOFT_AVC_DECODER, minimal = true)) {
            Log.w(TAG, "回落软编解码器 $SOFT_AVC_DECODER")
            return true
        }
        onError("$lastError [$dimsDesc, $capsDesc]——已尝试硬解全参数/精简参数/软编解码器")
        return false
    }

    /** 压一帧（等待输入缓冲；最多 500ms 放弃 = 丢帧）。返回是否真的压进解码器
     *  （false = 丢弃；丢关键帧时由调用方置 needKeyframe——参考链已断）。 */
    private fun feedOne(au: ByteArray, flags: Int): Boolean {
        val c = codec ?: return false
        val index: Int = synchronized(inputIdsLock) {
            if (inputIds.isEmpty()) {
                try { inputIdsLock.wait(500) } catch (_: InterruptedException) { framesDroppedFeed++; return false }
            }
            inputIds.pollFirst() ?: run { framesDroppedFeed++; return false } // 500ms 无缓冲 = 丢帧
        }
        try {
            val buf = c.getInputBuffer(index) ?: run { framesDroppedFeed++; return false }
            if (au.size > buf.capacity()) {
                // 异常大帧：丢（防 OOM；正常 AU 远小于 MAX_INPUT_SIZE）
                c.queueInputBuffer(index, 0, 0, 0, 0)
                framesDroppedFeed++
                return false
            }
            buf.clear()
            buf.put(au)
            val tsUs = 0L // Annex B 无时序；Surface 模式尽快渲染即可
            c.queueInputBuffer(index, 0, au.size, tsUs, 0)
            framesFed++
            return true
        } catch (e: Exception) {
            Log.w(TAG, "feed: ${e.message}")
            framesDroppedFeed++
            return false
        }
    }

    companion object {
        /** 待压帧队列上限（≈1s @30fps）。 */
        private const val MAX_QUEUE = 30

        /** 软编 AVC 解码器名（configure 降级链末级兜底；AOSP 全设备自带——
         *  c2.android.avc.decoder 是 Android 12+ 的标准名，老设备为
         *  OMX.google.h264.decoder，按系统版本取其一）。 */
        private val SOFT_AVC_DECODER: String? by lazy {
            try {
                val list = MediaCodecList(MediaCodecList.REGULAR_CODECS)
                val names = if (android.os.Build.VERSION.SDK_INT >= 31) {
                    list.codecInfos.mapNotNull { it.name } +
                        listOf("OMX.google.h264.decoder")
                } else {
                    listOf("c2.android.avc.decoder", "OMX.google.h264.decoder")
                }
                names.firstOrNull { n ->
                    runCatching {
                        val info = list.codecInfos.firstOrNull { it.name == n } ?: return@firstOrNull false
                        !info.isEncoder && info.supportedTypes.contains(MediaFormat.MIMETYPE_VIDEO_AVC)
                    }.getOrDefault(false)
                }
            } catch (_: Exception) {
                null
            }
        }

        /**
         * 从 SPS NAL（含 start code）解析 profile_idc 与 level_idc（level ×10，
         * 与 H.264 表述一致：level_idc=41 → "4.1"；-1,-1 = 解析失败）。
         * 纯字节运算，JVM 单测覆盖——诊断面板用它对照设备解码器能力表。
         */
        fun parseSpsProfileLevel(sps: ByteArray): Pair<Int, Int> {
            try {
                var pos = 0
                var sc = 0
                while (pos < sps.size) {
                    sc = when {
                        pos + 4 <= sps.size && sps[pos] == 0.toByte() && sps[pos + 1] == 0.toByte() &&
                            sps[pos + 2] == 0.toByte() && sps[pos + 3] == 1.toByte() -> 4
                        pos + 3 <= sps.size && sps[pos] == 0.toByte() && sps[pos + 1] == 0.toByte() &&
                            sps[pos + 2] == 1.toByte() -> 3
                        else -> 0
                    }
                    if (sc > 0) break
                    pos++
                }
                if (sc == 0) return -1 to -1
                pos += sc
                if (pos >= sps.size || (sps[pos].toInt() and 0x1f) != 7) return -1 to -1
                if (pos + 2 >= sps.size) return -1 to -1
                val profileIdc = sps[pos + 1].toInt() and 0xff
                val levelIdc = sps[pos + 2].toInt() and 0xff
                return profileIdc to levelIdc
            } catch (_: Exception) {
                return -1 to -1
            }
        }

        /**
         * 从 SPS NAL（含 start code）解析编码分辨率（宽×高，像素；失败回 0×0）。
         *
         * 为什么必须解析：原先 configure 硬编码 1920×1080 占位，而用户显示器
         * 不是 1080p 时占位与 csd（SPS）冲突——部分解码器在 configure 校验/
         * 首帧对齐时直接失败 → 黑屏。现在 configure 用 SPS 的真实宽高，csd 照发。
         *
         * 实现：H.264 SPS RBSP 手写解析（High profile 的 scaling list、POC
         * 类型 1、frame cropping 都按语法跳过/消费；crop 只影响对齐修正，
         * 不影响「编码宽高」量级，这里返回**含 crop 的最终显示宽高**：
         * crop 单位按 chroma 4:2:0 取 2 像素）。纯字节运算，JVM 单测覆盖。
         */
        fun parseSpsSize(sps: ByteArray): Pair<Int, Int> {
            try {
                // 定位 SPS NAL 载荷（跳过 start code + nal 头）
                var off = 0
                var sc = 0
                while (off < sps.size) {
                    sc = when {
                        off + 4 <= sps.size && sps[off] == 0.toByte() && sps[off + 1] == 0.toByte() &&
                            sps[off + 2] == 0.toByte() && sps[off + 3] == 1.toByte() -> 4
                        off + 3 <= sps.size && sps[off] == 0.toByte() && sps[off + 1] == 0.toByte() &&
                            sps[off + 2] == 1.toByte() -> 3
                        else -> 0
                    }
                    if (sc > 0) break
                    off++
                }
                if (sc == 0) return 0 to 0
                var pos = off + sc
                if (pos >= sps.size || (sps[pos].toInt() and 0x1f) != 7) return 0 to 0
                pos++
                // Emulation prevention 去除（00 00 03 → 00 00）
                val rbsp = ByteArray(sps.size - pos)
                var n = 0
                var zeros = 0
                while (pos < sps.size) {
                    val b = sps[pos].toInt() and 0xff
                    if (zeros == 2 && b == 3) {
                        zeros = 0
                    } else {
                        rbsp[n++] = b.toByte()
                        zeros = if (b == 0) zeros + 1 else 0
                    }
                    pos++
                }
                val bits = BitReader(rbsp, n)
                bits.skipBits(8) // profile_idc
                bits.skipBits(8) // constraint flags + level_idc
                bits.skipUE() // seq_parameter_set_id
                val profileIdc = rbsp[0].toInt() and 0xff
                if (profileIdc in intArrayOf(100, 110, 122, 244, 44, 83, 86, 118, 128, 138, 139, 134, 135)) {
                    val chroma = bits.skipUE() // chroma_format_idc
                    if (chroma == 3) bits.skipBits(1) // separate_colour_plane_flag
                    bits.skipUE() // bit_depth_luma_minus8
                    bits.skipUE() // bit_depth_chroma_minus8
                    bits.skipBits(1) // qpprime_y_zero_transform_bypass_flag
                    if (bits.readBit() == 1) { // seq_scaling_matrix_present_flag
                        val count = if (chroma != 0) 8 else 12
                        for (i in 0 until count) {
                            if (bits.readBit() == 1) { // seq_scaling_list_present_flag[i]
                                skipScalingList(bits, if (i < 6) 16 else 64)
                            }
                        }
                    }
                }
                bits.skipUE() // log2_max_frame_num_minus4
                val pocType = bits.skipUE()
                if (pocType == 0) {
                    bits.skipUE() // log2_max_pic_order_cnt_lsb_minus4
                } else if (pocType == 1) {
                    bits.skipBits(1) // delta_pic_order_always_zero_flag
                    bits.skipSE() // offset_for_non_ref_pic
                    bits.skipSE() // offset_for_top_to_bottom_field
                    val cnt = bits.skipUE()
                    repeat(cnt) { bits.skipSE() } // offset_for_ref_frame
                }
                bits.skipUE() // max_num_ref_frames
                bits.skipBits(1) // gaps_in_frame_num_value_allowed_flag
                val picWidthInMbsMinus1 = bits.skipUE()
                val picHeightInMapUnitsMinus1 = bits.skipUE()
                val frameMbsOnly = bits.readBit()
                if (frameMbsOnly == 0) bits.skipBits(1) // mb_adaptive_frame_field_flag
                bits.skipBits(1) // direct_8x8_inference_flag
                var width = (picWidthInMbsMinus1 + 1) * 16
                var height = (2 - frameMbsOnly) * (picHeightInMapUnitsMinus1 + 1) * 16
                if (bits.readBit() == 1) { // frame_cropping_flag
                    val cl = bits.skipUE(); val cr = bits.skipUE()
                    val ct = bits.skipUE(); val cb = bits.skipUE()
                    // crop 单位（4:2:0）：水平 2 像素；垂直 2×(2-frameMbsOnly)
                    width -= (cl + cr) * 2
                    height -= (ct + cb) * 2 * (2 - frameMbsOnly)
                    if (width <= 0 || height <= 0) return 0 to 0
                }
                return width to height
            } catch (_: Exception) {
                return 0 to 0
            }
        }

        /** scaling list 跳过（SE(v) 序列）。 */
        private fun skipScalingList(bits: BitReader, size: Int) {
            var lastScale = 8
            var nextScale = 8
            for (j in 0 until size) {
                if (nextScale != 0) {
                    val delta = bits.skipSE()
                    nextScale = (lastScale + delta + 256) % 256
                }
                lastScale = if (nextScale == 0) lastScale else nextScale
            }
        }

        /** LSB-first 位读器（H.264 RBSP 用）。 */
        private class BitReader(data: ByteArray, len: Int) {
            private val d = data
            private val n = len
            private var bit = 0

            fun readBit(): Int {
                if (bit >= n * 8) throw IllegalStateException("SPS 越界")
                val v = (d[bit ushr 3].toInt() ushr (7 - (bit and 7))) and 1
                bit++
                return v
            }

            fun skipBits(count: Int) { bit += count }

            /** UE(v)：读前导零 + 1 + (zeros 位信息)。返回值。 */
            fun skipUE(): Int {
                var zeros = 0
                while (readBit() == 0) {
                    zeros++
                    if (zeros > 31) throw IllegalStateException("UE 异常")
                }
                var v = 1
                repeat(zeros) { v = (v shl 1) or readBit() }
                return v - 1
            }

            /** SE(v)：映射到 UE。 */
            fun skipSE(): Int {
                val ue = skipUE()
                return if (ue and 1 == 1) (ue + 1) / 2 else -(ue / 2)
            }
        }
    }
}
