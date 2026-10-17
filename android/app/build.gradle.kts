// :app —— lxcode 安卓远端客户端。
// 依赖保持最小：Compose + Material3 + 图标，无 DI 框架、无导航库
//（原型期导航是显式的少量屏幕状态，引入 navigation-compose 只会多一层间接）。
import java.util.Properties

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.compose")
}

android {
    namespace = "com.moyunteng.lxcode.remote"
    compileSdk = 35
    buildToolsVersion = "35.0.0"

    // 单元测试用真实 org.json 实现（Android jar 里的 org.json 是 stub，方法一调就
    // 抛 "not mocked"——SessionCacheCodec 的缓存编解码测试需要真的读写 JSON）。
    // json 依赖只进 testImplementation（main 用平台自带的 org.json，见依赖纪律）。

    defaultConfig {
        applicationId = "com.moyunteng.lxcode.remote"
        minSdk = 26
        targetSdk = 35
        versionCode = 11
        versionName = "0.2.11"
    }

    // 签名（release）：凭据从 android/keystore.properties 读（gitignore，不入库）。
    // 文件缺失时 release 退回 debug 签名并打 warning——本地构建不断，出包前必须补上
    //（重新生成 keystore 的步骤见 android/RELEASE.md）。
    val keystoreProps = Properties().apply {
        val f = rootProject.file("keystore.properties")
        if (f.exists()) f.inputStream().use { load(it) }
    }
    val hasReleaseKeystore = keystoreProps.getProperty("storeFile") != null
    if (!hasReleaseKeystore) {
        logger.warn("android/keystore.properties 缺失：release 将退回 debug 签名（仅限本地构建，不得分发）")
    }

    signingConfigs {
        if (hasReleaseKeystore) {
            create("release") {
                storeFile = rootProject.file(keystoreProps.getProperty("storeFile"))
                storePassword = keystoreProps.getProperty("storePassword")
                keyAlias = keystoreProps.getProperty("keyAlias")
                keyPassword = keystoreProps.getProperty("keyPassword")
                // v1 + v2 双签名：AGP 对 minSdk>=24 默认只打 v2，但部分国产 ROM 的
                // 安装器/文件管理器拿到 v2-only 包时解析 PackageInfo 失败，安装时
                // 报「packageinfo is null」（0.2.2 线上包实测只有 v2 签名）。补上
                // v1 (JAR) 签名兜住这类安装器；v2 保持开启（Android 7+ 标准路径）。
                enableV1Signing = true
                enableV2Signing = true
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            if (hasReleaseKeystore) {
                signingConfig = signingConfigs.getByName("release")
            } else {
                signingConfig = signingConfigs.getByName("debug")
            }
        }
    }

    buildFeatures {
        compose = true
        // BuildConfig.DEBUG：连接页/扫码页的 mock 调试控件用它门控（release 不出现）
        buildConfig = true
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    packaging {
        resources.excludes += "/META-INF/{AL2.0,LGPL2.1}"
    }
}

kotlin {
    compilerOptions {
        jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17)
    }
}

dependencies {
    // 设计系统（token + 组件）单独成库：app 只是它的消费方，反过来 :design 不知道 app 存在。
    implementation(project(":design"))

    implementation("androidx.core:core-ktx:1.13.1")
    implementation("androidx.lifecycle:lifecycle-runtime-ktx:2.8.6")
    implementation("androidx.activity:activity-compose:1.9.3")

    implementation(platform("androidx.compose:compose-bom:2024.10.01"))
    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.ui:ui-graphics")
    implementation("androidx.compose.ui:ui-tooling-preview")
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.material:material-icons-extended")

    // WebSocket JSON-RPC（真实后端模式，net/ 包）：OkHttp 4.12.0 —— WS 支持成熟、
    // 传递面小（仅 okio + kotlin-stdlib），两者本机 gradle 缓存/m2 均有（离线可构建）。
    // 不引 kotlinx-serialization：帧结构浅（两层），org.json（平台自带）足够。
    implementation("com.squareup.okhttp3:okhttp:4.12.0")

    // 二维码解码（扫码配对）：手机是扫描方——扫桌面端「远程访问」页的二维码。
    // zxing-android-embedded 提供相机取景 + 连续解码（DecoratedBarcodeView），
    // 传递依赖 zxing:core 3.4.1；两者在本机 gradle 缓存中都有（离线也可构建）。
    // 只加到 :app（:design 是纯组件库，不掺业务/协议资产——见 design/build.gradle.kts 的纪律）。
    implementation("com.journeyapps:zxing-android-embedded:4.3.0")
    implementation("com.google.zxing:core:3.4.1")

    // 单元测试（纯 JVM）：markdown 解析器、sha256 校验等无 Android 依赖的核心逻辑。
    // 不配 robolectric——工程既有约定是「构建即验证」，这里只补最小 JUnit4
    // （本机 gradle 缓存/m2 均有）。
    testImplementation("junit:junit:4.13.2")
    // 真实 org.json（Android jar 的 org.json 是 stub——SessionCacheCodec 的编解码
    // 单测要真的读写 JSON；testImplementation 不进 APK，main 仍用平台自带实现）。
    testImplementation("org.json:json:20240303")

    debugImplementation("androidx.compose.ui:ui-tooling")
}
