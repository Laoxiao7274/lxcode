// 最小接入（React #300 防再犯）：只开 rules-of-hooks —— 早退在 hook 之前/条件
// 调用 hook 的静态检查（当年 ToolBlock 的 todoItems useMemo 排在两个早退之后，
// 同一 fiber 的 hook 数 13↔14 漂移，触发渲染错乱）。守则：改动范围最小，
// exhaustive-deps 与 v7 新增的风格类 hooks 规则一律不开——告警面大、修起来是
// 另一个量级的活，纳入属另一个任务。
import ts from "typescript-eslint";

const reactHooks = (await import("eslint-plugin-react-hooks")).default;

export default ts.config(
  { ignores: ["dist/", "node_modules/", "src-tauri/", "*.cjs"] },
  {
    files: ["src/**/*.{ts,tsx}"],
    linterOptions: {
      // 现有代码里的 eslint-disable react-hooks/exhaustive-deps 注释在本次
      // 只开 rules-of-hooks 的口径下全部「未使用」——它们是给将来开启
      // exhaustive-deps 留的，不报告、不删。
      reportUnusedDisableDirectives: "off",
    },
    extends: [ts.configs.eslintRecommended],
    languageOptions: {
      parser: ts.parser,
      parserOptions: { projectService: true, tsconfigRootDir: import.meta.dirname },
    },
    plugins: { "react-hooks": reactHooks },
    rules: {
      // 只兜 hooks 顺序这一类（React #300）。exhaustive-deps 不开：
      // 告警面太大（本仓库现有代码里 eslint-disable 注释大多是给它留的）。
      "react-hooks/rules-of-hooks": "error",
    },
  },
);

