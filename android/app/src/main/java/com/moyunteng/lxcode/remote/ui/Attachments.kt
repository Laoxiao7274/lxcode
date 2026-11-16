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
import androidx.compose.material.icons.filled.BrokenImage
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

/** url → ImageBitmap + 加载状态（IO 线程取件，LruCache 命中免 IO）。
 *  返回 null = 还在加载（produceState 未产出，暂态）；Failure = 已失败（HTTP 非 2xx /
 *  解码异常 / url 缺席，终态）。两个态必须分得开：加载中是 spinner 语义，失败是
 *  坏消息要看得见（对齐桌面端 att-dead 语义；不区分的话失败图永远转圈）。 */
private enum class AttLoadState { Done, Failure }

@Composable
private fun rememberAttachmentState(url: String?): Pair<ImageBitmap?, AttLoadState>? {
    val state = produceState<Pair<ImageBitmap?, AttLoadState>?>(initialValue = null, url) {
        if (url == null) {
            value = null to AttLoadState.Failure
            return@produceState
        }
        attachmentBitmapCache.get(url)?.let {
            value = it.asImageBitmap() to AttLoadState.Done
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
        value = bmp?.asImageBitmap() to (if (bmp != null) AttLoadState.Done else AttLoadState.Failure)
    }
    return state.value
}

/** 消息侧用户附件区（对齐桌面端 UserAttachmentSection）：
 *  图片 gallery（多图 64dp 方 tile 网格 / 单图长边 240dp）+ 文件卡行（240×64）。
 *  atts 为空不渲染。[onOpenImage] 打开全屏查看（灯箱）。
 *  [onOpenFile] 非空 = 文件卡可点（下载/打开）；缺省 = 静态卡（无入口不装死——
 *  没有下载能力的调用方不该渲染成点了没反应的卡）。 */
@Composable
fun UserAttachmentSection(
    atts: List<com.moyunteng.lxcode.remote.model.AttachmentMeta>,
    urlOf: (String) -> String?,
    onOpenImage: (List<String>, Int) -> Unit = { _, _ -> },
    onOpenFile: ((com.moyunteng.lxcode.remote.model.AttachmentMeta) -> Unit)? = null,
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
                        onClick = onOpenFile?.let { cb -> { cb(att) } } ?: {},
                    )
                }
            }
        }
    }
}

/** 消息侧图片 tile（原型 `.uatt img`：96×68、object-fit cover、r12 + 1px --line 边）。 */
@Composable
private fun GridImage(url: String, badge: String?, onClick: () -> Unit) {
    val state = rememberAttachmentState(url)
    Box(
        Modifier
            .size(width = 96.dp, height = 68.dp)
            .clip(AttTileShape)
            .background(Lx.colors.BorderSoft)
            .border(1.dp, Lx.colors.Border, AttTileShape)
            .clickable(onClick = onClick),
        contentAlignment = Alignment.Center,
    ) {
        when {
            state != null && state.first != null -> androidx.compose.foundation.Image(
                bitmap = state.first!!,
                contentDescription = "图片附件",
                contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxSize(),
            )
            // 失败 = 终态：BrokenImage 图标（看得见的坏消息，不是一直转圈）
            state != null -> LoadingOrBroken(loading = false)
            // 加载中 = 暂态：spinner
            else -> LoadingOrBroken(loading = true)
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

/** 图片加载/失败占位：加载中 = 旋转扇（暂态）；失败 = BrokenImage 图标（终态——
 *  加载完成的帧会被 Image 盖住，这里的两个态只服务于没加载出图的窗口）。 */
@Composable
private fun LoadingOrBroken(loading: Boolean) {
    Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        if (loading) {
            androidx.compose.material3.CircularProgressIndicator(
                modifier = Modifier.size(16.dp),
                strokeWidth = 2.dp,
                color = Lx.colors.FgFaint,
            )
        } else {
            Icon(
                imageVector = Icons.Filled.BrokenImage,
                contentDescription = "图片加载失败",
                tint = Lx.colors.FgFaint,
                modifier = Modifier.size(18.dp),
            )
        }
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
            val state = rememberAttachmentState(url)
            if (state?.first != null) {
                androidx.compose.foundation.Image(
                    bitmap = state.first!!,
                    contentDescription = "图片预览",
                    // 原图适配视口（fit 语义——与桌面端 fitSize 一致），点击不关闭
                    modifier = Modifier
                        .fillMaxSize()
                        .clickable(onClick = { /* 吞掉：点图不冒泡成关闭 */ }),
                    contentScale = ContentScale.Fit,
                )
            } else if (state != null) {
                // 失败（终态）：坏消息要看得见
                Icon(
                    imageVector = Icons.Filled.BrokenImage,
                    contentDescription = "图片加载失败",
                    tint = Lx.colors.FgFaint,
                    modifier = Modifier.size(40.dp),
                )
            } else {
                // 加载中（暂态）
                androidx.compose.material3.CircularProgressIndicator(
                    modifier = Modifier.size(40.dp),
                    color = Color.White,
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

/** 附件文件下载（批次 2：文件卡的打开入口）。
 *
 *  **实现选择 = DownloadManager 落系统下载目录，不用 Intent ACTION_VIEW**：
 *  附件 URL 是 `http://<addr>/attachments/<path>?token=...`——ACTION_VIEW 会把
 *  整个带 token 的 URL 交给外部浏览器（token 进浏览器历史/外部应用可见），且
 *  设备上没有浏览器的形态（车机/电视/极简 ROM）点了没反应；DownloadManager 由
 *  系统托管（通知栏进度 + 断点续传），落 `Downloads/` 后用户经系统下载记录或
 *  文件管理器取件——token 不出系统服务进程。缩略图同 URL 已走明文 http
 *  （rememberAttachmentState），下载走同一张网，不引入新的传输面。
 *
 *  立即 Toast 反馈（DownloadManager 本身有完成通知，这里只报「已开始/失败」）。
 *  失败最常见原因 = 经樱花隧道的明文 http 被节点 501（与缩略图加载失败同根源，
 *  已知限制，如实提示）。 */
fun downloadAttachment(context: android.content.Context, url: String, name: String) {
    if (url.isBlank()) {
        android.widget.Toast.makeText(context, "附件地址无效", android.widget.Toast.LENGTH_SHORT).show()
        return
    }
    try {
        val req = android.app.DownloadManager.Request(android.net.Uri.parse(url))
            .setTitle(name.ifBlank { "附件" })
            .setDescription("lxcode 附件")
            .setNotificationVisibility(android.app.DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED)
            .setAllowedOverMetered(true)
            .setAllowedOverRoaming(true)
        // 目标名：保持后端落盘名（Display 名若与磁盘名不一致，以 URL 末段为准
        // ——setDestinationUri 的文件名决定最终落盘名）。
        val fileName = url.substringAfterLast('/').substringBefore('?').ifBlank { "attachment" }
        req.setDestinationInExternalPublicDir(android.os.Environment.DIRECTORY_DOWNLOADS, fileName)
        val dm = context.getSystemService(android.content.Context.DOWNLOAD_SERVICE) as android.app.DownloadManager
        dm.enqueue(req)
        android.widget.Toast.makeText(context, "已开始下载：$fileName", android.widget.Toast.LENGTH_SHORT).show()
    } catch (e: Exception) {
        android.widget.Toast.makeText(context, "下载失败：${e.message ?: "未知错误"}", android.widget.Toast.LENGTH_SHORT).show()
    }
}
