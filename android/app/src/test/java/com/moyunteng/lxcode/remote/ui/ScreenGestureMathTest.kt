// ScreenGestureMathTest —— pinch 手势纯函数单测（JVM，无 Android/Compose 运行时依赖；
// Offset 是纯数据类，可直接实例化）。
//
// 覆盖真机 0.3.22「缩成一点」的三条回归线：
//  1. zoom 噪声帧（|zoom-1|<ε）不得改变 scale/offset——旧行为是每帧消费噪声往 1 以下乘；
//  2. 正常增量按相对语义累积（scale * zoom），且围绕质心锁定内容点；
//  3. 边界夹取（到 1× 或 5×）后不再做 offset 补偿（旧行为会吸向质心）。
package com.moyunteng.lxcode.remote.ui

import androidx.compose.ui.geometry.Offset
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class ScreenGestureMathTest {

    // ---- 噪声容忍：抖动帧不动画面（0.3.22 根因回归）----

    @Test
    fun noiseZoomBelow1DoesNotShrink() {
        val (s, o) = applyPinchZoom(1f, 0.99997f, Offset.Zero, Offset(100f, 200f))
        assertEquals(1f, s, 1e-6f)
        assertEquals(Offset.Zero, o)
    }

    @Test
    fun noiseZoomAbove1DoesNotGrow() {
        val (s, o) = applyPinchZoom(1f, 1.00003f, Offset.Zero, Offset(100f, 200f))
        assertEquals(1f, s, 1e-6f)
        assertEquals(Offset.Zero, o)
    }

    @Test
    fun exactOneZoomIsNoOp() {
        val (s, o) = applyPinchZoom(2f, 1f, Offset(30f, 40f), Offset(100f, 200f))
        assertEquals(2f, s, 1e-6f)
        assertEquals(Offset(30f, 40f), o)
    }

    // ---- 相对增量语义：scale 累积，zoom 是乘数不是绝对值 ----

    @Test
    fun zoomAccumulatesRelatively() {
        var s = 1f
        var o = Offset.Zero
        val c = Offset(200f, 200f)
        // 两帧各 ×1.5 → 2.25×（若误当绝对值，第二帧会回 1.5×）
        val r1 = applyPinchZoom(s, 1.5f, o, c); s = r1.first; o = r1.second
        val r2 = applyPinchZoom(s, 1.5f, o, c); s = r2.first; o = r2.second
        assertEquals(2.25f, s, 1e-4f)
    }

    @Test
    fun growAroundCentroidKeepsContentPointStill() {
        // 从 1× 放大到 2×：质心 (100,100) 下的内容点必须原地不动。
        // graphicsLayer 以左上角为原点：内容点 P 的屏幕位置 = P*scale + offset，
        // 质心不动 ⇔ P*scale2 + o2 = P*scale1 + o1（P = c - o1)/scale1 …取 o1=0、
        // P = (c.x/scale1, c.y/scale1) = (100,100)。
        val o1 = Offset.Zero
        val c = Offset(100f, 100f)
        val (s2, o2) = applyPinchZoom(1f, 2f, o1, c)
        assertEquals(2f, s2, 1e-6f)
        // P*scale2 + o2 == P*scale1 + o1 ⇔ o2 = P - 2P = -P
        assertEquals(-100f, o2.x, 1e-4f)
        assertEquals(-100f, o2.y, 1e-4f)
    }

    // ---- 边界夹取 ----

    @Test
    fun shrinkAt1fClampsAndDoesNotOffset() {
        // 已在 1×，zoom 0.5：scale 钳在 1，offset 必须原样（旧行为会按
        // effective 计算 offset 但 effective=1 仍拽向质心 →「缩成一点」）
        val o = Offset(40f, -60f)
        val (s, r) = applyPinchZoom(1f, 0.5f, o, Offset(100f, 100f))
        assertEquals(1f, s, 1e-6f)
        assertEquals(o, r)
    }

    @Test
    fun growAt5fClampsAndDoesNotOffset() {
        val o = Offset(-120f, 80f)
        val (s, r) = applyPinchZoom(5f, 1.4f, o, Offset(100f, 100f))
        assertEquals(5f, s, 1e-6f)
        assertEquals(o, r)
    }

    @Test
    fun zoomBelow1StopsClampedAtOne() {
        // 2× 时大幅捏合：一路钳到 1×，不产生 <1 的 scale
        var s = 2f
        var o = Offset(-50f, -50f)
        val c = Offset(150f, 150f)
        repeat(10) {
            val r = applyPinchZoom(s, 0.7f, o, c)
            s = r.first; o = r.second
            assertTrue("scale 不得低于 1，实际 $s", s >= 1f - 1e-6f)
        }
        assertEquals(1f, s, 1e-6f)
    }

    @Test
    fun repeatedPinchNeverExceedsBounds() {
        var s = 1f
        var o = Offset.Zero
        val c = Offset(90f, 90f)
        repeat(50) {
            val r = applyPinchZoom(s, 1.6f, o, c)
            s = r.first; o = r.second
            assertTrue(s >= 1f - 1e-6f && s <= 5f + 1e-6f)
        }
        assertEquals(5f, s, 1e-6f)
    }

    // ---- 平移 ----

    @Test
    fun panAccumulates() {
        val o = applyPinchPan(Offset(10f, 20f), Offset(5f, -7f))
        assertEquals(Offset(15f, 13f), o)
    }

    // ---- fitVideoSize：旋转场景（0.3.31 真机「四周巨大黑边」回归）----
    // 视口来源错误（旋转后仍拿竖屏视口）时，同一 16:9 视频在 2340×1080 横视口
    // 里应占满整宽，若拿成 1080×2340 竖视口则只剩 ~486×864 的小块居中——
    // 画面完整但四周黑边巨大。本组钉住：横竖视口给出的 fit 尺寸必须各自正确。

    @Test
    fun fitVideoSizeUnknownVideoFillsViewport() {
        val s = fitVideoSize(2340f, 1080f, 0, 0)
        assertEquals(2340f, s.width, 1e-3f)
        assertEquals(1080f, s.height, 1e-3f)
    }

    @Test
    fun fitVideoSizeDegenerateViewportIsZero() {
        assertTrue(fitVideoSize(0f, 1080f, 1920, 1080).isEmpty())
        assertTrue(fitVideoSize(2340f, -1f, 1920, 1080).isEmpty())
    }

    @Test
    fun fitVideoSizeLandscapeViewportFitsByWidth() {
        // 横视口 2340×1080 放 16:9（1920×1080）视频：高度贴满 1080，
        // 宽度 = 1080 × 16/9 = 1920（左右各 ~210px 黑边，正常 letterbox）
        val s = fitVideoSize(2340f, 1080f, 1920, 1080)
        assertEquals(1080f, s.height, 1e-3f)
        assertEquals(1920f, s.width, 1e-3f)
    }

    @Test
    fun fitVideoSizePortraitViewportFitsByWidthToo() {
        // 竖视口 1080×2340 放同一 16:9 视频：宽度贴满 1080，
        // 高度 = 1080 × 9/16 = 607.5（上下大黑边——竖屏看横画面的正常形态，
        // 也是 bug 现场：这个结果被错误地画在了横屏视口中央）
        val s = fitVideoSize(1080f, 2340f, 1920, 1080)
        assertEquals(1080f, s.width, 1e-3f)
        assertEquals(607.5f, s.height, 1e-3f)
    }

    @Test
    fun fitVideoSizeSquareVideoInLandscapeViewport() {
        // 视频比视口「窄」（1:1 < 20:9）→ 高度贴满、宽度按比例
        val s = fitVideoSize(2340f, 1080f, 1080, 1080)
        assertEquals(1080f, s.height, 1e-3f)
        assertEquals(1080f, s.width, 1e-3f)
    }

    @Test
    fun fitVideoSizeLandscapeAndPortraitViewportsMustDiffer() {
        // 回归断言本体：同一视频在旋转前后的两个视口里 fit 结果必须不同——
        // 若视口来源出错（旋转后仍喂旧视口），这两个断言的组合就是黑边现场
        val landscape = fitVideoSize(2340f, 1080f, 1920, 1080)
        val portrait = fitVideoSize(1080f, 2340f, 1920, 1080)
        assertEquals(1920f, landscape.width, 1e-3f)   // 占 82% 宽：正常
        assertEquals(607.5f, portrait.height, 1e-3f)  // 只占 26% 高：黑边现场
        assertTrue(landscape.width > portrait.width)
        assertTrue(landscape.height > portrait.height)
    }

    // ---- coverVideoSize：观看态铺满（2026-12「四周零边框」拍板）----
    // 观看态不再 letterbox：cover = min scale ≥ 1，一条边贴满、另一条超出被裁。

    @Test
    fun coverVideoSizeUnknownVideoFillsViewport() {
        val s = coverVideoSize(2340f, 1080f, 0, 0)
        assertEquals(2340f, s.width, 1e-3f)
        assertEquals(1080f, s.height, 1e-3f)
    }

    @Test
    fun coverVideoSizeDegenerateViewportIsZero() {
        assertTrue(coverVideoSize(0f, 1080f, 1920, 1080).isEmpty())
        assertTrue(coverVideoSize(2340f, -1f, 1920, 1080).isEmpty())
    }

    @Test
    fun coverVideoSizePortraitViewportOverscansHorizontally() {
        // 主场景：20:9 竖视口 1080×2340 放 16:9（1920×1080）视频——视频比视口
        // 「窄」→ cover 语义下高度贴满 2340、宽度按比例 = 2340×16/9 = 4160
        //（左右大幅超出被裁，可见宽度只占 1080/4160 ≈ 26%）。
        // 钉住：高 == 视口高、宽 ≥ 视口宽（竖屏看横画面 = 左右裁切，不是上下黑边）。
        val s = coverVideoSize(1080f, 2340f, 1920, 1080)
        assertEquals(2340f, s.height, 1e-3f)
        assertEquals(4160f, s.width, 1e-3f)
        assertTrue("cover 宽必须 ≥ 视口宽（铺满），实际 ${s.width}", s.width >= 1080f - 1e-3f)
    }

    @Test
    fun coverVideoSizeLandscapeViewportOverscansHorizontally() {
        // 横视口 2340×1080 放同一 16:9 视频：视频比视口「窄」（16/9 < 2340/1080）
        // → 高度贴满、宽度按比例 = 1080×16/9 = 1920 < 2340 视口宽？否——
        // cover 语义下不缩：宽度贴满 2340、高度 = 2340×9/16 = 1316.25（上下超出被裁）。
        val s = coverVideoSize(2340f, 1080f, 1920, 1080)
        assertEquals(2340f, s.width, 1e-3f)
        assertEquals(1316.25f, s.height, 1e-3f)
    }

    @Test
    fun coverVideoSizeMatchingAspectEqualsViewport() {
        // 视频比例 == 视口比例：cover == fit == 视口本身，无裁切无黑边
        val s = coverVideoSize(1920f, 1080f, 1920, 1080)
        assertEquals(1920f, s.width, 1e-3f)
        assertEquals(1080f, s.height, 1e-3f)
        val f = fitVideoSize(1920f, 1080f, 1920, 1080)
        assertEquals(f.width, s.width, 1e-3f)
        assertEquals(f.height, s.height, 1e-3f)
    }

    @Test
    fun coverVideoSizeAlwaysCoversViewport() {
        // 不变量：任意视频/视口组合下 cover 尺寸每轴都 ≥ 视口（铺满，零边框）
        val combos = listOf(
            Triple(1080f, 2340f, 1920 to 1080),
            Triple(2340f, 1080f, 1920 to 1080),
            Triple(1080f, 2340f, 1080 to 1080),
            Triple(2340f, 1080f, 3840 to 2160),
            Triple(600f, 900f, 4096 to 2160),
        )
        combos.forEach { (vw, vh, video) ->
            val s = coverVideoSize(vw, vh, video.first, video.second)
            assertTrue("w ${s.width} < $vw", s.width >= vw - 1e-3f)
            assertTrue("h ${s.height} < $vh", s.height >= vh - 1e-3f)
        }
    }

    @Test
    fun fitVideoSizeStillFitsInsideViewport() {
        // fit 语义未被破坏（设置页卡内态仍在用）：每轴 ≤ 视口
        val combos = listOf(
            Triple(1080f, 2340f, 1920 to 1080),
            Triple(2340f, 1080f, 1920 to 1080),
            Triple(600f, 900f, 4096 to 2160),
        )
        combos.forEach { (vw, vh, video) ->
            val s = fitVideoSize(vw, vh, video.first, video.second)
            assertTrue(s.width <= vw + 1e-3f && s.height <= vh + 1e-3f)
        }
    }

    // ---- coverOverflow：平移钳制边界 ----

    @Test
    fun coverOverflowPortraitViewportHalfWidth() {
        // 竖视口 1080×2340 放 16:9：cover 宽 4160 → 水平溢出半程 = (4160-1080)/2
        // = 1540，垂直恰好贴满（溢出 0）
        val (ox, oy) = coverOverflow(1080f, 2340f, 1920, 1080)
        assertEquals(1540f, ox, 1e-3f)
        assertEquals(0f, oy, 1e-3f)
    }

    @Test
    fun coverOverflowUnknownVideoIsZero() {
        val (ox, oy) = coverOverflow(1080f, 2340f, 0, 0)
        assertEquals(0f, ox, 1e-6f)
        assertEquals(0f, oy, 1e-6f)
    }

    // ---- cover 基准下的手势钳制 ----

    @Test
    fun clampPanToCoverLimitsDragToOverscan() {
        // 溢出半程 1540：拖动 ±1540 时画面边缘恰好贴住视口边缘；±2000 被钳回
        val lim = clampPanToCover(Offset(2000f, -50f), 1540f, 0f)
        assertEquals(1540f, lim.x, 1e-3f)
        assertEquals(0f, lim.y, 1e-3f)
        val neg = clampPanToCover(Offset(-999f, 5f), 1540f, 0f)
        assertEquals(-999f, neg.x, 1e-3f)
        assertEquals(0f, neg.y, 1e-3f)
    }

    @Test
    fun coverBaseScaleIsOne() {
        // cover 布局下（SurfaceView bounds = cover 尺寸）1× 就是铺满：
        // 缩放下限 = 1，任何更小的基准都会重新露出黑边/白底
        assertEquals(1f, coverBaseScale(), 1e-6f)
    }

    @Test
    fun pinchZoomBelowCoverBaseStopsAtBase() {
        // cover 语义：放大态下往回捏，一路钳在 cover 基准（1×）——不再允许
        // 缩回带黑边状态；配合 clampPanToCover，复位到基准时画面仍铺满
        var s = 3f
        var o = Offset(-80f, 0f)
        val c = Offset(400f, 800f)
        repeat(20) {
            val r = applyPinchZoom(s, 0.6f, o, c)
            s = r.first; o = r.second
            assertTrue("scale 不得低于 cover 基准，实际 $s", s >= coverBaseScale() - 1e-6f)
        }
        assertEquals(coverBaseScale(), s, 1e-6f)
    }

    @Test
    fun pinchZoomAtCoverBaseDoesNotOffset() {
        // 已在 cover 基准（1×）继续缩小：scale 钳住、offset 原样（不吸向质心——
        // 与旧 fit 语义的「缩成一点」同一条回归线，只是钳制点换成 cover 基准）
        val o = Offset(-60f, 30f)
        val (s, r) = applyPinchZoom(1f, 0.4f, o, Offset(200f, 200f))
        assertEquals(1f, s, 1e-6f)
        assertEquals(o, r)
    }

    @Test
    fun legacyFitCallsWithoutMinScaleKeepOldBounds() {
        // 兼容：不传 minScale 的旧调用（若有）行为 = 钳在 1f..5f（默认即 cover 基准）
        val (s, _) = applyPinchZoom(1f, 0.5f, Offset.Zero, Offset(100f, 100f))
        assertEquals(1f, s, 1e-6f)
    }
}
