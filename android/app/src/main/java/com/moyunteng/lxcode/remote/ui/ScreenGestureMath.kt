// ScreenGestureMath —— 投屏 pinch 手势的纯函数计算（无 Compose 依赖）。
//
// 为什么单拎出来：手势状态机的正确性核心是「scale 增量累积 + 围绕质心平移补偿 +
// 边界夹取后不再补偿」这三条算术，纯 JVM 单测可以直接覆盖；放 Composable 文件里
// （带 androidx 依赖）无法进 testDebugUnitTest。
//
// 语义（对齐官方 detectTransformGestures 的矩阵算术）：
//  - zoom 是**相对增量**（本次事件的两指间距比），必须乘进当前 scale；
//  - 浮点噪声容忍：|zoom - 1| < 1e-4 视为无缩放（calculateZoom 是两指连线长度
//    相除，抖动帧会给出 0.999xx/1.0002x，真机实测直接消费会往质心塌缩）；
//  - 夹取（1f..5f）后 effective = newScale/oldScale；effective == 1f 时（已被
//    边界钳住）**不做 offset 补偿**——补偿公式在 effective=1 时仍会把 offset
//    拽向质心，造成「放大到顶后画面被吸走」的假象。
package com.moyunteng.lxcode.remote.ui

import androidx.compose.ui.geometry.Offset

/** 噪声容忍阈值：zoom 偏离 1 小于它不算缩放（两指落下瞬间的采样抖动）。 */
const val ZOOM_NOISE_EPSILON = 1e-4f

/** 应用一次缩放增量，返回 (新 scale, 新 offset)。
 *  @param scale   当前缩放（1f..5f）
 *  @param zoom    本次事件的缩放增量（calculateZoom()，1f = 无缩放）
 *  @param offset  当前平移
 *  @param centroid 本事件质心（calculateCentroid()；未指定时调用方不进本函数）
 */
/**
 * 手势边界钳制（cover 语义）——观看态默认就是「放大态」（SurfaceView bounds =
 * cover 尺寸，1× 已铺满视口），缩放下限钉在 [coverBaseScale]，平移钳在 cover
 * 相对视口的溢出量内（放大后能拖回被裁的部分，缩小不许低于铺满基准）。
 *
 *  @param scale   当前缩放（[coverBaseScale]..5f）
 *  @param zoom    本次事件的缩放增量（calculateZoom()，1f = 无缩放）
 *  @param offset  当前平移
 *  @param centroid 本事件质心（calculateCentroid()；未指定时调用方不进本函数）
 *  @param minScale 缩放下限（cover 语义下 = coverBaseScale()；测试可注入）
 */
fun applyPinchZoom(
    scale: Float,
    zoom: Float,
    offset: Offset,
    centroid: Offset,
    minScale: Float = coverBaseScale(),
): Pair<Float, Offset> {
    if (kotlin.math.abs(zoom - 1f) < ZOOM_NOISE_EPSILON) return scale to offset
    val newScale = (scale * zoom).coerceIn(minScale, 5f)
    val effective = newScale / scale
    if (effective == 1f) return newScale to offset // 已被边界钳住：不动 offset
    // 围绕质心缩放：保持质心下的内容点不动
    // content = content * effective + centroid * (1 - effective)，
    // content = offset + centroid → offset' = (offset - centroid) * effective + centroid
    val newOffset = Offset(
        (offset.x - centroid.x) * effective + centroid.x,
        (offset.y - centroid.y) * effective + centroid.y,
    )
    return newScale to newOffset
}

/** 应用一次平移（仅放大态消费）：offset += pan。 */
fun applyPinchPan(offset: Offset, pan: Offset): Offset = offset + pan

/**
 * 平移边界钳制（cover 语义）——把 offset 钳进「画面不露出视口边缘」的范围。
 * graphicsLayer 的 translation 以 SurfaceView 自身 bounds 为基准，SurfaceView
 * bounds = cover 尺寸（中心对齐视口），允许的平移半程 = 每轴溢出量的一半：
 * 拖到极限时画面边缘恰好贴住视口边缘，再往外拖就露黑底。
 *
 *  @param offset    当前平移
 *  @param overflowX cover 相对视口的水平溢出半程（[coverOverflow]，px）
 *  @param overflowY cover 相对视口的垂直溢出半程（[coverOverflow]，px）
 *  @return 钳制后的平移；溢出半程为 0 的轴（画面该轴恰贴满或不足）钳在 0
 */
fun clampPanToCover(offset: Offset, overflowX: Float, overflowY: Float): Offset {
    val maxX = (overflowX * 2f).coerceAtLeast(0f) / 2f
    val maxY = (overflowY * 2f).coerceAtLeast(0f) / 2f
    return Offset(offset.x.coerceIn(-maxX, maxX), offset.y.coerceIn(-maxY, maxY))
}

