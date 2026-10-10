# lxcode 安卓端 P0 全量页面原型 —— 验收报告

> 全部页面 **mock 数据驱动**：不发任何网络请求、不连后端、不 import 任何网络库。
> 后端协议只作为**字段命名与语义的对照物**（`internal/protocol` / `internal/llm` / `internal/tools`）。
> 改动只在 `C:\Users\xzy\Desktop\my\lxcode\android\` 下；`:design` 模块**零改动**（公开 API 未动）。

---

## 1. 文件树（新增 / 修改）

### 新增（10 个）

```
android/
├── app/src/main/java/com/moyunteng/lxcode/remote/
│   ├── mock/MockData.kt            359 行  mock 数据层（协议对齐：连接/会话/项目/消息块/上下文/统计/todo/配对凭证）
│   ├── qr/QrCode.kt                153 行  二维码生成（真 zxing 自然模块矩阵 + 确定性假图案回落）+ Canvas 绘制
│   └── ui/
│       ├── AppRoot.kt              207 行  Screen 枚举 + MockAppState（连接/在线开关/搜索/范围/表单草稿）+ 底部导航
│       ├── Common.kt               335 行  ConnPill / ScreenHeader / TerminalBlock / CodeBlock / MockSwitch / LabeledRow / SmallEmpty / SmallIcon / Txt
│       ├── ConnectionsScreen.kt     450 行  连接页（本机 + 3 远程 + 添加 + 后端不可达错误条）+ 连接表单页（校验/取消/保存）
│       ├── PairingScreen.kt         303 行  配对/扫码页（二维码 + 地址/Token 两行 + 复制）+ 扫码结果模拟页
│       ├── SessionsScreen.kt        303 行  会话列表页（导航项 / 搜索 / 项目分组 / 会话行 / 设置）
│       ├── ThreadScreen.kt          502 行  对话线程页（八类消息块 + 确认门）
│       └── Composer.kt             266 行  输入区（输入框 / 发送·停止 / 模型·effort·审批档·上下文环·统计胶囊一行）
└── tools/
    ├── shot.sh                      11 行  截图助手（screencap + pull 到 android/）
    └── proto_summary.py             39 行  截图像素校验（非空白 + token 色命中）
```

### 修改（2 个）

| 文件 | 改动 | 说明 |
|---|---|---|
| `app/build.gradle.kts` | `+ implementation("com.google.zxing:core:3.4.1")` | 只加到 `:app`；`:design` 是纯组件库，不掺业务资产（见 `design/build.gradle.kts` 的纪律注释）。3.4.1 与本机 gradle 缓存中的版本一致 |
| `app/src/main/java/.../remote/MainActivity.kt` | 内容换成 `AppRoot()` | 首页不再是 `ShowcaseScreen`；`ShowcaseScreen.kt` **文件保留**，改成底部导航「展示」入口进入 |

**未改动**：`design/` 全模块（token + 12 组件）、`ShowcaseScreen.kt`、`settings.gradle.kts`、`build.gradle.kts`、`gradle.properties`、`local.properties`。

---

## 2. 验收证据

### a) `gradle assembleDebug` BUILD SUCCESSFUL，0 error

构建命令（本机无 wrapper，用本机 gradle 8.11.1）：

```
C:\Users\xzy\.gradle\wrapper\dists\gradle-8.11.1-bin\eac4u065zwes5phgltp5f9b9e\gradle-8.11.1\bin\gradle.bat assembleDebug
```

输出（`android/build-proto.log` 尾部）：

```
> Task :app:packageDebug
> Task :app:createDebugApkListingFileRedirect
> Task :app:assembleDebug

BUILD SUCCESSFUL in 5s
64 actionable tasks: 6 executed, 58 up-to-date
```

- 产物：`app/build/outputs/apk/debug/app-debug.apk`（20,784,913 字节 ≈ 19.8 MB）
- **0 error**（`grep -E "^e: "` 无命中）。剩余告警只有 Kotlin 的「未使用 import」提示，不影响构建。

### b) 安装 + 启动 + logcat

```
$ adb -s 10.10.5.202:30900 install -r app/build/outputs/apk/debug/app-debug.apk
Performing Streamed Install
Success

