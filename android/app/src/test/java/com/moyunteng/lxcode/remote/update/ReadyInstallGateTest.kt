// Ready 安装单飞位（installVerified 重复拉起安装器修复）的纯 JVM 测试。
// 工程纪律「构建即验证」：不引 robolectric，DownloadManager/Intent 的真机行为
// 由真机验证清单覆盖；这里钉的是 Ready.markInstalling 的 CAS 契约——
// 这是「同一 Ready 只弹一次系统安装器」这处修复的全部判定逻辑所在。
//
// apkUri 是 android.net.Uri（JVM 侧未 mock 的 stub）：单飞位只挂在对象身份上、
// apkUri 不参与判定——测试传 null（Ready.apkUri 是可空参数）。
package com.moyunteng.lxcode.remote.update

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File
import java.util.concurrent.CountDownLatch

class ReadyInstallGateTest {

    private fun ready(): DownloadState.Ready = DownloadState.Ready(
        apkUri = null,
        file = File.createTempFile("ready-gate", ".apk"),
    )

    @Test
    fun markInstallingFirstCallWins() {
        val r = ready()
        assertTrue("第一次调用应拿到安装权", r.markInstalling())
        assertFalse("之后重入不再放行", r.markInstalling())
        assertFalse(r.markInstalling())
    }

    @Test
    fun markInstallingIsPerInstance() {
        // 新 Ready（重试/重新下载）必须重新获得安装权——单飞位是实例级的，不是全局的
        val a = ready()
        val b = ready()
        assertTrue(a.markInstalling())
        assertTrue(b.markInstalling())
    }

    @Test
    fun markInstallingConcurrentSingleWinner() {
        // 广播与轮询收尾在不同线程各调一次 installVerified 的竞态：
        // CAS 保证恰好一个线程拿到安装权
        val r = ready()
        val n = 16
        val start = CountDownLatch(1)
        val done = CountDownLatch(n)
        var winners = 0
        val lock = Any()
        repeat(n) {
            Thread {
                start.await()
                if (r.markInstalling()) {
                    synchronized(lock) { winners++ }
                }
                done.countDown()
            }.start()
        }
        start.countDown()
        done.await()
        assertEquals("CAS 保证恰好一个赢家", 1, winners)
    }
}
