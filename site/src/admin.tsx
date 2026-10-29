// 管理站入口：挂载到 admin.html 的 #root。
//
// 样式引入顺序：tokens（变量）→ base（重置与原语）→ admin（管理站专属外壳），
// 管理站**不引** page-site.css（那是官网的页面编排），视觉上与官网刻意分开。

import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import "@fontsource-variable/inter";
import "@fontsource-variable/jetbrains-mono";
import "./styles/tokens.css";
import "./styles/base.css";
import "./styles/admin.css";

import AdminApp from "./admin/AdminApp";

const root = document.getElementById("root");
if (!root) {
  throw new Error("找不到挂载点 #root：admin.html 必须保留 <div id=\"root\">");
}

createRoot(root).render(
  <StrictMode>
    <AdminApp />
  </StrictMode>,
);