$ adb -s 10.10.5.202:30900 shell am start -n com.moyunteng.lxcode.remote/.MainActivity
Starting: Intent { cmp=com.moyunteng.lxcode.remote/.MainActivity }
ActivityTaskManager: Displayed com.moyunteng.lxcode.remote/.MainActivity for user 0: +1s906ms

$ adb -s 10.10.5.202:30900 shell pidof com.moyunteng.lxcode.remote
22205

$ adb -s 10.10.5.202:30900 logcat -d -b crash | tail -5
（空）

$ adb -s 10.10.5.202:30900 logcat -d | grep -c "FATAL EXCEPTION"
0
```

- 进程存活（pid 22205 / 21589 / 19856 每次启动都在）
- crash buffer 空，主 logcat **0 条 FATAL EXCEPTION**

### c) 逐页截图（21 张，全部在 `C:\Users\xzy\Desktop\my\lxcode\android\`）

全部截图经像素校验（`tools/proto_summary.py`）：**无空白页**（non_white 3.7%~80.5%），
且命中 lxcode token 色（`--bg` / `--border-soft` / `--fg` / `--fg-faint` / `--success` /
`--danger` / `--term-bg` / `amber` / `bubble #f1f1f3` / `code-bg #f4f5f7`）。

| 截图 | 页面 | 关键内容 |
|---|---|---|
| `proto-sessions-1.png` | 会话列表（首页，未分组范围） | LxCode 品牌 + 连接药丸「工作室主机」+ 6 个导航项 + 搜索框「Ctrl K」+ 项目分组（lxcode 3 / 官网站点 2 / 未分组 3）+ 3 条未分组会话 + 设置行 + 底部导航 |
| `proto-sessions-2.png` | 会话列表（lxcode 项目范围） | 项目行选中态 + 3 条项目会话（安卓端 P0 页面原型 3 分钟前 / compaction 影子区间修复 1 小时前 / 工具配对不变量排查 昨天） |
| `proto-sessions-3.png` | 会话列表（搜索过滤） | 搜索框输入 `a` → 3 条会话过滤为 1 条（compaction 影子区间修复），右侧恢复「Ctrl K」角标 |
| `proto-connections-1.png` | 连接页（在线态） | 本机卡（绿点 + 127.0.0.1:7789 + 连接）+ 3 条远程卡（工作室主机「当前」+ 断开/编辑/删除；云服务器、家里 NAS 各「连接」）+ 「+ 添加远程后端」+ 提示行 |
| `proto-connform-1.png` | 连接表单（编辑态） | 「编辑远程后端」+ 地址/名称/Token 三字段（已填）+ Token 提示行 + 取消 + 保存 |
| `proto-connform-2.png` | 连接表单（校验失败态） | 地址 `aaa` + 红字「地址格式应为 host:port（如 10.0.0.8:7789）或 https://host。」+ 保存（禁用） |
| `proto-offline-1.png` | 连接页（断线态） | `--danger` 错误条「后端不可达：无法连接 ws://127.0.0.1:7789/rpc」+ 重试按钮；顶栏药丸灰点 |
| `proto-offline-2.png` | 对话页（顶栏离线药丸） | 顶栏药丸圆点为灰（`--fg-faint`），无绿色像素（见下方像素断言） |
| `proto-pairing-1.png` | 配对/扫码页 | 二维码（200dp）+ 引擎标记 pill「zxing」+ 地址/Token 两行 + 复制按钮 + 「扫码配对」入口 + 说明 |
| `proto-scanresult-1.png` | 扫码结果模拟页 | 深色取景框（`--term-bg`）+ 「云机无摄像头 —— 这是模拟取景」+ 「扫到的码」地址/Token 两行 + 「模拟扫到一张码」按钮 |
| `proto-scanform-1.png` | 扫码 → 预填表单 | 「添加远程后端」+ 绿底提示「已从扫码结果填入地址与 Token，确认后保存。」+ 三字段已预填 + 保存 |
| `proto-scanform-2.png` | 扫码 → 保存 → 回到连接列表 | 列表第 5 条 = 扫码新增的后端（工作室主机 10.0.0.8:7789 · a1b2••••7890） |
| `proto-thread-1.png` | 对话线程（上段） | 顶栏（连接药丸 + 会话标题 + 确认门入口）+ 用户气泡 + 思考块（折叠一行「思考了 4.2s」）+ 助手正文 + 轮末统计行 + 工具卡「读文件 / app/build.gradle.kts / 成功 / 120ms」 |
| `proto-thread-2.png` | 对话线程（中段 1） | 工具卡「搜索 / LxButton\|LxListItem\|LxConfirmDialog / 执行中…」+ 输出块「等待工具输出…」+ 子 Agent 派发卡「已派发 → frontend-dev / 结论 / child-9f3a2b1c / ↗」+ 工具卡「bash / gradle assembleDebug / 失败 / 3.2s」+ 红底错误输出 |
| `proto-thread-3.png` | 对话线程（中段 2） | 派发卡 + bash 失败卡 + 压缩检查点块「已压缩历史 / 12 条 · 48000 → 12000 / 摘要正文」 |
| `proto-thread-4.png` | 对话线程（中段 3） | 压缩检查点块 + todo 清单块（任务清单 2/4：完成 / 完成 / 进行中 / 待办四行） |
| `proto-thread-5.png` | 对话线程（末段） | todo 清单 + 助手结论正文 + 轮末统计行 + 错误块（`--danger`「连接中断：websocket closed (1006)」） |
| `proto-confirm-1.png` | 确认门弹层 | 「执行此命令？」+ 正文 + 深色终端命令块（cwd + `$` 绿提示符 + 命令）+ 拒绝（ghost）+ 批准（黑主按钮） |
| `proto-composer-1.png` | 输入区特写（左半） | 输入框占位「发消息…」+ 发送黑按钮 + 控件行「模型 deepseek-v3.2 / effort medium / auto confirm strict」+ 上下文环 34% |
| `proto-composer-2.png` | 输入区特写（右半，控件行横向滚动后） | 控件行「effort medium / auto confirm strict / 上下文环 34% / 统计胶囊 12 轮 · 34 步 · 13.7 tok/s」 |
| `proto-showcase-1.png` | 展示页（原 ShowcaseScreen） | 12 个组件的变体铺开（token 色板 / 按钮四档 / 药丸 / 状态点 …），从底部导航「展示」进入 |

