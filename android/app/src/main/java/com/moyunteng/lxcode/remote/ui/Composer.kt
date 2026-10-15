// 输入区（Composer，固定在对话页底部）。
//
// 逐像素对齐桌面端 Composer.tsx + styles/composer.css（agent-console-v3）：
//  * 整个输入区是一张「白卡」——圆角 14dp、纯白底、1px #e5e5e5 ring + 微投影；
//    聚焦时 ring 变 1.5px 纯黑（150ms 过渡）——这是整个输入区最重要的视觉特征；
//  * 文本区透明无边框（不用 LxTextField）：14sp / 行高 1.5 / 内距 上14 左右16 下6 /
//    min 高 44dp / 自动增高 max 200dp（超过才内部滚动）/ placeholder #8e8ea0 三态文案；
//  * 卡内底部控件行（padding 8/12/10、gap 8、控件全 flex-none 不折行，360dp 宽放不下
//    就横向滚动）：模型药丸（唯一带描边）→ 审批档 ghost 药丸（橙 #c2410c）→
//    16dp 上下文环（>80% 变红，未知「—」）→ 统计胶囊 → 发送钮（30dp 正圆黑底）。
//
// 每个色值/字号/圆角/内距都标注对应的桌面端 CSS 选择器，不自创值。
package com.moyunteng.lxcode.remote.ui

import android.widget.Toast
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.animation.core.animateDpAsState
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsFocusedAsState
import androidx.compose.foundation.interaction.collectIsPressedAsState
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.defaultMinSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Description
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.platform.LocalContext
import com.moyunteng.lxcode.design.component.LxIconButton
import com.moyunteng.lxcode.design.motion.LxEnterSpec
import com.moyunteng.lxcode.design.motion.lxAnimateColor
import com.moyunteng.lxcode.design.motion.lxAnimateFloat
import com.moyunteng.lxcode.design.motion.lxEnter
import com.moyunteng.lxcode.design.token.Lx
import com.moyunteng.lxcode.design.token.LxColors

import com.moyunteng.lxcode.remote.model.EFFORT_IDS
import com.moyunteng.lxcode.remote.model.effortLabel
import com.moyunteng.lxcode.remote.model.timePillLabel
import kotlin.math.roundToInt

/** 输入卡片 Shape —— composer.css `.pi { border-radius: 14px }`。 */
private val ComposerCardShape = RoundedCornerShape(14.dp)

/** 输入区。
 *
 * @param real 真实后端模式：busy 跟 chat.busy、发送走 chat.send、
 *   停止走 chat.cancel、上下文环/统计胶囊用实测值、模型菜单读 model.list 注册表。
 */
