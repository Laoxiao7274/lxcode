// 应用内 APK 下载与安装触发 —— DownloadManager（系统下载器）方案。
//
// 与旧「跳系统浏览器」的差异（2026-12 用户拍板改为应用内下载替换）：
//   * DownloadManager 走系统下载器：无需存储权限、自带进度通知栏、断点续传、
//     下载完成由系统发 ACTION_DOWNLOAD_COMPLETE 广播——app 内进度条 + 完成后
//     直接拉起安装器，不再离开应用；
//   * 下载完成先本地 sha256 校验（与 android-release.json 的 sha256 比对），
//     不匹配提示重试——旧方案链路信任点只有 HTTPS，应用内下载后装进系统前
//     多一道完整性检查（这是新方案必须补的，不是可选项）；
//   * 安装 = ACTION_VIEW + application/vnd.android.package-archive +
//     FLAG_GRANT_READ_URI_PERMISSION，经 FileProvider 授权系统安装器读文件。
//     用户确认安装——这不是静默安装（不需要 REQUEST_INSTALL_PACKAGES，
//     系统安装器自己管理「未知来源」确认），RELEASE.md 的「不做静默安装」纪律不变。
//
// 进度查询选 DownloadManager.query（选实现简单的）：每 500ms 查一次游标，
// 拿 STATUS_PROGRESS 的 bytes_so_far / total_size_bytes；通知栏进度系统自带。
//
// 可靠性加固（2026-12，对齐桌面端更新脚本静默失败的教训——每个失败点都要有
// 人话反馈 + 日志，失败不许无声无息）：
//   * 轮询不只等广播：STATUS_SUCCESSFUL / STATUS_FAILED 在轮询里同样收尾
//     （广播是快捷路径不是唯一路径——receiver 丢广播/进程被杀时轮询兜底，
//     失败码映射人话，不再永远卡「下载中」）；
//   * 下载 id 持久化进 prefs：DownloadManager 的任务是系统级持久的，app 进程
//     被杀/后台回收后重启能按 id 找回进行中的下载（横幅不静默回「空闲」）；
//   * 防重复入队：同 URL 已有进行中任务时复用，不重复 enqueue；
//   * 旧 APK 清理：enqueue 前清掉 Download/lxcode/ 里的旧版本文件（保留当前
//     任务的），防多版本 APK 堆积；
//   * 拉起安装器失败（ActivityNotFoundException——国产 ROM 无安装器 / 未知来源
//     未授权等）给用户人话提示 + 文件路径，可去系统下载目录手动装。
package com.moyunteng.lxcode.remote.update

import android.app.DownloadManager
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.database.Cursor
import android.net.Uri
import android.os.Build
import android.os.Environment
import android.os.Handler
import android.os.Looper
import android.util.Log
import androidx.core.content.FileProvider
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.File
import java.io.FileInputStream
import java.security.MessageDigest
import java.util.concurrent.atomic.AtomicReference

/** 下载状态（UI 横幅渲染依据）。 */
sealed interface DownloadState {
    /** 排队/下载中（[total] ≤ 0 = 长度未知，进度条转圈）。 */
    data class Running(val downloaded: Long, val total: Long) : DownloadState

    /** 下载完成（等待 sha256 校验/安装；校验由 [ApkDownloader.verify] 做）。 */
    data class Ready(val apkUri: Uri, val file: File) : DownloadState

    /** 失败（[retryable] = 可重试：网络/校验失败；false = 环境问题如无下载器）。 */
    data class Failed(val message: String, val retryable: Boolean) : DownloadState
}

/**
 * APK 下载器。一个实例挂在应用状态上（横幅跨重组保持进度），
 * 生命周期：[enqueue] → 轮询进度 → 完成广播 → Ready → [verify] → [install]。
 */
class ApkDownloader(private val context: Context) {

