import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Tauri 生产构建用固定端口；原型阶段任意
export default defineConfig({
  plugins: [react()],
  clearScreen: false,
  server: {
    port: 5190,
    strictPort: true,
  },
  build: {
    target: "chrome110", // WebView2 现代基线
  },
});