**输入区那一行不折行**：`Modifier.horizontalScroll` + 各控件不收缩（等价桌面端 `.piBar` 的 `flex:none` + `nowrap`）。
如实说明：360dp 宽的云机放不下 5 项（桌面端 `Composer` 宽 720px），所以拆成 `proto-composer-1/2`
两张（左半 = 模型/effort/审批档，右半 = 审批档/上下文环/统计胶囊），滚动的是**同一个不折行的行**。

**断线态两种**：`proto-offline-1`（连接页错误条）与 `proto-offline-2`（顶栏离线药丸）；
用连接页的「模拟在线」mock 开关切换。像素断言：

```
proto-thread-1.png   药丸区最绿像素 = (148,214,198)  ← 绿点（--success 呼吸中）
proto-offline-2.png  药丸区最绿像素 = (255,255,255)  ← 无绿色像素，灰点（--fg-faint）
```

### d) uiautomator dump 核对（真实文案，逐条命中）

```
── 连接页（proto-connections-1.png）──
'连接'  '模拟在线'
'本机'  '127.0.0.1:7789'  '连接'
'工作室主机'  '当前'  '10.0.0.8:7789 · a1b2••••7890'  '断开'  '编辑'  '删除'
'云服务器'  'api.lxcode.example.com:7789 · lx_l••••3210'  '连接'  '编辑'  '删除'
'家里 NAS'  '192.168.1.50:7789 · nas-••••ffff'  '连接'  '编辑'  '删除'
'+ 添加远程后端'

── 连接表单页（proto-connform-1.png / -2.png）──
'编辑远程后端'  '地址'  '10.0.0.8:7789'  '名称'  '工作室主机'  'Token'
'a1b2c3d4-e5f6-7890-abcd-ef1234567890'
'对方机器「连接 → 远程访问」面板里展示的 Token；或问管理员要。'
'取消'  '保存'
（校验态）'aaa' + '地址格式应为 host:port（如 10.0.0.8:7789）或 https://host。'

── 配对页（proto-pairing-1.png）──
'配对'  '扫码即可配对这台后端'  'zxing'
'地址'  '10.0.0.8:7789'  '复制'  'Token'  'a1b2c3d4-e5f6-7890-abcd-ef1234567890'  '复制'
'扫码配对'

── 扫码结果模拟页（proto-scanresult-1.png）──
'扫码配对'  '云机无摄像头 —— 这是模拟取景'  '扫到的码'
'地址'  '10.0.0.8:7789'  '复制'  'Token'  '复制'  '模拟扫到一张码'

── 扫码 → 预填（proto-scanform-1.png）──
'添加远程后端'  '已从扫码结果填入地址与 Token，确认后保存。'
'地址'  '10.0.0.8:7789'  '名称'  '工作室主机'  'Token'  '保存'  '取消'

── 会话列表（proto-sessions-1.png）──
'LxCode'  '工作室主机'
'新对话'  'Agents'  '拓展'  'Git 管理'  '远程访问'  '自动化'
'搜索对话与项目'  'Ctrl K'
'项目'  'lxcode'  '3'  '官网站点'  '2'  '未分组'  '3'
'对话'  '解释一下 Go 的 context 包'  '5 分钟前'  '写一封周报邮件'  '上周'  '整理会议纪要'  '上周'
'设置'
（项目范围 proto-sessions-2.png）'安卓端 P0 页面原型'  '3 分钟前'
'compaction 影子区间修复'  '1 小时前'  '工具配对不变量排查'  '昨天'
（搜索过滤 proto-sessions-3.png）'a' → 'compaction 影子区间修复'（3 条过滤为 1 条）

── 对话线程（proto-thread-1~5.png）──
'安卓端 P0 页面原型'  '工作室主机'
'帮我把安卓端的 P0 页面原型做出来，全部用 mock 数据驱动，每类消息块都要能看到。'   ← 用户气泡
'思考'  '思考了 4.2s'                                                       ← 思考块（折叠态）
'我先勘察现场：读 :design 的 token 与 12 个组件，再对照桌面端 `ConnectionManager.tsx` / `Sidebar.tsx` / `thread/` 的布局。'  ← 助手正文
'deepseek-v3.2 · 首字 812ms · 13.7 tok/s'                                    ← 轮末统计行
'读文件'  'app/build.gradle.kts'  '成功'  '120ms'                              ← 工具卡（成功）
'搜索'  '执行中…'  'LxButton|LxListItem|LxConfirmDialog'  '等待工具输出…'        ← 工具卡（运行中）
'已派发 → frontend-dev'  '已实现 8 类块，思考块折叠态一行、工具卡带状态点与耗时。'  'child-9f3a2b1c'  ← 子 Agent 派发卡
'bash'  '失败'  'gradle assembleDebug'  '3.2s'  'FAILURE: Build failed with an exception...'  ← 工具卡（失败）
'已压缩历史'  '12 条 · 48000 → 12000'  '**目标**：产出安卓端 P0 全量页面原型（mock 驱动）。…'  ← 压缩检查点块
'任务清单 2/4'  '通读 :design 组件签名'  '完成'  '写 mock 数据层（协议对齐）'  '完成'
'实现 6 个页面 + 导航根'  '进行中'  '云机逐页截图验收'                             ← todo 清单块
'连接中断：websocket closed (1006) —— 已退回本机，点顶栏连接药丸重连。'              ← 错误块

── 确认门（proto-confirm-1.png）──
'执行此命令？'  'bash 工具申请执行以下命令（高危：会改动工作区）。'
'C:\\Users\\xzy\\Desktop\\my\\lxcode\\android'  '$'  'rm -rf build && ./gradlew clean assembleDebug'
'拒绝'  '批准'

── 输入区（proto-composer-1/2.png）──
'模型'  'deepseek-v3.2'  'effort'  'medium'  'auto'  'confirm'  'strict'
'34%'  '12 轮 · 34 步 · 13.7 tok/s'

── 断线态（proto-offline-1.png）──
'后端不可达：无法连接 ws://127.0.0.1:7789/rpc'  '重试'
```

