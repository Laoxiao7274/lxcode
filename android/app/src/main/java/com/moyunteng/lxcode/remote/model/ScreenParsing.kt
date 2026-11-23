// screen.* → 投影的**纯函数**（不碰 Android / Compose / 网络——RealBackend 的
// applyScreenState 只做副作用装配，字段映射口径在这里用 JVM 单测钉住）。
//
// 契约：internal/protocol/screen.go 的 ScreenState（quality/error 等 omitempty
// 键缺席 = 未知/未设，不编造）；golden.json 的 screenStateFull /
// screenStateStoppedWithError 样本钉 wire 形状。
package com.moyunteng.lxcode.remote.model

import org.json.JSONObject

/**
 * screen.state / screen.start / screen.changed 的 state 载荷 → [ScreenStateInfo]。
 *
 * 口径（与桌面端 protocol.ScreenState 的 JSON 标签逐字对齐）：
 *  * `quality` 缺席或空 = null（P1-1：壳未上报档位时保持「未知」，UI 的
 *    「沿用当前档」逻辑据此不把清晰度重置回 medium）；
 *  * `error` 缺席或空 = null（无失败就是无失败，不发空串冒充）；
 *  * `monitor` 缺席 = 0（主显示器——无 omitempty 语义下缺席与 0 同义）。
 */
fun parseScreenState(o: JSONObject): com.moyunteng.lxcode.remote.ui.ScreenStateInfo =
    com.moyunteng.lxcode.remote.ui.ScreenStateInfo(
        active = o.optBoolean("active", false),
        monitor = o.optInt("monitor", 0),
        quality = o.optString("quality", "").ifEmpty { null },
        // fps 缺席/0 = 老壳未上报（omitempty 整键缺席）→ 0，UI 按「未知，30」对待。
        fps = o.optInt("fps", 0),
        encoder = o.optString("encoder", ""),
        error = o.optString("error", "").ifEmpty { null },
    )

/** screen.monitors 应答 → 显示器列表（shell_online 缺席 = false）。 */
fun parseScreenMonitors(r: JSONObject): Pair<List<com.moyunteng.lxcode.remote.ui.ScreenMonitorInfo>, Boolean> {
    val arr = r.optJSONArray("monitors")
    val monitors = if (arr == null) {
        emptyList()
    } else {
        (0 until arr.length()).mapNotNull { i ->
            val m = arr.optJSONObject(i) ?: return@mapNotNull null
            com.moyunteng.lxcode.remote.ui.ScreenMonitorInfo(
                id = m.optInt("id"),
                name = m.optString("name"),
                primary = m.optBoolean("primary"),
                width = m.optInt("width"),
                height = m.optInt("height"),
            )
        }
    }
    return monitors to r.optBoolean("shell_online", false)
}
