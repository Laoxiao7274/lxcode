# lxcode 安卓原型 · 动效体系补齐 —— 验收报告

> 目标：把桌面端（agent-console-v3）的 GSAP / CSS 动效规格**照数值复刻**成 `:design` 的动效层，
> 并接到 app 模块的 9 个原型页面上；同时给出可复现的动效取证（连拍/录屏 + 像素 diff）与门控对照。
>
> 纪律：不改页面结构 / 文案 / mock 数据内容；不改 `:design` 已有公开 API 签名（只增不改，增了什么见 §5）；
> 不引入新依赖（全部用 Compose 自带动画 API）。

---

## 1. 新增 / 修改文件树

### 新增（11 个）

```
android/
├── design/src/main/java/com/moyunteng/lxcode/design/motion/     ← 新包（动效层）
│   ├── LxMotionGate.kt      106 行  动效门控（LocalLxMotionEnabled + 读系统动画缩放 + 调试覆盖）
│   ├── LxAnimate.kt         344 行  入场规格/交错/退场/展开折叠/按压缩放/颜色·数值过渡
│   ├── LxTypewriter.kt      101 行  打字机（REVEAL_* 常量 + rememberStreamReveal）
│   ├── LxCaret.kt            47 行  光标闪烁（caret-blink 1s steps(1)）
│   ├── LxShimmer.kt          92 行  思考微光（think-shimmer 2.25s，background-size 300%）
│   └── LxSweep.kt            72 行  工具行扫光（tool-sweep 300px 带 2.6s）
└── tools/
    ├── motion_capture.sh           录屏（screenrecord）+ 抽帧（ffmpeg 30fps 网格）
    ├── motion_pick.py             按「首个变化帧 +0/+120/+400/+1000ms」选帧 + 落盘帧两两像素 diff
    ├── motion_region.py           指定区域的逐帧指标（墨迹量 / 包围盒 / 扫光列 / 变化列）
    ├── motion_run.sh              场景驱动：把页面准备好 → 录屏触发 → 抽帧 → 选帧
    └── motion_burst.sh            连拍（adb exec-out screencap 快速循环 4 张，打印每张相对触发的时刻）
```

### 修改（14 个）

| 文件 | 改动 |
|---|---|
| `design/.../token/LxMotion.kt` | **只增**：22 个时长/曲线/spring 常量（§2 对照表） |
| `design/.../component/LxStatusDot.kt` | 接门控（关掉时呼吸停）；呼吸改成「单程 800ms」= 整轮 1.6s（原来 RepeatMode.Reverse + 1600ms 单程 = 3.2s 整轮，与桌面端 job-pulse 1.6s 不符） |
| `design/.../component/LxSpinner.kt` | 接门控；**只增**可选参数 `periodMillis` / `easing`（默认 = 原行为 960ms 标准曲线，不破坏观感） |
| `design/.../component/LxButton.kt` | 内部：按压缩放/透明度改 120ms 过渡（原来是瞬变） |
| `design/.../component/LxListItem.kt` | 内部：hover/按压填充改 120ms 过渡 + 按压缩放 |
| `design/.../component/LxIconButton.kt` | 内部：同上 |
| `design/.../component/LxConfirmDialog.kt` | 遮罩自绘 `rgba(0,0,0,.22)` 纯淡入 160ms（平台 dimAmount 置 0）+ 卡片 `ap-card-in` 380ms |
| `app/.../ui/AppRoot.kt` | `LxMotionGate` 包全树；页面切换 `page-in` 200ms；底部导航按压缩放 + 选中色 120ms 过渡；`MockAppState.motionOn` |
| `app/.../ui/Common.kt` | `ConnPill`：接门控 + 圆点颜色 160ms 过渡；`MockSwitch`：120ms 轨道色 + 按压缩放 |
| `app/.../ui/SessionsScreen.kt` | 导航项/搜索框/项目行/会话行**交错入场**（`staggerIn`） |
| `app/.../ui/ThreadScreen.kt` | 消息块逐个入场 `block-in`；用户气泡回弹；思考微光；展开折叠 220ms；工具行扫光；打字机 + 光标；todo 条目入场 |
| `app/.../ui/ConnectionsScreen.kt` | 卡片交错入场；表单/添加弹层 `pop-in`；「当前」回弹；断线错误条入场滑入 + 退场 `LxCollapseAway`；「动效」调试开关 |
| `app/.../ui/PairingScreen.kt` | 二维码区块 `settings-in`；扫码结果卡 `pop-in`；复制按钮按压缩放 |
| `app/.../ui/Composer.kt` | 控件行 `pop-in`；发送/停止按压缩放；上下文环 220ms 数值过渡 |

