// 扫码配对页（PairingScreen）—— 手机是扫描方。
//
// 方向：桌面端在「连接 → 远程访问」页**展示**二维码；手机用摄像头**扫**它，
// 解析出配对凭证（载荷格式 `lxcode://pair?addr=<host:port>&token=<token>&name=<可选>`，
// URL encode）后预填连接表单。手机上不展示二维码、不展示地址/Token——那是桌面端的职责。
//
// 结构：① 顶部说明文案
//      ② 取景框（四角框线 + 内嵌 DecoratedBarcodeView 真相机连续解码；
//         无相机硬件/未授权时显示占位提示）
//      ③ 非法载荷错误提示条
//      ④ 调试区（云机没有可用摄像头：模拟扫到一张码 / 模拟非法载荷；
//         与连接页「组件展示」调试入口同一先例，真机联调也用它）
//
// 无论真假扫码，解析走同一条路径：字符串 → parsePairPayload → 预填表单（见 MockAppState.applyPairPayload）。
package com.moyunteng.lxcode.remote.ui

import android.Manifest
import android.content.pm.PackageManager
import android.net.Uri
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ErrorOutline
import androidx.compose.material.icons.filled.QrCode2
import androidx.compose.material.icons.filled.QrCodeScanner
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.ContextCompat
import com.journeyapps.barcodescanner.DecoratedBarcodeView
import com.moyunteng.lxcode.design.component.LxButton
import com.moyunteng.lxcode.design.component.LxButtonSize
import com.moyunteng.lxcode.design.component.LxButtonVariant
import com.moyunteng.lxcode.design.token.Lx
import com.moyunteng.lxcode.design.token.LxColors
import com.moyunteng.lxcode.remote.BuildConfig
import com.moyunteng.lxcode.remote.mock.MockData
import com.moyunteng.lxcode.remote.mock.addrValid

// ===== 配对载荷解析（真扫码 / mock / deep-link 三条入口共用）=====

/** 一条解析成功的配对载荷（对应桌面端「远程访问」二维码的三个字段）。 */
data class PairPayload(
    val addr: String,
    val token: String,
    val name: String,
)

/**
 * 解析配对载荷字符串（`lxcode://pair?addr=…&token=…&name=…`）。
 *
 * 规则：
 *  * scheme 必须是 `lxcode`、host 必须是 `pair`（大小写不敏感）；
 *  * `addr` / `token` 必填且非空，`addr` 还要过地址格式校验（host:port 或 https://host）；
 *  * `name` 可选（空则连接表单按地址自动带出显示名）；
 *  * 查询参数值按 URL 规则自动解码（`Uri.getQueryParameter`）。
 *
 * 任何一条不满足返回 null（调用方给错误提示，不崩溃）。
 */
fun parsePairPayload(raw: String): PairPayload? = runCatching {
    val uri = Uri.parse(raw)
    if (!uri.scheme.equals("lxcode", ignoreCase = true)) return@runCatching null
    if (!uri.host.equals("pair", ignoreCase = true)) return@runCatching null
    val addr = uri.getQueryParameter("addr")?.trim().orEmpty()
    val token = uri.getQueryParameter("token")?.trim().orEmpty()
    val name = uri.getQueryParameter("name")?.trim().orEmpty()
    if (addr.isEmpty() || token.isEmpty() || !addrValid(addr)) return@runCatching null
    PairPayload(addr, token, name)
}.getOrNull()

/** 按两端约定格式拼一条载荷（mock 扫码用它走与真扫码完全相同的解析路径）。 */
fun buildPairPayload(addr: String, token: String, name: String): String =
    "lxcode://pair?addr=${Uri.encode(addr)}&token=${Uri.encode(token)}&name=${Uri.encode(name)}"

