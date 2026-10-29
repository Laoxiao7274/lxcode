// :design —— lxcode 安卓端设计系统（token + 核心组件）。
//
// 纪律：这个模块是「能独立复用的组件库」，不是 app 的一部分。
//   * 只依赖 Compose（BOM 对齐 :app 的 2024.10.01）+ androidx.core；
//   * 绝不依赖 :app（反向依赖会让组件库失去复用价值，也会形成循环）；
//   * 不含任何业务逻辑（网络/会话/扫码一律不进这个模块）。
// Compose 依赖用 api 而非 implementation：LxTheme 与组件的公开签名里直接出现 Compose 类型
//（@Composable、Modifier、Color…），消费方必须能看到它们，这是库模块的正确形态。
plugins {
    id("com.android.library")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.compose")
}

android {
    namespace = "com.moyunteng.lxcode.design"
    compileSdk = 35
    buildToolsVersion = "35.0.0"

    defaultConfig {
        minSdk = 26
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
    api("androidx.core:core-ktx:1.13.1")

    api(platform("androidx.compose:compose-bom:2024.10.01"))
    api("androidx.compose.ui:ui")
    api("androidx.compose.ui:ui-graphics")
    api("androidx.compose.foundation:foundation")
    api("androidx.compose.material3:material3")

    debugImplementation("androidx.compose.ui:ui-tooling")
}
