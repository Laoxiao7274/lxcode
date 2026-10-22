// 连接条目持久化 —— SharedPreferences 存 JSON 数组（连接管理 = 真实连接的落地）。
//
// 存什么：连接列表（含本机条目）+ 当前连接 id。字段 addr/token/name 逐条落盘，
// App 重启后连接列表原样恢复——「扫码/表单保存的连接」不再是一次性 mock 草稿。
//
// 选型：SharedPreferences 而非 DataStore——数据就一个 key 列表（<1KB）、读写频率
// 极低（增删改连接时写一次），引 DataStore + coroutines 是杀鸡用牛刀。
// 敏感性：token 是后端访问凭证，SharedPreferences 私有目录（MODE_PRIVATE），
// 与桌面端把 remote.json 放用户配置目录同级敏感度；不落明文日志。
package com.moyunteng.lxcode.remote

import android.content.Context
import android.content.SharedPreferences
import android.util.Log
import com.moyunteng.lxcode.remote.model.BackendConn
import org.json.JSONArray
import org.json.JSONObject

class ConnStore(context: Context) {

    private val prefs: SharedPreferences =
        context.getSharedPreferences("lxcode_connections", Context.MODE_PRIVATE)

    /** 读连接列表；没有任何存档返回 null（调用方决定 DEBUG 种子 / release 最小集）。 */
    fun loadConns(): List<BackendConn>? {
        val raw = prefs.getString(KEY_CONNS, null) ?: return null
        return try {
            val arr = JSONArray(raw)
            (0 until arr.length()).map { i ->
                val o = arr.getJSONObject(i)
                BackendConn(
                    id = o.optString("id"),
                    name = o.optString("name"),
                    addr = o.optString("addr"),
                    token = o.optString("token"),
                    local = o.optBoolean("local"),
                )
            }.filter { it.id.isNotEmpty() }
        } catch (e: Exception) {
            // 存档损坏不崩：当没有存档处理（回到种子列表），下次保存会覆盖坏档
            Log.w(TAG, "连接存档解析失败，回到种子列表: $e")
            null
        }
    }

    fun saveConns(conns: List<BackendConn>) {
        val arr = JSONArray()
        conns.forEach { c ->
            arr.put(
                JSONObject()
                    .put("id", c.id)
                    .put("name", c.name)
                    .put("addr", c.addr)
                    .put("token", c.token)
                    .put("local", c.local),
            )
        }
        prefs.edit().putString(KEY_CONNS, arr.toString()).apply()
    }

    /**
     * 连接草稿（引导态/表单里**尚未保存为连接条目**的输入：地址/Token/名称）。
     *
     * 与 [loadConns] 的已保存列表刻意分开：用户没点「保存」前重启不该丢输入——
     * 「真实后端」开关的手填地址与手动添加表单都从这里预填上次输入。
     * 仍属「连接设备信息」范畴（addr/token/name），同一个 prefs 文件，不落明文日志。
     */
    data class Draft(val addr: String, val token: String, val name: String)

    /** 读连接草稿；没有存档返回 null（调用方回各自默认值）。 */
    fun loadDraft(): Draft? {
        val raw = prefs.getString(KEY_DRAFT, null) ?: return null
        return try {
            val o = JSONObject(raw)
            Draft(o.optString("addr"), o.optString("token"), o.optString("name"))
        } catch (e: Exception) {
            Log.w(TAG, "连接草稿解析失败，当作没有草稿: $e")
            null
        }
    }

    fun saveDraft(addr: String, token: String, name: String) {
        val o = JSONObject().put("addr", addr).put("token", token).put("name", name)
        prefs.edit().putString(KEY_DRAFT, o.toString()).apply()
    }

    /** 读当前连接 id；没有存档返回 null。 */
    fun loadActiveId(): String? = prefs.getString(KEY_ACTIVE, null)

    fun saveActiveId(id: String) {
        prefs.edit().putString(KEY_ACTIVE, id).apply()
    }

    /**
     * 「真实后端」开关的持久化（true = 上次退出时处于真实模式）。
     * 重启恢复的依据：true → 启动时按当前连接条目自动 [重连]（连接持久化的另一半
     * ——凭证/列表/activeId 早已落盘，缺的就是这一个布尔）。
     * 用户显式断开必须写 false，否则重启又自动连上，违背断开的预期。
     */
    fun loadRealOn(): Boolean = prefs.getBoolean(KEY_REAL_ON, false)

    fun saveRealOn(on: Boolean) {
        prefs.edit().putBoolean(KEY_REAL_ON, on).apply()
    }

    companion object {
        private const val TAG = "ConnStore"
        private const val KEY_CONNS = "conns"
        private const val KEY_ACTIVE = "activeConnId"
        private const val KEY_DRAFT = "draft"
        private const val KEY_REAL_ON = "realOn"
    }
}
