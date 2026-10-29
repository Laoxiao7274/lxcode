# LOGO 派生链（防再次漂移）

> 2026-12 定案。此前各端 LOGO 各画各的占位（前端 topbar 文本「L」、安卓矢量
> foreground 手绘「L」、安卓应用内 Text("L")、site「LX」色块、favicon.svg 独立
> 字标），用户报「不是我给你的」——本次全部替换为从正式母版派生的位图，并以脚本
> 钉住派生链，防止再次漂移。

## 唯一母版

`shell/build/icon.png`（512×512 RGBA，浅灰圆角瓷砖 + 近黑 LX 笔画 + 「code」字标）。
**没有 SVG 源**（向用户要过，尚未提供）——所有下游 LOGO 只能从这张 PNG 派生。

## 唯一入口：`node scripts/gen-app-icons.cjs`

零依赖（node 内置 zlib，手写 PNG 解码/编码 + 面积平均降采样，缩放代码与
`gen-tray-icon.cjs` 同款管线）。幂等，重跑覆盖。产物：

| 产物 | 尺寸 | 用途 |
|---|---|---|
| `frontend/public/favicon-32.png` | 32 | 前端 favicon + Topbar 品牌位 |
| `android/.../res/mipmap-{m,h,xh,xxh,xxxh}dpi/ic_launcher.png` `ic_launcher_round.png` | 48/72/96/144/192 | legacy 启动图标 |
| `android/.../res/mipmap-*/ic_launcher_foreground.png` `ic_launcher_background.png` | 108/162/216/324/432（108dp 三密度×2） | adaptive 图标前景/背景 |
| `android/.../res/drawable-nodpi/ic_logo.png` | 192 | 安卓应用内品牌位（SessionsScreen 顶栏 20dp / 引导页 56dp） |
| `site/public/logo.png` | 256 | 官网导航/页脚品牌位 |
| `site/public/favicon-32.png` + `favicon.ico` | 32 | 官网 favicon |

## 结构决策

- **adaptive icon 走「PNG drawable 引用」**：`<adaptive-icon><background
  android:drawable="@mipmap/ic_launcher_background"/><foreground
  android:drawable="@mipmap/ic_launcher_foreground"/></adaptive-icon>`（AOSP 模板
  惯例，mipmap 与 drawable 都是合法 drawable 资源；密度分档放 mipmap 符合「launcher
  图标资源进 mipmap」惯例）。旧的矢量 foreground（`drawable/ic_launcher_foreground.xml`）
  与 `values/ic_launcher_background.xml` 已删除。
- **前景 = 母版整图居中缩进 66/108 ≈ 61% 安全区**（接受遮罩四角留白，最简单可靠）；
  背景 = 母版瓷砖纯色铺满（颜色取自母版中心像素）——遮罩外露部分与瓷砖同色，视觉无缝。
- **32px 级小图（favicon）去掉「code」字标**（32px 下字标不足 2px 高，糊成噪点）；
  去字标的几何与 tray 链共用 `WORDMARK` 常量（512 源图实测区域）——**该常量在
  `gen-tray-icon.cjs` 与 `gen-app-icons.cjs` 各持一份，改母版版式时两份一起改**。
- 大尺寸（≥192px 应用内/官网 LOGO）保留「code」字标。

## 引用位置（改版式时对照）

- 前端：`frontend/index.html`（link rel=icon）、`Topbar.tsx` + `styles/topbar.css`（brandMark img 20px）
- 安卓：`SessionsScreen.kt` 两处 `painterResource(R.drawable.ic_logo)`、
  `mipmap-anydpi-v26/ic_launcher{,_round}.xml`
- 官网：`site/index.html`（favicon 链接）、`App.tsx` 两处 brand-mark img、
  `styles/chrome.css`（brand-mark 样式）
- 打包：`scripts/build.mjs` 在壳打包前重生托盘 ico 与本派生链（源图换了必须同步）

## 兄弟链（不在本脚本内，别混淆）

- `scripts/gen-tray-icon.cjs` → `shell/build/tray.ico`（托盘专用：多尺寸 + 笔画加粗
  配方，见该文件头注的撕裂根因分析）；本脚本不碰它。
- `site/public/scripts/gen-site-favicon.cjs` → 旧「lx」字标 favicon.svg → favicon.ico
  的链路**已废弃但脚本保留**：它的产物与正式 LOGO 不同源，**不要再跑**（重跑会用
  旧字标覆盖本派生链生成的 favicon.ico）；favicon.ico 现由 `gen-app-icons.cjs` 生成
  （正式 LOGO 的 32px DIB），与 favicon-32.png 同源。

## 遗留

母版只有 PNG 位图、无矢量源。若用户日后提供 SVG：把 `gen-app-icons.cjs` /
`gen-tray-icon.cjs` 的源图换成 SVG 栅格化产物，或重写派生入口——引用点无需动。