### e) 二维码：用了真 zxing（不是假图案）

- 二维码内容 = mock 的「地址 + Token」拼成的 JSON（`MockData.pairingPayload`）：
  `{"lxcode":1,"name":"工作室主机","addr":"10.0.0.8:7789","token":"a1b2c3d4-e5f6-7890-abcd-ef1234567890"}`
- 配对页上的引擎标记 pill 显示 **`zxing`**（`QrImage.Engine.Zxing`）——真 zxing 路径成功。
- 生成方式：`com.google.zxing.qrcode.encoder.Encoder.encode(content, EC=M, UTF-8, margin=1)`
  取**自然模块矩阵**（29×29）。**不用 `QRCodeWriter` 的像素尺寸**：它会把二维码缩放到请求尺寸，
  29 模块塞进 33px 时模块被重采样合并 → 扫不出来（实测第一版就是这样，zxing-cpp 解不出）。
- **解码复核**（拿截图裁出二维码区域，用 `zxing-cpp` 反解）：

```
$ python  （裁 proto-pairing-1.png 的二维码区域，含静区，2× 放大）
format= QR Code
text  = {"lxcode":1,"name":"工作室主机","addr":"10.0.0.8:7789","token":"a1b2c3d4-e5f6-7890-abcd-ef1234567890"}
```

  反解出的文本与写入内容**逐字符一致** → 这张码是真二维码，不是图案。