    companion object {
        private const val TAG = "LxApkDL"

        /** 下载 id 持久化 prefs 文件名与键（与 ConnStore 分开——它存的是连接凭证）。 */
        private const val PREFS = "lxcode_update"
        private const val KEY_DOWNLOAD_ID = "download_id"
        private const val KEY_DOWNLOAD_URL = "download_url"

        /** DownloadManager 落点目录（/sdcard/Download/lxcode/，与 file_paths.xml 一致）。 */
        fun downloadsDir(): File = File(
            Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DOWNLOADS),
            "lxcode",
        )
    }

    /** 当前下载任务（null = 空闲）。AtomicReference：完成广播在 binder 线程到达。 */
    private val active = AtomicReference<ActiveDownload?>(null)

    /** UI 状态回调（Compose 侧转成 mutableState 驱动横幅重绘）。 */
    var onState: ((DownloadState) -> Unit)? = null

    private data class ActiveDownload(
        val id: Long,
        val release: ApkRelease,
        val file: File,
    )

    private val handler = Handler(Looper.getMainLooper())

    private val prefs
        get() = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)

    /** 完成广播接收器（DownloadManager 的 ACTION_DOWNLOAD_COMPLETE）。 */
    private val receiver = object : BroadcastReceiver() {
        override fun onReceive(ctx: Context, intent: Intent) {
            val id = intent.getLongExtra(DownloadManager.EXTRA_DOWNLOAD_ID, -1)
            val cur = active.get() ?: return
            if (id != cur.id) return // 别的下载（浏览器等）完成，与本次任务无关
            Log.i(TAG, "ACTION_DOWNLOAD_COMPLETE: id=$id")
            finishSuccess(cur)
        }
    }

    private var receiverRegistered = false

    private fun registerReceiver() {
        if (receiverRegistered) return
        receiverRegistered = true
        val filter = IntentFilter(DownloadManager.ACTION_DOWNLOAD_COMPLETE)
        if (Build.VERSION.SDK_INT >= 33) {
            context.registerReceiver(receiver, filter, Context.RECEIVER_NOT_EXPORTED)
        } else {
            context.registerReceiver(receiver, filter)
        }
    }

    /**
     * 入队下载。落点 = 公开下载目录的 lxcode 子目录（DownloadManager 目标须在公有目录；
     * 子目录隔离，不污染用户下载列表根）。
     * 同 URL 已有进行中任务 → 复用（防连点入队多个任务）；
     * 同名旧文件先删（重试场景不覆盖）+ 目录里的其它旧 APK 顺带清理（多版本堆积）。
     */
    fun enqueue(release: ApkRelease) {
        val dm = context.getSystemService(Context.DOWNLOAD_SERVICE) as? DownloadManager
        if (dm == null) {
            Log.w(TAG, "DownloadManager 不可用（设备没有系统下载服务）")
            onState?.invoke(DownloadState.Failed("设备没有系统下载服务", retryable = false))
            return
        }

        // 防重复：同 URL 的任务已在跑 → 直接复用当前状态（连点「下载并安装」不入队第二个）
        val cur = active.get()
        if (cur != null && cur.release.url == release.url) {
            Log.i(TAG, "同 URL 下载进行中，复用 id=${cur.id}")
            return
        }
        if (cur != null) {
            // 换了目标 URL（版本元数据更新）：取消旧任务再入队新的
            Log.w(TAG, "已有任务指向 ${cur.release.url}，取消后换新 URL ${release.url}")
            cancel()
        }

        registerReceiver()

        val dir = downloadsDir()
        val file = File(dir, release.fileName())
        // 旧 APK 清理：删同名旧文件 + 目录里其它 .apk（保留下载中的当前目标，
        // 但当前目标此刻也不该存在——重试前它必然已被删或校验失败删过）
        cleanupStaleApks(dir, keep = file)

        val req = DownloadManager.Request(Uri.parse(release.url)).apply {
            setTitle("lxcode v${release.version}")
            setDescription("lxcode 应用更新包")
            setMimeType("application/vnd.android.package-archive")
            setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED)
            setDestinationUri(Uri.fromFile(file))
        }
        val id = try {
            dm.enqueue(req)
        } catch (e: Exception) {
            Log.w(TAG, "DownloadManager enqueue 失败", e)
            onState?.invoke(DownloadState.Failed("无法开始下载：${e.message ?: "未知错误"}", retryable = true))
            return
        }
        Log.i(TAG, "已入队下载 id=$id url=${release.url} file=${file.name}")
        active.set(ActiveDownload(id, release, file))
        // 持久化 id + URL：进程被杀后冷启动按它找回 DownloadManager 里的任务
        prefs.edit().putLong(KEY_DOWNLOAD_ID, id).putString(KEY_DOWNLOAD_URL, release.url).apply()
        onState?.invoke(DownloadState.Running(0, release.size))
        startPolling(id)
    }

    /** 旧 APK 清理：删目录里除 [keep] 外的所有 .apk（多版本堆积；不碰子目录/其它文件）。 */
    private fun cleanupStaleApks(dir: File, keep: File) {
        val list = dir.listFiles() ?: return
        for (f in list) {
            if (f.isFile && f.name.endsWith(".apk") && f.absolutePath != keep.absolutePath) {
                if (f.delete()) Log.i(TAG, "清理旧安装包：${f.name}")
                else Log.w(TAG, "旧安装包删除失败（下次再试）：${f.name}")
            }
        }
    }

    /**
     * app 冷启动后找回进行中的下载：prefs 里有持久化的 download id →
     * query DownloadManager，STATUS_RUNNING/PAUSED → 恢复 active 与轮询（横幅回到
     * 进度态而不是静默回「空闲」）；SUCCESSFUL → 走 Ready（广播可能错过）；
     * FAILED → 映射人话失败态并清 id；记录丢失（0 行）→ 清 id 回空闲。
     */
    fun resumePendingDownload() {
        if (active.get() != null) return // 已有任务（热重启），不重复找
        val id = prefs.getLong(KEY_DOWNLOAD_ID, -1)
        if (id <= 0) return
        val url = prefs.getString(KEY_DOWNLOAD_URL, null)
        if (url.isNullOrEmpty()) {
            prefs.edit().remove(KEY_DOWNLOAD_ID).apply()
            return
        }
        val dm = context.getSystemService(Context.DOWNLOAD_SERVICE) as? DownloadManager
        if (dm == null) return
        var c: Cursor? = null
        try {
            c = dm.query(DownloadManager.Query().setFilterById(id))
            if (c == null || !c.moveToFirst()) {
                Log.i(TAG, "持久化下载 id=$id 已无记录（系统清理/安装完成），清 id")
                prefs.edit().remove(KEY_DOWNLOAD_ID).remove(KEY_DOWNLOAD_URL).apply()
                return
            }
            val status = c.getInt(c.getColumnIndexOrThrow(DownloadManager.COLUMN_STATUS))
            val file = File(downloadsDir(), fileNameFromUrl(url))
            val release = ApkRelease(
                version = "", // 冷启动恢复只有 URL 可依赖；版本名不参与下载/校验/安装
                versionCode = 0,
                name = "",
                size = 0,
                sha256 = "", // 校验需 release 元数据；恢复路径的校验由 UI 层用 state.updateAvailable 补
                url = url,
                notes = emptyList(),
            )
            when (status) {
                DownloadManager.STATUS_SUCCESSFUL -> {
                    Log.i(TAG, "冷启动找回：id=$id 已完成（错过了广播），进 Ready")
                    active.set(ActiveDownload(id, release, file))
                    finishSuccess(active.get()!!)
                }
                DownloadManager.STATUS_FAILED -> {
                    val reason = c.getInt(c.getColumnIndexOrThrow(DownloadManager.COLUMN_REASON))
                    Log.w(TAG, "冷启动找回：id=$id 已失败（reason=$reason）")
                    prefs.edit().remove(KEY_DOWNLOAD_ID).remove(KEY_DOWNLOAD_URL).apply()
                    onState?.invoke(DownloadState.Failed(downloadFailReason(reason), retryable = true))
                }
                DownloadManager.STATUS_PENDING, DownloadManager.STATUS_RUNNING, DownloadManager.STATUS_PAUSED -> {
                    Log.i(TAG, "冷启动找回：id=$id 状态 $status，恢复轮询")
                    active.set(ActiveDownload(id, release, file))
                    registerReceiver()
                    startPolling(id)
                }
                else -> {
                    Log.w(TAG, "冷启动找回：id=$id 未知状态 $status，清 id")
                    prefs.edit().remove(KEY_DOWNLOAD_ID).remove(KEY_DOWNLOAD_URL).apply()
                }
            }
        } catch (e: Exception) {
            Log.w(TAG, "冷启动找回下载 id=$id 查询失败", e)
        } finally {
            c?.close()
        }
    }

    /** 从 URL 提取文件名（与 enqueue 的 fileName 一致的口径；恢复路径用）。 */
    internal fun fileNameFromUrl(url: String): String = apkFileNameFromUrl(url)

    /** 轮询进度：500ms 一次查 DownloadManager 游标（简单直接，不引 WorkManager）。
     *  成功/失败都在轮询里收尾——广播只是快捷路径，轮询是兜底（桌面教训：
     *  「完成通知不可靠」的等价物在这里是「广播可能收不到」）。 */
    private fun startPolling(id: Long) {
        val poll = object : Runnable {
            override fun run() {
                val cur = active.get() ?: return
                if (cur.id != id) return
                val dm = context.getSystemService(Context.DOWNLOAD_SERVICE) as? DownloadManager
                if (dm == null) {
                    Log.w(TAG, "轮询中止：DownloadManager 不可用")
                    return
                }
                var c: Cursor? = null
                try {
                    c = dm.query(DownloadManager.Query().setFilterById(id))
                    if (c != null && c.moveToFirst()) {
                        val status = c.getInt(c.getColumnIndexOrThrow(DownloadManager.COLUMN_STATUS))
                        val soFar = c.getLong(c.getColumnIndexOrThrow(DownloadManager.COLUMN_BYTES_DOWNLOADED_SO_FAR))
                        val total = c.getLong(c.getColumnIndexOrThrow(DownloadManager.COLUMN_TOTAL_SIZE_BYTES))
                        when (status) {
                            DownloadManager.STATUS_FAILED -> {
                                val reason = c.getInt(c.getColumnIndexOrThrow(DownloadManager.COLUMN_REASON))
                                Log.w(TAG, "下载失败 id=$id reason=$reason")
                                active.set(null)
                                prefs.edit().remove(KEY_DOWNLOAD_ID).remove(KEY_DOWNLOAD_URL).apply()
                                onState?.invoke(DownloadState.Failed(downloadFailReason(reason), retryable = true))
                                return
                            }
                            DownloadManager.STATUS_SUCCESSFUL -> {
                                Log.i(TAG, "轮询发现下载完成 id=$id（广播未到/已错过）")
                                finishSuccess(cur)
                                return
                            }
                            // 其余状态只报进度
                            else -> onState?.invoke(
                                DownloadState.Running(soFar, if (total > 0) total else cur.release.size),
                            )
                        }
                    } else {
                        // 游标空 = 系统把这条记录清了（remove/系统清理）：如实报失败，不卡「下载中」
                        Log.w(TAG, "轮询发现 id=$id 记录消失（被清理）")
                        active.set(null)
                        prefs.edit().remove(KEY_DOWNLOAD_ID).remove(KEY_DOWNLOAD_URL).apply()
                        onState?.invoke(DownloadState.Failed("下载任务已不存在（可能被系统清理），请重试", retryable = true))
                        return
                    }
                } catch (e: Exception) {
                    // 查询异常（存储不可用等）：记日志继续轮询，下一拍重试；连续失败由 FAILED 态兜底
                    Log.w(TAG, "轮询查询异常（下一拍重试）", e)
                } finally {
                    c?.close()
                }
                handler.postDelayed(this, 500)
            }
        }
        handler.post(poll)
    }

    /** 成功收尾（广播与轮询共用）：进 Ready 态并清持久化 id。 */
    private fun finishSuccess(cur: ActiveDownload) {
        handler.removeCallbacksAndMessages(null)
        prefs.edit().remove(KEY_DOWNLOAD_ID).remove(KEY_DOWNLOAD_URL).apply()
        onState?.invoke(DownloadState.Ready(cur.file.toApkUri(), cur.file))
    }

    /**
     * sha256 校验（调用方放 IO 线程）。读文件算哈希与 release.sha256 比对：
     * 通过 → 原样返回 Ready；失败 → Failed(retryable=true) + 删坏文件（提示重试）。
     * release.sha256 为空（元数据没带哈希）→ 跳过校验直接通过（不因元数据缺字段拒装）。
     */
    suspend fun verify(state: DownloadState.Ready, release: ApkRelease): DownloadState {
        val expected = release.sha256.trim().lowercase()
        if (expected.isEmpty()) return state
        return withContext(Dispatchers.IO) {
            try {
                if (sha256Of(state.file) == expected) {
                    state
                } else {
                    state.file.delete()
                    Log.w(TAG, "sha256 校验失败，已删除损坏文件：${state.file.name}")
                    DownloadState.Failed(
                        "安装包校验失败（sha256 不匹配）——下载不完整或已损坏，请重试",
                        retryable = true,
                    )
                }
            } catch (e: Exception) {
                Log.w(TAG, "sha256 读取失败：${state.file.name}", e)
                DownloadState.Failed("校验安装包失败：${e.message ?: "读取错误"}", retryable = true)
            }
        }
    }

    /**
     * 拉起系统安装器（Ready 之后）。FileProvider URI + FLAG_GRANT_READ_URI_PERMISSION。
     * 返回 false = 启动失败（ActivityNotFoundException：国产 ROM 装了「纯净模式」/
     * 无包安装器、未知来源未授权被拦）——调用方给用户人话提示（路径可手动装），
     * 静默吞掉就是桌面端「更新脚本静默失败」的安卓复刻。
     */
    fun install(state: DownloadState.Ready): Boolean {
        return try {
            val i = Intent(Intent.ACTION_VIEW).apply {
                setDataAndType(state.apkUri, "application/vnd.android.package-archive")
                addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
                addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
            }
            context.startActivity(i)
            Log.i(TAG, "已拉起系统安装器：${state.file.name}")
            true
        } catch (e: Exception) {
            Log.w(TAG, "拉起系统安装器失败", e)
            false
        }
    }

    /** 取消当前下载（用户点横幅的 ×；DownloadManager remove 连通知一起清）。 */
    fun cancel() {
        val cur = active.get() ?: return
        try {
            (context.getSystemService(Context.DOWNLOAD_SERVICE) as? DownloadManager)?.remove(cur.id)
        } catch (e: Exception) {
            Log.w(TAG, "DownloadManager.remove(id=${cur.id}) 失败", e)
        }
        cur.file.delete()
        active.set(null)
        prefs.edit().remove(KEY_DOWNLOAD_ID).remove(KEY_DOWNLOAD_URL).apply()
        handler.removeCallbacksAndMessages(null)
    }

    /** 空闲（无进行中的任务）。 */
    fun idle(): Boolean = active.get() == null
}

