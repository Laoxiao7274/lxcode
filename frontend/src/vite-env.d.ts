/// <reference types="vite/client" />

// import.meta.env 的项目级声明（vite/client 的默认 shapes 不含本项目注入的自定义键）。
// 仅 scripts/dev.mjs 起 vite 时注入 LXCODE_DEV_BACKEND_PORT（vite.config.ts define →
// VITE_BACKEND_PORT）——dev 后端端口（7790，与生产 7789 隔离）；未注入的构建里
// 该键不存在，消费方必须按 undefined 处理（frontend/src/agent/index.ts defaultLiveAddr）。
interface ImportMetaEnv {
  readonly VITE_BACKEND_PORT?: string;
}

/** 壳层侧键探针（2026-12 侧键修复）：AssistantWorkspace 在助手模式可见时注册、
 *  切走注销；壳层 app-command（shell/src/main.ts）探针优先调用、回落 history。
 *  项目域不注册——它的侧键走浏览器历史 + popstate 原生路径。 */
interface Window {
  __lxNavBack?: () => void;
  __lxNavForward?: () => void;
}
