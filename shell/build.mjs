// build.mjs —— esbuild 打包壳主进程 + preload（CJS，双入口）。
// 不引入 vite-plugin-electron：薄壳主进程改频率低（窗口/托盘/sidecar 生命周期），
// HMR 无价值；esbuild 单命令产出，与 frontend 的 vite 配置完全解耦。
import { build } from "esbuild";

// preload 必须非 bundle 成独立文件（webPreferences.preload 指向它），
// 且 Sandboxed preload 不能引外部模块——单文件内联打包正合适。
await build({
  entryPoints: ["src/preload.ts"],
  bundle: true,
  platform: "node",
  format: "cjs",
  target: "node22",
  external: ["electron"],
  outfile: "dist/preload.js",
  logLevel: "info",
});

await build({
  entryPoints: ["src/main.ts"],
  bundle: true,
  platform: "node",
  format: "cjs",
  target: "node22",
  external: ["electron"], // 主进程 API 由运行时注入，不能打进包
  outfile: "dist/main.js",
  sourcemap: true,
  logLevel: "info",
});
console.log("[shell] 主进程 + preload 构建完成 → dist/");
