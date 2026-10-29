// lxcode 安卓端（远端客户端）——Gradle 设置。
// :app = 远端客户端（唯一应用形态）；:design = 设计系统库（token + 核心组件），
// 两者是「消费方 → 库」的单向依赖，:design 不认识 :app。
pluginManagement {
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
}

dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories {
        google()
        mavenCentral()
    }
}

rootProject.name = "lxcode-remote"
// :design = lxcode 安卓端设计系统（token + 核心组件），独立库模块，不依赖 :app。
include(":design")
include(":app")
