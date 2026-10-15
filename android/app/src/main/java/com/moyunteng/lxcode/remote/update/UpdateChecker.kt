// 应用内更新检查 —— 查最新 APK 的发布元数据并与本机 versionCode 比对。
//
// 数据流（与桌面更新器的 manifest.json 同一条公网链路，但 APK 不进桌面发布模型）：
//   发版时把元数据写到 site/public/downloads/android-release.json（仓库内唯一版本源，
//   随站点构建进 dist/downloads/）→ 站点后端静态托管 → 本类 GET 它。
//   下载页（site/src/pages/Download.tsx）import 同一份 JSON，两处显示永远同源。
//
// 2026-12 用户拍板：下载动作从「跳系统浏览器」改为**应用内下载替换**——
// DownloadManager 下载 → sha256 校验 → 拉起系统安装器（见 ApkDownloader.kt）。
// 刻意不做的仍然只有一件：
//   * 应用内**静默**安装：需要 REQUEST_INSTALL_PACKAGES + 引导用户开「未知来源」，
//     且远控壳没有应用商店分发渠道，静默装包对用户是不可预期行为——明确不做。
//     应用内下载后拉起**系统安装器**（用户确认安装）不是静默安装，是标准做法。
package com.moyunteng.lxcode.remote.update

import android.content.Context
import java.net.SocketTimeoutException
import java.net.UnknownHostException
import java.util.concurrent.TimeUnit
import okhttp3.OkHttpClient
import okhttp3.Request
import org.json.JSONObject

/** 站点根（版本元数据与 APK 都挂在这条公网入口的 /site/ 之下，与桌面 manifest.json 同源）。 */
private const val SITE_BASE = "https://api.r96314722.nyat.app:15714/site/"

/** 最新 APK 的发布元数据（android-release.json 的投影）。 */
data class ApkRelease(
    val version: String,
    val versionCode: Long,
    val name: String,
    val size: Long,
    val sha256: String,
    /** 下载 URL（已解析成绝对地址，ACTION_VIEW 直接用）。 */
    val url: String,
    val notes: List<String>,
)

/** 一次检查的结果。三态：有新版 / 已最新 / 查询失败。 */
sealed interface UpdateCheckResult {
    /** 远端有比本机 versionCode 新的版本。 */
    data class Available(val release: ApkRelease) : UpdateCheckResult

    /** 已是最新（versionCode 不低于远端）。 */
    data object UpToDate : UpdateCheckResult

    /** 查询失败（网络/格式）。自动检查静默处理，手动检查把 [message] 给用户看。 */
    data class Failed(val message: String) : UpdateCheckResult
}

/**
 * 更新检查器。无状态对象：每次 [check] 现查现回（站点元数据是静态文件，
 * 服务端发布即生效，不需要本地缓存「检查过了」的状态）。
 */
object UpdateChecker {

    /** 版本元数据地址（仓库 site/public/downloads/android-release.json 的部署位置）。 */
    const val RELEASE_JSON_URL = SITE_BASE + "downloads/android-release.json"

    private val http = OkHttpClient.Builder()
        .connectTimeout(10, TimeUnit.SECONDS)
        .readTimeout(10, TimeUnit.SECONDS)
        .build()

    /**
     * 阻塞查询最新版本并与 [currentVersionCode] 比较（调用方放 IO 线程）。
     * **任何失败都归 [UpdateCheckResult.Failed]，不向外抛**——更新检查是锦上添花，
     * 不许把主流程打崩（网络失败自动检查静默、手动检查人话提示，见 UI 侧）。
     * 比较只用 versionCode 数字（versionName 是给人看的，不参与判定）。
     */
    fun check(currentVersionCode: Long): UpdateCheckResult {
        return try {
            val body = http.newCall(Request.Builder().url(RELEASE_JSON_URL).build())
                .execute().use { resp ->
                    if (!resp.isSuccessful) return UpdateCheckResult.Failed("更新服务返回了错误状态（${resp.code}）")
                    resp.body?.string() ?: return UpdateCheckResult.Failed("更新服务返回了空响应")
                }
            val json = JSONObject(body)
            val release = ApkRelease(
                version = json.getString("version"),
                versionCode = json.getLong("versionCode"),
                name = json.optString("name"),
                size = json.optLong("size"),
                sha256 = json.optString("sha256"),
                url = absoluteUrl(json.getString("url")),
                notes = json.optJSONArray("notes")?.let { arr ->
                    (0 until arr.length()).map { arr.getString(it) }
                } ?: emptyList(),
            )
            if (release.versionCode > currentVersionCode) {
                UpdateCheckResult.Available(release)
            } else {
                UpdateCheckResult.UpToDate
            }
        } catch (e: UnknownHostException) {
            UpdateCheckResult.Failed("网络不可用（域名解析失败）")
        } catch (e: SocketTimeoutException) {
            UpdateCheckResult.Failed("连接更新服务超时")
        } catch (e: Exception) {
            UpdateCheckResult.Failed("更新信息解析失败（${e.message ?: "未知错误"}）")
        }
    }

    /** 元数据里的 url 是站点根相对路径（"downloads/…"），解析成绝对地址。 */
    private fun absoluteUrl(url: String): String =
        if (url.startsWith("http")) url else SITE_BASE + url.removePrefix("/")
}

/** 字节数人性化（对齐下载页 formatBytes：1024 进制、四档单位）。 */
fun formatBytes(bytes: Long): String {
    val units = arrayOf("B", "KB", "MB", "GB", "TB")
    var v = bytes.toDouble()
    var i = 0
    while (v >= 1024 && i < units.size - 1) {
        v /= 1024
        i++
    }
    return if (v >= 100) "%.0f %s".format(v, units[i]) else "%.1f %s".format(v, units[i])
}
