// 附件（图片/文件卡）——对齐桌面端 AttachmentCard.tsx / thread.css 的 .att-* 系列
// （2026-12 DSH 对齐批次）：上传侧 64dp 方 tile（radius 16）+ 18dp 移除钮、
// 240×64 文件卡；消息侧 gallery 网格（多图 64dp / 单图长边 240）+ 文件卡行。
//
// 安卓的「灯箱」= 全屏图片查看（点缩略图打开，点关闭/返回收起）——功能语义与
// 桌面端 ImageLightbox 一致（查看原图），交互按触摸端惯例（不做双击缩放）。
//
// 取件：GET /attachments/<session>/<file>?token=...（HTTP，OkHttp 已有依赖）。
// 图片经 remember(url) 缓存的 Bitmap 显示；加载失败 = 灰底图标占位（看得见的
// 坏消息，不静默消失——对齐桌面端 att-dead 语义）。
package com.moyunteng.lxcode.remote.ui

import android.graphics.Bitmap
import android.graphics.BitmapFactory
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Description
import androidx.compose.material.icons.filled.Image
import androidx.compose.material.icons.filled.KeyboardArrowLeft
import androidx.compose.material.icons.filled.KeyboardArrowRight
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import com.moyunteng.lxcode.design.token.Lx
import com.moyunteng.lxcode.remote.net.RealBackend
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.net.URL

/** 附件卡 Shape —— thread.css .att-file/.att-thumb 的 border-radius: 16px。 */
private val AttShape = RoundedCornerShape(16.dp)

/** 消息侧缩略 tile Shape —— 原型 `.uatt img { border-radius: var(--r-md) }` = 12px。 */
private val AttTileShape = RoundedCornerShape(12.dp)

/** 附件位图缓存（P3 性能）：url → Bitmap 的 LruCache（128 条目上限——附件图尺寸不一，
 *  条目数上限 + Compose LazyList 的可见窗口规模匹配；滚出窗口的图被逐出，滚回来重取）。
 *  之前每张缩略图各发一次 HTTP + 解码且滚出视口就丢——列表回滚重解码、流式期间
 *  同一 url 的 gallery 网格重复解码（单图 + 灯箱两处同 url 各一份）。命中缓存 = 零 IO。 */
private val attachmentBitmapCache = androidx.collection.LruCache<String, Bitmap>(128)

/** url → ImageBitmap（HTTP 同步取件 + BitmapFactory 解码，IO 线程；LruCache 命中免 IO）。
 *  null = 失败（占位态）；produceState 在 url 变化时重取——调用方读 .value。 */
@Composable
private fun rememberAttachmentBitmap(url: String?): ImageBitmap? =
    produceState<ImageBitmap?>(initialValue = null, url) {
        if (url == null) return@produceState
        attachmentBitmapCache.get(url)?.let {
            value = it.asImageBitmap()
            return@produceState
        }
        val bmp = withContext(Dispatchers.IO) {
            try {
                val conn = URL(url).openConnection() as java.net.HttpURLConnection
                conn.connectTimeout = 5_000
                conn.readTimeout = 10_000
                conn.connect()
                if (conn.responseCode !in 200..299) {
                    conn.disconnect()
                    return@withContext null
                }
                val decoded: Bitmap? = BitmapFactory.decodeStream(conn.inputStream)
                conn.disconnect()
                decoded
            } catch (_: Exception) {
                null
            }
        }
        if (bmp != null) attachmentBitmapCache.put(url, bmp)
        value = bmp?.asImageBitmap()
    }.value

/** 消息侧用户附件区（对齐桌面端 UserAttachmentSection）：
 *  图片 gallery（多图 64dp 方 tile 网格 / 单图长边 240dp）+ 文件卡行（240×64）。
 *  atts 为空不渲染。[onOpenImage] 打开全屏查看（灯箱）。 */
@Composable
fun UserAttachmentSection(
    atts: List<com.moyunteng.lxcode.remote.model.AttachmentMeta>,
    urlOf: (String) -> String?,
    onOpenImage: (List<String>, Int) -> Unit = { _, _ -> },
) {
    if (atts.isEmpty()) return
    val images = atts.filter { it.kind == "image" && urlOf(it.path) != null }
    val files = atts.filter { it !in images }
    Column(
        Modifier
            .fillMaxWidth(0.82f)
            // 原型 `.msg-user .uatt`：附件行右对齐、与气泡间距 6px
            .padding(bottom = 6.dp),
        verticalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        if (images.isNotEmpty()) {
            Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                // 原型 `.uatt img`：96×68 cover tile（r12 + 1px --line 边 + --soft 底），
                // 不再区分单图/多图两档——移动端窄屏 96×68 统一网格
                images.take(4).forEachIndexed { i, att ->
                    GridImage(
                        url = urlOf(att.path)!!,
                        badge = if (i == minOf(images.size, 4) - 1 && images.size > 4) "+${images.size - 4}" else null,
                        onClick = { onOpenImage(images.mapNotNull { urlOf(it.path) }, i) },
                    )
                }
            }
        }
        if (files.isNotEmpty()) {
            Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                files.forEach { att ->
                    AttFileCard(
                        name = att.name.ifBlank { "文件" },
                        sub = fileSub(att.size, att.mime),
                        onClick = { /* 手机端下载入口：系统浏览器打开（token 在 query 里） */ },
                    )
                }
            }
        }
    }
}

