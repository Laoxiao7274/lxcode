// :app —— lxcode 安卓远端客户端。
// 依赖保持最小：Compose + Material3 + 图标，无 DI 框架、无导航库
//（原型期导航是显式的少量屏幕状态，引入 navigation-compose 只会多一层间接）。
plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.compose")
}

android {
    namespace = "com.moyunteng.lxcode.remote"
    compileSdk = 35
    buildToolsVersion = "35.0.0"

    defaultConfig {
        applicationId = "com.moyunteng.lxcode.remote"
        minSdk = 26
        targetSdk = 35
        versionCode = 1
        versionName = "0.1.0-prototype"
    }

    buildTypes {
        release {
            isMinifyEnabled = false
        }
    }

    buildFeatures {
        compose = true
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

    // 二维码解码（扫码配对）：手机是扫描方——扫桌面端「远程访问」页的二维码。
    // zxing-android-embedded 提供相机取景 + 连续解码（DecoratedBarcodeView），
    // 传递依赖 zxing:core 3.4.1；两者在本机 gradle 缓存中都有（离线也可构建）。
    // 只加到 :app（:design 是纯组件库，不掺业务/协议资产——见 design/build.gradle.kts 的纪律）。
    implementation("com.journeyapps:zxing-android-embedded:4.3.0")
    implementation("com.google.zxing:core:3.4.1")

    debugImplementation("androidx.compose.ui:ui-tooling")
}