/** sha256 大文件流式哈希（8KB 缓冲，不整读内存）。 */
fun sha256Of(file: File): String {
    val md = MessageDigest.getInstance("SHA-256")
    FileInputStream(file).use { ins ->
        val buf = ByteArray(8192)
        while (true) {
            val n = ins.read(buf)
            if (n <= 0) break
            md.update(buf, 0, n)
        }
    }
    return md.digest().joinToString("") { "%02x".format(it) }
}

/** 下载失败的人话原因（DownloadManager.COLUMN_REASON → 中文）。
 *  常量取自 AOSP DownloadManager：UNKNOWN=1000 / FILE_ERROR=1001 /
 *  UNHANDLED_HTTP_CODE=1002 / HTTP_DATA_ERROR=1004 / TOO_MANY_REDIRECTS=1005 /
 *  INSUFFICIENT_SPACE=1006 / DEVICE_NOT_FOUND=1007 / CANNOT_RESUME=1008 /
 *  FILE_ALREADY_EXISTS=1009 / BLOCKED=1010（ERROR_UNKNOWN 落兜底，同样带人话）。 */
internal fun downloadFailReason(reason: Int): String = when (reason) {
    DownloadManager.ERROR_INSUFFICIENT_SPACE -> "存储空间不足，请清理后重试"
    DownloadManager.ERROR_HTTP_DATA_ERROR -> "下载数据传输错误（网络中断或服务端异常），请重试"
    DownloadManager.ERROR_FILE_ALREADY_EXISTS -> "本地已存在同名文件，请重试"
    DownloadManager.ERROR_UNHANDLED_HTTP_CODE -> "下载服务返回了无法处理的状态码（可能链接已失效），请稍后重试"
    DownloadManager.ERROR_TOO_MANY_REDIRECTS -> "下载地址重定向过多，请稍后重试"
    DownloadManager.ERROR_FILE_ERROR -> "下载文件读写失败（存储不可用？），请重试"
    DownloadManager.ERROR_DEVICE_NOT_FOUND -> "找不到外部存储设备（SD 卡已拔出？）"
    DownloadManager.ERROR_CANNOT_RESUME -> "下载中断且无法续传，请重试"
    1010 -> "下载被系统策略拦截（网络管控/运营商限制）" // ERROR_BLOCKED（API 36 才有常量，用字面值兜住低 SDK 编译）
    else -> "下载失败（错误码 $reason），请重试"
}

