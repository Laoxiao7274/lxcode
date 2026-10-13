# Release 签名说明（安卓）

## 现状

- `lxcode-release.keystore`（本目录，**不入库**，gitignore 掉了）
- `keystore.properties`（本目录，**不入库**，含 keystore 密码）
- `app/build.gradle.kts` 的 `signingConfigs.release` 从 `keystore.properties` 读凭据；
  文件缺失时 release 构建退回 debug 签名并打 warning（仅限本地，**不得分发这种包**）。

## 出包

```bash
cd android
# JAVA_HOME 用 Android Studio 自带 JBR（GraalVM 不行）
JAVA_HOME="C:\Program Files\Android\Android Studio\jbr" \
  gradle assembleRelease
# 产物：app/build/outputs/apk/release/app-release.apk
```

> 本机没有 `gradlew` 包装脚本（仓库只有 wrapper 配置），用 PATH 里的 gradle 或
> `~/.gradle/wrapper/dists/gradle-8.11.1-bin/<hash>/gradle-8.11.1/bin/gradle.bat`。

## 发新 APK 的完整流程（含应用内更新闭环）

应用内更新检查（0.2.4 起）读站点上的 `downloads/android-release.json`，
**版本号判定用 versionCode 数字比较**。所以发版 =「传新 APK + 更新这份 JSON」，
老版本 app 下次启动就会弹横幅、连接页「检查更新」也能看到：

1. **改版本号**：`android/app/build.gradle.kts` 的 `versionCode`（**必须 +1**，更新判定就靠它）
   与 `versionName`；
2. **出包**：上面的 `assembleRelease`；记下产物的字节数与 sha256：
   ```bash
   ls -l android/app/build/outputs/apk/release/app-release.apk   # size
   sha256sum android/app/build/outputs/apk/release/app-release.apk
   ```
3. **更新版本元数据**（唯一版本源）：`site/public/downloads/android-release.json` ——
   version / versionCode / name / size / sha256 / url / notes（更新说明，横幅与弹层显示第一行）；
4. **重建站点**：`cd site && npm run build`（下载页 import 同一份 JSON，自动同步；
   产物 dist/downloads/android-release.json 与新资产哈希一起生成）；
5. **上传服务器**（`site-backend/scripts/remote.py`，paramiko）：
   ```bash
   # 先备份线上 dist（沿用 dist.bak-<日期> 命名）
   python site-backend/scripts/remote.py run 'cp -r /home/linaro/lxcode-site/dist /home/linaro/lxcode-site/dist.bak-$(date +%Y%m%d-%H%M)'
   # 上传 APK + 元数据（新 APK 直接从安卓构建产物传）
   python site-backend/scripts/remote.py put android/app/build/outputs/apk/release/app-release.apk \
     /home/linaro/lxcode-site/dist/downloads/lxcode-<版本>-android.apk
   python site-backend/scripts/remote.py put site/public/downloads/android-release.json \
     /home/linaro/lxcode-site/dist/downloads/android-release.json
   # 上传重建后的站点（下载页）
   python site-backend/scripts/remote.py putdir site/dist /home/linaro/lxcode-site/dist
   ```
6. **验证**：
   ```bash
   curl -sS https://api.r96314722.nyat.app:15714/site/downloads/android-release.json
   curl -sSI https://api.r96314722.nyat.app:15714/site/downloads/lxcode-<版本>-android.apk
   ```
   老版本 app（versionCode 更小）下次启动自动弹「发现新版本」横幅；连接页「检查更新」
   点一下 → 弹层给版本/大小/说明 →「去下载」跳系统浏览器下载 APK。

### 更新语义与边界

- **不做应用内静默安装**：那需要 `REQUEST_INSTALL_PACKAGES` 权限 + 引导用户开
  「未知来源」，且远控壳没有商店渠道——明确不做。下载动作交给系统浏览器
  （ACTION_VIEW 打开 APK 地址，下载完成后的安装提示由浏览器/系统接管）。
- 应用内**不下载、不校验** APK 文件本身：sha256 展示在下载页供人工核对，
  链路信任点在 HTTPS + 站点后端（与桌面 manifest 同一条公网入口）。
- 查询失败（无网/超时/格式错）：自动检查静默、手动检查 toast 人话原因——更新检查
  永远不打断主流程。
- `android-release.json` 由站点后端静态托管（与 APK 同目录），**不需要改后端代码**；
  元数据更新 = 传文件，即时生效。


## ⚠️ keystore 保管（重要）

keystore 一旦丢失，无法再发同签名的更新包（用户必须卸载重装，数据全丢）。
请把以下两样**离线备份**（密码管理器 / 加密盘）：

- `android/lxcode-release.keystore` 整个文件
- `android/keystore.properties`（含 storePassword / keyPassword）

## keystore 丢失后重新生成

```bash
keytool -genkeypair -v \
  -keystore lxcode-release.keystore \
  -alias lxcode -keyalg RSA -keysize 2048 -validity 10950 \
  -dname "CN=lxcode"
```

然后更新 `keystore.properties` 的四行（storeFile / storePassword / keyAlias / keyPassword）。
注意：新 keystore 签名与旧包不同，用户需卸载旧版再装（数据丢失）——所以优先备份而非重生成。

## 查验签名

```bash
keytool -list -keystore lxcode-release.keystore
# 已装包：
adb shell dumpsys package com.moyunteng.lxcode.remote | grep -A3 signatures
```
