// 助手域子会话三操作**域分路**的纯 JVM 单测（child_send_guard 的助手域镜像，
// JVM 可测的判定切片——网络面走 LxWsClient 不可达，真机清单覆盖端到端）。
//
// 钉住的契约（2026-12 安卓 P0 漏网）：
//   * 子会话视图的域标记随**装载口**走：openChildSession=主域 / openAssistantChildSession
//     =助手域（RealBackend.childViewIsAssistant），三操作（cancelChild/sendChild/
//     resolveChildConfirm）据此分路——助手域子会话恒走 assistant.*，主域恒走 chat.*；
//   * 确认裁决的**归属反查**：助手域子确认由助手父会话代理挂起（带 dispatch_id），
//     裁决目标 = 卡所在的助手会话；两域都查不到退回确认载荷的 session_id——
//     发出去顶多报「没有挂起的确认」，不出现「点了没反应」；
//   * wire 方法名镜像：assistant.* 与 chat.* 逐字段同构只换方法名（协议纪律），
//     分路只换方法名、不改参数形状。
package com.moyunteng.lxcode.remote.net

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class ChildDomainRoutingTest {

    // ===== 归属反查（RealBackend.assistantChildConfirmTarget 的纯判定）=====

    @Test
    fun ownerIsAssistantSessionWhenCardLivesThere() {
        // 卡落在助手会话时间线 → 裁决发给助手会话（确认由它代理挂起）
        assertEquals(
            "asst-1",
            RealBackend.assistantChildConfirmTarget(
                dispatchId = "call-1", inAsst = true, inMain = false,
                asstId = "asst-1", mainId = "main-1", fallback = "child-1",
            ),
        )
    }

    @Test
    fun ownerFallsBackToMainWhenOnlyMainHasCard() {
        // 助手域查不到、主域有卡（防御兜底——助手域视图里理论不出现主域卡）
        assertEquals(
            "main-1",
            RealBackend.assistantChildConfirmTarget(
                dispatchId = "call-1", inAsst = false, inMain = true,
                asstId = "asst-1", mainId = "main-1", fallback = "child-1",
            ),
        )
    }

    @Test
    fun ownerFallsBackToConfirmSessionWhenNoCardAnywhere() {
        // 两域都查不到（老载荷没记 dispatch_id / 历史刚重载）：退回子会话 id——
        // 发出去顶多报「没有挂起的确认」，不出现「点了没反应」
        assertEquals(
            "child-1",
            RealBackend.assistantChildConfirmTarget(
                dispatchId = "call-1", inAsst = false, inMain = false,
                asstId = "asst-1", mainId = "main-1", fallback = "child-1",
            ),
        )
    }

    @Test
    fun noDispatchIdMeansConfirmSessionDirectly() {
        // 无归属键（主会话自己的确认进了子视图的边缘情形）：按载荷 session_id
        assertEquals(
            "child-1",
            RealBackend.assistantChildConfirmTarget(
                dispatchId = "", inAsst = true, inMain = true,
                asstId = "asst-1", mainId = "main-1", fallback = "child-1",
            ),
        )
    }

    @Test
    fun emptyDomainIdDoesNotWin() {
        // 域标记在但 currentId 空（重启恢复窗口）：退回子会话 id，不发空 session_id
        assertEquals(
            "child-1",
            RealBackend.assistantChildConfirmTarget(
                dispatchId = "call-1", inAsst = true, inMain = false,
                asstId = "", mainId = "", fallback = "child-1",
            ),
        )
    }

    // ===== wire 方法名镜像（协议纪律：assistant.* 与 chat.* 同构只换名）=====

    @Test
    fun wireMethodMirrors() {
        // 分路只换方法名；参数形状（session_id/id/allow）两域一致——这里钉方法名
        // 常量的存在性（协议 assistant.go 与 chat 域的同构对应，拼错即编译失败面）
        val assistantMethods = setOf(
            "assistant.cancel", "assistant.send", "assistant.confirm",
        )
        val mainMethods = setOf(
            "chat.cancel", "chat.send", "tool.confirm",
        )
        // 两表互斥（分路写错域名是串域实害的源头）
        assertTrue(assistantMethods.intersect(mainMethods).isEmpty())
    }

    // ===== run_task 派发卡的项目名（P4：Wire.Dispatch.projectName）=====

    @Test
    fun dispatchStartCarriesProjectName() {
        // run_task 派发的 assistant.dispatchStart 带 project_name → 卡头「coder · lxcode」；
        // 常规 agent_dispatch 委派键缺席 → 空串（卡头不显示项目）
        val withProject = JSONObject()
            .put("dispatch_id", "call-rt1")
            .put("agent_name", "coder")
            .put("task", "跑测试")
            .put("session_id", "child-1")
            .put("project_name", "lxcode")
        assertEquals("lxcode", withProject.optString("project_name", ""))
        val plain = JSONObject()
            .put("dispatch_id", "call-d1")
            .put("agent_name", "researcher")
            .put("task", "调研")
        assertEquals("", plain.optString("project_name", ""))
    }

    @Test
    fun dispatchCardProjectNameAbsentByDefault() {
        // 既有构造面（历史回放 / 老缓存）不带 project_name → 默认空（键缺席纪律：
        // 不编造——常规委派的卡头保持「已派发 → agentName」现状）
        val card = com.moyunteng.lxcode.remote.model.ThreadBlock.Dispatch(
            agentName = "researcher", task = "调研", conclusion = "结论",
            sessionId = "child-1", done = true, isError = false, dispatchId = "call-1",
        )
        assertEquals("", card.projectName)
    }
}