/** 扫码配对页：取景框 + 说明 + 错误提示 + 调试区。 */
@Composable
fun PairingScreen(state: MockAppState) {
    // 系统返回键同样回连接页（与顶栏返回箭头一致）
    BackHandler { state.route = Route.Connections }

    Column(
        Modifier
            .fillMaxSize()
            .background(LxColors.Bg),
    ) {
        ScreenHeader(title = "扫码配对", onBack = { state.route = Route.Connections })

        Column(
            Modifier
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(Lx.space.s16),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(Lx.space.s16),
        ) {
            // ① 说明文案（对准桌面端「连接 → 远程访问」页展示的二维码）
            Column(
                horizontalAlignment = Alignment.CenterHorizontally,
                verticalArrangement = Arrangement.spacedBy(Lx.space.s4),
            ) {
                Text(
                    text = "对准桌面端「连接 → 远程访问」页展示的二维码",
                    style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size12),
                    color = Lx.colors.FgMuted,
                    textAlign = TextAlign.Center,
                )
                Text(
                    text = "扫到后自动填入连接表单，确认后保存即可。",
                    style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size10_5),
                    color = Lx.colors.FgFaint,
                    textAlign = TextAlign.Center,
                )
            }

            // ② 取景框（有相机且已授权 → 真相机预览 + 连续解码；否则占位提示）
            Viewfinder(
                onResult = { raw -> state.applyPairPayload(raw) },
            )

            // ③ 错误提示（非法载荷 / 未授权相机）
            state.scanError?.let { ScanErrorBanner(it) }

            // ④ 调试区（云机无摄像头；真机联调同款入口）——BuildConfig.DEBUG 门控：
            // 「模拟扫到一张码」「模拟非法载荷」是 mock 调试控件，release 构建不出现
            if (BuildConfig.DEBUG) {
                ScanDebugArea(state)
            }
        }
    }
}

/** 取景框：外层四角框线（纯 Compose 画），内层真相机解码或占位。 */
@Composable
private fun Viewfinder(onResult: (String) -> Unit) {
    val context = LocalContext.current
    val hasCamera = remember {
        context.packageManager.hasSystemFeature(PackageManager.FEATURE_CAMERA_ANY)
    }
    var granted by remember {
        mutableStateOf(
            ContextCompat.checkSelfPermission(context, Manifest.permission.CAMERA) ==
                PackageManager.PERMISSION_GRANTED,
        )
    }
    val permissionLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestPermission(),
    ) { ok ->
        granted = ok
        if (!ok) {
            // 不崩溃：占位提示 + 调试区按钮仍可用
        }
    }

    // 有相机硬件但还没授权 → 申请一次（授权后重组，内嵌取景开始工作）
    LaunchedEffect(hasCamera, granted) {
        if (hasCamera && !granted) permissionLauncher.launch(Manifest.permission.CAMERA)
    }

    Box(
        Modifier
            .fillMaxWidth()
            .aspectRatio(1f)
            .clip(RoundedCornerShape(Lx.radius.r10))
            .background(LxColors.TermBg),
        contentAlignment = Alignment.Center,
    ) {
        when {
            hasCamera && granted -> ScannerView(onResult = onResult)
            else -> ViewfinderPlaceholder(
                text = if (hasCamera) "相机权限未授权，无法扫码" else "此设备没有可用摄像头",
            )
        }
        // 四角框线永远画在上层
        ViewfinderCorners()
    }
}

/** 内嵌真扫码：DecoratedBarcodeView 连续解码，同一张码只处理一次。 */
@Composable
private fun ScannerView(onResult: (String) -> Unit) {
    val context = LocalContext.current
    var handled by remember { mutableStateOf(false) }
    val barcodeView = remember(context) {
        DecoratedBarcodeView(context).apply {
            barcodeView.cameraSettings.isAutoFocusEnabled = true
            decodeContinuous { result ->
                if (handled) return@decodeContinuous
                handled = true
                pause() // 拿到结果即停，避免同一张码反复触发
                onResult(result.text)
            }
        }
    }

    AndroidView(factory = { barcodeView }, modifier = Modifier.fillMaxSize())
    DisposableEffect(Unit) {
        runCatching { barcodeView.resume() }
        onDispose { runCatching { barcodeView.pause() } }
    }
}

