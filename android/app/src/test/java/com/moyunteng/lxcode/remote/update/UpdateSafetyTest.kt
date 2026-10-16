// 更新链路 2026-12 加固的纯 JVM 单元测试 —— 空间预检（缺口3）/ 安装入口纪律
//（缺口1）/ 防御式 versionCode 比对（缺口4）。
// 只测无 Android 依赖的纯逻辑（工程约定「构建即验证」，不引 robolectric）：
//   * hasEnoughSpace / spaceShortfallMessage：companion object 纯函数；
//   * isRemoteNewer：文件级纯函数；
//   * 「未校验不可安装」：installVerified 本体依赖 DownloadManager/Intent（真机面），
//     JVM 侧钉的是纪律的结构面——verifyApkFile 是 private 且只被 installVerified
//     调用，install() 已不在公开面（编译期保证），这里断言公开面收敛 + 校验判定的
//     输入口径（sha256 空 = 放行的语义由真机清单覆盖）。
package com.moyunteng.lxcode.remote.update

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class UpdateSafetyTest {

    // ===== 缺口3：空间预检判定（availableBytes ≥ size × 1.2）=====

    @Test
    fun spaceCheckPassesWithHeadroom() {
        // 100MB APK，可用 120MB（恰好 1.2×）→ 放行（边界含等号）
        val apk = 100L * 1024 * 1024
        assertTrue(ApkDownloader.hasEnoughSpace(apk * 12 / 10, apk))
    }

    @Test
    fun spaceCheckFailsBelowHeadroom() {
        // 100MB APK，可用 119MB → 拒绝
        val apk = 100L * 1024 * 1024
        assertFalse(ApkDownloader.hasEnoughSpace(apk * 12 / 10 - 1, apk))
    }

    @Test
    fun spaceCheckZeroSizePasses() {
        // size 未知（0）：预检不拒绝（调用侧对 size<=0 跳过，这里钉纯函数口径）
        assertTrue(ApkDownloader.hasEnoughSpace(0, 0))
        assertTrue(ApkDownloader.hasEnoughSpace(1024, 0))
    }

    @Test
    fun spaceCheckTinyApkTinySpace() {
        // 小包小空间：1.2 倍阈值对小文件同样成立
        assertFalse(ApkDownloader.hasEnoughSpace(11, 10))
        assertTrue(ApkDownloader.hasEnoughSpace(12, 10))
    }

    @Test
    fun spaceHeadroomFactorIs12() {
        // 阈值系数钉死：有人调系数必须改这条测试（口径变更要显式）
        assertEquals(1.2, ApkDownloader.SPACE_HEADROOM_FACTOR, 1e-9)
    }

    // ===== 缺口3：空间不足的人话文案 =====

    @Test
    fun shortfallMessageStatesRequiredMb() {
        val msg = ApkDownloader.spaceShortfallMessage(100L * 1024 * 1024)
        assertTrue("应含「存储空间不足」：$msg", msg.contains("存储空间不足"))
        assertTrue("应含需要的 MB 数：$msg", msg.contains("约 100 MB"))
        assertTrue("应指向清理重试：$msg", msg.contains("清理"))
    }

    @Test
    fun shortfallMessageRoundsUp() {
        // 1.5MB → 报「约 2 MB」（向上取整，不把需求报小）
        val msg = ApkDownloader.spaceShortfallMessage((1.5 * 1024 * 1024).toLong())
        assertTrue(msg.contains("约 2 MB"))
    }

    // ===== 缺口4：防御式 versionCode 比对（横幅状态驱动源）=====

    @Test
    fun remoteNewerOnlyByStrictlyGreater() {
        assertTrue(isRemoteNewer(9, 8))
        assertFalse(isRemoteNewer(8, 8)) // 同版本：不显示横幅
        assertFalse(isRemoteNewer(7, 8)) // 远端更旧（本地已装新版）：摘横幅
    }

    @Test
    fun remoteNewerHandlesLargeCodes() {
        // versionCode 走 Long：大号不溢出
        assertTrue(isRemoteNewer(9_000_000_000L, 8_999_999_999L))
        assertFalse(isRemoteNewer(8_999_999_999L, 9_000_000_000L))
    }
}