@Composable
fun Composer(state: MockAppState, real: com.moyunteng.lxcode.remote.net.RealBackend?) {
    var text by remember { mutableStateOf("") }
    // 忙闲由后端事件驱动（chat.busy / done / error 收敛）
    val busy = real?.busy ?: false

    // ===== 斜杠命令面板（桌面端 SlashPalette.tsx：输入以 / 开头弹出）=====
    // slashClosed = 用户按返回键关掉面板（关面板不清输入，再输入 / 会重新弹出）
    var slashClosed by remember { mutableStateOf(false) }
    val slashOpen = text.startsWith("/") && !text.contains("\n") && !slashClosed
    // 面板过滤：/ 后的前缀词（对齐桌面端 slashQuery = value.replace(/^\/+/, "")）
    val slashQuery = if (slashOpen) text.dropWhile { it == '/' } else ""
    val context = LocalContext.current
    fun toast(msg: String) = Toast.makeText(context, msg, Toast.LENGTH_SHORT).show()
    // /new 走协议 session.new，/compact 走协议 chat.compact；agents/catalog/settings 是
    // 桌面端页面导航命令——安卓原型没有对应页，如实提示不假装执行。
    // 不用 remember 缓存：闭包要捕获当前 real（mock↔真实切换后动作必须跟着切）。
    val slashCommands = listOf(
            SlashCmd("new", "开始新对话") {
                real?.newSession(onReady = { id -> state.route = Route.Thread(id) })
            },
            SlashCmd("compact", "压缩早期历史（腾出上下文）") {
                if (real != null) real.compact() else toast("需连接后端才能压缩")
            },
            SlashCmd("agents", "打开 Agent 名单与组装") { toast("桌面端页面导航命令：安卓原型未提供该页") },
            SlashCmd("catalog", "打开拓展（工具/技能/模板/MCP）") { toast("桌面端页面导航命令：安卓原型未提供该页") },
            SlashCmd("settings", "打开设置") { toast("桌面端页面导航命令：安卓原型未提供该页") },
    )
    // ===== 模型 / 审批档选择状态 =====
    // 审批档是**会话级状态**（后端落库）：真实模式读 RealBackend（chat.history
    // 回读 + approvalChanged 广播维护，切会话即随会话走）。
    // null = 后端没报（老后端/刚切会话还没装载历史）→ 回落「默认」，不猜。
    val approval = real?.approval
    var modelMenuOpen by remember { mutableStateOf(false) }
    var effortPanelOpen by remember { mutableStateOf(false) } // 模型菜单里的二级推理强度面板
    var permMenuOpen by remember { mutableStateOf(false) }
    // 「完全访问」两步确认态——菜单收起即复位（对齐 PermPicker 的 useEffect(open)）
    var permConfirming by remember { mutableStateOf(false) }
    LaunchedEffect(permMenuOpen) {
        if (!permMenuOpen) permConfirming = false
    }

    // 返回键第一层：输入区的弹层（模型/权限菜单、斜杠面板）开着 → 返回先关弹层
    LaunchedEffect(modelMenuOpen, effortPanelOpen, permMenuOpen, slashOpen) {
        state.backInterceptor =
            if (modelMenuOpen || effortPanelOpen || permMenuOpen || slashOpen) {
                {
                    modelMenuOpen = false
                    effortPanelOpen = false
                    permMenuOpen = false
                    slashClosed = true
                    true
                }
            } else {
                null
            }
    }

    // 当前模型元数据与 effort 投影（对齐 ModelPicker.tsx）：
    // 模型注册表来自后端 model.list（RealBackend.modelGroups；role.set 切换，
    // model.changed 广播对齐——后端是唯一事实源，客户端不存本地选择）。
    // effortOptions = 当前模型支持的档位；当前档位不在其中时回落到最后一档
    val currentModel = real?.allModels?.find { it.id == real.defaultModelId }
    val effortOptions = currentModel?.efforts?.filter { it in EFFORT_IDS } ?: emptyList()
    // 会话本轮的 effort（内存视图，不落盘）：协议里 effort 只是 chat.send 的
    // 请求级参数（后端没有会话级 effort 方法、chat.history 不回它——远端无
    // 读写通道），所以本地只在**发送时**把当前视图带上，不发明持久化语义。
    // remember 无存档：App 重启回落模型默认（与「远端化」原则一致——连接
    // 信息之外不持久化业务状态）。
    var effort by remember { mutableStateOf("medium") }
    val effortId = if (effort in effortOptions) effort else effortOptions.lastOrNull() ?: ""
    // 发送携带的档位：模型不支持 effort 时空串（不带键——对非推理端点传参会 400）
    val sendEffort = if (effortOptions.isNotEmpty()) effortId else ""
    // 药丸灰字「· 中」——模型不支持 effort 时整个不显示（chip-dim 同款隐藏）
    val effortDim = if (effortOptions.isNotEmpty()) "· ${effortLabel(effortId)}" else null
    // 档位名对齐桌面端 APPROVALS（shared/settings.tsx）：默认 / 完全访问 / 只读
    val approvalLabel = when (approval) {
        "auto" -> "完全访问"
        "strict" -> "只读"
        else -> "默认"
    }

    // ===== 附件（图片/文件上传；对齐桌面端 Composer 的 att-preview 区）=====
    // 图片走 PickVisualMedia（Photo Picker，免存储权限）、文件走 OpenDocument
    //（SAF，免权限）；mime 由系统解析。base64 读入暂存（手机与后端不同机，
    // path 形态不适用），随下一条消息发送。
    val imagePicker = rememberLauncherForActivityResult(ActivityResultContracts.PickMultipleVisualMedia(maxItems = 4)) { uris ->
        if (uris.isNotEmpty()) {
            real?.addPendingImages(
                uris.map { uri ->
                    val mime = context.contentResolver.getType(uri) ?: "image/jpeg"
                    uri to mime
                },
            )
        }
    }
    val filePicker = rememberLauncherForActivityResult(ActivityResultContracts.OpenMultipleDocuments()) { uris ->
        if (uris.isNotEmpty()) real?.addPendingFiles(uris)
    }

    Column(
        Modifier
            .fillMaxWidth()
            // 卡片下方留 14dp 空隙（桌面端 .composer-zone 的 padding-bottom 18px 的近邻档），
            // scrim 渐变一并盖住这段
            .padding(bottom = 14.dp),
    ) {
        // 斜杠命令面板（输入 / 弹出，位于输入卡上方；照桌面端 composer.css .slash-palette：
        // 白卡 / 1px --border-strong / 圆角 12 / padding 5 / 条目名+描述 / pop-in 160ms）
        if (slashOpen) {
            SlashPalette(
                query = slashQuery,
                commands = slashCommands,
                onPick = { cmd ->
                    text = "" // 选中清输入（对齐桌面端 Composer.pick）
                    cmd.action()
                },
            )
        }

        // ===== 附件预览区（.att-preview：卡内、文本行上方；有暂存或有提示才渲染）=====
        // 图片 = 64dp 方缩略图（radius 16）+ 右上 18dp 移除钮；文件 = 240×64 卡。
        if (real != null && (real.pendingImages.isNotEmpty() || real.pendingFiles.isNotEmpty() || real.attachNotice != null)) {
            Column(
                Modifier
                    .fillMaxWidth()
                    .padding(start = 16.dp, end = 16.dp, top = 12.dp),
                verticalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                real.attachNotice?.let { notice ->
                    Text(
                        text = notice,
                        style = TextStyle(fontFamily = Lx.type.Sans, fontSize = 11.5.sp),
                        color = Lx.colors.FgMuted,
                    )
                }
                if (real.pendingImages.isNotEmpty()) {
                    Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                        real.pendingImages.forEach { im ->
                            PendingImageThumb(
                                image = im,
                                urlOf = { real.attachmentURL(it) },
                                onRemove = { real.removePendingImage(im.id) },
                            )
                        }
                    }
                }
                if (real.pendingFiles.isNotEmpty()) {
                    Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                        real.pendingFiles.forEach { f ->
                            PendingFileCard(
                                name = f.name,
                                sub = fileSub(f.size, f.mime),
                                onRemove = { real.removePendingFile(f.id) },
                            )
                        }
                    }
                }
            }
        }

        // ===== 白卡 =====
        val input = remember { MutableInteractionSource() }
        val focused by input.collectIsFocusedAsState()
        // 聚焦环：1px #e5e5e5 → 1.5px #0d0d0d，150ms 过渡（`.pi` / `.pi:focus-within` 的 box-shadow）
        val ringWidth by animateDpAsState(
            targetValue = if (focused) 1.5.dp else 1.dp,
            animationSpec = tween(150),
            label = "composer-ring-width",
        )
        val ringColor by lxAnimateColor(
            target = if (focused) Lx.colors.Fg else Lx.colors.BorderStrong,
            durationMillis = 150,
        )
        Column(
            Modifier
                .fillMaxWidth()
                // 微投影 —— `.pi { box-shadow: 0 1px 3px rgba(0,0,0,0.04) }`
                .shadow(elevation = 3.dp, shape = ComposerCardShape, clip = false, spotColor = Color(0x0A000000))
                .clip(ComposerCardShape)
                .background(Lx.colors.Surface)
                .border(width = ringWidth, color = ringColor, shape = ComposerCardShape),
        ) {
            // ===== 文本输入区（透明无边框；.piInput）=====
            ComposerTextField(
                value = text,
                onValueChange = { text = it },
                busy = busy,
                interactionSource = input,
                modifier = Modifier.fillMaxWidth(),
            )

            // ===== 卡内底部控件行（.piBar：padding 8/12/10、gap 8、不折行）=====
            Row(
                Modifier
                    .fillMaxWidth()
                    .padding(start = 12.dp, end = 12.dp, top = 8.dp, bottom = 10.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                // 「＋」添加附件（对齐桌面端 att-add：输入区左端图标、贴底对齐）——
                // 点图片（Photo Picker）/文件（SAF）两个入口
                LxIconButton(onClick = {
                    imagePicker.launch(
                        androidx.activity.result.PickVisualMediaRequest(
                            ActivityResultContracts.PickVisualMedia.ImageOnly,
                        ),
                    )
                }) {
                    Icon(
                        imageVector = Icons.Filled.Add,
                        contentDescription = "添加图片或文件",
                        tint = Lx.colors.FgMuted,
                        modifier = Modifier.size(16.dp),
                    )
                }
                LxIconButton(onClick = { filePicker.launch(arrayOf("*/*")) }) {
                    Icon(
                        imageVector = Icons.Filled.Description,
                        contentDescription = "添加文件",
                        tint = Lx.colors.FgMuted,
                        modifier = Modifier.size(16.dp),
                    )
                }
                // 药丸组：360dp 放不下就横滚（控件各自 flex:none，视觉不缩水）
                Row(
                    Modifier
                        .weight(1f)
                        // 控件入场：pop-in（与桌面端弹层同款，160ms 标准曲线）
                        .lxEnter(spec = LxEnterSpec.PopIn)
                        .horizontalScroll(rememberScrollState()),
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    // 模型药丸 + 模型菜单（左面板）+ 推理强度二级右面板 —— 见 PickerMenus.kt
                    Box {
                        ModelPill(
                            label = currentModel?.name ?: (real?.defaultModelId?.ifBlank { null } ?: "模型"),
                            effortDim = effortDim,
                            expanded = modelMenuOpen,
                            onToggle = {
                                modelMenuOpen = !modelMenuOpen
                                if (modelMenuOpen) effortPanelOpen = false // 重新打开回到模型面板
                            },
                        )
                        AnchorMenu(expanded = modelMenuOpen, onDismiss = { modelMenuOpen = false }) {
                            ModelMenuContent(
                                groups = real?.modelGroups ?: emptyList(),
                                currentModelId = real?.defaultModelId ?: "",
                                effortDim = effortDim,
                                effortPanelOpen = effortPanelOpen,
                                onPickModel = { id ->
                                    // 切模型：role.set（后端角色绑定，多端同步——
                                    // model.changed 广播回来对齐）；当前档位在新模型上
                                    // 不存在 → 本地回落（优先「中」，否则首档）
                                    val m = real?.allModels?.find { it.id == id }
                                    val supported = m?.efforts?.filter { it in EFFORT_IDS } ?: emptyList()
                                    if (supported.isNotEmpty() && effort !in supported) {
                                        effort = if ("medium" in supported) "medium" else supported.first()
                                    }
                                    real?.setDefaultModel(id) { ok, msg ->
                                        // role.set 失败要给反馈：选择态由 model.changed
                                        // 广播对齐（后端没改绑就不会广播），本地乐观选中
                                        // 会永远停在旧模型上——toast 说明，不假装成功
                                        if (!ok) toast("切换模型失败${if (msg.isNotBlank()) "：$msg" else ""}")
                                    }
                                    modelMenuOpen = false // 选完带退场动画收起
                                },
                                onToggleEffortPanel = { effortPanelOpen = !effortPanelOpen },
                            )
                        }
                        // 二级推理强度面板：紧贴左面板右缘 +8dp（shiftX = 200 + 8），从左滑入；
                        // 超屏钳到屏右缘。不抢焦点（focusable = false）：左面板条目保持可点、
                        // 跳转行可确定性地往返切换（可聚焦版实测有两处 ACTION_OUTSIDE 收起竞态）
                        AnchorMenu(
                            expanded = modelMenuOpen && effortPanelOpen,
                            onDismiss = { effortPanelOpen = false },
                            focusable = false,
                            shiftXDp = 208.dp,
                            enter = mpSlideInEnter(),
                        ) {
                            EffortMenuContent(
                                effortIds = effortOptions,
                                currentEffort = effortId,
                                onPick = { id ->
                                    effort = id
                                    modelMenuOpen = false // 选完连左面板一起收起
                                },
                            )
                        }
                    }
                    // 审批档药丸 + 权限菜单（三档 + 「完全访问」两步确认）—— 见 PickerMenus.kt
                    Box {
                        ApprovalPill(
                            label = approvalLabel,
                            expanded = permMenuOpen,
                            onToggle = { permMenuOpen = !permMenuOpen },
                        )
                        AnchorMenu(expanded = permMenuOpen, onDismiss = { permMenuOpen = false }) {
                            PermMenuContent(
                                current = approval ?: "confirm",
                                confirming = permConfirming,
                                onPick = { id ->
                                    if (id == "auto" && approval != "auto") {
                                        // 危险方向才拦：首次点击进 confirming 态，再点才生效；
                                        // 从完全访问切回安全档不需要确认（收权总是安全的）
                                        if (!permConfirming) {
                                            permConfirming = true
                                            return@PermMenuContent
                                        }
                                    }
                                    permConfirming = false
                                    // 走协议（chat.approval：后端落库 + 广播，
                                    // approvalChanged 回来对齐药丸）
                                    real?.changeApproval(id)
                                    permMenuOpen = false
                                },
                            )
                        }
                    }
                    // 上下文环 + 百分比（.ctx-chip / .ctx-pct）
                    // 实测值（context 键缺席 = null → 环显示「—」）
                    ContextRing(real?.contextUsage)
                    // 会话统计胶囊（.stats-pill：步数 0 整个胶囊不渲染）
                    StatsCapsule(real?.sessionStats)
                }
                Spacer(Modifier.width(4.dp))
                SendButton(
                    busy = busy,
                    enabled = (text.trim().isNotEmpty() && !text.startsWith("/")) ||
                        real?.pendingImages?.isNotEmpty() == true ||
                        real?.pendingFiles?.isNotEmpty() == true,
                    onClick = {
                        if (busy) {
                            real?.cancel()
                        } else {
                            real?.send(text, sendEffort)
                            text = ""
                        }
                    },
                )
            }
        }
    }
}

