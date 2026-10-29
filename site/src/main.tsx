// 站点入口：挂载 App 到 index.html 的 #root，并引入三份全局样式。
//
// 样式引入顺序固定：tokens（变量）→ base（重置与原语）→ chrome（外壳），
// 后两者都用 var() 引用 token，顺序反了会拿到未定义变量。

import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import "@fontsource-variable/inter";
import "@fontsource-variable/jetbrains-mono";
import "./styles/tokens.css";
import "./styles/base.css";
import "./styles/chrome.css";

import App from "./App";

const root = document.getElementById("root");
if (!root) {
  // 挂载点是 index.html 与入口的契约，缺了说明 HTML 被改坏，早失败好过白屏。
  throw new Error("找不到挂载点 #root：index.html 必须保留 <div id=\"root\">");
}

createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
