import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Tauri 适配：固定端口 + 明确 host（Tauri 的 devUrl 指向这里）
export default defineConfig({
  plugins: [react()],
  clearScreen: false,
  server: {
    port: 5190,
    strictPort: true,
    host: "127.0.0.1",
  },
  build: {
    target: "chrome110", // WebView2 现代基线
    // Tauri 生产构建输出到 dist/（tauri.conf.json 的 frontendDist）
    outDir: "dist",
  },
  // Tauri WebView2 环境（不让 vite 注入导致 CSP 冲突的内容）
  envPrefix: ["VITE_", "TAURI_"],
});