---

## 2. 动效常量 ↔ 桌面端 keyframes 对照表

`LxMotion`（唯一标准曲线恒为 `cubic-bezier(0.22,1,0.36,1)`；退场另用 power2.in 型曲线）：

| 安卓端 | 桌面端 keyframes / 规则 | 时长 | 曲线 / 数值 |
|---|---|---|---|
| `LxMotion.DurationStandard` | 标准过渡（border-color/box-shadow） | 120ms | 标准曲线 |
| `LxMotion.DurationMenu` | 会话菜单淡入 | 140ms | 标准曲线 |
| `LxMotion.DurationPopIn` / `DurationMedium` | `pop-in`、`settings-mask-in`、composer transform | 160ms | 标准曲线 |
| `LxMotion.DurationPageIn` | `mp-slide-in`、页面切换 | 200ms | 标准曲线 |
| `LxMotion.DurationExit` / `DurationLong` | `collapseAway` 退场、`settings-in` 设置面板 | 220ms | 标准曲线（退场用 `EasingExit`） |
| `LxMotion.DurationToast` | toast | 240ms | 标准曲线 |
| `LxMotion.DurationBlockIn` | `block-in` 消息块入场 | 320ms | 标准曲线 |
| `LxMotion.DurationApCardIn` | `ap-card-in` 确认卡入场 | 380ms | 标准曲线 |
| `LxMotion.DurationMarkdown` | markdown 段落 / 思考句 | 420ms | 标准曲线 |
| `LxMotion.DurationSpinnerLarge` | 连接 spinner 一圈 | 800ms | 线性（匀速） |
| `LxMotion.DurationSpinnerSmall` | 小 spinner 一圈 | 700ms | 线性（匀速） |
| `LxMotion.DurationStaggerItem` | `staggerIn` 单条目 | 300ms | 标准曲线 |
| `LxMotion.StaggerStepMaxMillis` | `staggerIn` 步长上限 `min(40, 360/n)` | 40ms | — |
| `LxMotion.StaggerTotalMaxMillis` | `staggerIn` 总时长上限 | 360ms | — |
| `LxMotion.DurationPulse` | `job-pulse` 呼吸（整轮） | 1600ms | 标准曲线（近似 ease-in-out） |
| `LxMotion.DurationCaret` | `caret-blink` | 1000ms | `steps(1)`（硬切） |
| `LxMotion.DurationShimmer` | `think-shimmer` | 2250ms | 线性，`background-size: 300%` |
| `LxMotion.DurationToolSweep` | `tool-sweep` | 2600ms | 标准曲线（≈ ease-out） |
| `LxMotion.Easing` | 全仓唯一标准曲线 | — | `cubic-bezier(.22,1,.36,1)` |
| `LxMotion.EasingExit` | 退场（GSAP `power2.in`） | — | `cubic-bezier(.4,0,1,1)` |
| `LxMotion.EasingLinear` | 循环动画 | — | `linear` |
| `LxMotion.SpringDampingRatio` / `SpringStiffness` | `back.out(1.8)` / `back.out(2)` | — | spring(dampingRatio 0.55, stiffness 500) |
| `LxMotion.PressedScale` | `:active { transform: scale(.98) }` | — | 0.98 |
| `LxReveal.MinCps` / `MaxCps` / `PourSeconds` / `MaxLagChars` | `REVEAL_MIN_CPS=30` / `REVEAL_MAX_CPS=1000` / `REVEAL_POUR_SECONDS=0.35` / `REVEAL_MAX_LAG=4000` | — | 与桌面端逐字同值 |

`LxEnterSpec`（入场规格，每条都标注桌面端 keyframes 名）：

