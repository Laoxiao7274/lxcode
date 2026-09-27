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
    watch: {
      // 排除编辑工具原子写的瞬时目录/文件。
      //
      // 症状：chokidar 对 `.index.ts.<pid>.<uuid>.tmpdir/index.ts.tmp` 建 watch
      // 时拿到 EBUSY（Windows 上该临时文件被写者持有），vite 抛未捕获的
      // FSWatcher error 直接**整个 dev 栈崩掉**——前端热更失效、窗口白屏，
      // 而报错信息里只有一串临时目录路径，极易误判成源码问题。
      // 这类临时目录写完即删，本来就没有 watch 的价值。
      ignored: ["**/.*.tmpdir/**", "**/*.tmp"],
    },
  },
  build: {
    target: "chrome110", // 浏览器基线（Electron 自带 Chromium，向下兼容）
    outDir: "dist",
    base: "./", // Electron 产线以 file:// 加载 asar 内 renderer，必须相对路径
  },
});