/**
 * 视口内按视频真实比例取最大不失真（fit-center）尺寸——0.3.30「分辨率跟手机
 * 的适配要做好，不要看着高矮胖瘦」：SurfaceView 拉伸填满自身 bounds，不变形
 * 的唯一办法是把 bounds 本身算成目标比例。黑边 = letterbox（黑底容器自然透出）。
 *
 *  @param viewportW/H 视口尺寸（px，>0）
 *  @param videoW/H    视频真实尺寸（DecoderStats 的 SPS 解析结果；<=0 = 未知）
 *  @return 视口内最大等比尺寸；视频未知时返回**整个视口**（先铺满，帧到位后
 *          stats 变化自动重排为 letterbox——比编一个默认 16:9 少一次跳变）
 */
fun fitVideoSize(viewportW: Float, viewportH: Float, videoW: Int, videoH: Int): androidx.compose.ui.geometry.Size {
    if (viewportW <= 0f || viewportH <= 0f) return androidx.compose.ui.geometry.Size.Zero
    if (videoW <= 0 || videoH <= 0) return androidx.compose.ui.geometry.Size(viewportW, viewportH)
    val videoAspect = videoW.toFloat() / videoH.toFloat()
    val viewportAspect = viewportW / viewportH
    // 视频更「宽」→ 宽度贴满、高度按比例；否则高度贴满、宽度按比例
    return if (videoAspect > viewportAspect) {
        androidx.compose.ui.geometry.Size(viewportW, viewportW / videoAspect)
    } else {
        androidx.compose.ui.geometry.Size(viewportH * videoAspect, viewportH)
    }
}

/** coverVideoSize 返回的尺寸至少为多大时才算「铺满」（相对视口的最小覆盖比例）。 */
const val COVER_MIN_OVERSCAN = 1f

/**
 * 视口内按视频真实比例取**最小铺满（cover-center）**尺寸——观看态「整个页面全是
 * 投屏画面、四周零边框」（2026-12 用户拍板）：scale = max(视口宽/视频宽, 视口高/视频高)，
 * 结果一条边贴满视口、另一条边**超出**视口（多出部分被 clipToBounds 裁掉）。
 * 与 [fitVideoSize]（fit-center，视口内最大等比、必然 letterbox）互补：
 *  - 视频未知（<=0）时同 fit 的回落 = 整个视口（先铺满，帧到位后自动重排）；
 *  - 视频比例恰好等于视口比例时 cover == fit == 视口本身，无裁切。
 *
 *  @param viewportW/H 视口尺寸（px，>0）
 *  @param videoW/H    视频真实尺寸（DecoderStats 的 SPS 解析结果；<=0 = 未知）
 *  @return 最小铺满尺寸；宽或高至少一边 == 视口，另一边 >= 视口对应边
 */
fun coverVideoSize(viewportW: Float, viewportH: Float, videoW: Int, videoH: Int): androidx.compose.ui.geometry.Size {
    if (viewportW <= 0f || viewportH <= 0f) return androidx.compose.ui.geometry.Size.Zero
    if (videoW <= 0 || videoH <= 0) return androidx.compose.ui.geometry.Size(viewportW, viewportH)
    val videoAspect = videoW.toFloat() / videoH.toFloat()
    val viewportAspect = viewportW / viewportH
    // 视频更「宽」→ 高度贴满、宽度按比例（超出视口宽，左右被裁）；
    // 否则宽度贴满、高度按比例（超出视口高，上下被裁）
    return if (videoAspect > viewportAspect) {
        androidx.compose.ui.geometry.Size(viewportH * videoAspect, viewportH)
    } else {
        androidx.compose.ui.geometry.Size(viewportW, viewportW / videoAspect)
    }
}

/**
 * cover 尺寸相对视口的溢出量（px，每轴 ≥0）——平移钳制边界按它算：放大态下
 * 画面可拖动的极限 = 把被裁的部分拖回来，即 offset 钳在 ±(coverSize - viewport)/2
 * （graphicsLayer 缩放以 SurfaceView 中心为变换原点时）。
 *
 *  @return (overflowX, overflowY)；视频未知或视口退化时两轴均为 0（不可平移）
 */
fun coverOverflow(viewportW: Float, viewportH: Float, videoW: Int, videoH: Int): Pair<Float, Float> {
    val cover = coverVideoSize(viewportW, viewportH, videoW, videoH)
    if (cover.isEmpty()) return 0f to 0f
    return ((cover.width - viewportW).coerceAtLeast(0f) / 2f) to
        ((cover.height - viewportH).coerceAtLeast(0f) / 2f)
}

/**
 * 视频铺满视口所需的最小缩放（cover 基准）——graphicsLayer 的 scale 是相对
 * SurfaceView 自身 bounds 的倍率，而 SurfaceView bounds 由 [coverVideoSize] 布下
 * （本身就已铺满），所以 cover 基准 scale 恒为 1：SurfaceView bounds = cover 尺寸时
 * 1× 即铺满，任何 <1 的缩放都会重新露出黑边/白底。观看态手势的 min scale 钉死 1f，
 * 本函数把这条不变量显式化（纯函数单测钉住「cover 布局下 1× 就是铺满」）。
 */
fun coverBaseScale(): Float = 1f