| 规格 | 桌面端 keyframes | 时长 | 位移 / 缩放 |
|---|---|---|---|
| `BlockIn` | `block-in` | 320ms | translateY(4px)→0 + 淡入 |
| `ApCardIn` | `ap-card-in` | 380ms | translateY(8px)→0 + 淡入 |
| `PopIn` | `pop-in` | 160ms | translateY(-4px)→0 + scale(.98)→无变换 |
| `MpSlideIn` | `mp-slide-in` | 200ms | translateX(-10px) + 淡入 |
| `SettingsIn` | `settings-in` | 220ms | scale(.97) + 淡入 |
| `SettingsMaskIn` | `settings-mask-in` | 160ms | 纯淡入 |
| `PageIn` | 规格未给 keyframes 名（桌面端侧栏切路由无页面动画） | 200ms | translateY(6px)（取 4dp/8dp 之间） |
| `StaggerItem` | `staggerIn` 单条目 | 300ms | translateY(8px) + 淡入 |
| `TodoItemIn` | todo 条目（规格给「delay = i*50ms，y-7dp，360ms」） | 360ms | translateY(-7px) + 淡入 |
| `BubbleIn` | 用户气泡 / 裁决徽标 `back.out(1.8)` | spring | dampingRatio 0.55 + stiffness 500 |
| `ErrorBarIn` | 规格未给 keyframes 名（`.ag-err-bar` 行内出现） | 220ms | translateY(8px) + 淡入 |
| `LxCollapseAway` | `collapseAway` | 220ms | 高度/内距/外距归零 + 淡出，`EasingExit`（入场可传 `enter` 覆盖：错误条传 `None`，入场交给 `ErrorBarIn`） |
| `LxExpandable` | 思考块展开/折叠 | 220ms | 高度 + 淡入淡出，标准曲线 |

---

## 3. 每个页面接了哪些动效

### 3.1 AppRoot（全局）
- `LxMotionGate(override = motionOn ? null : false)` 包住全树 → 全应用唯一门控入口；
- 页面切换：`lxEnter(PageIn, key = route)`（200ms 淡入 + y+6dp）；
- 底部导航：每个 tab `lxPressScale`（120ms 按压缩放）+ 选中/未选中色 `lxAnimateColor` 120ms 过渡。

### 3.2 SessionsScreen（会话列表页）
- 导航项 6 项：`lxStaggerEnter(count = 6, index = i)` → 300ms + y+8dp，延迟 `min(40ms, 360/6)*i`；
- 搜索框：`lxEnter(StaggerItem, delay = min(40, 360/7)*6 = 240ms)`（接在导航项之后）；
- 项目行 3 行（lxcode / 官网站点 / 未分组）：`lxStaggerEnter(count = 3)`；
- 会话行：`lxStaggerEnter(count = sessions.size)`（搜索过滤后的条数）。

### 3.3 ThreadScreen（对话线程页）
- 12 个消息块：`lxEnter(BlockIn, delay = min(40, 360/12)*i = 30ms*i)` → 320ms + y+4dp 逐个入场（工具行/派发卡/压缩块/错误块同款）；
- 用户气泡：额外 `lxEnter(BubbleIn)`（spring，回弹）；
- 思考块：「思考」标签 `LxShimmerText`（2.25s 微光，运行中）+ 展开/折叠 `LxExpandable`（220ms 标准曲线高度动画）+ 按压缩放；
- 「运行中」工具行：`lxSweep(300dp 带, 2.6s)` 扫光；
- 助手正文：`rememberStreamReveal`（打字机逐字揭示，`LxReveal` 常量）+ 揭示期间 `lxCaret()` 光标（1s steps(1)）；
- todo 条目：`lxEnter(TodoItemIn, delay = i*50ms)` → 360ms + y-7dp；
- 运行中状态点：`LxStatusDot(Run)` 呼吸 1.6s（整轮）。

### 3.4 ConnectionsScreen（连接页 + 连接表单页）
- 连接卡（本机 + 3 远程）：`lxStaggerEnter(count = conns.size + 1)`；
- 「+ 添加远程后端」：`lxEnter(PopIn)` + 接在卡片后 + `lxPressScale`；
- 「当前」标签：`lxEnter(BubbleIn)`（回弹）；
- 断线错误条：`lxEnter(ErrorBarIn)`（220ms + y+8dp）+ 「重试」按钮内部按压缩放；
- 删除按钮：`lxPressScale`；
- 连接表单页（添加/编辑远程后端）：`lxEnter(PopIn, key = draft.id)`；
- 顶栏新增「动效」调试开关（`MockSwitch`：120ms 轨道色 + 按压缩放）。