/** 下载文件名：从 URL 取最后一段；没有就按版本拼。 */
internal fun ApkRelease.fileName(): String {
    val last = url.substringAfterLast('/').substringBefore('?')
    return if (last.isNotBlank() && last.endsWith(".apk")) last else "lxcode-$version.apk"
}

/** URL → 文件名（纯函数，enqueue 与冷启动恢复共用同一口径，JVM 可测）。 */
internal fun apkFileNameFromUrl(url: String): String {
    val last = url.substringAfterLast('/').substringBefore('?')
    return if (last.isNotBlank() && last.endsWith(".apk")) last else "lxcode.apk"
}

/** FileProvider content URI（authority 与 Manifest 声明一致）。 */
internal fun File.toApkUri(): Uri =
    FileProvider.getUriForFile(
        AppContextHolder.context,
        "com.moyunteng.lxcode.remote.fileprovider",
        this,
    )

// FileProvider.getUriForFile 需要 Context；下载文件都出自 ApkDownloader 持有的
// context（构造时把应用 context 注入这里），authority 解析不依赖调用方。
internal object AppContextHolder {
    @Volatile
    lateinit var context: Context
        private set

    /** ApkDownloader 构造时调用（注入应用 context）。 */
    fun init(context: Context) {
        this.context = context.applicationContext
    }
}

/** ApkDownloader 构造时调用（注入应用 context 给 FileProvider URI 生成用）。 */
internal fun initDownloaderContext(context: Context) {
    AppContextHolder.init(context)
}
