// RingLog —— 投屏可观测性的环形事件日志（纯 JVM，无 Android 依赖——JVM 单测覆盖）。
//
// 痛点：投屏断开时手机端只有 toast「投屏已断开」，原因链（后端拉停/接收者切换/
// WS 断线/解码错误）不留痕，排查全靠 logcat（环形缓冲易冲掉）。本类把投屏
// 生命周期关键事件留在内存环形缓冲里，诊断面板（ScreenScreen 的 DiagPanel）
// 展示 + 一键复制到剪贴板回报——零权限、零文件写入。
//
// 线程模型：事件从 WS 线程 / 解码器回调线程 / 主线程多路写入，snapshot 复制读——
// synchronized 保写入原子性，读侧拿快照（容量 200 条 × ~60 字节，复制成本可忽略）。
package com.moyunteng.lxcode.remote.net

import java.time.LocalDateTime

/**
 * 有界环形事件日志。超容量淘汰最旧（ArrayDeque removeFirst）。
 *
 * [clock] 可注入：单测喂固定值断言时间戳格式与淘汰次序；运行时用默认。
 */
class RingLog(
    private val capacity: Int = 200,
    private val clock: () -> LocalDateTime = { LocalDateTime.now() },
) {
    /** 一条事件：时间戳 + 文本。 */
    data class Entry(val atMs: Long, val text: String) {
        /** 展示/导出用（HH:mm:ss——诊断面板与剪贴板共用一份口径）。 */
        fun render(): String = "${atMs.toClock()} $text"
    }

    private val deque = ArrayDeque<Entry>()
    private var seq = 0L

    /** 追加一条事件（线程安全；超容量淘汰最旧）。 */
    @Synchronized
    fun add(text: String) {
        val now = clock()
        deque.addLast(
            Entry(
                atMs = ((now.hour * 3600 + now.minute * 60 + now.second) * 1000L + (now.nano / 1_000_000)) % 86_400_000L,
                text = text,
            ),
        )
        while (deque.size > capacity) deque.removeFirst()
        seq++
    }
    /** 快照（渲染行数组；UI 与剪贴板共用，顺序 = 时间序）。 */
    @Synchronized
    fun snapshot(): List<String> = deque.map { it.render() }

    /** 是否有内容（诊断面板「最近事件」行是否渲染的依据）。 */
    @Synchronized
    fun isEmpty(): Boolean = deque.isEmpty()

    /** 自增版本：Compose 快照轮询用它判断有无新事件（无变化不重排）。 */
    @Synchronized
    fun version(): Long = seq

    companion object {
        /** 毫秒日内偏移 → HH:mm:ss（Entry.render 的格式化器；嵌套类 Entry 调不到外层私有成员）。 */
        internal fun Long.toClock(): String {
            val h = this / 3_600_000
            val m = this % 3_600_000 / 60_000
            val s = this % 60_000 / 1000
            return "%02d:%02d:%02d".format(h, m, s)
        }
    }
}