### 3.5 PairingScreen（配对/扫码页 + 扫码结果页）
- 二维码显示区：`lxEnter(SettingsIn)`（220ms + scale .97）；
- 凭证卡「复制」按钮：`lxPressScale`；
- 扫码结果页「扫到的码」卡：`lxEnter(PopIn)`。

### 3.6 Composer（输入区）
- 控件行（模型/effort/审批档/上下文环/统计胶囊）：`lxEnter(PopIn)`（160ms + y-4dp）；
- 发送/停止按钮：`lxPressScale`（120ms）；
- 上下文占用环：`lxAnimateFloat(target)` 220ms 数值过渡（百分比不瞬变）。

### 3.7 断线态（ConnPill + 错误条）
- `ConnPill` 圆点：在线 2.4s 呼吸（整轮）+ 状态切换时圆点颜色 160ms 过渡 + 门控；
- 断线错误条：`lxEnter(ErrorBarIn)` 滑入 220ms。

---

## 4. 动效真实存在的证据（连拍 / 录屏 + 像素 diff）

**取证方法**（`tools/` 三个脚本，全部可复现）：
1. `motion_run.sh <scene>`：重启应用 → 把页面准备到「动作发生前」→ 开始 `screenrecord` → 执行触发动作 → 停止录屏 → 抽帧；
2. 抽帧：`ffmpeg -vf fps=30`（统一 30fps 网格，源帧率约 23~30fps，重复帧如实保留）；
3. 选帧：`motion_pick.py <scene> pick <动作起点ms>` → 取「首个变化帧 +0 / +120 / +400 / +1000ms」四帧落盘为 `motion-<场景>-<序号>.png`；
4. 连拍：`motion_burst.sh <scene> <触发命令>` → 触发后立刻用 `adb exec-out screencap` 连拍 4 张（这台云机单张约 360ms，四张覆盖约 1.5s）。

> 「动作起点」= 录屏里**画面首次出现变化的那一帧**（`input tap` 的设备端延迟约 0.2~0.4s，所以不是命令发出的时刻）；
> 每张落盘帧都打印它相对动作起点的实际偏移（下表括号内）。

### 4.1 五个必需场景（动效开启）

| 场景 | 录屏 | 变化窗口（帧间 diff>0.30） | 四帧落盘时刻（相对动作起点） | 墨迹量（亮度<200 像素数） | 与第 1 张的平均像素差 |
|---|---|---|---|---|---|
| **会话列表交错入场** | `rec-sessions.mp4` | 1.833 ~ 2.300s（0.47s，峰值 5.03） | +0 / +134 / +400 / +1000ms | 34784 → 13935 → 26336 → 27476 | — / **6.509 / 9.591 / 9.811** |
| **线程消息块入场** | `rec-thread.mp4` | 1.867 ~ 2.400s（峰值 5.61）+ 2.667~2.700 | +0 / +133 / +400 / +1000ms | 27610 → 9127 → 43055 → 46280 | — / **7.169 / 15.549 / 15.828** |
| **连接页卡片交错入场** | `rec-conn.mp4` | 1.767 ~ 2.200s（峰值 4.93） | +0 / +133 / +400 / +600ms | 27611 → 9215 → 35095 → 34920 | — / **5.774 / 9.661 / 9.777** |
| **确认门弹层入场** | `rec-confirm.mp4` | 1.733 ~ 2.033s（峰值 25.20） | +0 / +134 / +400 / +1000ms | 47468 → 235227 → 699464 → 699462 | — / **52.627 / 58.416 / 58.418** |
| **配对页二维码区块入场** | `rec-pairing.mp4` | 1.733 ~ 2.033s（峰值 21.26） | +0 / +134 / +334 / +334ms | 27732 → 5065 → 152819 → 152819 | — / **8.540 / 40.806** |

> 说明：`+1000ms` 帧在部分场景与最后一帧重合，是因为 `screenrecord` **画面不再变化就不再写帧**，录屏长度=画面变化时长（例如 conn 只录到 2.0s）。偏移一栏如实打印实际值。

**确认门遮罩单独量化**（左上角 x0-60 / y300-400 区域平均亮度，桌面端遮罩 = `rgba(0,0,0,.22)`）：