/** 消息侧图片 tile（原型 `.uatt img`：96×68、object-fit cover、r12 + 1px --line 边）。 */
@Composable
private fun GridImage(url: String, badge: String?, onClick: () -> Unit) {
    val bmp = rememberAttachmentBitmap(url)
    Box(
        Modifier
            .size(width = 96.dp, height = 68.dp)
            .clip(AttTileShape)
            .background(Lx.colors.BorderSoft)
            .border(1.dp, Lx.colors.Border, AttTileShape)
            .clickable(onClick = onClick),
        contentAlignment = Alignment.Center,
    ) {
        if (bmp != null) {
            androidx.compose.foundation.Image(
                bitmap = bmp,
                contentDescription = "图片附件",
                contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxSize(),
            )
        } else {
            LoadingOrBroken()
        }
        if (badge != null) {
            Box(
                Modifier
                    .fillMaxSize()
                    .background(Color.Black.copy(alpha = 0.40f)),
                contentAlignment = Alignment.Center,
            ) {
                Text(
                    text = badge,
                    style = Lx.type.Body.copy(fontWeight = FontWeight.SemiBold),
                    color = Color.White,
                )
            }
        }
    }
}

/** 图片加载中/失败占位：灰底 + 图标（加载完成的帧会被 Image 盖住）。 */
@Composable
private fun LoadingOrBroken() {
    Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        Icon(
            imageVector = Icons.Filled.Image,
            contentDescription = null,
            tint = Lx.colors.FgFaint,
            modifier = Modifier.size(18.dp),
        )
    }
}