- 假图案回落仍在（`QrCode.fallback`：内容 hash 做种 LCG + 三个定位角，确定性可复现），
  只在 zxing 拉不下来/运行时抛异常时启用；本次构建**未走回落**（所以没有「假图案」pill 的截图）。

### f) mock 数据 ↔ 真实协议字段对应

| mock（Kotlin） | 协议来源（Go） | 字段对应 |
|---|---|---|
| `BackendConn(id,name,addr,token,local)` | 桌面端 `shared/connections.tsx` 的 `RemoteConn{id,name,addr,token}` + 内置本机 `LOCAL_ADDR="127.0.0.1:7789"` | 逐字段同名；`local` 是本机卡标记（协议里本机不是实体） |
| `maskToken(t)` | 桌面端 `maskToken` | 保头尾各 4 位、中间 `••••`（逐字节一致） |
| `nameFromAddr(a)` / `addrValid(a)` | 桌面端 `RemoteForm.nameFromAddr` / 地址正则 `^(https?://)?[a-zA-Z0-9.-]+(:\d{1,5})(/.*)?$` | 正则与提示文案逐字符照搬 |
| `SessionMeta(id,title,updatedAt,messages,archived,workspace)` | `protocol.SessionMeta{ID,Title,UpdatedAt,Messages,Archived,Workspace}` | `updated_at` 在原型里直接存展示串「3 分钟前」；`running` 是 `busyBySession[session_id]` 的投影（不是 SessionMeta 字段） |
| `ProjectMeta(id,name,path)` | 桌面端 `ProjectMeta`（侧栏项目） | 同名 |
| `TodoItem(content,status)` | `tools.TodoItem{Content,Status}` | 同名；`status ∈ pending/active/done` |
| `ToolCall(id,name,arguments)` | `llm.ToolCall{ID,Type,Function{Name,Arguments}}` | 原型里扁平化（`name`/`arguments` 直接取 `function.*`） |
| `ContextUsage(used,window,system,tools,toolResults,messages,reasoning,estimated)` | `protocol.ContextUsage{Used,Window,System,Tools,ToolResults,Messages,Reasoning,Estimated}` | 同名；分类之和 == used（`5200+9800+12400+36000+5000 = 68400`） |
| `SessionStats(turns,steps,llmMs,toolMs,ttftMs,ttftSteps,decodeMs,decodeTokens,inputTokens,cacheReadTokens,cacheWriteTokens,outputTokens,legacyTokens)` | `protocol.SessionStats` 同名字段 | 生成速度口径 = `decodeTokens*1000/decodeMs` = `1726*1000/126000 = 13.7 tok/s`（与桌面端 `decodeTokensPerSec` 同式） |
| `ThreadBlock.User(text)` | `llm.Message{role:"user", content}` | — |
| `ThreadBlock.Assistant(content,reasoning,reasoningMs,model)` | `llm.Message{role:"assistant", content, reasoning_content}` | 轮末统计行对应 `Model` / `FirstTokenMs` |
| `ThreadBlock.Tool(name,title,argsSummary,command,cwd,output,running,isError,durationMs)` | `llm.Message{role:"tool"}` + `ToolCall` + `tool.*` 事件（`ToolResultParams{content,is_error}`） | `durationMs` ↔ tool 消息的 `DurationMs`（工具耗时，会话统计的「工具时间」按它折叠） |
| `ThreadBlock.Dispatch(agentName,task,conclusion,sessionId,done,isError)` | `chat.dispatchStart/End`（`agent_name` / `task` / `session_id`） | — |
| `ThreadBlock.Compaction(summary,shadowed,before,after,manual)` | `chat.compacted`（`summary`/`shadowed`/`before`/`after`/`manual`） | — |
| `ThreadBlock.Todo(items)` | `todo.updated` 事件的 `[]tools.TodoItem` | — |
| `ThreadBlock.Error(message)` | `chat.turnError`（`TurnErrorEvent`） | — |
| 确认门 `ConfirmRequest{SessionID,ID,Name,Arguments,Prompt}` | `protocol.ConfirmRequest` | 弹层的标题/命令块/拒绝/批准对应 ApprovalCard 的语义 |
| 顶栏药丸「本机/远程名」+ 在线态 | `Topbar.tsx` 的 `.conn-pill` + `.pulse-dot[data-off]` | 绿点 2.4s 呼吸 = 在线；灰点无动画 = 离线 |
| 输入区 `模型 / effort / 审批档` | `chat.send` 的 `model`（`ChatHistoryResult.Model`）/ `effort`（minimal/low/medium/high）/ `approval`（auto/confirm/strict） | 三档字面量与协议常量一致 |