| 帧时刻 | 1.70s | 1.87s | 1.90s | 1.93s | 1.97s | 2.00s | 2.67s（终态） |
|---|---|---|---|---|---|---|---|
| 遮罩区均值 | 247.9 | 247.9 | 223.6 | 209.1 | 198.3 | **193.5** | 193.4 |

→ 遮罩在 **1.87s→2.00s（约 160ms）** 内从白（255）淡到 193.4 ≈ 0.22 黑遮罩，正是 `settings-mask-in` 的 160ms。

### 4.2 打字机逐字揭示（同一段文字长度递增）

用「关掉原型动效开关」的线程页（`off-thread`）隔离：此时**只有打字机在跑**（入场动画已瞬变）。

**助手正文区（x24-700 / y400-700）墨迹量逐帧递增**（`motion_region.py off-thread 24,400,700,700`）：

| 帧时刻 | 1.83s | 1.87s | 1.93s | 1.97s | 2.03s | 2.10s | 2.20s | 2.57s | 3.13s（终态） |
|---|---|---|---|---|---|---|---|---|---|
| 区域墨迹 | 4562 | 8245 | 10398 | 11998 | 12600 | 14696 | 15811 | 17368 | **20909** |

→ 文字**逐字长出来**：末行的「墨迹最右列 x」也逐帧右移（60 → 77 → 134 → 162 → 215 → 281 后稳定），说明是同一段文字在变长，不是整块淡入。

**同一场景的连拍 4 张**（`motion-off-thread-burst-1..4.png`，触发后 +322 / +703 / +1023 / +1382ms）：

| 连拍序号 | 1 | 2 | 3 | 4 |
|---|---|---|---|---|
| 助手正文区墨迹 | 4551 | 11889 | 12967 | 16659 |
| 与上一张平均像素差 | — | **15.796** | **16.549** | **5.815** |

→ 四张连拍里同一段文字的墨迹量 **4551 → 16659（3.7 倍）**，逐张递增。

**时长自证**：145 字的助手块实测揭示时长约 **1.26s**，与桌面端算法一致 —— `cps = 积压/0.35` 夹在 30~1000，是**指数浇注**（时间常数 0.35s）：`0.35×ln(145/10.5)+0.35 ≈ 1.27s`。不是「固定 0.35s 总时长」，与桌面端逐行同算法。

### 4.3 运行中扫光 / 呼吸（连续循环，`rec-loops.mp4`）

**扫光**：取「运行中」工具行区域（x24-700 / y540-592），逐帧找**帧间变化最大的列 x**：

| 帧时刻 | 0.07s | 0.30s | 0.57s | 0.97s | 1.20s | 1.43s | 1.57s | 2.37s |
|---|---|---|---|---|---|---|---|---|
| 变化最大列 x | 245 | 460 | 587 | 620 | 649 | 666 | 670 | 16（回绕） |

→ 扫光带从左扫到右、到右端回绕，周期约 **2.3~2.6s**（对齐 `tool-sweep` 2.6s）。

**呼吸**：取「运行中」状态点核心区（x26-38 / y548-562）平均 B 通道：

| 帧时刻 | 0.33s | 1.13s | 1.93s | 2.77s | 3.57s |
|---|---|---|---|---|---|
| B 通道 | **138.9（最饱和 = 透明度 1）** | 210.9 | **144.5** | 210.8 | **141.1** |

→ 极小值出现在 0.33s / 1.93s / 3.57s，间隔 **1.6s**，正是 `job-pulse 1.6s` 的一轮。

**连拍对照**（`motion-loops-1..4.png`）：四张的全局平均像素差只有 0.003~0.021（扫光/呼吸是局部小面积变化，全局均值本来就不敏感），所以上面改用**区域指标**证明 —— 这就是为什么取证要落到区域上。

### 4.4 其它场景

| 场景 | 录屏 | 变化窗口 | 四帧墨迹量 | 与第 1 张差 |
|---|---|---|---|---|
| 断线错误条**入场**（滑入 220ms） | `rec-offline.mp4` | 1.600 ~ 1.800s（峰值 7.85） | 34843 → 32035 → 32623 → 32623 | 8.807 / 9.224 |
| 断线错误条**退场**（`collapseAway` 220ms） | `rec-offline2.mp4` | **2.900 ~ 3.100s（0.2s）** | 34277 → 35185 → 34793 | 8.640 / 9.008 |
| 会话列表（门控开）连拍对照 | `motion-sessions-burst-1..4.png` | 触发后 +337 / +692 / +1073 / +1428ms | — | **5.468 / 9.478 / 9.495 / 4.869 / 4.890**（两两） |