/**
 * 文本输入区 —— 透明无边框（composer.css `.pi textarea.piInput`）：
 * 14sp / 行高 1.5 / 内距 上14 左右16 下6 / min 高 44dp / 自动增高 max 200dp
 * （到上限才内部滚动）/ 颜色 #0d0d0d / placeholder #8e8ea0 三态文案。
 */
@Composable
private fun ComposerTextField(
    value: String,
    onValueChange: (String) -> Unit,
    busy: Boolean,
    interactionSource: MutableInteractionSource,
    modifier: Modifier = Modifier,
) {
    // 三态文案（桌面端 Composer.tsx 的 placeholder 三元链：locked / busy / normal）
    val placeholder = when {
        busy -> "生成中… 可以先输入下一条（完成后发送）"
        else -> "让智能体构建、审查或解释点什么…"
    }
    BasicTextField(
        value = value,
        onValueChange = onValueChange,
        modifier = modifier
            .heightIn(min = 44.dp, max = 200.dp)
            // 自增高：内容 ≤200dp 时随内容长高、不出内部滚动条，到上限才内部滚动
            .verticalScroll(rememberScrollState())
            .padding(start = 16.dp, end = 16.dp, top = 14.dp, bottom = 6.dp),
        textStyle = TextStyle(
            fontFamily = Lx.type.Sans,
            fontSize = 14.sp,
            lineHeight = 21.sp, // 14 × 1.5（.piInput 的 font-size:14px / line-height:1.5）
            color = Lx.colors.Fg,
        ),
        cursorBrush = SolidColor(Lx.colors.Fg),
        interactionSource = interactionSource,
        decorationBox = { inner ->
            Box(Modifier.fillMaxWidth()) {
                if (value.isEmpty()) {
                    Text(
                        text = placeholder,
                        style = TextStyle(
                            fontFamily = Lx.type.Sans,
                            fontSize = 14.sp,
                            lineHeight = 21.sp,
                            color = Lx.colors.FgFaint, // ::placeholder { color: var(--fg-faint) }
                        ),
                        maxLines = 1,
                    )
                }
                inner()
            }
        },
    )
}

