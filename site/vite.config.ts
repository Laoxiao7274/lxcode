import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// 两个站点（Vite 多页构建）：
//   index.html → 公开官网（src/main.tsx）
//   admin.html → **独立的管理站**（src/admin.tsx）：自己的登录页与管理台外壳
// 产物路径：dist/index.html 与 dist/admin.html；`/admin/` 由后端 staticFiles 映射到 admin.html。
// 子路径部署（网关 /site/ 前缀）由部署侧 `vite build --base=/site/` 覆盖，两个入口共用同一 base。
// 端口 5200：与产品 dev server（5190）错开，两者可同时开着对照设计。
/**
 * dev 下把 /admin 与 /admin/<任意> 重写到 /admin.html。
 *
 * 为什么需要：vite dev 默认按 SPA 回落，/admin/ 会拿到公开站入口（index.html）；
 * 而线上是后端 staticFiles 把 /admin/ 映射到 dist/admin.html。不补这一下，
 * 本地看到的就不是线上的那个站点，等于「本地过了线上不过」这类事故的温床。
 */
function adminEntry() {
  return {
    name: "lxcode-admin-entry",
    configureServer(server: { middlewares: { use: (fn: unknown) => void } }) {
      server.middlewares.use((req: { url?: string }, _res: unknown, next: () => void) => {
        const url = req.url ?? "";
        if (url === "/admin" || url.startsWith("/admin/")) {
          req.url = "/admin.html";
        }
        next();
      });
    },
  };
}

export default defineConfig({
  plugins: [react(), adminEntry()],
  clearScreen: false,
  server: {
    port: 5200,
    strictPort: true,
    host: "127.0.0.1",
    // 站点后端（site-backend，5201）代理：/api 与 /releases 都转过去，
    // 于是前端代码里一律用相对路径（不用配 base URL，也不需要 CORS）。
    proxy: {
      "/api": { target: "http://127.0.0.1:5201", changeOrigin: false },
      "/releases": { target: "http://127.0.0.1:5201", changeOrigin: false },
      "/manifest.json": { target: "http://127.0.0.1:5201", changeOrigin: false },
    },
    watch: {
      // 排除编辑工具原子写的瞬时目录/文件：chokidar 在写入窗口内对它建 watch 会拿到
      // EBUSY，vite 抛未捕获的 FSWatcher error 直接崩掉整个 dev server（AGENTS.md §5 坑 15）。
      ignored: ["**/.*.tmpdir/**", "**/*.tmp"],
    },
  },
  build: {
    target: "chrome110",
    outDir: "dist",
    // 站点以 http(s) 托管（静态目录），不用相对路径——子路径部署由部署侧 base 覆盖。
    base: "/",
    rollupOptions: {
      input: {
        // 两个 HTML 入口（MPA）：官网 + 独立管理站
        main: fileURLToPath(new URL("./index.html", import.meta.url)),
        admin: fileURLToPath(new URL("./admin.html", import.meta.url)),
      },
    },
  },
});
