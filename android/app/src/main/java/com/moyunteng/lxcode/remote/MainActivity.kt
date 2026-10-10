package com.moyunteng.lxcode.remote

import android.content.Intent
import android.net.Uri
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.moyunteng.lxcode.remote.ui.AppRoot

class MainActivity : ComponentActivity() {

    /**
     * 待处理的配对 deep-link（`lxcode://pair?...`）。
     *
     * 来源：系统相机扫码（Manifest 已注册 lxcode scheme 的 VIEW intent-filter）
     * 或 adb `am start -a android.intent.action.VIEW -d "lxcode://pair?..."`。
     * cold start 走 [onCreate] 里的 intent；应用已在前台走 [onNewIntent]。
     */
    private var pairUri by mutableStateOf<String?>(null)

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        takePairUri(intent)
        setContent {
            // 根 = AppRoot（底部导航 + 页面切换；首页进会话列表）。
            // deep-link 的解析/预填在 AppRoot 内完成（消费后经回调置空，防重组重复消费）。
            // 背景铺满整屏与 safeDrawing 内边距在 AppRoot 内部处理（enableEdgeToEdge 之后
            // 不加 insets padding，内容会压到状态栏/导航栏上）。
            AppRoot(
                pairUri = pairUri,
                onPairUriConsumed = { pairUri = null },
            )
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        takePairUri(intent)
    }

    /** 只认 `lxcode://pair?...` 的 VIEW intent；其余 intent 不动。 */
    private fun takePairUri(intent: Intent?) {
        val uri: Uri = intent?.data ?: return
        if (uri.scheme == "lxcode" && uri.host == "pair") {
            pairUri = uri.toString()
        }
    }
}