/** 无相机/未授权时的取景框占位。 */
@Composable
private fun ViewfinderPlaceholder(text: String) {
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(Lx.space.s6),
    ) {
        SmallIcon(Icons.Filled.QrCode2, LxColors.TermDim, size = 22)
        Text(
            text = text,
            style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size11),
            color = LxColors.TermDim,
            textAlign = TextAlign.Center,
            modifier = Modifier.padding(horizontal = Lx.space.s12),
        )
    }
}

/** 四角框线（对齐常见扫码取景 UI；纯 Canvas 画，不动 :design）。 */
@Composable
private fun ViewfinderCorners() {
    val color = Lx.colors.FgMuted
    Canvas(
        Modifier
            .fillMaxSize()
            .padding(Lx.space.s10),
    ) {
        val len = 26.dp.toPx()
        val w = 3.dp.toPx()
        val inset = w / 2
        fun corner(x1: Float, y1: Float, x2: Float, y2: Float) =
            drawLine(color, Offset(x1, y1), Offset(x2, y2), strokeWidth = w)

        // 左上
        corner(inset, inset, inset + len, inset)
        corner(inset, inset, inset, inset + len)
        // 右上
        corner(size.width - inset, inset, size.width - inset - len, inset)
        corner(size.width - inset, inset, size.width - inset, inset + len)
        // 左下
        corner(inset, size.height - inset, inset + len, size.height - inset)
        corner(inset, size.height - inset, inset, size.height - inset - len)
        // 右下
        corner(size.width - inset, size.height - inset, size.width - inset - len, size.height - inset)
        corner(size.width - inset, size.height - inset, size.width - inset, size.height - inset - len)
    }
}

/** 非法载荷 / 相机异常的错误提示条（--danger 软底，与连接页断线条同款视觉）。 */
@Composable
private fun ScanErrorBanner(message: String) {
    Row(
        Modifier
            .fillMaxWidth()
            .clip(Lx.radius.FieldShape)
            .background(Lx.colors.DangerSoft)
            .padding(horizontal = Lx.space.s10, vertical = Lx.space.s8),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(Lx.space.s8),
    ) {
        SmallIcon(Icons.Filled.ErrorOutline, Lx.colors.Danger)
        Text(
            text = message,
            style = Lx.type.BodySmall.copy(fontSize = Lx.type.Size11_5),
            color = Lx.colors.Danger,
            modifier = Modifier.weight(1f),
        )
    }
}

/** 调试区：云机没有可用摄像头，验收/联调靠模拟入口（连接页调试区同一先例）。 */
@Composable
private fun ScanDebugArea(state: MockAppState) {
    Column(
        Modifier
            .fillMaxWidth()
            .clip(Lx.radius.CardShape)
            .background(LxColors.Surface)
            .border(1.dp, Lx.colors.BorderSoft, Lx.radius.CardShape)
            .padding(Lx.space.s12),
        verticalArrangement = Arrangement.spacedBy(Lx.space.s10),
    ) {
        Text(
            text = "调试（云机无摄像头 / 真机联调）",
            style = Lx.type.Pill.copy(fontSize = Lx.type.Size11, fontWeight = FontWeight.SemiBold),
            color = Lx.colors.FgFaint,
        )
        LxButton(
            text = "模拟扫到一张码",
            onClick = {
                // 拼 `lxcode://pair?...` 载荷再走统一解析路径——与真扫码同一条代码路径
                state.applyPairPayload(
                    buildPairPayload(MockData.PAIRING_ADDR, MockData.PAIRING_TOKEN, "工作室主机"),
                )
            },
            variant = LxButtonVariant.Primary,
            size = LxButtonSize.Large,
            modifier = Modifier.fillMaxWidth(),
            leadingIcon = { SmallIcon(Icons.Filled.QrCodeScanner, LxColors.Bg, size = 16) },
        )
        LxButton(
            text = "模拟非法载荷",
            onClick = { state.applyPairPayload("lxcode://pair?xxx") },
            variant = LxButtonVariant.Secondary,
            size = LxButtonSize.Medium,
            modifier = Modifier.fillMaxWidth(),
        )
    }
}