/**
 * 上下文占用环 —— 16dp 自绘 Canvas（桌面端 ContextIndicator 的 SVG 环：
 * viewBox 20 / r=8 / 线宽 2.5 / 圆头，从 12 点方向顺时针）。
 * 轨道 #e5e5e5、进度弧正常 #6e6e80、>80% 变 #e02e2a；右侧 10sp mono 百分比；
 * 窗口未知显示「—」。
 */
@Composable
private fun ContextRing(usage: com.moyunteng.lxcode.remote.model.ContextUsage?) {
    val known = (usage?.window ?: 0) > 0
    val target = if (!known) 0f else (usage!!.used.toFloat() / usage.window).coerceIn(0f, 1f)
    // 环读数变化 220ms 动画过渡（桌面端 ContextIndicator 的环不瞬跳）
    val pct by lxAnimateFloat(target = target)
    val track = Lx.colors.BorderStrong // #e5e5e5
    // >80% 变红（#e02e2a = --danger），否则 #6e6e80 = --fg-muted
    val ink = if (target > 0.8f) LxColors.Danger else Lx.colors.FgMuted
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(4.dp)) {
        Box(Modifier.size(16.dp), contentAlignment = Alignment.Center) {
            Canvas(Modifier.size(16.dp)) {
                // viewBox 20 / r 8 / stroke 2.5 → 16dp 下 stroke = 2dp
                val stroke = 2.5f * (size.width / 20f)
                val inset = stroke / 2f
                val arcSize = Size(size.width - stroke, size.height - stroke)
                drawArc(
                    color = track,
                    startAngle = -90f,
                    sweepAngle = 360f,
                    useCenter = false,
                    topLeft = Offset(inset, inset),
                    size = arcSize,
                    style = Stroke(width = stroke, cap = StrokeCap.Round),
                )
                drawArc(
                    color = ink,
                    startAngle = -90f,
                    sweepAngle = 360f * pct,
                    useCenter = false,
                    topLeft = Offset(inset, inset),
                    size = arcSize,
                    style = Stroke(width = stroke, cap = StrokeCap.Round),
                )
            }
        }
        Text(
            text = if (known) "${(pct * 100).roundToInt()}%" else "—",
            style = TextStyle(fontFamily = Lx.type.Mono, fontSize = 10.sp, color = Lx.colors.FgFaint),
        )
    }
}

