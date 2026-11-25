/// <reference types="vite/client" />

// import.meta.env 的项目级声明（vite/client 的默认 shapes 不含本项目注入的自定义键）。
// 仅 scripts/dev.mjs 起 vite 时注入 LXCODE_DEV_BACKEND_PORT（vite.config.ts define →
// VITE_BACKEND_PORT）——dev 后端端口（7790，与生产 7789 隔离）；未注入的构建里
// 该键不存在，消费方必须按 undefined 处理（frontend/src/agent/index.ts defaultLiveAddr）。
interface ImportMetaEnv {
  readonly VITE_BACKEND_PORT?: string;
}

/** 壳层侧键探针（2026-12 侧键修复）：AssistantWorkspace 在助手模式可见时注册、
 *  切走注销；壳层 app-command（shell/src/main.ts）**存在性分流**——探针存在
 *  调用它、不存在回落 history（不用 `??`：探针处理完返回 undefined 会误触发
 *  history.back 双重导航）。探针返回 true = 已处理（双保险，壳层可据此判）；
 *  项目域不注册——它的侧键走浏览器历史 + popstate 原生路径。 */
interface Window {
  __lxNavBack?: () => boolean | void;
  __lxNavForward?: () => boolean | void;
}
