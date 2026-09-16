// build.mjs —— esbuild 打包壳主进程（单入口 bundle，CJS）。
// 不引入 vite-plugin-electron：薄壳主进程改频率低（窗口/托盘/sidecar 生命周期），
// HMR 无价值；esbuild 单命令产出，与 frontend 的 vite 配置完全解耦。
import { build } from "esbuild";

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
console.log("[shell] 主进程构建完成 → dist/main.js");