/** 会话统计胶囊 —— ghost 药丸（.stats-pill：11sp、#8e8ea0、tabular-nums；步数 0 整个胶囊不渲染）。 */
@Composable
private fun StatsCapsule(stats: com.moyunteng.lxcode.remote.model.SessionStats?) {
    if (stats == null || stats.steps <= 0) return
    Text(
        text = stats.timePillLabel(), // 「N 轮 · M 步 · X.X tok/s」，缺哪段省哪段
        style = TextStyle(
            fontFamily = Lx.type.Sans,
            fontSize = 11.sp,
            color = Lx.colors.FgFaint,
            fontFeatureSettings = "tnum", // tabular-nums
        ),
        maxLines = 1,
        modifier = Modifier.padding(horizontal = 8.dp, vertical = 2.dp),
    )
}

/**
 * 发送 / 停止按钮 —— 30dp 正圆、黑底 #0d0d0d、白色向上箭头（stroke 2.2 圆头）；
 * busy 时同位置换停止钮（白色 11dp 圆角方块图标）；禁用 = 25% 透明度；按压 scale 0.95。
 */
@Composable
private fun SendButton(busy: Boolean, enabled: Boolean, onClick: () -> Unit) {
    val clickable = busy || enabled
    val interaction = remember { MutableInteractionSource() }
    val pressed by interaction.collectIsPressedAsState()
    // 按压缩放 0.95（.send-btn:active { transform: scale(0.95) }，120ms）
    // —— :design 的 lxPressScale 固定 0.98 token，这里规格要求 0.95，app 内私有实现
    val scale by animateFloatAsState(
        targetValue = if (pressed && clickable) 0.95f else 1f,
        animationSpec = tween(120),
        label = "send-press-scale",
    )
    // 禁用 = 25% 透明度（.send-btn:disabled { opacity: 0.25 }），120ms 过渡
    val alpha by animateFloatAsState(
        targetValue = if (clickable) 1f else 0.25f,
        animationSpec = tween(120),
        label = "send-alpha",
    )
    Box(
        Modifier
            .graphicsLayer {
                scaleX = scale
                scaleY = scale
                this.alpha = alpha
            }
            // 无障碍标签（对齐桌面端 aria-label：「发送」/「停止生成」）
            .semantics { contentDescription = if (busy) "停止生成" else "发送" }
            .size(30.dp)
            .clip(androidx.compose.foundation.shape.CircleShape)
            .background(Lx.colors.Fg)
            .clickable(
                enabled = clickable,
                interactionSource = interaction,
                indication = null,
                onClick = onClick,
            ),
        contentAlignment = Alignment.Center,
    ) {
        if (busy) {
            // 停止图标：白色 11dp 圆角方块（rx 2.5）
            Box(
                Modifier
                    .size(11.dp)
                    .clip(RoundedCornerShape(2.5.dp))
                    .background(LxColors.Bg),
            )
        } else {
            // 向上箭头：M12 19V5 + m5 12 7-7 7 7（stroke 2.2 圆头），自绘对齐桌面端 SVG
            Canvas(Modifier.size(14.dp)) {
                val s = size.width / 24f
                val stroke = 2.2f * s
                drawLine(LxColors.Bg, Offset(12 * s, 19 * s), Offset(12 * s, 5 * s), strokeWidth = stroke, cap = StrokeCap.Round)
                drawLine(LxColors.Bg, Offset(5 * s, 12 * s), Offset(12 * s, 5 * s), strokeWidth = stroke, cap = StrokeCap.Round)
                drawLine(LxColors.Bg, Offset(19 * s, 12 * s), Offset(12 * s, 5 * s), strokeWidth = stroke, cap = StrokeCap.Round)
            }
        }
    }
}

