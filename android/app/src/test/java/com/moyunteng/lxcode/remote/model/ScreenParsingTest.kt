// screen 域解析的纯 JVM 单测（工程约定「构建即验证」，不引 robolectric；与
// ModelListParsingTest 同模式——org.json 用 testImplementation 的真实现）。
//
// 口径契约（model/ScreenParsing.kt，对齐 internal/protocol/screen.go 的 JSON
// 标签与 golden.json 的 screenState* / screenMonitorsResult 样本）：
//  * quality/error 缺席或空 = null（P1-1：不编造 "medium"，无失败不发空串）；
//  * monitor 缺席 = 0（主显示器）；
//  * monitors 缺席 = 空表、shell_online 缺席 = false（壳不在线的降级形态）。
package com.moyunteng.lxcode.remote.model

import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class ScreenParsingTest {

    // ===== parseScreenState：golden screenStateFull 的全量形态 =====

    @Test
    fun fullStateMapsEveryField() {
        val o = JSONObject()
            .put("active", true)
            .put("monitor", 1)
            .put("quality", "high")
            .put("encoder", "h264_nvenc")
            .put("width", 1920)
            .put("height", 1080)
            .put("receiver", "rcpt_1")
        val st = parseScreenState(o)
        assertTrue(st.active)
        assertEquals(1, st.monitor)
        assertEquals("high", st.quality)
        assertEquals("h264_nvenc", st.encoder)
        assertNull(st.error)
    }

    // ===== parseScreenState：golden screenStateStoppedWithError 的冷态 =====
    // active=false 是显式零值（无 omitempty 恒发）；其余键缺席。

    @Test
    fun stoppedWithErrorKeepsReason() {
        val st = parseScreenState(JSONObject().put("active", false).put("error", "壳已退出（投屏中断）"))
        assertFalse(st.active)
        assertEquals(0, st.monitor)
        assertEquals("壳已退出（投屏中断）", st.error)
        assertNull(st.quality)
        assertEquals("", st.encoder)
    }

    @Test
    fun missingKeysFallBackToUnknownNotFabricated() {
        // P1-1 的钉子：quality/error 缺席 = null（老壳不上报档位时「沿用当前档」
        // 不得把它重置回 medium——编造 "medium" 就是那个 bug 的根因）。
        val st = parseScreenState(JSONObject().put("active", true))
        assertTrue(st.active)
        assertNull(st.quality)
        assertNull(st.error)
        assertEquals(0, st.monitor)
    }

    @Test
    fun emptyStringQualityIsUnknownToo() {
        val st = parseScreenState(JSONObject().put("quality", ""))
        assertNull(st.quality)
    }

    // ===== fps（帧率上限扩展）=====

    @Test
    fun fpsParsedWhenPresent() {
        // 帧率上限 120（144 已摘档）——解析层原样透传 wire 值，钳制在上层
        // （AppRoot screenPickFps / rememberScreenCast / 后端 NormalizeFPS）。
        val st = parseScreenState(JSONObject().put("active", true).put("fps", 120))
        assertEquals(120, st.fps)
        // 旧壳/旧记忆仍可能带 144：解析层不编造，值照传（防御性钳制在 UI 层）。
        assertEquals(144, parseScreenState(JSONObject().put("active", true).put("fps", 144)).fps)
    }

    @Test
    fun fpsMissingOrZeroIsUnknownNotFabricated() {
        // 老壳不上报 fps（omitempty 整键缺席）→ 0，UI 按「未知，默认 30」对待；
        // 不编造 30（与 quality 的「未知沿用」同款纪律）。
        assertEquals(0, parseScreenState(JSONObject().put("active", true)).fps)
        assertEquals(0, parseScreenState(JSONObject().put("active", true).put("fps", 0)).fps)
    }

    // ===== fallback_active（兼容模式捕获位，三端透传）=====

    @Test
    fun fallbackActiveParsedWhenPresent() {
        val st = parseScreenState(
            JSONObject().put("active", true).put("fallback_active", true)
        )
        assertTrue(st.fallbackActive)
        assertFalse(parseScreenState(JSONObject().put("active", true)).fallbackActive)
    }

    @Test
    fun fallbackActiveMissingIsFalseNotFabricated() {
        // 老壳不上报该键（omitempty 整键缺席）→ false；错误键值（字符串等）
        // optBoolean 也回落 false，不编造兼容模式态。
        assertEquals(false, parseScreenState(JSONObject()).fallbackActive)
        assertEquals(false, parseScreenState(JSONObject().put("fallback_active", "yes")).fallbackActive)
    }

    // ===== parseScreenMonitors：golden screenMonitorsResult 形态 =====

    @Test
    fun monitorsMapAllFields() {
        val arr = JSONArray()
            .put(JSONObject().put("id", 0).put("name", "内置显示器").put("primary", true)
                .put("width", 2560).put("height", 1440).put("attached", true))
            .put(JSONObject().put("id", 1).put("name", "外接 4K").put("primary", false)
                .put("width", 3840).put("height", 2160).put("attached", false))
        val (monitors, shellOnline) = parseScreenMonitors(JSONObject().put("monitors", arr).put("shell_online", true))
        assertEquals(2, monitors.size)
        assertEquals("内置显示器", monitors[0].name)
        assertTrue(monitors[0].primary)
        assertEquals(2560, monitors[0].width)
        assertFalse(monitors[1].primary)
        assertTrue(shellOnline)
    }

    @Test
    fun monitorsMissingFallsBackEmpty() {
        // 壳不在线：monitors/shell_online 都缺席 → 空表 + false（refreshScreen 的降级形态）。
        val (monitors, shellOnline) = parseScreenMonitors(JSONObject())
        assertTrue(monitors.isEmpty())
        assertFalse(shellOnline)
    }
}
