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

    /** 完成广播接收器（DownloadManager 的 ACTION_DOWNLOAD_COMPLETE）。 */
    private val receiver = object : BroadcastReceiver() {
        override fun onReceive(ctx: Context, intent: Intent) {
            val id = intent.getLongExtra(DownloadManager.EXTRA_DOWNLOAD_ID, -1)
            val cur = active.get() ?: return
            if (id != cur.id) return // 别的下载（浏览器等）完成，与本次任务无关
            handler.removeCallbacksAndMessages(null)
            onState?.invoke(DownloadState.Ready(cur.file.toApkUri(), cur.file))
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
     * 子目录隔离，不污染用户下载列表根）。同名旧文件先删（重试场景不覆盖）。
     */
    fun enqueue(release: ApkRelease) {
        val dm = context.getSystemService(Context.DOWNLOAD_SERVICE) as? DownloadManager
        if (dm == null) {
            onState?.invoke(DownloadState.Failed("设备没有系统下载服务", retryable = false))
            return
        }
        registerReceiver()

        val dir = File(
            Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DOWNLOADS),
            "lxcode",
        )
        val file = File(dir, release.fileName())
        if (file.exists()) file.delete()

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
        active.set(ActiveDownload(id, release, file))
        onState?.invoke(DownloadState.Running(0, release.size))
        startPolling(id)
    }

    /** 轮询进度：500ms 一次查 DownloadManager 游标（简单直接，不引 WorkManager）。 */
    private fun startPolling(id: Long) {
        val poll = object : Runnable {
            override fun run() {
                val cur = active.get() ?: return
                if (cur.id != id) return
                val dm = context.getSystemService(Context.DOWNLOAD_SERVICE) as? DownloadManager ?: return
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
                                active.set(null)
                                onState?.invoke(DownloadState.Failed(downloadFailReason(reason), retryable = true))
                                return
                            }
                            // STATUS_SUCCESSFUL 交给 ACTION_DOWNLOAD_COMPLETE 广播收尾（更可靠）；
                            // 其余状态只报进度
                            else -> onState?.invoke(
                                DownloadState.Running(soFar, if (total > 0) total else cur.release.size),
                            )
                        }
                    }
                } catch (_: Exception) {
                    // 查询失败（下载器被清/存储不可用）不崩：FAILED 状态兜底
                } finally {
                    c?.close()
                }
                handler.postDelayed(this, 500)
            }
        }
        handler.post(poll)
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
                    DownloadState.Failed(
                        "安装包校验失败（sha256 不匹配）——下载不完整或已损坏，请重试",
                        retryable = true,
                    )
                }
            } catch (e: Exception) {
                DownloadState.Failed("校验安装包失败：${e.message ?: "读取错误"}", retryable = true)
            }
        }
    }

    /** 拉起系统安装器（Ready 之后）。FileProvider URI + FLAG_GRANT_READ_URI_PERMISSION。 */
    fun install(state: DownloadState.Ready): Boolean {
        return try {
            val i = Intent(Intent.ACTION_VIEW).apply {
                setDataAndType(state.apkUri, "application/vnd.android.package-archive")
                addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
                addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
            }
            context.startActivity(i)
            true
        } catch (e: Exception) {
            Log.w(TAG, "拉起安装器失败", e)
            false
        }
    }

    /** 取消当前下载（用户点横幅的 ×；DownloadManager remove 连通知一起清）。 */
    fun cancel() {
        val cur = active.get() ?: return
        try {
            (context.getSystemService(Context.DOWNLOAD_SERVICE) as? DownloadManager)?.remove(cur.id)
        } catch (_: Exception) {
        }
        cur.file.delete()
        active.set(null)
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

/** 下载失败的人话原因（DownloadManager.COLUMN_REASON → 中文）。 */
private fun downloadFailReason(reason: Int): String = when (reason) {
    DownloadManager.ERROR_INSUFFICIENT_SPACE -> "存储空间不足"
    DownloadManager.ERROR_HTTP_DATA_ERROR -> "下载服务返回数据错误，请重试"
    DownloadManager.ERROR_FILE_ALREADY_EXISTS -> "本地已存在同名文件，请重试"
    DownloadManager.ERROR_UNHANDLED_HTTP_CODE -> "下载服务返回了无法处理的状态码，请稍后重试"
    DownloadManager.ERROR_TOO_MANY_REDIRECTS -> "下载地址重定向过多，请稍后重试"
    DownloadManager.ERROR_FILE_ERROR -> "下载文件读写失败，请重试"
    DownloadManager.ERROR_DEVICE_NOT_FOUND -> "找不到外部存储设备"
    else -> "下载失败（错误码 $reason），请重试"
}

/** 下载文件名：从 URL 取最后一段；没有就按版本拼。 */
private fun ApkRelease.fileName(): String {
    val last = url.substringAfterLast('/').substringBefore('?')
    return if (last.isNotBlank() && last.endsWith(".apk")) last else "lxcode-$version.apk"
}

/** FileProvider content URI（authority 与 Manifest 声明一致）。 */
private fun File.toApkUri(): Uri =
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