---

## 3. 每个页面用了哪些 Lx 组件

| 页面 | 消费的 `:design` 组件 / token |
|---|---|
| **AppRoot**（导航根） | `LxTheme`、`Lx`/`LxColors`（token）、Material 图标（底栏图标） |
| **Common.kt**（共用件） | `LxIconButton`（返回/图标按钮）、`Lx`/`LxColors`（token）；`TerminalBlock`/`CodeBlock`/`ConnPill`/`MockSwitch` 等为本地组合件（`Column`+`Row`+`background`） |
| **连接页** | `LxButton`（ghost/secondary/small × 连接·断开·编辑·删除·取消·保存·重试）、`LxTextField`（表单三字段）、`Lx`/`LxColors`（token） |
| **连接表单页** | `LxButton`（ghost 取消 / primary 保存 + `enabled`）、`LxTextField`（地址/名称/Token） |
| **配对/扫码页** | `LxButton`（primary large 扫码配对 / 模拟扫到一张码）、`LxPill`（success/amber 引擎标记）、`Lx`/`LxColors`、`LocalClipboardManager`（复制按钮） |
| **扫码结果模拟页** | `LxButton`（primary large 模拟扫到一张码）、`LxColors`（`TermBg` 取景框） |
| **会话列表页** | `LxListItem`（导航项 / 项目行 / 会话行 / 设置行）、`LxTextField`（搜索框）、`LxSectionHeader`（「项目」「对话」分组标题）、`LxStatusDot`（会话行运行中态 `LxStatus.Run/Idle`）、`Lx`/`LxColors` |
| **对话线程页** | `LxConfirmDialog`（确认门）、`LxIconButton`（返回 / 确认门入口）、`LxStatusDot`（工具卡成功/运行/失败 + todo 两态）、`Lx`/`LxColors`/`LxType.Mono12`（终端块/代码块） |
| **输入区** | `LxTextField`（输入框）、`Lx`/`LxColors`（发送黑按钮、控件药丸、上下文环、统计胶囊） |