// ===== 斜杠命令面板（桌面端 SlashPalette.tsx / composer.css .slash-palette）=====

/** 斜杠命令（桌面端 SlashCommand：name 不含 /；desc 一句话描述）。 */
private data class SlashCmd(val name: String, val desc: String, val action: () -> Unit)

/** 面板 Shape —— composer.css `.slash-palette { border-radius: 12px }`。 */
private val SlashPaletteShape = RoundedCornerShape(12.dp)

/**
 * 斜杠命令面板 —— composer.css `.slash-palette`：白卡、1px --border-strong、圆角 12、
 * 菜单级阴影；条目 `.cmd-item`（padding 8/10、圆角 8、命令名 12sp/600 mono + 描述
 * 11.5sp 灰）；`.cmd-list` padding 5、max-height 264。入场 pop-in 160ms。
 * 输入 / 前缀弹出，前缀过滤（name 或 desc 包含关键字，对齐桌面端 matches）；
 * 触摸设备没有键盘导航：点按条目执行（桌面端的 ↑↓/Enter/Esc 提示行不照抄）。
 */
@Composable
private fun SlashPalette(
    query: String,
    commands: List<SlashCmd>,
    onPick: (SlashCmd) -> Unit,
) {
    val matches = remember(query, commands) {
        val q = query.trim().lowercase()
        if (q.isEmpty()) commands
        else commands.filter { it.name.contains(q) || it.desc.lowercase().contains(q) }
    }
    Column(
        Modifier
            .fillMaxWidth()
            // bottom: calc(100% + 8px) 的近邻档（面板贴输入卡上方留 8dp）
            .padding(bottom = 8.dp)
            .shadow(
                elevation = 12.dp,
                shape = SlashPaletteShape,
                clip = false,
                ambientColor = Color(0x24000000),
                spotColor = Color(0x14000000),
            )
            .clip(SlashPaletteShape)
            .background(Lx.colors.Surface)
            .border(1.dp, Lx.colors.BorderStrong, SlashPaletteShape)
            .padding(5.dp)
            .lxEnter(spec = LxEnterSpec.PopIn),
        verticalArrangement = Arrangement.spacedBy(1.dp),
    ) {
        if (matches.isEmpty()) {
            // `.cmd-empty`：padding 14/12、12sp、#8e8ea0
            Text(
                text = "没有匹配「${query.trim()}」的命令",
                style = TextStyle(fontFamily = Lx.type.Sans, fontSize = 12.sp),
                color = Lx.colors.FgFaint,
                modifier = Modifier.padding(horizontal = 12.dp, vertical = 14.dp),
            )
        } else {
            matches.forEach { cmd ->
                val interaction = remember { MutableInteractionSource() }
                Row(
                    Modifier
                        .fillMaxWidth()
                        .clip(RoundedCornerShape(8.dp))
                        .clickable(interactionSource = interaction, indication = null) { onPick(cmd) }
                        .padding(horizontal = 10.dp, vertical = 8.dp),
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(12.dp),
                ) {
                    Text(
                        text = "/${cmd.name}",
                        style = TextStyle(
                            fontFamily = Lx.type.Mono,
                            fontSize = 12.sp,
                            fontWeight = FontWeight.SemiBold,
                        ),
                        color = Lx.colors.Fg,
                        modifier = Modifier.defaultMinSize(minWidth = 88.dp),
                    )
                    Text(
                        text = cmd.desc,
                        style = TextStyle(fontFamily = Lx.type.Sans, fontSize = 11.5.sp),
                        color = Lx.colors.FgFaint,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
            }
        }
    }
}
