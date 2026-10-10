// 根构建脚本：只声明插件版本，不在这里配置任何模块。
// 版本组合（本机实测可构建）：Gradle 8.11.1 + AGP 8.7.3 + Kotlin 2.0.21 + compileSdk 35。
plugins {
    id("com.android.application") version "8.7.3" apply false
    id("org.jetbrains.kotlin.android") version "2.0.21" apply false
    id("org.jetbrains.kotlin.plugin.compose") version "2.0.21" apply false
}
