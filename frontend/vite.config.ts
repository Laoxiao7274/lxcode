import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// 双形态：
// - 浏览器纯前端开发（node scripts/dev.mjs / npm run dev）：vite dev server 5190
// - Electron 壳（node scripts/dev.mjs --electron）：壳加载本 dev server（LXCODE_DEV_URL）
export default defineConfig({
  plugins: [react()],
  clearScreen: false,
  server: {
    port: 5190,
    strictPort: true,
    host: "127.0.0.1",
  },
  build: {
    target: "chrome110", // 浏览器基线（Electron 自带 Chromium，向下兼容）
    outDir: "dist",
    base: "./", // Electron 产线以 file:// 加载 asar 内 renderer，必须相对路径
  },
});
