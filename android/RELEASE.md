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