---

## 5. 门控验证（reduced-motion 对照）

门控语义（对齐桌面端 `motionAllowed()`）：入场**直接落终态（不播）**、循环动画**停**、退场**直接完成**；
打字机**不受门控**（桌面端注释明说 reduced-motion 不关闭它）。

### 5.1 原型「动效」开关关掉（连接页顶栏 → 动效）

| 场景 | 录屏变化窗口 | 连拍 4 张两两平均像素差 |
|---|---|---|
| **会话列表入场**（`off-sessions`） | **1.833 ~ 1.867s（只有 1 帧边界）** | 第 2/3/4 张：**0.0000 / 0.0000 / 0.0000**（最大单像素差 0，不同像素 0） |
| **确认门弹层**（`off-confirm`） | 1.733 ~ 1.933s | 第 2/3/4 张：**0.0000 / 0.0000 / 0.0000** |
| **扫光 + 呼吸**（`off-loops`） | **录屏只有 1 帧**（画面完全不变） | 四张两两：**0.0000（全 0，不同像素 0）** |
| **线程页入场**（`off-thread`） | 1.833~2.033（只剩打字机） | 打字机照旧（见 §4.2，墨迹 4551→16659） |

对照（同一动作、同一连拍方法，只改门控开关）：

| 会话列表入场 | 第 1 vs 2 | 第 1 vs 3 | 第 1 vs 4 |
|---|---|---|---|
| **动效开** | 5.468 | 9.478 | 9.495 |
| **动效关** | **0.0000** | **0.0000** | **0.0000** |

→ 关掉门控后连拍**像素完全一致（平均差 0.0000、最大单像素差 0、不同像素数 0）**，就是「静态到终态」。

### 5.2 系统动画缩放 = 0

`adb shell settings put global animator_duration_scale 0`（原值 `null`，测完已 `settings delete` 还原）：

| 场景 | 录屏变化窗口 | 连拍 4 张两两平均像素差 |
|---|---|---|
| **会话列表入场**（`sys-off-sessions`） | **1.833 ~ 1.867s（只有 1 帧边界，峰值 9.58）** | 四张两两：**0.0000（最大单像素差 0，不同像素 0）** |

→ 系统动画缩放为 0 时，`LxMotionGate` 读到 0 → 走静态路径，与关掉原型开关一致。

### 5.3 如实记录的偏差（不藏）

1. **确认门遮罩**：桌面端是 `rgba(0,0,0,.22)`；本库用 `Dialog` 实现时平台默认调光 0.6，本次改为**首帧把 `window.setDimAmount(0f)` 置零、遮罩自绘**（§4.1 的 193.4 ≈ 255×(1−0.242) 已对齐 0.22）。**但**原型开关关掉时，平台 dialog **窗口自身**的出现动画（约 200ms，受系统 `animator_duration_scale` 控制，不归本库管）仍会播；只有把系统动画缩放也置 0 才彻底静态。
2. **扫光/呼吸曲线的形状**：桌面端 `tool-sweep` 是 `ease-out`、`job-pulse` 是 `ease-in-out`；本库统一用全仓唯一标准曲线 `cubic-bezier(.22,1,.36,1)`（= ease-out 族）近似，不引入第二条曲线。
3. **`LxSpinner` 的 800ms / 700ms**：原型页面里没有加载态位点，所以只把两个周期常量与可选参数加进组件（默认仍是原观感 960ms 标准曲线），没有为了用而硬塞到页面上。
4. **页面切换 / 断线错误条**：桌面端没有对应的命名 keyframes（桌面端是侧栏切路由、错误条是行内出现），数值取自任务规格（200ms + 6dp、220ms + 8dp），KDoc 里已注明「规格未给 keyframes 名」。
5. **`LxSweep` 高光颜色**：规格只给几何与时长（300px 带、2.6s、ease-out），颜色取 token 派生（`--fg` 10% 透明度）。
6. **打字机在长文本下不是固定 0.35s**：桌面端 `REVEAL_POUR_SECONDS` 是**指数浇注的时间常数**，145 字实测约 1.26s（与桌面端算法一致），不是「0.35s 播完」。

