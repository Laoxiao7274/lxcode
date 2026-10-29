// ScreenDecoder 帧解析的纯函数单测：帧头解析（对齐 Go protocol.FrameHeader）、
// SPS/PPS 抽取（MediaCodec csd 配置的数据来源）。不涉及 MediaCodec 实例。
package com.moyunteng.lxcode.remote.ui

import okio.ByteString
import okio.ByteString.Companion.toByteString
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class ScreenDecoderTest {

    /** 构造帧：13 字节头（version/flags/len24/ts）+ AU。 */
    private fun frame(flags: Int, ts: Long, au: ByteArray): ByteString {
        val buf = ByteArray(13 + au.size)
        buf[0] = 1
        buf[1] = flags.toByte()
        buf[2] = ((au.size shr 16) and 0xff).toByte()
        buf[3] = ((au.size shr 8) and 0xff).toByte()
        buf[4] = (au.size and 0xff).toByte()
        var t = ts
        for (i in 12 downTo 5) {
            buf[i] = (t and 0xff).toByte()
            t = t shr 8
        }
        au.copyInto(buf, 13)
        return buf.toByteString()
    }

    private fun sc4() = byteArrayOf(0, 0, 0, 1)
    private fun sc3() = byteArrayOf(0, 0, 1)
    private fun nal(type: Int) = byteArrayOf((0x60 or type).toByte())

    @Test
    fun `帧头解析 - 各字段逐字节对齐`() {
        val au = byteArrayOf(1, 2, 3, 4)
        val meta = parseFrame(frame(FRAME_FLAG_KEYFRAME, 123456L, au))!!
        assertEquals(FRAME_FLAG_KEYFRAME, meta.flags)
        assertEquals(123456L, meta.tsMs)
        assertEquals(4, meta.payloadLen)
        assertArrayEquals(au, meta.au)
    }

    @Test
    fun `帧头解析 - 非法输入回 null`() {
        assertNull(parseFrame(ByteString.EMPTY))
        assertNull(parseFrame(ByteArray(10).toByteString()))
        // 版本不符
        val bad = ByteArray(20); bad[0] = 2
        assertNull(parseFrame(bad.toByteString()))
        // 载荷长度超出实际
        val short = ByteArray(13 + 2); short[2] = 0; short[3] = 0; short[4] = 9
        assertNull(parseFrame(short.toByteString()))
    }

    @Test
    fun `SPS PPS IDR 同 AU - 全部抽出`() {
        val sps = byteArrayOf(0x67.toByte(), 1, 2)
        val pps = byteArrayOf(0x68.toByte(), 3)
        val idr = byteArrayOf(0x65.toByte(), 4, 5, 6)
        val au = sc4() + sps + sc4() + pps + sc4() + idr
        val (foundSps, foundPps) = extractSpsPps(au)
        assertArrayEquals(sc4() + sps, foundSps)
        assertArrayEquals(sc4() + pps, foundPps)
    }

    @Test
    fun `无 SPS PPS 的 AU - 回 null`() {
        val au = sc3() + nal(1) + byteArrayOf(7)
        val (sps, pps) = extractSpsPps(au)
        assertNull(sps)
        assertNull(pps)
    }

    @Test
    fun `3字节与4字节start code 混用 - 抽取正常`() {
        val sps = byteArrayOf(0x67.toByte(), 9)
        val au = sc3() + sps + sc4() + nal(5) + byteArrayOf(1)
        val (foundSps, foundPps) = extractSpsPps(au)
        assertArrayEquals(sc3() + sps, foundSps)
        assertTrue(foundPps == null)
    }

    /** ByteArray + ByteArray 拼接（测试可读性）。 */
    private operator fun ByteArray.plus(other: ByteArray): ByteArray =
        copyOf(size + other.size).also { other.copyInto(it, size) }

    // ---- parseSpsSize：SPS 分辨率解析（JVM 纯函数；黑屏根因修复的钉子）----

    /** 手写 SPS RBSP 位流构造器（LSB-first 位序，与 BitReader 对偶）。 */
    private class BitWriter {
        private val out = java.io.ByteArrayOutputStream()
        private var cur = 0
        private var n = 0

        fun bit(v: Int) {
            cur = (cur shl 1) or (v and 1)
            n++
            if (n == 8) {
                out.write(cur); cur = 0; n = 0
            }
        }

        fun bits(v: Int, count: Int) {
            for (i in count - 1 downTo 0) bit((v shr i) and 1)
        }

        fun ue(v: Int) {
            val x = v + 1
            val len = 32 - java.lang.Integer.numberOfLeadingZeros(x)
            repeat(len - 1) { bit(0) }
            bits(x, len)
        }

        fun se(v: Int) = ue(if (v > 0) v * 2 - 1 else -v * 2)

        fun toBytes(): ByteArray {
            if (n == 0) return out.toByteArray()
            bits(1, 1) // rbsp_trailing_bits: 首个 1
            while (n != 0) bit(0) // 零填充到字节边界
            return out.toByteArray()
        }
    }

    /** 构造一个 baseline profile（66）的 SPS：宽 = (wMbs)*16、高 = (hMbs)*16，
     *  可选 bottom crop（4:2:0 单位）。SPS 载荷里出现 00 00 0x 时按规范插 03
     *  （emulation prevention）——与真实编码器产出同构。 */
    private fun buildSps(
        wMbs: Int,
        hMbs: Int,
        frameMbsOnly: Int = 1,
        cropBottom: Int = 0,
        profileIdc: Int = 66,
        levelIdc: Int = 0,
    ): ByteArray {
        val w = BitWriter()
        w.bits(profileIdc, 8) // profile_idc
        w.bits(levelIdc, 8) // constraint flags + level_idc（约束位 0 时字节值 = level_idc）
        w.ue(0) // sps_id
        if (profileIdc in intArrayOf(100, 110, 122, 244, 44, 83, 86, 118, 128, 138, 139, 134, 135)) {
            w.ue(1) // chroma_format_idc = 1（4:2:0）
            w.ue(0) // bit_depth_luma_minus8
            w.ue(0) // bit_depth_chroma_minus8
            w.bit(0) // qpprime_y_zero_transform_bypass_flag
            w.bit(0) // seq_scaling_matrix_present_flag
        }
        w.ue(0) // log2_max_frame_num_minus4
        w.ue(0) // pic_order_cnt_type = 0
        w.ue(0) // log2_max_pic_order_cnt_lsb_minus4
        w.ue(1) // max_num_ref_frames
        w.bit(0) // gaps_in_frame_num
        w.ue(wMbs - 1)
        w.ue(hMbs - 1)
        w.bit(frameMbsOnly)
        w.bit(1) // direct_8x8_inference
        if (cropBottom > 0) {
            w.bit(1) // frame_cropping_flag
            w.ue(0); w.ue(0); w.ue(0); w.ue(cropBottom)
        } else {
            w.bit(0) // frame_cropping_flag = 0（不写的话 rbsp trailing 的 1 会被当 flag）
        }
        w.bit(0) // vui_parameters_present_flag = 0
        val body = w.toBytes()
        // emulation prevention 只作用于 NAL 载荷（start code 本身不处理——
        // 否则 00 00 00 01 会被插成 00 00 03 00 01，起始码被破坏）
        val ep = mutableListOf<Byte>()
        var zeros = 0
        for (b in byteArrayOf(0x67.toByte()) + body) {
            if (zeros >= 2 && (b.toInt() and 0xff) <= 3) ep.add(3)
            if (b.toInt() == 0) zeros++ else zeros = 0
            ep.add(b)
        }
        return byteArrayOf(0, 0, 0, 1) + ep.toByteArray()
    }

    @Test
    fun `SPS 分辨率 - 1920x1088 无 crop`() {
        val sps = buildSps(120, 68)
        assertEquals(1920 to 1088, ScreenDecoder.parseSpsSize(sps))
    }

    @Test
    fun `SPS 分辨率 - bottom crop 到 1080`() {
        // 1088 高 crop 掉 8 像素（4:2:0 垂直单位 2×frameMbsOnly=2 → cropBottom=4）
        val sps = buildSps(120, 68, cropBottom = 4)
        assertEquals(1920 to 1080, ScreenDecoder.parseSpsSize(sps))
    }

    @Test
    fun `SPS 分辨率 - 2560x1440（用户非 1080p 显示器场景）`() {
        val sps = buildSps(160, 90)
        assertEquals(2560 to 1440, ScreenDecoder.parseSpsSize(sps))
    }

    @Test
    fun `SPS 分辨率 - 非法输入回 0x0`() {
        assertEquals(0 to 0, ScreenDecoder.parseSpsSize(ByteArray(4)))
        // IDR NAL 而非 SPS
        assertEquals(0 to 0, ScreenDecoder.parseSpsSize(byteArrayOf(0, 0, 0, 1, 0x65)))
    }

    @Test
    fun `SPS profile level - High 4_1（AMF 默认输出场景）`() {
        // profile_idc=100（High）、level_idc=41（4.1）
        val sps = buildSps(120, 68, profileIdc = 100, levelIdc = 41)
        assertEquals(100 to 41, ScreenDecoder.parseSpsProfileLevel(sps))
        // 同一 SPS 的分辨率解析不受影响（High 分支语法被正确消费）
        assertEquals(1920 to 1088, ScreenDecoder.parseSpsSize(sps))
    }

    @Test
    fun `SPS profile level - Baseline level 空`() {
        val sps = buildSps(120, 68, profileIdc = 66, levelIdc = 30)
        assertEquals(66 to 30, ScreenDecoder.parseSpsProfileLevel(sps))
    }

    @Test
    fun `SPS profile level - 非法输入回 -1`() {
        assertEquals(-1 to -1, ScreenDecoder.parseSpsProfileLevel(ByteArray(4)))
        assertEquals(-1 to -1, ScreenDecoder.parseSpsProfileLevel(byteArrayOf(0, 0, 0, 1, 0x65)))
    }

    @Test
    fun `丢关键帧后 gate 丢弃非关键帧 - 关键帧到达恢复`() {
        val gate = KeyframeGate()
        // 初始态：等首关键帧
        assertTrue(gate.needKeyframe)
        assertTrue(!gate.allow(0))
        assertTrue(gate.allow(FRAME_FLAG_KEYFRAME))
        // 关键帧入队 → 链重建，非关键帧放行
        gate.onKeyframeAccepted()
        assertTrue(!gate.needKeyframe)
        assertTrue(gate.allow(0))
        // 丢了一个 IDR（队列满丢最旧 / feedOne 超时丢 IDR）：回到等关键帧
        gate.onKeyframeLost()
        assertTrue(gate.needKeyframe)
        // 后续非关键帧全部被丢，关键帧仍放行
        assertTrue(!gate.allow(0))
        assertTrue(gate.allow(FRAME_FLAG_KEYFRAME))
        // 关键帧再次通过 → 恢复正常喂数
        gate.onKeyframeAccepted()
        assertTrue(!gate.needKeyframe)
        assertTrue(gate.allow(0))
    }

    @Test
    fun `帧流模拟 - 队列丢 IDR 后 P 帧不入队直到下一个 IDR`() {
        // 模拟 onParsedFrame 的判定顺序：gate.allow → 丢/入队。
        val gate = KeyframeGate()
        gate.onKeyframeAccepted() // 正常流中
        val queued = mutableListOf<Int>()
        fun onParsedFrame(flags: Int): Boolean {
            if (!gate.allow(flags)) return false
            if (flags and FRAME_FLAG_KEYFRAME != 0) gate.onKeyframeAccepted()
            // 队列满且没有非关键帧可丢 → 丢最旧（关键帧）→ gate 置位
            if (queued.size >= 2) {
                val dropIdx = queued.indexOfFirst { it and FRAME_FLAG_KEYFRAME == 0 }
                if (dropIdx >= 0) queued.removeAt(dropIdx) else gate.onKeyframeLost()
            }
            queued.add(flags)
            return true
        }
        assertTrue(onParsedFrame(FRAME_FLAG_KEYFRAME))
        assertTrue(onParsedFrame(0))
        assertTrue(onParsedFrame(0))
        // 队列里有非关键帧可丢 → 不丢关键帧，needKeyframe 仍为 false
        assertTrue(!gate.needKeyframe)
        // 后续塞满关键帧（极端情形：队列全是关键帧，再丢就只能丢 IDR）
        repeat(2) { assertTrue(onParsedFrame(FRAME_FLAG_KEYFRAME)) }
        assertTrue(onParsedFrame(FRAME_FLAG_KEYFRAME)) // 此时队列全关键帧 → 丢最旧 IDR
        // 队列满（2 帧且最旧是关键帧）→ 丢关键帧 → 置 needKeyframe
        assertTrue(gate.needKeyframe)
        // 后续 P 帧被丢弃
        assertTrue(!onParsedFrame(0))
        assertTrue(!onParsedFrame(0))
        // 下一个 IDR 到达：allow 放行并清零，但溢出又丢了最旧的 IDR → 重新置位
        // （真实链路里 feederLoop 压入这个新 IDR 时会再次 onKeyframeAccepted——
        //  置位窗口内的 P 帧被保守丢弃，安全方向）。
        assertTrue(onParsedFrame(FRAME_FLAG_KEYFRAME))
        assertTrue(gate.needKeyframe)
        // feederLoop 消费到该关键帧 → 接受 → 恢复正常喂数
        gate.onKeyframeAccepted()
        assertTrue(onParsedFrame(0))
    }
}