**未使用的 `:design` 组件**（如实记录，不是遗漏）：`LxSurface`/`LxCard`、`LxRow`、`LxDivider`/`LxVerticalDivider`、`LxBadge`、`LxEmptyState`、`LxSpinner`（`LxButton` 的 `loading` 内部会用）。
理由：桌面端的连接卡/消息块是**多行复合结构**（名字行 + mono 副行 + 动作行、气泡 + 输出块），
`LxCard`/`LxRow` 的单一插槽装不下，硬套会把行距/圆角压成与桌面端不一致；
空态与分隔线在原型里没有出现（列表都有数据、分组标题已由 `LxSectionHeader` 承担）。

---

## 4. 已知偏差与如实说明

1. **输入区一行在 360dp 上放不下 5 项**（桌面端 `Composer` 宽 720px）。处理：
   保持**不折行**（`horizontalScroll`，等价桌面端 `flex:none` + `nowrap`），拆两张截图（左半/右半）。
   不缩到 9sp 硬塞的理由：9sp 的中文标签在手机观看距离下几乎读不出来。
2. **子 Agent 派发卡「整行可点」**：原型里点了不换页（子会话内容与主会话同一份 mock 线程），
   只做 `clickable` + `↗` 视觉。不假装能进的理由：点了没反应的按钮比没有入口更糟（桌面端同款纪律）。
3. **确认门用 `Dialog`**（`LxConfirmDialog` 的实现），遮罩调光由平台给（Android 默认 0.6），
   桌面端是 `rgba(0,0,0,.22)`；`LxConfirmDialog` 的 KDoc 已如实记录这条偏差（本次不改 `:design`）。
4. **二维码 `margin=1`**：桌面端没有二维码实现，`margin` 是 zxing 的必填 hint；
   取 1（最小静区），页面外面再包一层白底卡片，视觉上静区更宽。
5. **云机 iFlytek 输入法**：`adb shell input text` 与字母 keyevent 在该 IME 下会被拼音组合吞掉，
   需 `keyevent <字母>... + ENTER` 才提交；本报告里表单校验态（`aaa`）与搜索过滤（`a`）都是这样输入的。
6. **连接列表里扫码新增的条目与既有「工作室主机」重名**：扫码 payload 用的就是配对页展示的凭证
   （地址 + Token 与 `10.0.0.8:7789` 相同），所以保存后列表里出现两条同名条目——这是「扫了一台已经在列表里的机器」的
   真实后果，不是 bug（`proto-scanform-2.png` 里可见第 5 条）。
7. **`SessionMeta.updatedAt` 在原型里直接存展示串**（「3 分钟前」）。协议里是 ISO 时间戳，
   真实客户端应做人性化换算——原型不做这层（与「此刻页面只画样子」的目标一致）。