/** 文件卡（对齐 .att-file：240×64、radius 16、细边框、28dp 图标框 + 名称 + meta 行）。 */
@Composable
fun AttFileCard(name: String, sub: String, onClick: () -> Unit) {
    Row(
        Modifier
            .width(240.dp)
            .height(64.dp)
            .clip(AttShape)
            .background(Lx.colors.BgSide)
            .border(1.dp, Lx.colors.Border, AttShape)
            .clickable(onClick = onClick)
            .padding(horizontal = 12.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        Icon(
            imageVector = Icons.Filled.Description,
            contentDescription = null,
            tint = Lx.colors.FgMuted,
            modifier = Modifier.size(28.dp),
        )
        Column(Modifier.weight(1f)) {
            Text(
                text = name,
                style = Lx.type.ListTitle.copy(fontWeight = FontWeight.Medium),
                color = Lx.colors.Fg,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                text = sub,
                style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size12),
                color = Lx.colors.FgFaint,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

/** 文件卡 meta 行：扩展名 · 大小（对齐桌面端 fileSub）。 */
fun fileSub(size: Long, mime: String): String {
    val ext = if (mime.isNotEmpty()) mime.substringAfter('/', "").uppercase() else ""
    val sizeText = RealBackend.formatBytes(size)
    return if (ext.isNotEmpty()) "$ext · $sizeText" else sizeText
}

/** Composer 暂存的图片缩略图（原型 `.atts img`：76×56 cover tile、r12、1px --line 边；
 *  右上**外角** 18dp 正圆 fg 底白叉移除钮（`.atts .x`：top/right -5px、`box-shadow` 浮起）
 *  ——触摸端恒显，桌面端的 hover 显在手机上不可达）。 */
@Composable
fun PendingImageThumb(
    image: RealBackend.PendingImage,
    urlOf: (String) -> String?,
    onRemove: () -> Unit,
) {
    val context = LocalContext.current
    val bmp = remember(image.uri, image.id) {
        try {
            context.contentResolver.openInputStream(image.uri)?.use {
                BitmapFactory.decodeStream(it)
            }?.asImageBitmap()
        } catch (_: Exception) {
            null
        }
    }
    Box(
        Modifier
            .size(width = 76.dp, height = 56.dp)
            .clip(AttTileShape)
            .background(Lx.colors.BorderSoft)
            .border(1.dp, Lx.colors.Border, AttTileShape),
        contentAlignment = Alignment.Center,
    ) {
        if (bmp != null) {
            androidx.compose.foundation.Image(
                bitmap = bmp,
                contentDescription = image.name,
                contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxSize(),
            )
        } else {
            Icon(
                imageVector = Icons.Filled.Image,
                contentDescription = null,
                tint = Lx.colors.FgFaint,
                modifier = Modifier.size(18.dp),
            )
        }
    }
    // 移除钮骑在 tile 外角（原型 `.atts .x`：top -5 / right -5；正圆 fg 底 + bg 叉）
    // ——外层再包一层 Box 承载负偏移（Compose 无负 padding，用 offset 出血）。
    Box(
        Modifier
            .offset(x = 5.dp, y = (-5).dp)
            .size(18.dp)
            .shadow(elevation = 2.dp, shape = CircleShape, clip = false)
            .clip(CircleShape)
            .background(Lx.colors.Fg)
            .clickable(onClick = onRemove),
        contentAlignment = Alignment.Center,
    ) {
        Icon(
            imageVector = Icons.Filled.Close,
            contentDescription = "移除",
            tint = Lx.colors.Bg,
            modifier = Modifier.size(10.dp),
        )
    }
}

/** Composer 暂存的文件卡（240×64 + 右上移除钮，结构同消息侧 AttFileCard）。 */
@Composable
fun PendingFileCard(name: String, sub: String, onRemove: () -> Unit) {
    Box(Modifier.width(240.dp)) {
        AttFileCard(name = name, sub = sub, onClick = {})
        Box(
            Modifier
                .align(Alignment.TopEnd)
                .padding(4.dp)
                .size(18.dp)
                .clip(RoundedCornerShape(9.dp))
                .background(Color.Black.copy(alpha = 0.72f))
                .clickable(onClick = onRemove),
            contentAlignment = Alignment.Center,
        ) {
            Icon(
                imageVector = Icons.Filled.Close,
                contentDescription = "移除",
                tint = Color.White,
                modifier = Modifier.size(11.dp),
            )
        }
    }
}

/** 全屏图片查看（安卓的「灯箱」）：黑底 + 原图 + 关闭钮；点背景或返回收起。
 *  左右切换 = 多图集合下的左右箭头（触摸端也保留按钮——比滑动翻页可发现性强）。 */
@Composable
fun ImageLightbox(
    urls: List<String>,
    initialIndex: Int,
    onDismiss: () -> Unit,
) {
    var index by remember { mutableStateOf(initialIndex.coerceIn(0, (urls.size - 1).coerceAtLeast(0))) }
    Dialog(
        onDismissRequest = onDismiss,
        properties = DialogProperties(usePlatformDefaultWidth = false),
    ) {
        Box(
            Modifier
                .fillMaxSize()
                .background(Lx.colors.TermBg.copy(alpha = 0.82f)) // 桌面端 .lightbox 同值
                .clickable(onClick = onDismiss),
            contentAlignment = Alignment.Center,
        ) {
            val url = urls.getOrNull(index)
            val bmp = rememberAttachmentBitmap(url)
            if (bmp != null) {
                androidx.compose.foundation.Image(
                    bitmap = bmp,
                    contentDescription = "图片预览",
                    // 原图适配视口（fit 语义——与桌面端 fitSize 一致），点击不关闭
                    modifier = Modifier
                        .fillMaxSize()
                        .clickable(onClick = { /* 吞掉：点图不冒泡成关闭 */ }),
                    contentScale = ContentScale.Fit,
                )
            } else {
                Icon(
                    imageVector = Icons.Filled.Image,
                    contentDescription = null,
                    tint = Lx.colors.FgFaint,
                    modifier = Modifier.size(40.dp),
                )
            }
            Row(
                Modifier
                    .align(Alignment.TopEnd)
                    .padding(16.dp),
            ) {
                Box(
                    Modifier
                        .size(36.dp)
                        .clip(RoundedCornerShape(18.dp))
                        .background(Color.Black.copy(alpha = 0.20f))
                        .clickable(onClick = onDismiss),
                    contentAlignment = Alignment.Center,
                ) {
                    Icon(
                        imageVector = Icons.Filled.Close,
                        contentDescription = "关闭",
                        tint = Color.White,
                        modifier = Modifier.size(20.dp),
                    )
                }
            }
            if (urls.size > 1) {
                if (index > 0) {
                    Box(
                        Modifier
                            .align(Alignment.CenterStart)
                            .padding(8.dp)
                            .size(36.dp)
                            .clip(RoundedCornerShape(18.dp))
                            .background(Color.Black.copy(alpha = 0.20f))
                            .clickable { index-- },
                        contentAlignment = Alignment.Center,
                    ) {
                        Icon(
                            imageVector = Icons.Filled.KeyboardArrowLeft,
                            contentDescription = "上一张",
                            tint = Color.White,
                            modifier = Modifier.size(22.dp),
                        )
                    }
                }
                if (index < urls.size - 1) {
                    Box(
                        Modifier
                            .align(Alignment.CenterEnd)
                            .padding(8.dp)
                            .size(36.dp)
                            .clip(RoundedCornerShape(18.dp))
                            .background(Color.Black.copy(alpha = 0.20f))
                            .clickable { index++ },
                        contentAlignment = Alignment.Center,
                    ) {
                        Icon(
                            imageVector = Icons.Filled.KeyboardArrowRight,
                            contentDescription = "下一张",
                            tint = Color.White,
                            modifier = Modifier.size(22.dp),
                        )
                    }
                }
            }
        }
    }
}