---

## 6. 构建 / 运行 / 验收

| 验收项 | 结果 | 证据 |
|---|---|---|
| a) `gradle assembleDebug` | **BUILD SUCCESSFUL，0 error**（`grep -c "^e: " build-motion-final.log` = **0**；只剩 3 条既有 `Icons.Filled.KeyboardArrowLeft/Right` 弃用 warning，非本次引入） | `build-motion-final.log` |
| b) 安装启动 | `adb -s 10.10.5.202:30900 install -r` → Success；`am start` → `pidof` = **31821** | 终端输出 |
| b) logcat | `logcat -d \| grep -ci 'FATAL\|AndroidRuntime'` = **0** | 终端输出 |
| c) 动效真实存在 | §4.1~4.4：五场景四帧 + 区域指标（墨迹量/包围盒/扫光列/呼吸 B 通道）+ 打字机连拍 | `motion-*.png`、`rec-*.mp4`、`frames-*/` |
| d) 门控验证 | §5.1~5.2：连拍像素差 **0.0000 / 最大 0 / 不同像素 0**，与开启动效时的 5.5~9.5 对照 | `motion-off-*.png`、`motion-sys-off-sessions-*.png` |
| e) 报告 | 本节（文件树 / 常量对照表 / 每页动效清单） | `MOTION-REPORT.md` |

### 证据文件清单（`android/` 下）

```
motion-sessions-1..4.png        会话列表交错入场（录屏四帧）
motion-thread-1..4.png           线程消息块入场 + 打字机
motion-conn-1..4.png             连接页卡片交错入场
motion-confirm-1..4.png          确认门弹层入场
motion-pairing-1..4.png           配对页二维码区块入场
motion-loops-1..4.png            运行中扫光 / 呼吸（连续循环）
motion-offline-1..4.png           断线错误条入场（滑入 220ms）
motion-offline2-1..4.png          断线错误条退场（collapseAway 220ms）
motion-sessions-burst-1..4.png     会话列表入场 · 连拍（动效开，四张互不相同）
motion-off-sessions-1..4.png       会话列表入场 · 连拍（动效关，第 2/3/4 张完全一致）
motion-off-thread-1..4.png         线程页（动效关，只剩打字机）
motion-off-thread-burst-1..4.png    打字机 · 连拍（墨迹 4551→16659）
motion-off-confirm-1..4.png        确认门（动效关，第 2/3/4 张完全一致）
motion-off-loops-1..4.png          扫光/呼吸（动效关，四张完全一致）
motion-sys-off-sessions-1..4.png   系统动画缩放=0（四张完全一致）
rec-*.mp4 / frames-*/                原始录屏与抽帧（可复现中间产物）
```

---

## 7. 复现命令

```bash
cd C:/Users/xzy/Desktop/my/lxcode/android

# 构建 + 装机
C:/Users/xzy/.gradle/wrapper/dists/gradle-8.11.1-bin/*/gradle-8.11.1/bin/gradle.bat --offline assembleDebug
adb -s 10.10.5.202:30900 install -r app/build/outputs/apk/debug/app-debug.apk

# 场景取证（动效开）：sessions / conn / thread / confirm / pairing / loops / offline
bash tools/motion_run.sh sessions      # 打印变化窗口；再 pick 落盘四帧
python tools/motion_pick.py sessions pick 1833
python tools/motion_region.py loops 24,540,700,592   # 扫光：变化最大列 x 逐帧右移
python tools/motion_region.py off-thread 24,400,700,700  # 打字机：区域墨迹逐帧递增

# 门控取证（原型开关关）：off-sessions / off-thread / off-confirm / off-loops
bash tools/motion_run.sh off-sessions
bash tools/motion_burst.sh off-sessions "adb -s 10.10.5.202:30900 shell 'input tap 90 1253'" 0

# 门控取证（系统动画缩放 = 0）
adb -s 10.10.5.202:30900 shell settings put global animator_duration_scale 0
bash tools/motion_run.sh sys-off-sessions
adb -s 10.10.5.202:30900 shell settings delete global animator_duration_scale   # 还原为 null
```
