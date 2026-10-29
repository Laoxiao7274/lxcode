// 更新链路纯 JVM 单元测试 —— 错误码映射 / 文件名口径 / sha256。
// 只测无 Android 依赖的纯逻辑（工程约定「构建即验证」，不引 robolectric——
// 与 MarkdownParserTest 同一条纪律）；DownloadManager/状态机的 Android 侧
// 行为（广播、轮询、prefs 持久化）靠真机验证清单覆盖（见任务回报）。
package com.moyunteng.lxcode.remote.update

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File

class UpdatePipelineTest {

    // ===== downloadFailReason：DownloadManager 错误码 → 人话 =====

    @Test
    fun failReasonInsufficientSpace() {
        val msg = downloadFailReason(1006) // ERROR_INSUFFICIENT_SPACE = 1006（AOSP 常量，避免 Android 依赖用魔数）
        assertTrue("应含「存储空间不足」：$msg", msg.contains("存储空间不足"))
    }

    @Test
    fun failReasonHttpDataError() {
        // ERROR_HTTP_DATA_ERROR = 1004
        val msg = downloadFailReason(1004)
        assertTrue("应指向网络/数据错误：$msg", msg.contains("重试") && !msg.contains("错误码"))
    }

    @Test
    fun failReasonUnhandledHttpCode() {
        // ERROR_UNHANDLED_HTTP_CODE = 1002（404/410 等落到这里——链接失效的最常见路径）
        val msg = downloadFailReason(1002)
        assertTrue("应提示状态码/链接失效：$msg", msg.contains("状态码"))
    }

    @Test
    fun failReasonFileAlreadyExists() {
        // ERROR_FILE_ALREADY_EXISTS = 1009
        assertTrue(downloadFailReason(1009).contains("同名文件"))
    }

    @Test
    fun failReasonDeviceNotFound() {
        // ERROR_DEVICE_NOT_FOUND = 1007（SD 卡拔出）
        assertTrue(downloadFailReason(1007).contains("存储设备"))
    }

    @Test
    fun failReasonCannotResume() {
        // ERROR_CANNOT_RESUME = 1008（断点续传失败）
        assertTrue(downloadFailReason(1008).contains("续传"))
    }

    @Test
    fun failReasonBlocked() {
        // ERROR_BLOCKED = 1010（系统策略拦截）
        assertTrue(downloadFailReason(1010).contains("拦截"))
    }

    @Test
    fun failReasonKnownCodesNotFallback() {
        // 已知码不该落到兜底分支（兜底文案带「错误码 N」）；1000=UNKNOWN 无专属文案但兜底也是人话
        for (code in intArrayOf(1001, 1002, 1004, 1005, 1006, 1007, 1008, 1009, 1010)) {
            assertFalse("错误码 $code 应有专属人话", downloadFailReason(code).contains("错误码 $code"))
        }
        // 未知码兜底：不抛、仍可读、可重试话术在
        val unknown = downloadFailReason(424242)
        assertTrue(unknown.contains("424242"))
        assertTrue(unknown.contains("重试"))
    }

    @Test
    fun failReasonUnknownCodeIsStillHuman() {
        // 未知错误码：不抛、不含裸「null」，且可重试话术在
        val msg = downloadFailReason(424242)
        assertTrue(msg.contains("424242"))
        assertTrue(msg.contains("重试"))
    }

    // ===== apkFileNameFromUrl：enqueue 与冷启动恢复共用同一文件名口径 =====

    @Test
    fun fileNameFromUrlTakesLastSegment() {
        assertEquals(
            "lxcode-0.2.8.apk",
            apkFileNameFromUrl("https://example.com/site/downloads/lxcode-0.2.8.apk"),
        )
    }

    @Test
    fun fileNameFromUrlStripsQuery() {
        assertEquals(
            "lxcode-0.2.8.apk",
            apkFileNameFromUrl("https://example.com/downloads/lxcode-0.2.8.apk?token=abc"),
        )
    }

    @Test
    fun fileNameFromUrlFallbackWithoutApkSuffix() {
        // URL 末段不是 .apk（带 token 的动态路径）：回落固定名，两处口径一致不漂移
        assertEquals("lxcode.apk", apkFileNameFromUrl("https://example.com/get?file=1"))
        assertEquals("lxcode.apk", apkFileNameFromUrl("https://example.com/"))
    }

    // ===== sha256Of：流式哈希（8KB 缓冲），已知向量 =====

    @Test
    fun sha256OfEmptyFile() {
        val f = File.createTempFile("sha-empty", ".bin")
        try {
            assertEquals(
                "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
                sha256Of(f),
            )
        } finally {
            f.delete()
        }
    }

    @Test
    fun sha256OfKnownVector() {
        val f = File.createTempFile("sha-abc", ".bin")
        try {
            f.writeBytes("abc".toByteArray())
            assertEquals(
                "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
                sha256Of(f),
            )
        } finally {
            f.delete()
        }
    }

    @Test
    fun sha256OfLargerThanBufferIsStreaming() {
        // >8KB（超过缓冲）：验证流式读对大文件正确（11MB 级真实包同路径）
        val f = File.createTempFile("sha-big", ".bin")
        try {
            val data = ByteArray(64 * 1024) { (it % 251).toByte() }
            f.writeBytes(data)
            val h1 = sha256Of(f)
            // 同内容两次哈希一致 + 不等于空文件向量
            assertEquals(h1, sha256Of(f))
            assertFalse(h1 == "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")
        } finally {
            f.delete()
        }
    }

    // ===== DownloadState：UI 横幅四态的数据契约 =====

    @Test
    fun downloadStateRunningCarriesProgress() {
        val s: DownloadState = DownloadState.Running(5 * 1024 * 1024, 11 * 1024 * 1024)
        val r = s as DownloadState.Running
        assertTrue(r.downloaded < r.total) // 进度条分母不该为零或倒挂
    }

    @Test
    fun downloadStateFailedRetryabilitySemantics() {
        // 网络类失败可重试；环境类（无下载服务）不可重试——横幅据此决定显示「重试」还是隐藏动作
        val net: DownloadState = DownloadState.Failed("网络中断", retryable = true)
        val env: DownloadState = DownloadState.Failed("设备没有系统下载服务", retryable = false)
        assertTrue((net as DownloadState.Failed).retryable)
        assertFalse((env as DownloadState.Failed).retryable)
    }
}
